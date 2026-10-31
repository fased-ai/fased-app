//go:build linux

package signer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func ordinaryVault(t *testing.T, dir string) *Vault {
	t.Helper()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	v, e := OpenControlledOrdinaryCustody(dir, "00112233445566778899aabbccddeeff", "devnet")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = v.Close() })
	return v
}
func TestOrdinaryCustodyLifecycle(t *testing.T) {
	password := []byte("disposable ordinary custody password")
	for _, origin := range []string{"generated", "imported"} {
		t.Run(origin, func(t *testing.T) {
			dir := t.TempDir()
			v := ordinaryVault(t, dir)
			var err error
			if origin == "generated" {
				err = v.Create(password)
			} else {
				err = v.ImportControlledOrdinarySeed(bytes.Repeat([]byte{43}, 32), password)
			}
			if err != nil {
				t.Fatal(err)
			}
			s := v.Status()
			if s.Account == nil || !ValidOrdinaryAccountRef(*s.Account) || s.Custody == nil || s.Custody.Origin != origin || !s.AdmissionHeld || !s.RecoveryRequired || !s.Locked || s.WalletSigner || s.FinancialExecution {
				t.Fatalf("unsafe ordinary status: %+v", s)
			}
			ref := *s.Account
			if execute(v, Request{Action: "create", Password: append([]byte(nil), password...)}, false).Error == "" {
				t.Fatal("app created a key")
			}
			for _, action := range []string{"fixture-approve", "fixture-execute", "swap-sign", "fixture-update-prepare", "export-fixture-seed"} {
				if execute(v, Request{Action: action}, true).Error == "" {
					t.Fatal("unexpected authority", action)
				}
			}
			if err = v.Unlock(password, time.Minute); err != nil {
				t.Fatal(err)
			}
			backup, err := v.Backup(password)
			if err != nil {
				t.Fatal(err)
			}
			if err = v.VerifyBackup(backup, password); err != nil {
				t.Fatal(err)
			}
			var e envelope
			if decode(backup, &e) != nil || e.Version != 4 || e.Admission == nil || e.Admission.Origin != origin {
				t.Fatal("backup lost admission")
			}
			changed := e
			metadata := *e.Admission
			changed.Admission = &metadata
			if origin == "generated" {
				metadata.Origin = "imported"
			} else {
				metadata.Origin = "generated"
			}
			if _, err = unseal(changed, password, "backup"); err == nil {
				t.Fatal("provenance not authenticated")
			}
			changed = e
			metadata = *e.Admission
			metadata.Profile = "owner:v1"
			changed.Admission = &metadata
			if _, err = unseal(changed, password, "backup"); err == nil {
				t.Fatal("test provenance promoted")
			}
			v.Close()
			v = ordinaryVault(t, dir)
			if *v.Status().Account != ref || !v.Status().Locked || !v.Status().RecoveryRequired {
				t.Fatal("restart changed identity/hold")
			}
			old, err := os.ReadFile(filepath.Join(dir, "vault.json"))
			if err != nil {
				t.Fatal(err)
			}
			if x, err := openVaultNetwork(dir, ref.SignerID, false, "devnet"); err == nil {
				x.Close()
				t.Fatal("fixture reader accepted ordinary format")
			}
			// The old-reader probe must occur after releasing the exclusive lock.
			v.Close()
			if x, err := openVaultNetwork(dir, ref.SignerID, false, "devnet"); err == nil {
				x.Close()
				t.Fatal("fixture reader adopted ordinary custody")
			}
			now, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
			if !bytes.Equal(old, now) {
				t.Fatal("rejected reader changed key")
			}
			restored := ordinaryVault(t, t.TempDir())
			if err = restored.Restore(backup, password, password); err != nil {
				t.Fatal(err)
			}
			rs := restored.Status()
			if rs.Account.Epoch == ref.Epoch || !sameWallet(*rs.Account, ref) || !rs.Locked || !rs.RecoveryRequired || rs.Custody.Origin != origin {
				t.Fatal("unsafe recovery")
			}
			if err = restored.Restore(backup, password, password); err == nil {
				t.Fatal("restore overwrote a key")
			}
			if err = restored.Unlock(password, time.Minute); err != nil {
				t.Fatal(err)
			}
			if err = restored.VerifyBackup(backup, password); err != nil || !restored.Status().RecoveryRequired {
				t.Fatal("backup verification released hold")
			}
		})
	}
}
func TestOrdinaryCustodyPublicationAndNoPromotion(t *testing.T) {
	password := []byte("disposable ordinary custody password")
	for _, phase := range []string{"staged", "published"} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			v := ordinaryVault(t, dir)
			v.store.crash = func(at string) error {
				if at == phase {
					return errors.New("cut")
				}
				return nil
			}
			if err := v.Create(password); err == nil {
				t.Fatal("fault not reached")
			}
			v.Close()
			v = ordinaryVault(t, dir)
			s := v.Status()
			if s.Account == nil || s.Custody == nil || s.Custody.Origin != "generated" || !s.RecoveryRequired {
				t.Fatal("interrupted identity lost")
			}
			if err := v.Create(password); err == nil {
				t.Fatal("retry regenerated a key")
			}
		})
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	v, err := openVaultNetwork(dir, "00112233445566778899aabbccddeeff", false, "devnet")
	if err != nil {
		t.Fatal(err)
	}
	if err = v.Create(password); err != nil {
		t.Fatal(err)
	}
	v.Close()
	old, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
	if x, err := OpenControlledOrdinaryCustody(dir, "00112233445566778899aabbccddeeff", "devnet"); err == nil {
		x.Close()
		t.Fatal("fixture key promoted")
	}
	current, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
	if !bytes.Equal(old, current) {
		t.Fatal("promotion refusal changed key")
	}
	c := Config{Version: 3, Mode: OrdinaryCustodyMode, Network: "devnet", SignerID: "00112233445566778899aabbccddeeff", OwnerUID: 1101, AppUID: 1102, SignerUID: 1103, StateDir: dir, SocketDir: dir + "-ipc"}
	if server, err := Serve(context.Background(), c); err == nil {
		server.Close()
		t.Fatal("ordinary startup activated")
	}
}
func TestOrdinaryCustodyRejectsHistoryAndMetadataErasure(t *testing.T) {
	dir := t.TempDir()
	v := ordinaryVault(t, dir)
	password := []byte("disposable ordinary custody password")
	if err := v.Create(password); err != nil {
		t.Fatal(err)
	}
	e := *v.record
	e.Admission = nil
	if e.validate("vault") == nil {
		t.Fatal("admission erased")
	}
	if err := v.ledger.apply(journalRecord{Kind: "policy"}); err == nil {
		t.Fatal("ordinary replay fell through to fixture policy")
	}
	if err := v.store.appendJournal(v.ledger.main, journalRecord{Kind: "key-only-history"}); err != nil {
		t.Fatal(err)
	}
	v.Close()
	if x, err := OpenControlledOrdinaryCustody(dir, "00112233445566778899aabbccddeeff", "devnet"); err == nil {
		x.Close()
		t.Fatal("unrecognized history adopted")
	}
	b, _ := json.Marshal(e)
	if len(b) == 0 {
		t.Fatal("test fixture")
	}
}

