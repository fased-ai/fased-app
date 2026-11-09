//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/onchaintest"
	"github.com/fased-ai/fased-app/internal/signer"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTradeWindowOriginalReceiptLockAndRefusals(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	v, err := signer.OpenControlledOwnerCustody(dir, strings.Repeat("e", 32), "devnet")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	chain := &onchaintest.Chain{WrongNetwork: true}
	venue := onchaintest.Client(chain)
	if err = v.ConfigureControlledOrdinarySwapObserver(venue); err != nil {
		t.Fatal(err)
	}
	if err = v.Create(flowUnlock); err != nil {
		t.Fatal(err)
	}
	ref := *v.Status().Account
	pub, _ := hex.DecodeString(ref.PublicKey)
	now := time.Now().Unix()
	q, err := venue.Prepare(t.Context(), onchain.Quote{Version: onchain.Version, ID: strings.Repeat("1", 32), ProfileID: "trading-analyst", InstanceID: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)), AccountID: strings.Repeat("3", 32), Network: ref.Network, Owner: onchain.Base58(pub), RPCSHA256: strings.Repeat("4", 64), InputMint: onchain.USDC, OutputMint: onchain.SOL, Amount: "12500000", SlippageBPS: 50, MaxFeeLamports: 5000, CreatedAt: now, ExpiresAt: now + 45})
	if err != nil {
		t.Fatal(err)
	}
	p := signer.NewSwapProposal(ref, q)
	intent, err := signer.NewTradeOwnerIntent(p)
	if err != nil {
		t.Fatal(err)
	}
	cfg := signer.Config{Version: 3, Profile: signer.OwnerCustodyProfile, Provenance: signer.ControlledCustodyProfile, SignerID: ref.SignerID, Network: ref.Network, Mode: signer.OrdinaryCustodyMode}
	unlocks, approvals := 0, 0
	call := func(ctx context.Context, r signer.Request) (out signer.Response, e error) {
		switch r.Action {
		case "status":
			out.Status = v.Status()
		case "unlock":
			unlocks++
			e = v.Unlock(r.Password, time.Duration(r.UnlockSeconds)*time.Second)
			out.Status = v.Status()
		case "lock":
			v.Lock()
		case "swap-approve":
			approvals++
			out.Swap, e = v.ApproveSwap(*r.Swap)
		case "swap-receipt":
			out.Swap, e = v.SwapReceipt(r.OperationID)
		case "swap-cancel":
			out.Swap, e = v.CancelSwap(r.OperationID)
		default:
			e = errors.New("unexpected action")
		}
		return
	}
	input := signer.OwnerInput{Intent: intent, SignerID: ref.SignerID, Phase: "inspect"}
	exchange := func(in signer.OwnerInput) signer.OwnerResult {
		t.Helper()
		var out bytes.Buffer
		raw, _ := json.Marshal(in)
		var parsed signer.OwnerInput
		if e := readOwnerWindowInput(cfg, intent.InstanceID, bytes.NewReader(raw), &parsed); e != nil {
			t.Fatal(e)
		}
		if e := conductWindow(t.Context(), cfg, t.TempDir(), parsed, &out, call); e != nil {
			t.Fatal(e)
		}
		var r signer.OwnerResult
		if e := json.Unmarshal(out.Bytes(), &r); e != nil {
			t.Fatal(e)
		}
		return r
	}
	if r := exchange(input); !r.ReadOnly || r.Swap != nil || unlocks != 0 {
		t.Fatal("inspection mutated")
	}
	input.Phase = "approve"
	input.Unlock = bytes.Clone(flowUnlock)
	input.Confirmation = p.Digest
	r := exchange(input)
	if r.ReadOnly || r.Swap == nil || r.Swap.ApprovalAvailable == nil || !*r.Swap.ApprovalAvailable || approvals != 1 || unlocks != 1 {
		t.Fatal("missing scoped original approval")
	}
	if r = exchange(input); !r.ReadOnly || approvals != 1 || unlocks != 1 {
		t.Fatal("replayed approval")
	}
	v.Lock()
	input.Phase = "inspect"
	input.Unlock = nil
	input.Confirmation = ""
	if r = exchange(input); r.Swap.ApprovalAvailable == nil || *r.Swap.ApprovalAvailable {
		t.Fatal("locked approval falsely available")
	}
	input.Phase = "cancel"
	if r = exchange(input); r.Swap.State != "cancelled" {
		t.Fatal("unsigned cancellation missing")
	}
	input.Phase = "approve"
	input.Unlock = bytes.Clone(flowUnlock)
	input.Confirmation = p.Digest
	if r = exchange(input); !r.ReadOnly || r.Swap.State != "cancelled" || unlocks != 1 {
		t.Fatal("cancelled order reapproved")
	}
	foreign := input
	foreign.SignerID = strings.Repeat("f", 32)
	if e := conductTradeWindow(t.Context(), cfg, foreign, &bytes.Buffer{}, call); e == nil {
		t.Fatal("foreign signer accepted")
	}
	lost := p
	lost.Quote.ID = strings.Repeat("6", 32)
	lost.Quote.Digest = onchain.QuoteDigest(lost.Quote)
	lost.Digest = signer.SwapProposalDigest(lost)
	lostIntent, e := signer.NewTradeOwnerIntent(lost)
	if e != nil {
		t.Fatal(e)
	}
	lostInput := signer.OwnerInput{Intent: lostIntent, SignerID: ref.SignerID, Phase: "approve", Unlock: bytes.Clone(flowUnlock), Confirmation: lost.Digest}
	if e = conductTradeWindow(t.Context(), cfg, lostInput, failedTradeOutput{}, call); e == nil || !v.Status().Locked {
		t.Fatal("lost reply retained authority", e)
	}
	before := unlocks
	var recovered bytes.Buffer
	if e = conductTradeWindow(t.Context(), cfg, lostInput, &recovered, call); e != nil || unlocks != before {
		t.Fatal("lost reply replayed", e)
	}
	var recoveredResult signer.OwnerResult
	if json.Unmarshal(recovered.Bytes(), &recoveredResult) != nil || !recoveredResult.ReadOnly || recoveredResult.Swap.ApprovalAvailable == nil || *recoveredResult.Swap.ApprovalAvailable {
		t.Fatal("lost approval recovery lied")
	}
	expired := p
	expired.Quote.ID = strings.Repeat("9", 32)
	expired.Quote.CreatedAt = now - 90
	expired.Quote.ExpiresAt = now - 45
	expired.Quote.Digest = onchain.QuoteDigest(expired.Quote)
	expired.Digest = signer.SwapProposalDigest(expired)
	input.Intent, err = signer.NewTradeOwnerIntent(expired)
	if err != nil {
		t.Fatal(err)
	}
	input.Confirmation = expired.Digest
	if e := conductTradeWindow(t.Context(), cfg, input, &bytes.Buffer{}, call); e == nil || unlocks != before {
		t.Fatal("expired order unlocked", e)
	}
}

type failedTradeOutput struct{}

func (failedTradeOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
