//go:build linux

package signer

import (
	"bytes"
	"github.com/fased-ai/fased-app/internal/update"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockedOwnerBackupPreservesStagedRecovery(t *testing.T) {
	source := ordinaryVault(t, fixtureDir(t))
	if e := source.Create(fixturePassword); e != nil {
		t.Fatal(e)
	}
	if e := source.Unlock(fixturePassword, time.Minute); e != nil {
		t.Fatal(e)
	}
	backup, e := source.BackupHistory(backupPassword)
	if e != nil {
		t.Fatal(e)
	}
	source.Close()
	dir := fixtureDir(t)
	id := "bcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbc"
	open := func() *Vault {
		v, e := OpenControlledOrdinaryCustody(dir, id, "devnet")
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	v := open()
	defer func() { v.Close() }()
	if e = v.RestoreHistory(backup, backupPassword, fixturePassword, BackupFingerprint(backup)); e != nil {
		t.Fatal(e)
	}
	intent, _ := installationFixtureAtSequence(t, v, 2)
	ref := *v.Status().Account
	before, e := v.UpdateInventory(update.NewProbe())
	if e != nil {
		t.Fatal(e)
	}
	key, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
	history, _ := os.ReadFile(filepath.Join(dir, "operations.log"))
	for restart := 0; restart < 2; restart++ {
		for _, kind := range []string{"ok", "app", "vault-password", "backup-password", "fingerprint", "foreign-backup", "tampered-backup", "other-action"} {
			r := Request{Action: "verify-backup", Document: bytes.Clone(backup), Password: bytes.Clone(backupPassword), VaultPassword: bytes.Clone(fixturePassword), SnapshotSHA256: BackupFingerprint(backup)}
			owner := true
			switch kind {
			case "app":
				owner = false
			case "vault-password":
				r.VaultPassword = []byte("wrong vault password")
			case "backup-password":
				r.Password = []byte("wrong backup password")
			case "fingerprint":
				r.SnapshotSHA256 = metadataFixtureHash([]byte("different"))
			case "foreign-backup":
				w := ordinaryVault(t, fixtureDir(t))
				if e = w.Create(fixturePassword); e != nil {
					t.Fatal(e)
				}
				if e = w.Unlock(fixturePassword, time.Minute); e != nil {
					t.Fatal(e)
				}
				doc, e := w.BackupHistory(backupPassword)
				w.Close()
				if e != nil {
					t.Fatal(e)
				}
				r.Document = doc
				r.SnapshotSHA256 = BackupFingerprint(doc)
			case "tampered-backup":
				r.Document[len(r.Document)-5] ^= 1
				r.SnapshotSHA256 = BackupFingerprint(r.Document)
			case "other-action":
				r.Action = "unlock"
			}
			response := execute(v, r, owner)
			if (response.Error == "") != (kind == "ok") {
				t.Fatal("locked backup result", kind, response.Error)
			}
			if len(v.seed) != 0 || !v.until.IsZero() || v.timer != nil || !v.Status().Locked || !v.Status().RecoveryRequired || *v.Status().Account != ref {
				t.Fatal("verification changed signer authority", kind)
			}
			for _, buf := range [][]byte{r.Password, r.VaultPassword} {
				for _, b := range buf {
					if b != 0 {
						t.Fatal("credential retained", kind)
					}
				}
			}
		}
		if e = v.Unlock(fixturePassword, time.Minute); e == nil {
			t.Fatal("exclusive update unlock guard weakened")
		}
		after, e := v.UpdateInventory(update.NewProbe())
		if e != nil || !update.SameOrdinarySnapshot(*before, *after) || after.ActiveIntentID != intent.ID || after.SequenceFloor != 2 {
			t.Fatal("staged snapshot changed", e)
		}
		got, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
		journal, _ := os.ReadFile(filepath.Join(dir, "operations.log"))
		if !bytes.Equal(key, got) || !bytes.Equal(history, journal) {
			t.Fatal("verification wrote keys or history")
		}
		v.Close()
		v = open()
	}
}

func TestLockedBackupKeyCredentialsAndNoImplicitLock(t *testing.T) {
	v := ordinaryVault(t, fixtureDir(t))
	if e := v.Create(fixturePassword); e != nil {
		t.Fatal(e)
	}
	defer v.Close()
	if e := v.Unlock(fixturePassword, time.Minute); e != nil {
		t.Fatal(e)
	}
	document, e := v.Backup(backupPassword)
	if e != nil {
		t.Fatal(e)
	}
	seed := bytes.Clone(v.seed)
	until, timer := v.until, v.timer
	defer clear(seed)
	if e = v.VerifyBackupLocked(document, backupPassword, fixturePassword, BackupFingerprint(document)); e == nil {
		t.Fatal("already-unlocked verification accepted")
	}
	if !bytes.Equal(seed, v.seed) || !v.until.Equal(until) || v.timer != timer {
		t.Fatal("read-only verifier changed an existing unlock")
	}
	v.Lock()
	if e = v.VerifyBackupLocked(document, backupPassword, fixturePassword, BackupFingerprint(document)); e != nil {
		t.Fatal("locked ordinary key backup", e)
	}
	if !v.Status().Locked || len(v.seed) != 0 || v.timer != nil {
		t.Fatal("verification created unlock authority")
	}
}
