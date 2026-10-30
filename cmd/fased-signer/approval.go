package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/onchain"
	"io"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
)

func conductApproval(ctx context.Context, p signer.FixtureProposal, ask askPassword, confirm func(string) error, review, out io.Writer, call ownerCall) (result error) {
	if e := p.Validate(time.Now().Unix()); e != nil {
		return e
	}
	status, e := call(ctx, signer.Request{Action: "status"})
	if e != nil {
		return e
	}
	if status.Status.Account == nil || *status.Status.Account != p.Policy.Account || status.Status.RecoveryRequired {
		return errors.New("proposal is stale, held or belongs to another signer account")
	}
	digest := signer.ProposalDigest(p)
	b, _ := json.MarshalIndent(p, "", "  ")
	fmt.Fprintf(review, "DISPOSABLE FIXTURE ONLY — no real wallet execution\nReview the complete public mandate. Wallet budget is cumulative, including earlier spent/held units.\n%s\nDigest: %s\n", b, digest)
	if e = confirm("APPROVE " + digest); e != nil {
		return e
	}
	defer func() {
		lockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, le := call(lockCtx, signer.Request{Action: "lock"})
		result = errors.Join(result, le)
	}()
	pw, e := ask("Fixture unlock password", false)
	defer clear(pw)
	if e != nil {
		return e
	}
	if _, e = call(ctx, signer.Request{Action: "unlock", Password: pw, UnlockSeconds: 60}); e != nil {
		return e
	}
	r, e := call(ctx, signer.Request{Action: "fixture-approve-proposal", PublicInput: signer.PublicInput{Proposal: &p}})
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(map[string]any{"approvals": r.Approvals, "fixtureOnly": true, "financialExecution": false})
}

// conductSwapApproval leaves only a short, explicitly approved signing lease.
// Neither the browser nor the core handles the owner's password.
func conductSwapApproval(ctx context.Context, p signer.SwapProposal, ask askPassword, confirm func(string) error, review, out io.Writer, call ownerCall) (result error) {
	raw, _ := json.Marshal(p)
	if _, e := signer.ParseSwapProposal(raw); e != nil {
		return e
	}
	status, e := call(ctx, signer.Request{Action: "status"})
	if e != nil {
		return e
	}
	if status.Status.Account == nil || *status.Status.Account != p.Account || status.Status.UpdateHeld || status.Status.RecoveryRequired && !(p.Account.Version == 2 && status.Status.SwapReady) {
		return errors.New("wallet changed or protected swap history unavailable")
	}
	fmt.Fprintf(review, "Disposable swap\nWallet: %s\nNetwork: %s\nPay: %s\nReceive: %s (minimum %s)\nFee cap: %d lamports\nRent: %d lamports\nExpires: %s\nDigest: %s\n", p.Quote.Owner, p.Quote.Network, swapAmount(p.Quote.Amount, p.Quote.InputMint), swapAmount(p.Quote.Output, p.Quote.OutputMint), swapAmount(p.Quote.MinimumOutput, p.Quote.OutputMint), p.Quote.MaxFeeLamports, p.Quote.RentLamports, time.Unix(p.Quote.ExpiresAt, 0).UTC().Format(time.RFC3339), p.Digest)
	if e = confirm("APPROVE " + p.Digest); e != nil {
		return e
	}
	pw, e := ask("Signer password", false)
	defer clear(pw)
	if e != nil {
		return e
	}
	seconds := p.Quote.ExpiresAt - time.Now().Unix()
	if seconds < 1 || seconds > 60 {
		return errors.New("swap review expired")
	}
	if _, e = call(ctx, signer.Request{Action: "unlock", Password: pw, UnlockSeconds: int(seconds)}); e != nil {
		return e
	}
	defer func() {
		if result != nil {
			lockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, le := call(lockCtx, signer.Request{Action: "lock"})
			result = errors.Join(result, le)
		}
	}()
	r, e := call(ctx, signer.Request{Action: "swap-approve", PublicInput: signer.PublicInput{Swap: &p}})
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(map[string]any{"swap": r.Swap, "unlockExpiresAt": p.Quote.ExpiresAt, "liveExecution": false})
}

func swapAmount(raw, mint string) string {
	decimals, symbol := 6, "USDC"
	if mint == onchain.SOL {
		decimals, symbol = 9, "wSOL"
	}
	if len(raw) <= decimals {
		raw = strings.Repeat("0", decimals+1-len(raw)) + raw
	}
	whole, fraction := raw[:len(raw)-decimals], strings.TrimRight(raw[len(raw)-decimals:], "0")
	if fraction != "" {
		whole += "." + fraction
	}
	return whole + " " + symbol
}
