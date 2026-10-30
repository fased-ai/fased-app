//go:build linux

package signer

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/onchaintest"
)

func managedSwapQuote(t *testing.T, v *Vault, chain *onchaintest.Chain) SwapProposal {
	t.Helper()
	ref := *v.Status().Account
	pub, _ := hex.DecodeString(ref.PublicKey)
	now := time.Now().Unix()
	q, e := onchaintest.Client(chain).Prepare(t.Context(), onchain.Quote{Version: onchain.Version, ID: randomID(), ProfileID: "trading-analyst", InstanceID: strings.Repeat("2", 32), AccountID: strings.Repeat("3", 32), Network: ref.Network, Owner: onchain.Base58(pub), RPCSHA256: strings.Repeat("4", 64), InputMint: onchain.USDC, OutputMint: onchain.SOL, Amount: "12500000", SlippageBPS: 50, MaxFeeLamports: 5000, CreatedAt: now, ExpiresAt: now + 45})
	if e != nil {
		t.Fatal(e)
	}
	return NewSwapProposal(ref, q)
}
func TestManagedSwapCreatedWalletBoundedApproval(t *testing.T) {
	dir := fixtureDir(t)
	v := managedFixture(t, dir, "mainnet-beta")
	if e := v.Create(fixturePassword); e != nil {
		t.Fatal(e)
	}
	chain := &onchaintest.Chain{}
	if e := v.ConfigureSwapObserver(onchaintest.Client(chain)); e != nil {
		t.Fatal("installed-wallet swap construction refused", e)
	}
	p := managedSwapQuote(t, v, chain)
	if got := execute(v, Request{Action: "swap-approve", PublicInput: PublicInput{Swap: &p}}, false); got.Error == "" {
		t.Fatal("app approved its own intent")
	}
	if _, e := v.SignSwap(p.Quote.ID); e == nil {
		t.Fatal("signature without protected approval")
	}
	if e := v.Unlock(fixturePassword, time.Minute); e != nil {
		t.Fatal(e)
	}
	if _, e := v.ApproveSwap(p); e != nil {
		t.Fatal(e)
	}
	signed, e := v.SignSwap(p.Quote.ID)
	if e != nil {
		t.Fatal(e)
	}
	snap, e := v.UpdateInventory(randomID())
	if e != nil || snap.Unknown != 1 || snap.Pending != 1 {
		t.Fatal("managed hold omitted", e, snap)
	}
	if !v.Status().RecoveryRequired {
		t.Fatal("general recovery hold was cleared")
	}
	v.Close()
	v = managedFixture(t, dir, "mainnet-beta")
	if e = v.ConfigureSwapObserver(onchaintest.Client(chain)); e != nil {
		t.Fatal(e)
	}
	if _, e = v.SignSwap(p.Quote.ID); e == nil {
		t.Fatal("restart unlocked a key")
	}
	retained, e := v.SwapReceipt(p.Quote.ID)
	if e != nil || !bytes.Equal(retained.Signed, signed.Signed) {
		t.Fatal("signed original lost", e)
	}
	if _, e = onchaintest.Client(chain).Send(t.Context(), p.Quote, signed.Signed); e != nil {
		t.Fatal(e)
	}
	result, e := v.ReconcileSwap(context.Background(), p.Quote.ID)
	if e != nil || result.State != "confirmed" {
		t.Fatal(e, result)
	}
	snap, e = v.UpdateInventory(randomID())
	if e != nil || snap.Unknown != 0 || snap.Pending != 0 {
		t.Fatal("settled hold", e, snap)
	}
	if !v.Status().RecoveryRequired {
		t.Fatal("settlement released unrelated recovery hold")
	}
}
func TestManagedSwapImportsAndKeyOnlyRestoreRemainHeld(t *testing.T) {
	fresh := managedFixture(t, fixtureDir(t), "mainnet-beta")
	if e := fresh.Create(fixturePassword); e != nil {
		t.Fatal(e)
	}
	if e := fresh.Unlock(fixturePassword, time.Minute); e != nil {
		t.Fatal(e)
	}
	backup, e := fresh.Backup(backupPassword)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(backup)
	imported := managedFixture(t, fixtureDir(t), "mainnet-beta")
	if e = imported.importManagedSeed(bytes.Repeat([]byte{9}, 32), fixturePassword); e != nil {
		t.Fatal(e)
	}
	restored := managedFixture(t, fixtureDir(t), "mainnet-beta")
	if e = restored.Restore(backup, backupPassword, fixturePassword); e != nil {
		t.Fatal(e)
	}
	for _, v := range []*Vault{imported, restored} {
		chain := &onchaintest.Chain{}
		_ = v.ConfigureSwapObserver(onchaintest.Client(chain))
		if e = v.Unlock(fixturePassword, time.Minute); e != nil {
			t.Fatal(e)
		}
		if _, e = v.ApproveSwap(managedSwapQuote(t, v, chain)); e == nil {
			t.Fatal("incomplete history gained authority")
		}
		if !v.Status().RecoveryRequired {
			t.Fatal("recovery hold lost")
		}
	}
	// Removing all provenance/history must not reconstruct it from a key backup.
	keyOnlyDir := fixtureDir(t)
	if e = os.WriteFile(filepath.Join(keyOnlyDir, "unrelated"), []byte("preserve"), 0600); e != nil {
		t.Fatal(e)
	}
	keyOnly := managedFixture(t, keyOnlyDir, "mainnet-beta")
	if e = keyOnly.Restore(backup, backupPassword, fixturePassword); e != nil {
		t.Fatal(e)
	}
	_ = keyOnly.ConfigureSwapObserver(onchaintest.Client(&onchaintest.Chain{}))
	if e = keyOnly.Unlock(fixturePassword, time.Minute); e != nil {
		t.Fatal(e)
	}
	if _, e = keyOnly.ApproveSwap(managedSwapQuote(t, keyOnly, &onchaintest.Chain{})); e == nil {
		t.Fatal("key-only restoration admitted")
	}
}

