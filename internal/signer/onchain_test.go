package signer

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/onchaintest"
)

func swapFixture(t *testing.T) (*Vault, SwapProposal, *onchaintest.Chain, string) {
	t.Helper()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	v, e := Open(dir, strings.Repeat("e", 32))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { v.Close() })
	pw := []byte("disposable-swap-signer-password")
	if e = v.Create(pw); e != nil {
		t.Fatal(e)
	}
	if e = v.Unlock(pw, time.Minute); e != nil {
		t.Fatal(e)
	}
	chain := &onchaintest.Chain{}
	client := onchaintest.Client(chain)
	if e = v.ConfigureSwapObserver(client); e != nil {
		t.Fatal(e)
	}
	ref := *v.Status().Account
	pub, _ := onchainOwner(ref)
	now := time.Now().Unix()
	q, e := client.Prepare(t.Context(), onchain.Quote{Version: onchain.Version, ID: strings.Repeat("1", 32), ProfileID: "trading-analyst", InstanceID: strings.Repeat("2", 32), AccountID: strings.Repeat("3", 32), Network: "mainnet-beta", Owner: pub, RPCSHA256: strings.Repeat("4", 64), InputMint: onchain.USDC, OutputMint: onchain.SOL, Amount: "12500000", SlippageBPS: 50, MaxFeeLamports: 5000, CreatedAt: now, ExpiresAt: now + 45})
	if e != nil {
		t.Fatal(e)
	}
	return v, NewSwapProposal(ref, q), chain, dir
}
func onchainOwner(ref AccountRef) (string, error) {
	b, e := hex.DecodeString(ref.PublicKey)
	return onchain.Base58(b), e
}
func TestSwapOwnerRolesExactApprovalAndJournalRecovery(t *testing.T) {
	v, p, chain, dir := swapFixture(t)
	if got := execute(v, Request{Action: "swap-approve", PublicInput: PublicInput{Swap: &p}}, false); got.Error == "" {
		t.Fatal("app approved swap")
	}
	if got := execute(v, Request{Action: "swap-sign", PublicInput: PublicInput{OperationID: p.Quote.ID, Swap: &p}}, false); got.Error == "" {
		t.Fatal("mixed request accepted")
	}
	if _, e := v.SignSwap(p.Quote.ID); e == nil {
		t.Fatal("unapproved signature")
	}
	forged := p
	forged.Quote.Blockhash = "11111111111111111111111111111111"
	forged.Quote.Digest = onchain.QuoteDigest(forged.Quote)
	forged.Digest = SwapProposalDigest(forged)
	if _, e := v.ApproveSwap(forged); e == nil {
		t.Fatal("public-chain blockhash admitted")
	}
	if _, e := v.ApproveSwap(p); e != nil {
		t.Fatal(e)
	}
	if _, e := v.ApproveSwap(p); e == nil {
		t.Fatal("duplicate approval")
	}
	signed, e := v.SignSwap(p.Quote.ID)
	if e != nil {
		t.Fatal(e)
	}
	repeated, e := v.SignSwap(p.Quote.ID)
	if e != nil || string(repeated.Signed) != string(signed.Signed) {
		t.Fatal("second signature identity", e)
	}
	second := p
	second.Quote.ID = strings.Repeat("5", 32)
	second.Quote.Digest = onchain.QuoteDigest(second.Quote)
	second.Digest = SwapProposalDigest(second)
	if _, e := v.ApproveSwap(second); e == nil {
		t.Fatal("unknown hold allowed another approval")
	}
	snapshot, e := v.updateSnapshot(strings.Repeat("a", 32))
	if e != nil || snapshot.Unknown != 1 || snapshot.Pending != 1 {
		t.Fatal("unknown hold lost", e)
	}
	id := v.signerID
	if e = v.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(dir, id)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if e = reopened.ConfigureSwapObserver(onchaintest.Client(chain)); e != nil {
		t.Fatal(e)
	}
	original, e := reopened.SwapReceipt(p.Quote.ID)
	if e != nil || string(original.Signed) != string(signed.Signed) {
		t.Fatal("durable original missing", e)
	}
	if _, e = reopened.SignSwap(p.Quote.ID); e == nil {
		t.Fatal("restart unlocked key")
	}
	if _, e = onchaintest.Client(chain).Send(t.Context(), p.Quote, signed.Signed); e != nil {
		t.Fatal(e)
	}
	settled, e := reopened.ReconcileSwap(context.Background(), p.Quote.ID)
	if e != nil || settled.State != "confirmed" {
		t.Fatal("independent reconciliation", e)
	}
	snapshot, e = reopened.updateSnapshot(strings.Repeat("a", 32))
	if e != nil || snapshot.Unknown != 0 || snapshot.Pending != 0 {
		t.Fatal("settled update hold", e)
	}
}
func TestSwapExpiredApprovalAndOrdinaryStartupRemainClosed(t *testing.T) {
	v, p, _, _ := swapFixture(t)
	v.now = func() time.Time { return time.Unix(p.Quote.ExpiresAt, 0) }
	if _, e := v.ApproveSwap(p); e == nil {
		t.Fatal("expired approval")
	}
	raw, _ := json.Marshal(p)
	if _, e := ParseSwapProposal(append(raw, []byte(" {}")...)); e == nil {
		t.Fatal("trailing document")
	}
	ordinaryDir := t.TempDir()
	if e := os.Chmod(ordinaryDir, 0700); e != nil {
		t.Fatal(e)
	}
	ordinary, e := Open(ordinaryDir, strings.Repeat("d", 32))
	if e != nil {
		t.Fatal(e)
	}
	defer ordinary.Close()
	if _, e = ordinary.ApproveSwap(p); e == nil {
		t.Fatal("ordinary constructor admitted swap")
	}
	if _, e = ordinary.SignSwap(p.Quote.ID); e == nil {
		t.Fatal("ordinary signer signed")
	}
	if os.Getuid() == 0 {
		t.Fatal("no root test execution")
	}
}

