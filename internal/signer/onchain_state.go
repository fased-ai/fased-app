package signer

import (
	"encoding/hex"
	"errors"
	"github.com/fased-ai/fased-app/internal/onchain"
)

// SwapProposal is one exact reviewed order, not an arbitrary signing request or
// reusable grant. Managed keys require a separate provenance-bound fixture capability.
type SwapProposal struct {
	ReceiptRPC  *ReceiptRPCBinding `json:"receiptRPC,omitempty"`
	Version     string             `json:"version"`
	Account     AccountRef         `json:"account"`
	Quote       onchain.Quote      `json:"quote"`
	FixtureOnly bool               `json:"fixtureOnly"`
	Digest      string             `json:"digest"`
}
type SwapReceipt struct {
	Proposal    SwapProposal         `json:"proposal"`
	State       string               `json:"state"`
	Signed      []byte               `json:"signed,omitempty"`
	Signature   string               `json:"signature,omitempty"`
	Observation *onchain.Observation `json:"observation,omitempty"`
}

const ManagedSwapVersion = "managed-onchain-swap-fixture-v1"
const ManagedCredentialSwapVersion = "managed-credential-swap-fixture-v1"

func SwapProposalDigest(p SwapProposal) string { p.Digest = ""; return onchain.Hash(p) }
func NewSwapProposal(ref AccountRef, q onchain.Quote) SwapProposal {
	p := SwapProposal{Version: onchain.Version, Account: ref, Quote: q, FixtureOnly: true}
	if ref.Version == 2 {
		p.Version = ManagedSwapVersion
	}
	p.Digest = SwapProposalDigest(p)
	return p
}
func NewCredentialSwapProposal(ref AccountRef, q onchain.Quote, binding ReceiptRPCBinding) SwapProposal {
	p := SwapProposal{Version: ManagedCredentialSwapVersion, Account: ref, Quote: q, ReceiptRPC: &binding, FixtureOnly: true}
	p.Digest = SwapProposalDigest(p)
	return p
}
func validateSwapProposal(p SwapProposal, now int64) error {
	if p.Version == ManagedCredentialSwapVersion {
		if p.ReceiptRPC == nil || p.ReceiptRPC.Version != 1 || !validID(p.ReceiptRPC.Ref) || !receiptRPCOrigin(p.ReceiptRPC.Origin, p.Quote.Network) {
			return errors.New("exact receipt RPC binding required")
		}
	} else if p.ReceiptRPC != nil {
		return errors.New("unexpected receipt RPC binding")
	}
	public, e := hex.DecodeString(p.Account.PublicKey)
	if e != nil || !validSwapRef(p.Account, p.Quote.Network, p.Version) || !p.FixtureOnly || p.Digest != SwapProposalDigest(p) || onchain.Base58(public) != p.Quote.Owner || p.Quote.Blockhash != onchain.ControlledBlockhash {
		return errors.New("controlled, exact-wallet swap approval required")
	}
	return onchain.ValidateQuote(p.Quote, now)
}

func ValidateSwapHistory(p SwapProposal) error { return validateSwapProposal(p, p.Quote.CreatedAt) }

func (l *ledger) applySwap(r journalRecord) error {
	expected := journalRecord{Version: r.Version, Sequence: r.Sequence, Previous: r.Previous, Hash: r.Hash, At: r.At, Kind: r.Kind, ID: r.ID}
	switch r.Kind {
	case "onchain-approve":
		expected.Swap = r.Swap
		if r.Swap == nil || r.ID != r.Swap.Quote.ID || r.Swap.Account != l.ref || validateSwapProposal(*r.Swap, r.At) != nil || len(l.swaps) >= 64 {
			return errors.New("invalid swap approval history")
		}
		if b := r.Swap.ReceiptRPC; b != nil && (l.receiptRPC == nil || l.receiptRPC.Account != r.Swap.Account || l.receiptRPC.Ref != b.Ref || l.receiptRPC.Origin != b.Origin) {
			return errors.New("swap receipt RPC history mismatch")
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

func (l *ledger) swapStateFeatures(features []string) []string {
	out := []string{}
	for _, f := range features {
		if f != "onchain-swap-fixture-v1" || len(l.swaps) > 0 {
			out = append(out, f)
		}
	}
	return out
}
func (l *ledger) swapPending(now int64) (pending, unknown int) {
	for _, s := range l.swaps {
		if s.State == "signed" {
			pending++
			unknown++
		}
		if s.State == "approved" && s.Proposal.Quote.ExpiresAt > now {
			pending++
		}
	}
	return
}

func validSwapRef(r AccountRef, network, version string) bool {
	if r.Version == 1 {
		return validRef(r) && version == onchain.Version
	}
	b, e := hex.DecodeString(r.PublicKey)
	return r.Version == 2 && (version == ManagedSwapVersion || version == ManagedCredentialSwapVersion) && (network == "devnet" || network == "mainnet-beta") && r.Network == network && validID(r.SignerID) && validID(r.WalletID) && validID(r.Epoch) && e == nil && len(b) == 32 && hex.EncodeToString(b) == r.PublicKey
}
