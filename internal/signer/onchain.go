package signer

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/update"
	"time"
)

type SwapObserver interface {
	Reconcile(context.Context, onchain.Quote, []byte) (onchain.Observation, error)
}

// ConfigureSwapObserver is a trusted in-process construction dependency, never
// an RPC/CLI setting. Ordinary signer startup does not configure this capability.
// It verifies settlement independently of the app; app-supplied status cannot
// release a signer hold or restore a consumed approval.
func (v *Vault) ConfigureSwapObserver(observer SwapObserver) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || observer == nil {
		return ErrUnavailable
	}
	v.swapObserver = observer
	return nil
}
func (v *Vault) ApproveSwap(p SwapProposal) (*SwapReceipt, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.swapObserver == nil {
		return nil, ErrUnavailable
	}
	if e := v.swapReady(true); e != nil {
		return nil, e
	}
	if e := v.validReceiptRPCBinding(p); e != nil {
		return nil, e
	}
	if p.Account != v.record.Ref {
		return nil, errors.New("swap wallet/epoch changed")
	}
	if e := validateSwapProposal(p, v.now().Unix()); e != nil {
		return nil, e
	}
	for _, s := range v.ledger.swaps {
		if s.State == "signed" || s.State == "approved" && s.Proposal.Quote.ExpiresAt > v.now().Unix() {
			return nil, ErrHeld
		}
	}
	if len(v.ledger.swaps) >= 64 {
		return nil, errors.New("swap history capacity reached")
	}
	if _, ok := v.ledger.swaps[p.Quote.ID]; ok {
		return nil, errors.New("swap approval identity already used")
	}
	if e := v.ledger.append(v, journalRecord{Kind: "onchain-approve", Swap: &p, ID: p.Quote.ID}); e != nil {
		return nil, e
	}
	deadline := time.Unix(p.Quote.ExpiresAt, 0)
	if p.ReceiptRPC != nil {
		v.receiptRPCLease.cap(deadline)
	}
	if deadline.Before(v.until) {
		if v.timer != nil {
			v.timer.Stop()
		}
		v.until = deadline
		v.timer = time.AfterFunc(deadline.Sub(v.now()), func() {
			v.mu.Lock()
			defer v.mu.Unlock()
			if v.until == deadline {
				v.lock()
			}
		})
	}
	r := clone(v.ledger.swaps[p.Quote.ID])
	return &r, nil
}
func (v *Vault) SwapReceipt(id string) (*SwapReceipt, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.ledger == nil || !validID(id) {
		return nil, ErrUnavailable
	}
	r, ok := v.ledger.swaps[id]
	if !ok {
		return nil, errors.New("swap approval required")
	}
	r = clone(r)
	return &r, nil
}
func (v *Vault) SignSwap(id string) (*SwapReceipt, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.swapObserver == nil {
		return nil, ErrUnavailable
	}
	if e := v.swapReady(true); e != nil {
		return nil, e
	}
	r, ok := v.ledger.swaps[id]
	if !ok || r.Proposal.Account != v.record.Ref {
		return nil, errors.New("swap approval or current epoch missing")
	}
	if e := v.validReceiptRPCBinding(r.Proposal); e != nil {
		return nil, e
	}
	if len(r.Signed) > 0 {
		out := clone(r)
		return &out, nil
	}
	if r.State != "approved" {
		return nil, errors.New("swap approval cancelled")
	}
	if e := validateSwapProposal(r.Proposal, v.now().Unix()); e != nil {
		return nil, e
	}
	message, e := onchain.Message(r.Proposal.Quote)
	if e != nil {
		return nil, e
	}
	key := ed25519.NewKeyFromSeed(v.seed)
	defer wipe(key)
	signature := ed25519.Sign(key, message)
	packet, e := onchain.Signed(r.Proposal.Quote, signature)
	if e != nil {
		return nil, e
	}
	for otherID, s := range v.ledger.swaps {
		if otherID != id && bytes.Equal(s.Signed, packet) {
			return nil, errors.New("transaction already signed under another order; obtain a fresh blockhash")
		}
	}
	if e = v.ledger.append(v, journalRecord{Kind: "onchain-sign", ID: id, Original: packet}); e != nil {
		return nil, e
	}
	out := clone(v.ledger.swaps[id])
	return &out, nil
}
func (v *Vault) ReconcileSwap(ctx context.Context, id string) (*SwapReceipt, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.record == nil || v.swapObserver == nil || v.ledger == nil || v.store.failedState() || v.ledger.activeUpdate != "" {
		return nil, ErrUnavailable
	}
	r, ok := v.ledger.swaps[id]
	if !ok || !sameWallet(r.Proposal.Account, v.record.Ref) || len(r.Signed) == 0 {
		return nil, errors.New("signed swap original missing or epoch changed")
	}
	if (r.Proposal.ReceiptRPC != nil || v.ledger.receiptRPC != nil) && v.receiptRPCFactory == nil {
		return nil, ErrUnavailable
	}
	if r.State == "confirmed" || r.State == "failed" {
		out := clone(r)
		return &out, nil
	}
	if v.receiptRPCFactory != nil && (v.receiptRPCLease == nil || v.receiptRPCLease.valid() != nil) {
		return nil, ErrLocked
	}
	observation, e := v.swapObserver.Reconcile(ctx, r.Proposal.Quote, r.Signed)
	if v.receiptRPCFactory != nil && (v.receiptRPCLease == nil || v.receiptRPCLease.valid() != nil) {
		return nil, ErrLocked
	}
	if e != nil {
		return nil, e
	}
	if observation.State == "confirmed" || observation.State == "failed" {
		if e = v.ledger.append(v, journalRecord{Kind: "onchain-settled", ID: id, SwapObservation: &observation}); e != nil {
			return nil, e
		}
	}
	out := clone(v.ledger.swaps[id])
	return &out, nil
}
func ParseSwapProposal(raw []byte) (SwapProposal, error) {
	var p SwapProposal
	if update.StrictJSONBounded(raw, &p, onchain.MaxDocument) != nil {
		return p, errors.New("invalid swap approval document")
	}
	return p, validateSwapProposal(p, time.Now().Unix())
}