func TestManagedSwapRecoveryKeepsOriginalWithoutRestoringAuthority(t *testing.T) {
	dir := fixtureDir(t)
	v := managedFixture(t, dir, "mainnet-beta")
	if e := v.Create(fixturePassword); e != nil {
		t.Fatal(e)
	}
	chain := &onchaintest.Chain{}
	client := onchaintest.Client(chain)
	if e := v.ConfigureSwapObserver(client); e != nil {
		t.Fatal(e)
	}
	if e := v.Unlock(fixturePassword, time.Minute); e != nil {
		t.Fatal(e)
	}
	backup, e := v.Backup(backupPassword)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(backup)
	var b envelope
	if e = json.Unmarshal(backup, &b); e != nil || b.FreshManaged || b.Version != 2 {
		t.Fatal("backup inherited authority", e)
	}
	p := managedSwapQuote(t, v, chain)
	if _, e = v.ApproveSwap(p); e != nil {
		t.Fatal(e)
	}
	signed, e := v.SignSwap(p.Quote.ID)
	if e != nil {
		t.Fatal(e)
	}
	v.Close()
	if e = os.Remove(filepath.Join(dir, "vault.json")); e != nil {
		t.Fatal(e)
	} // disposable simulated key loss only
	recovered := managedFixture(t, dir, "mainnet-beta")
	if e = recovered.Restore(backup, backupPassword, fixturePassword); e != nil {
		t.Fatal(e)
	}
	if e = recovered.ConfigureSwapObserver(client); e != nil {
		t.Fatal(e)
	}
	if e = recovered.Unlock(fixturePassword, time.Minute); e != nil {
		t.Fatal(e)
	}
	if recovered.Status().SwapReady || !recovered.Status().RecoveryRequired {
		t.Fatal("restore granted fresh authority")
	}
	if _, e = recovered.SignSwap(p.Quote.ID); e == nil {
		t.Fatal("old epoch signed")
	}
	snap, e := recovered.UpdateInventory(randomID())
	if e != nil || snap.Unknown != 1 {
		t.Fatal("recovery lost unknown hold", e)
	}
	if _, e = client.Send(t.Context(), p.Quote, signed.Signed); e != nil {
		t.Fatal(e)
	}
	settled, e := recovered.ReconcileSwap(t.Context(), p.Quote.ID)
	if e != nil || settled.State != "confirmed" || !bytes.Equal(settled.Signed, signed.Signed) {
		t.Fatal("original settlement", e)
	}
	if recovered.Status().SwapReady || !recovered.Status().RecoveryRequired {
		t.Fatal("settlement implied recovery consent")
	}
}
func TestManagedSwapNetworkEpochBoundsAndInterruption(t *testing.T) {
	dir := fixtureDir(t)
	v := managedFixture(t, dir, "mainnet-beta")
	if e := v.Create(fixturePassword); e != nil {
		t.Fatal(e)
	}
	chain := &onchaintest.Chain{}
	client := onchaintest.Client(chain)
	if e := v.ConfigureSwapObserver(client); e != nil {
		t.Fatal(e)
	}
	if e := v.Unlock(fixturePassword, time.Minute); e != nil {
		t.Fatal(e)
	}
	p := managedSwapQuote(t, v, chain)
	for _, which := range []string{"network", "epoch", "amount", "fee", "blockhash"} {
		changed := p
		switch which {
		case "network":
			changed.Quote.Network = "devnet"
		case "epoch":
			changed.Account.Epoch = randomID()
		case "amount":
			changed.Quote.Amount = "1"
		case "fee":
			changed.Quote.MaxFeeLamports = 1
		case "blockhash":
			changed.Quote.Blockhash = "11111111111111111111111111111111"
		}
		changed.Quote.Digest = onchain.QuoteDigest(changed.Quote)
		changed.Digest = SwapProposalDigest(changed)
		if _, e := v.ApproveSwap(changed); e == nil {
			t.Fatal("wrong boundary admitted", which)
		}
	}
	if _, e := v.ApproveSwap(p); e != nil {
		t.Fatal(e)
	}
	v.store.crash = func(phase string) error {
		if phase == "journal:onchain-sign:durable" {
			return errors.New("disposable interrupted sign")
		}
		return nil
	}
	if _, e := v.SignSwap(p.Quote.ID); e == nil {
		t.Fatal("interruption not observed")
	}
	v.Close()
	v = managedFixture(t, dir, "mainnet-beta")
	r, e := v.SwapReceipt(p.Quote.ID)
	if e != nil || r.State != "signed" || len(r.Signed) == 0 {
		t.Fatal("durable sign lost", e, r)
	}
	if !v.Status().Locked || v.Status().SwapReady {
		t.Fatal("restart inherited runtime permission")
	}
}

func TestManagedSwapInstalledConstructorRejectsUntrustedIdentity(t *testing.T) {
	c := Config{Version: 2, Mode: ManagedFixtureMode, Network: "mainnet-beta", SignerID: randomID(), OwnerUID: os.Getuid() + 1, AppUID: os.Getuid() + 2, SignerUID: os.Getuid(), StateDir: fixtureDir(t), SocketDir: fixtureDir(t)}
	if server, e := ServeControlledSwapFixture(t.Context(), c, onchaintest.Client(&onchaintest.Chain{})); e == nil {
		server.Close()
		t.Fatal("uninstalled owner-controlled source promoted to installed signer")
	}
}
