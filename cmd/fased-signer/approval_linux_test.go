//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
)

func ownerProposal(v *signer.Vault) signer.FixtureProposal {
	ref := *v.Status().Account
	p := signer.FixturePolicy{Account: ref, Asset: signer.FixtureAsset, Budget: 100, MaxOperation: 10, Destinations: []string{"fixture:treasury"}, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	g := signer.FixtureGrant{Account: ref, ID: strings.Repeat("a", 32), BotID: strings.Repeat("b", 32), Asset: p.Asset, Budget: 50, MaxOperation: 10, Destinations: p.Destinations, ExpiresAt: p.ExpiresAt}
	return signer.FixtureProposal{Version: 1, ID: g.ID, Policy: p, Grant: g}
}
func TestProtectedApprovalReviewCancelCredentialsAndLock(t *testing.T) {
	v := flowVault(t)
	if e := v.Create(flowUnlock); e != nil {
		t.Fatal(e)
	}
	p := ownerProposal(v)
	base := flowCall(v)
	call := func(ctx context.Context, r signer.Request) (signer.Response, error) {
		if r.Action == "fixture-approve-proposal" {
			a, e := v.ApproveProposal(*r.Proposal)
			return signer.Response{Status: v.Status(), Approvals: a}, e
		}
		return base(ctx, r)
	}
	var review, out bytes.Buffer
	asked := false
	ask := func(string, bool) ([]byte, error) { asked = true; return bytes.Clone(flowUnlock), nil }
	cancel := func(string) error { return errors.New("owner declined") }
	if e := conductApproval(t.Context(), p, ask, cancel, &review, &out, call); e == nil || asked {
		t.Fatal("cancel asked for a credential or granted authority")
	}
	r, e := v.ApprovalReceipts([]string{p.ID})
	if e != nil || r[0].State != "not-approved" {
		t.Fatal("cancel persisted approval")
	}
	confirm := func(s string) error {
		if s != "APPROVE "+signer.ProposalDigest(p) || !bytes.Contains(review.Bytes(), []byte(p.Policy.Account.PublicKey)) || !bytes.Contains(review.Bytes(), []byte(`"budget": "100"`)) {
			t.Fatal("review omitted exact binding/limits")
		}
		return nil
	}
	if e = conductApproval(t.Context(), p, ask, confirm, &review, &out, call); e != nil {
		t.Fatal(e)
	}
	if !v.Status().Locked || bytes.Contains(review.Bytes(), flowUnlock) || bytes.Contains(out.Bytes(), flowUnlock) {
		t.Fatal("credential leak or unlocked completion")
	}
	p.ID = strings.Repeat("c", 32)
	p.Grant.ID = p.ID
	badAsk := func(string, bool) ([]byte, error) { return []byte("Wrong disposable password"), nil }
	if e = conductApproval(t.Context(), p, badAsk, func(string) error { return nil }, &review, &out, call); e == nil || !v.Status().Locked {
		t.Fatal("wrong password granted or left unlocked")
	}
}
func TestPublicProposalFileDoesNotRelaxBackupPrivacy(t *testing.T) {
	dir := ownerFixture(t)
	if e := os.Chmod(dir, 0755); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(dir, "proposal.json")
	if e := os.WriteFile(file, []byte(`{}`), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := readPublicProposal(file); e != nil {
		t.Fatal(e)
	}
	if _, e := readOwnerFile(file); e == nil {
		t.Fatal("public directory accepted for secret backup")
	}
	if e := os.Chmod(file, 0666); e != nil {
		t.Fatal(e)
	}
	if _, e := readPublicProposal(file); e == nil {
		t.Fatal("writable public input accepted")
	}
	os.Chmod(file, 0644)
	link := filepath.Join(dir, "link.json")
	if e := os.Symlink(file, link); e != nil {
		t.Fatal(e)
	}
	if _, e := readPublicProposal(link); e == nil {
		t.Fatal("symlinked proposal")
	}
}