func TestSwapKeyRecoveryRetainsUnknownUntilIndependentSettlement(t *testing.T) {
	v, p, chain, dir := swapFixture(t)
	backupPassword := []byte("disposable-swap-backup-password")
	backup, e := v.Backup(backupPassword)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(backup)
	if _, e = v.ApproveSwap(p); e != nil {
		t.Fatal(e)
	}
	signed, e := v.SignSwap(p.Quote.ID)
	if e != nil {
		t.Fatal(e)
	}
	id := v.signerID
	v.Close()
	// Disposable fixture key loss only. Application removal never deletes keys.
	if e = os.Remove(filepath.Join(dir, "vault.json")); e != nil {
		t.Fatal(e)
	}
	restored, e := Open(dir, id)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	restored.ConfigureSwapObserver(onchaintest.Client(chain))
	if _, e = restored.ReconcileSwap(t.Context(), p.Quote.ID); e == nil {
		t.Fatal("keyless history accepted")
	}
	password := []byte("disposable-restored-swap-password")
	if e = restored.Restore(backup, backupPassword, password); e != nil {
		t.Fatal(e)
	}
	ref := *restored.Status().Account
	if ref.Epoch == p.Account.Epoch || !restored.Status().RecoveryRequired {
		t.Fatal("recovery hold/epoch lost")
	}
	if e = restored.Unlock(password, time.Minute); e != nil {
		t.Fatal(e)
	}
	if e = restored.RecoverFixtureAuthority(ref); e == nil {
		t.Fatal("unknown swap erased by epoch recovery")
	}
	if _, e = onchaintest.Client(chain).Send(t.Context(), p.Quote, signed.Signed); e != nil {
		t.Fatal(e)
	}
	r, e := restored.ReconcileSwap(t.Context(), p.Quote.ID)
	if e != nil || r.State != "confirmed" || string(r.Signed) != string(signed.Signed) {
		t.Fatal("original recovery settlement", e)
	}
	if e = restored.RecoverFixtureAuthority(ref); e != nil || restored.Status().RecoveryRequired {
		t.Fatal("settled retained history recovery", e)
	}
	if _, e = restored.SignSwap(p.Quote.ID); e == nil {
		t.Fatal("old epoch signed after recovery")
	}
}
