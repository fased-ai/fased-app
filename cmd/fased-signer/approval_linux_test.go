//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/onchaintest"
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

func TestProtectedSwapApprovalConsentAndBoundedLease(t *testing.T) {
	v := flowVault(t)
	if e := v.Create(flowUnlock); e != nil {
		t.Fatal(e)
	}
	chain := &onchaintest.Chain{}
	client := onchaintest.Client(chain)
	if e := v.ConfigureSwapObserver(client); e != nil {
		t.Fatal(e)
	}
	ref := *v.Status().Account
	public, _ := hex.DecodeString(ref.PublicKey)
	now := time.Now().Unix()
	q, e := client.Prepare(t.Context(), onchain.Quote{Version: onchain.Version, ID: strings.Repeat("1", 32), ProfileID: "trading-analyst", InstanceID: strings.Repeat("2", 32), AccountID: strings.Repeat("3", 32), Network: "mainnet-beta", Owner: onchain.Base58(public), RPCSHA256: strings.Repeat("4", 64), InputMint: onchain.USDC, OutputMint: onchain.SOL, Amount: "12500000", SlippageBPS: 50, MaxFeeLamports: 5000, CreatedAt: now, ExpiresAt: now + 45})
	if e != nil {
		t.Fatal(e)
	}
	p := signer.NewSwapProposal(ref, q)
	var review, out bytes.Buffer
	asked := false
	base := flowCall(v)
	call := func(ctx context.Context, r signer.Request) (signer.Response, error) {
		if r.Action == "unlock" {
			e := v.Unlock(r.Password, time.Duration(r.UnlockSeconds)*time.Second)
			return signer.Response{Status: v.Status()}, e
		}
		if r.Action == "swap-approve" {
			x, e := v.ApproveSwap(*r.Swap)
			return signer.Response{Swap: x}, e
		}
		return base(ctx, r)
	}
	ask := func(string, bool) ([]byte, error) { asked = true; return bytes.Clone(flowUnlock), nil }
	if e = conductSwapApproval(t.Context(), p, ask, func(string) error { return errors.New("cancel") }, &review, &out, call); e == nil || asked {
		t.Fatal("cancel prompted for password")
	}
	if e = conductSwapApproval(t.Context(), p, ask, func(s string) error {
		for _, text := range []string{q.Owner, q.Network, "12.5 USDC", "0.1 wSOL", "0.0995 wSOL", "Rent:", "5000 lamports"} {
			if !strings.Contains(review.String(), text) {
				return errors.New("trusted approval summary missing " + text)
			}
		}
		if s != "APPROVE "+p.Digest {
			return errors.New("digest differs")
		}
		return nil
	}, &review, &out, call); e != nil {
		t.Fatal(e)
	}
	status := v.Status()
	if status.Locked || status.UnattendedUnlock || status.UnlockUntil.Unix() > q.ExpiresAt {
		t.Fatal("unbounded lease")
	}
	if _, e = v.SignSwap(q.ID); e != nil {
		t.Fatal(e)
	}
}

func TestProtectedManagedSwapApprovalUsesScopedCapability(t *testing.T) {
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	chain := &onchaintest.Chain{}
	client := onchaintest.Client(chain)
	v, e := signer.OpenManagedSwapFixture(dir, strings.Repeat("e", 32), "mainnet-beta", client)
	if e != nil {
		t.Fatal(e)
	}
	defer v.Close()
	if e = v.Create(flowUnlock); e != nil {
		t.Fatal(e)
	}
	ref := *v.Status().Account
	pub, _ := hex.DecodeString(ref.PublicKey)
	now := time.Now().Unix()
	q, e := client.Prepare(t.Context(), onchain.Quote{Version: onchain.Version, ID: strings.Repeat("1", 32), ProfileID: "trading-analyst", InstanceID: strings.Repeat("2", 32), AccountID: strings.Repeat("3", 32), Network: ref.Network, Owner: onchain.Base58(pub), RPCSHA256: strings.Repeat("4", 64), InputMint: onchain.USDC, OutputMint: onchain.SOL, Amount: "12500000", SlippageBPS: 50, MaxFeeLamports: 5000, CreatedAt: now, ExpiresAt: now + 45})
	if e != nil {
		t.Fatal(e)
	}
	p := signer.NewSwapProposal(ref, q)
	call := func(ctx context.Context, r signer.Request) (signer.Response, error) {
		switch r.Action {
		case "status":
			return signer.Response{Status: v.Status()}, nil
		case "unlock":
			e := v.Unlock(r.Password, time.Duration(r.UnlockSeconds)*time.Second)
			return signer.Response{Status: v.Status()}, e
		case "lock":
			v.Lock()
			return signer.Response{}, nil
		case "swap-approve":
			x, e := v.ApproveSwap(*r.Swap)
			return signer.Response{Swap: x, Status: v.Status()}, e
		}
		return signer.Response{}, errors.New("unexpected owner operation")
	}
	var review, out bytes.Buffer
	asked := false
	ask := func(string, bool) ([]byte, error) { asked = true; return bytes.Clone(flowUnlock), nil }
	if e = conductSwapApproval(t.Context(), p, ask, func(string) error { return errors.New("cancel") }, &review, &out, call); e == nil || asked {
		t.Fatal("cancel unlocked")
	}
	if e = conductSwapApproval(t.Context(), p, ask, func(s string) error {
		if s != "APPROVE "+p.Digest {
			return errors.New("wrong identity")
		}
		return nil
	}, &review, &out, call); e != nil {
		t.Fatal(e)
	}
	if _, e = v.SignSwap(q.ID); e != nil {
		t.Fatal(e)
	}
	if !v.Status().RecoveryRequired || v.Status().UnlockUntil.Unix() > q.ExpiresAt {
		t.Fatal("generic hold or bounded lease lost")
	}
	if !strings.Contains(review.String(), q.Owner) || !strings.Contains(review.String(), "12.5 USDC") {
		t.Fatal("trusted review incomplete")
	}
}
