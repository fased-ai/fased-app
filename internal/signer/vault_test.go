//go:build linux

package signer

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var fixturePassword = []byte("Disposable unlock password")
var backupPassword = []byte("Disposable backup password")

func fixtureDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../.evidence/p3-signer")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "unit-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if filepath.Dir(dir) != root {
			t.Fatal("fixture cleanup escaped root")
		}
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	})
	return dir
}
func openFixture(t *testing.T, dir string) *Vault {
	t.Helper()
	v, err := Open(dir, "11111111111111111111111111111111")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	return v
}
func requireError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("operation unexpectedly allowed")
	}
}

func TestVaultBackupRecoveryAndRestart(t *testing.T) {
	dir := fixtureDir(t)
	v := openFixture(t, dir)
	if err := v.Create(fixturePassword); err != nil {
		t.Fatal(err)
	}
	initial := v.Status()
	if !initial.Locked || initial.WalletSigner || initial.FinancialExecution || initial.UnattendedUnlock || !initial.FixtureOnly {
		t.Fatal("capability or creation lock")
	}
	requireError(t, v.Create(fixturePassword))
	if _, err := Open(dir, v.signerID); err == nil {
		t.Fatal("second signer obtained storage lock")
	}
	if err := v.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	seed, err := v.ExportFixtureSeed()
	if err != nil {
		t.Fatal(err)
	}
	defer wipe(seed)
	stored, err := os.ReadFile(filepath.Join(dir, "vault.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, seed) || bytes.Contains(stored, fixturePassword) {
		t.Fatal("plaintext persisted")
	}
	backup, err := v.Backup(backupPassword)
	if err != nil {
		t.Fatal(err)
	}
	second, err := v.Backup(backupPassword)
	if err != nil || bytes.Equal(backup, second) {
		t.Fatal("backup encryption randomness")
	}
	requireError(t, v.Unlock([]byte("Wrong disposable password"), time.Minute))
	if !v.Status().Locked {
		t.Fatal("wrong unlock left key available")
	}
	if _, err = v.ExportFixtureSeed(); !errors.Is(err, ErrLocked) {
		t.Fatal("locked export")
	}
	if err = v.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	v.Close()
	requireError(t, v.Unlock(fixturePassword, time.Minute))
	v = openFixture(t, dir)
	if !v.Status().Locked || *v.Status().Account != *initial.Account {
		t.Fatal("restart changed identity or auto-unlocked")
	}

	restored, err := Open(fixtureDir(t), "22222222222222222222222222222222")
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	requireError(t, restored.Restore(backup, fixturePassword, fixturePassword))
	if restored.Status().Account != nil {
		t.Fatal("failed restore changed empty state")
	}
	newPassword := []byte("Replacement disposable password")
	if err = restored.Restore(backup, backupPassword, newPassword); err != nil {
		t.Fatal(err)
	}
	s := restored.Status()
	if !s.Locked || !s.RecoveryRequired || s.Account.PublicKey != initial.Account.PublicKey || s.Account.WalletID != initial.Account.WalletID || s.Account.Epoch == initial.Account.Epoch || s.Account.SignerID == initial.Account.SignerID {
		t.Fatal("restore identity/hold/epoch")
	}
	requireError(t, restored.Restore(backup, backupPassword, newPassword))
	requireError(t, restored.Unlock(fixturePassword, time.Minute))
	if err = restored.Unlock(newPassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	recovered, err := restored.ExportFixtureSeed()
	if err != nil {
		t.Fatal(err)
	}
	defer wipe(recovered)
	if !bytes.Equal(seed, recovered) || !restored.Status().RecoveryRequired || restored.Status().WalletSigner {
		t.Fatal("key recovery or recovery hold bypass")
	}
	if r := execute(restored, Request{Action: "clear-recovery-hold"}, true); r.Error == "" {
		t.Fatal("hold cleared")
	}
}

func TestCorruptBackupAndDomainSeparation(t *testing.T) {
	v := openFixture(t, fixtureDir(t))
	if err := v.Create(fixturePassword); err != nil {
		t.Fatal(err)
	}
	if err := v.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	backup, err := v.Backup(backupPassword)
	if err != nil {
		t.Fatal(err)
	}
	var original envelope
	if err = json.Unmarshal(backup, &original); err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"ciphertext", "public-key", "epoch", "purpose", "network", "version", "kdf", "truncated", "unknown", "oversized"} {
		t.Run(variant, func(t *testing.T) {
			var e envelope
			json.Unmarshal(backup, &e)
			switch variant {
			case "ciphertext":
				e.Ciphertext[0] ^= 1
			case "public-key":
				e.Ref.PublicKey = "0000000000000000000000000000000000000000000000000000000000000000"
			case "epoch":
				e.Ref.Epoch = randomID()
			case "purpose":
				e.Purpose = "vault"
			case "network":
				e.Ref.Network = "solana-mainnet"
			case "version":
				e.Version = 2
			case "kdf":
				e.KDF = "unbounded-kdf"
			}
			b, _ := json.Marshal(e)
			if variant == "truncated" {
				b = b[:len(b)-8]
			}
			if variant == "unknown" {
				b = append(b[:len(b)-1], []byte(",\"importMasterKey\":true}")...)
			}
			if variant == "oversized" {
				b = bytes.Repeat([]byte("x"), maxDocument+1)
			}
			restored := openFixture(t, fixtureDir(t))
			requireError(t, restored.Restore(b, backupPassword, fixturePassword))
			if restored.Status().Account != nil {
				t.Fatal("damaged backup persisted")
			}
		})
	}
	dir := fixtureDir(t)
	if err = os.WriteFile(filepath.Join(dir, "vault.json"), backup, 0600); err != nil {
		t.Fatal(err)
	}
	if v, err := Open(dir, original.Ref.SignerID); err == nil {
		v.Close()
		t.Fatal("backup accepted as live vault")
	}
}

func TestManualUnlockBoundsExpiryAndConcurrency(t *testing.T) {
	v := openFixture(t, fixtureDir(t))
	if err := v.Create(fixturePassword); err != nil {
		t.Fatal(err)
	}
	for _, lifetime := range []time.Duration{0, -time.Second, MaxUnlock + time.Second} {
		requireError(t, v.Unlock(fixturePassword, lifetime))
	}
	if err := v.Unlock(fixturePassword, time.Hour); err != nil {
		t.Fatal(err)
	}
	now := v.now()
	v.now = func() time.Time { return now.Add(2 * time.Hour) }
	if !v.Status().Locked || len(v.seed) != 0 {
		t.Fatal("expired key retained")
	}
	v.now = time.Now
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			for range 20 {
				v.Status()
				v.Lock()
			}
		})
	}
	group.Wait()
	if _, err := v.Backup(backupPassword); !errors.Is(err, ErrLocked) {
		t.Fatal("lock did not block backup")
	}
}

