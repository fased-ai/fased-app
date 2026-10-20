package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
