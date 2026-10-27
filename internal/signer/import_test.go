//go:build linux

package signer

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"testing"
	"time"
)

func TestDisposableImportIdentityRecoveryAndRefusal(t *testing.T) {
	dir := fixtureDir(t)
	v := openFixture(t, dir)
	seed := bytes.Repeat([]byte{0x42}, 32)
	for _, bad := range [][]byte{nil, seed[:31], append(bytes.Clone(seed), 0)} {
		requireError(t, v.ImportFixtureSeed(bad, fixturePassword))
		if v.Status().Account != nil {
			t.Fatal("invalid seed persisted")
		}
	}
	requireError(t, v.ImportFixtureSeed(seed, []byte("short")))
	if err := v.ImportFixtureSeed(seed, fixturePassword); err != nil {
		t.Fatal(err)
	}
	initial := v.Status()
	private := ed25519.NewKeyFromSeed(seed)
	if initial.Account.PublicKey != hex.EncodeToString(private[32:]) || !initial.Locked || !initial.RecoveryRequired || initial.WalletSigner || initial.FinancialExecution {
		t.Fatal("unsafe import", initial)
	}
	requireError(t, v.ImportFixtureSeed(seed, fixturePassword))
	requireError(t, v.Create(fixturePassword))
	if err := v.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	backup, err := v.Backup(backupPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err = v.VerifyBackup(backup, backupPassword); err != nil {
		t.Fatal(err)
	}
	v.Close()
	v = openFixture(t, dir)
	if *v.Status().Account != *initial.Account || !v.Status().RecoveryRequired || !v.Status().Locked {
		t.Fatal("restart lost hold or identity")
	}
	restored := openFixture(t, fixtureDir(t))
	if err = restored.Restore(backup, backupPassword, fixturePassword); err != nil {
		t.Fatal(err)
	}
	if restored.Status().Account.PublicKey != initial.Account.PublicKey || restored.Status().Account.Epoch == initial.Account.Epoch || !restored.Status().RecoveryRequired {
		t.Fatal("recovery lost identity or fresh authority")
	}
	second := openFixture(t, fixtureDir(t))
	if err = second.ImportFixtureSeed(seed, fixturePassword); err != nil {
		t.Fatal(err)
	}
	if second.Status().Account.WalletID == initial.Account.WalletID || second.Status().Account.Epoch == initial.Account.Epoch {
		t.Fatal("import reused authority")
	}
}

func TestDisposableImportRPCBoundary(t *testing.T) {
	v := openFixture(t, fixtureDir(t))
	for _, owner := range []bool{false, true} {
		seed := bytes.Repeat([]byte{3}, 32)
		r := execute(v, Request{Action: "import-fixture-seed", Document: seed, Password: bytes.Clone(fixturePassword)}, owner)
		if (r.Error == "") != owner || !bytes.Equal(seed, make([]byte, 32)) {
			t.Fatal("owner boundary or clearing", r.Error)
		}
	}
}