func TestUnlockTimerClearsKeyWithoutRequests(t *testing.T) {
	v := openFixture(t, fixtureDir(t))
	if err := v.Create(fixturePassword); err != nil {
		t.Fatal(err)
	}
	if err := v.Unlock(fixturePassword, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for {
		v.mu.Lock()
		cleared := len(v.seed) == 0
		v.mu.Unlock()
		if cleared {
			return
		}
		select {
		case <-deadline:
			t.Fatal("unlock timer retained seed without incoming requests")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestInterruptedPublicationPreservesOriginal(t *testing.T) {
	for _, phase := range []string{"staged", "published"} {
		t.Run(phase, func(t *testing.T) {
			dir := fixtureDir(t)
			v := openFixture(t, dir)
			v.store.crash = func(p string) error {
				if p == phase {
					return errors.New("disposable crash injection")
				}
				return nil
			}
			requireError(t, v.Create(fixturePassword))
			pending, err := os.ReadFile(filepath.Join(dir, "vault.pending"))
			if err != nil {
				t.Fatal(err)
			}
			var e envelope
			if err = json.Unmarshal(pending, &e); err != nil {
				t.Fatal(err)
			}
			requireError(t, v.Create(fixturePassword))
			v.Close()
			recovered := openFixture(t, dir)
			if recovered.Status().Account == nil || *recovered.Status().Account != e.Ref || !recovered.Status().Locked {
				t.Fatal("replaced interrupted identity")
			}
			if err = recovered.Unlock(fixturePassword, time.Minute); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(dir, "vault.pending")); !os.IsNotExist(err) {
				t.Fatal("pending original not reconciled")
			}
		})
	}
}

func TestStorageRefusesUnsafePathsAndConflicts(t *testing.T) {
	dir := fixtureDir(t)
	os.Chmod(dir, 0755)
	if v, err := Open(dir, randomID()); err == nil {
		v.Close()
		t.Fatal("broad state permissions")
	}
	os.Chmod(dir, 0700)
	other := fixtureDir(t)
	link := filepath.Join(dir, "linked")
	os.Symlink(other, link)
	if v, err := Open(link, randomID()); err == nil {
		v.Close()
		t.Fatal("symlink directory")
	}
	os.Symlink(filepath.Join(other, "foreign"), filepath.Join(dir, "vault.json"))
	if v, err := Open(dir, randomID()); err == nil {
		v.Close()
		t.Fatal("symlink state")
	}
	os.Remove(filepath.Join(dir, "vault.json"))
	v := openFixture(t, dir)
	if err := v.Create(fixturePassword); err != nil {
		t.Fatal(err)
	}
	v.Close()
	original, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
	os.WriteFile(filepath.Join(dir, "vault.pending"), []byte("damaged interrupted original"), 0600)
	if v, err := Open(dir, randomID()); err == nil {
		v.Close()
		t.Fatal("corrupt pending accepted")
	}
	current, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
	if !bytes.Equal(current, original) {
		t.Fatal("rejected recovery altered original")
	}
}

func TestOwnerBackupVerificationPreservesAuthority(t *testing.T) {
	v := openFixture(t, fixtureDir(t))
	if err := v.Create(fixturePassword); err != nil {
		t.Fatal(err)
	}
	if err := v.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	backup, err := v.Backup(backupPassword)
	if err != nil {
		t.Fatal(err)
	}
	defer wipe(backup)
	before, _ := json.Marshal(v.Status())
	check := func(document, password []byte, owner bool, wantError bool) {
		t.Helper()
		response := execute(v, Request{Action: "verify-backup", Document: document, Password: append([]byte(nil), password...)}, owner)
		if (response.Error != "") != wantError || len(response.Document) != 0 {
			t.Fatalf("unexpected verification outcome: %s", response.Error)
		}
		after, _ := json.Marshal(v.Status())
		if !bytes.Equal(before, after) {
			t.Fatal("backup check changed authority")
		}
	}
	check(backup, backupPassword, true, false)
	check(backup, []byte("wrong disposable password"), true, true)
	check(backup, backupPassword, false, true)
	other := openFixture(t, fixtureDir(t))
	if err := other.Create(fixturePassword); err != nil {
		t.Fatal(err)
	}
	if err := other.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	foreign, err := other.Backup(backupPassword)
	if err != nil {
		t.Fatal(err)
	}
	defer wipe(foreign)
	check(foreign, backupPassword, true, true)
	v.Lock()
	before, _ = json.Marshal(v.Status())
	check(backup, backupPassword, true, true)
}