func TestOrdinaryCustodyEmptyStatusIsNotAdmission(t *testing.T) {
	v := ordinaryVault(t, t.TempDir())
	s := v.Status()
	if s.Account != nil || s.Custody == nil || s.Custody.Class != OrdinaryCustodyMode || s.Custody.Valid() || !s.AdmissionHeld || !s.Locked {
		t.Fatal("empty class became admission or lost its namespace")
	}
}

func TestOrdinaryOwnerProfileLifecycleAndNoProvenancePromotion(t *testing.T) {
	password := []byte("disposable owner policy password")
	for _, origin := range []string{"generated", "imported"} {
		t.Run(origin, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			v, err := OpenControlledOwnerCustody(dir, "00112233445566778899aabbccddeeff", "devnet")
			if err != nil {
				t.Fatal(err)
			}
			defer v.Close()
			request := Request{Action: "create", Password: append([]byte(nil), password...)}
			if origin == "imported" {
				request.Action = "import-ordinary-seed"
				request.Document = bytes.Repeat([]byte{54}, 32)
			}
			if r := execute(v, request, true); r.Error != "" {
				t.Fatal(r.Error)
			}
			s := v.Status()
			if s.Custody.Profile != OwnerCustodyProfile || s.Custody.Provenance != ControlledCustodyProfile || s.Custody.Origin != origin || !s.AdmissionHeld || !s.FixtureOnly || !s.RecoveryRequired {
				t.Fatal("owner policy or controlled provenance lost")
			}
			if err = v.Unlock(password, time.Minute); err != nil {
				t.Fatal(err)
			}
			backup, err := v.Backup(password)
			if err != nil {
				t.Fatal(err)
			}
			if err = v.VerifyBackup(backup, password); err != nil {
				t.Fatal(err)
			}
			restoredDir := t.TempDir()
			if err = os.Chmod(restoredDir, 0700); err != nil {
				t.Fatal(err)
			}
			restored, err := OpenControlledOwnerCustody(restoredDir, "00112233445566778899aabbccddeeff", "devnet")
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			if err = restored.Restore(backup, password, password); err != nil {
				t.Fatal(err)
			}
			if restored.Status().Account.Epoch == s.Account.Epoch || !restored.Status().RecoveryRequired || restored.Status().Custody.Profile != OwnerCustodyProfile {
				t.Fatal("unsafe owner recovery")
			}
			// The live constructor stays closed before I/O. Even bypassing only that
			// outer gate cannot adopt controlled state under the owner-input namespace.
			if x, err := OpenOrdinaryCustody(dir, s.Account.SignerID, "devnet"); err == nil {
				x.Close()
				t.Fatal("normal admission activated")
			}
			v.Close()
			if x, err := openVaultNetwork(dir, s.Account.SignerID, false, ordinaryOwnerNamespace+"devnet"); err == nil {
				x.Close()
				t.Fatal("controlled provenance promoted by startup binding")
			}
			if x, err := openVaultNetwork(dir, s.Account.SignerID, false, ordinaryNamespace+"devnet"); err == nil {
				x.Close()
				t.Fatal("owner policy became controlled policy")
			}
			if r := execute(restored, Request{Action: "swap-sign"}, true); r.Error == "" {
				t.Fatal("owner profile granted signing")
			}
		})
	}
}