func (v *Vault) CancelSwap(id string) (*SwapReceipt, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.ledger == nil || v.swapObserver == nil || v.store.failedState() || v.ledger.activeUpdate != "" {
		return nil, ErrUnavailable
	}
	s, ok := v.ledger.swaps[id]
	if !ok {
		return nil, errors.New("swap approval missing")
	}
	if s.State == "cancelled" {
		out := clone(s)
		return &out, nil
	}
	if s.State != "approved" {
		return nil, errors.New("signed orders require reconciliation")
	}
	if e := v.ledger.append(v, journalRecord{Kind: "onchain-cancel", ID: id}); e != nil {
		return nil, e
	}
	out := clone(v.ledger.swaps[id])
	return &out, nil
}

// General recovery and legacy fixture grants remain held. This capability is
// only a controlled, exact-message swap under its independent owner approval.
func (v *Vault) managedSwapEligible() bool {
	return v.network != "" && !v.closed && v.swapObserver != nil && (v.ledger == nil || v.ledger.receiptRPC == nil || v.receiptRPCFactory != nil) && v.record != nil && v.record.Version == 3 && v.record.FreshManaged && v.managedRef != nil && v.ledger != nil && v.ledger.managedFresh && *v.managedRef == v.record.Ref && v.ledger.ref == v.record.Ref && v.record.Ref.Network == v.network && !v.store.failedState() && v.ledger.activeUpdate == ""
}
func (v *Vault) swapReady(unlock bool) error {
	if v.network == "" {
		return v.fixtureReady(unlock)
	}
	if !v.managedSwapEligible() {
		return ErrUnavailable
	}
	if unlock && !v.unlocked() {
		return ErrLocked
	}
	return nil
}

// OpenManagedSwapFixture opens only disposable network-bound custody. The
// observer is a trusted in-process dependency; no runtime URL/CA override.
// Callers must still obtain owner consent for every inert-blockhash operation.
func OpenManagedSwapFixture(dir, id, network string, observer SwapObserver) (*Vault, error) {
	if !managedNetwork(network) || observer == nil {
		return nil, ErrUnavailable
	}
	v, e := openVaultNetwork(dir, id, false, network)
	if e != nil {
		return nil, e
	}
	if e = v.ConfigureSwapObserver(observer); e != nil {
		v.Close()
		return nil, e
	}
	return v, nil
}
