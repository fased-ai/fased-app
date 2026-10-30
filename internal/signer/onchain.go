package signer

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/update"
	"time"
)

// SwapProposal is one exact reviewed order, not an arbitrary signing request or
// reusable grant. This development namespace cannot unlock managed-v2 keys.
type SwapProposal struct {
	Version     string        `json:"version"`
	Account     AccountRef    `json:"account"`
	Quote       onchain.Quote `json:"quote"`
	FixtureOnly bool          `json:"fixtureOnly"`
	Digest      string        `json:"digest"`
}
type SwapReceipt struct {
	Proposal    SwapProposal         `json:"proposal"`
	State       string               `json:"state"`
	Signed      []byte               `json:"signed,omitempty"`
	Signature   string               `json:"signature,omitempty"`
	Observation *onchain.Observation `json:"observation,omitempty"`
}
type SwapObserver interface {
	Reconcile(context.Context, onchain.Quote, []byte) (onchain.Observation, error)
}

func SwapProposalDigest(p SwapProposal) string { p.Digest = ""; return onchain.Hash(p) }
func NewSwapProposal(ref AccountRef, q onchain.Quote) SwapProposal {
	p := SwapProposal{Version: onchain.Version, Account: ref, Quote: q, FixtureOnly: true}
	p.Digest = SwapProposalDigest(p)
	return p
}
func validateSwapProposal(p SwapProposal, now int64) error {
	public, e := hex.DecodeString(p.Account.PublicKey)
	if e != nil || !validRef(p.Account) || !p.FixtureOnly || p.Version != onchain.Version || p.Digest != SwapProposalDigest(p) || onchain.Base58(public) != p.Quote.Owner || p.Quote.Blockhash != onchain.ControlledBlockhash {
		return errors.New("controlled, exact-wallet swap approval required")
	}
	return onchain.ValidateQuote(p.Quote, now)
}

// ConfigureSwapObserver is a trusted in-process construction dependency, never
// an RPC/CLI setting. Ordinary signer startup does not configure this capability.
// It verifies settlement independently of the app; app-supplied status cannot
// release a signer hold or restore a consumed approval.
func (v *Vault) ConfigureSwapObserver(observer SwapObserver) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.network != "" || observer == nil {
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
	if e := v.fixtureReady(true); e != nil {
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
	if e := v.fixtureReady(true); e != nil {
		return nil, e
	}
	r, ok := v.ledger.swaps[id]
	if !ok || r.Proposal.Account != v.record.Ref {
		return nil, errors.New("swap approval or current epoch missing")
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
	if r.State == "confirmed" || r.State == "failed" {
		out := clone(r)
		return &out, nil
	}
	observation, e := v.swapObserver.Reconcile(ctx, r.Proposal.Quote, r.Signed)
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
func (l *ledger) applySwap(r journalRecord) error {
	expected := journalRecord{Version: r.Version, Sequence: r.Sequence, Previous: r.Previous, Hash: r.Hash, At: r.At, Kind: r.Kind, ID: r.ID}
	switch r.Kind {
	case "onchain-approve":
		expected.Swap = r.Swap
		if r.Swap == nil || r.ID != r.Swap.Quote.ID || r.Swap.Account != l.ref || validateSwapProposal(*r.Swap, r.At) != nil || len(l.swaps) >= 64 {
			return errors.New("invalid swap approval history")
		}
		if _, ok := l.swaps[r.ID]; ok {
			return errors.New("duplicate swap approval")
		}
	case "onchain-cancel":
		s, ok := l.swaps[r.ID]
		if !ok || s.State != "approved" {
			return errors.New("only unsigned approval can be cancelled")
		}
	case "onchain-sign":
		expected.Original = r.Original
		s, ok := l.swaps[r.ID]
		if !ok || s.State != "approved" || len(s.Signed) != 0 || validateSwapProposal(s.Proposal, r.At) != nil {
			return errors.New("invalid swap sign history")
		}
		if _, e := onchain.VerifyPacket(s.Proposal.Quote, r.Original); e != nil {
			return e
		}
	case "onchain-settled":
		expected.SwapObservation = r.SwapObservation
		s, ok := l.swaps[r.ID]
		if !ok || len(s.Signed) == 0 || s.State != "signed" || r.SwapObservation == nil || (r.SwapObservation.State != "confirmed" && r.SwapObservation.State != "failed") || r.SwapObservation.Slot < s.Proposal.Quote.ObservedSlot {
			return errors.New("invalid swap settlement history")
		}
		if r.SwapObservation.State == "confirmed" {
			minimum, _ := onchain.Uint(s.Proposal.Quote.MinimumOutput)
			out, e := onchain.Uint(r.SwapObservation.Output)
			if e != nil || out < minimum || r.SwapObservation.Input != s.Proposal.Quote.Amount {
				return errors.New("swap settlement exceeds approval")
			}
		}
	default:
		return errors.New("unsupported swap record")
	}
	if recordHash(expected) != recordHash(r) {
		return errors.New("unexpected swap history fields")
	}
	switch r.Kind {
	case "onchain-approve":
		l.swaps[r.ID] = SwapReceipt{Proposal: clone(*r.Swap), State: "approved"}
	case "onchain-cancel":
		s := l.swaps[r.ID]
		s.State = "cancelled"
		l.swaps[r.ID] = s
	case "onchain-sign":
		s := l.swaps[r.ID]
		s.Signed = append([]byte(nil), r.Original...)
		s.Signature, _ = onchain.VerifyPacket(s.Proposal.Quote, r.Original)
		s.State = "signed"
		l.swaps[r.ID] = s
	case "onchain-settled":
		s := l.swaps[r.ID]
		s.Observation = clone(r.SwapObservation)
		s.State = r.SwapObservation.State
		l.swaps[r.ID] = s
	}
	return nil
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
