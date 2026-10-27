//go:build linux

package signer

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestManagedUpdateRestartCompletionAndRollback(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "rollback"}[rollback], func(t *testing.T) {
			dir := fixtureDir(t)
			v := managedFixture(t, dir, "devnet")
			if e := v.Create(fixturePassword); e != nil {
				t.Fatal(e)
			}
			original, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
			ref := *v.Status().Account
			intent, binding := installationFixture(t, v)
			phases := []string{"active", "healthy-held", "committed-held", "completed"}
			if rollback {
				phases = []string{"active", "rolled-back-held"}
			}
			for _, phase := range phases {
				p := installationPreflight(t, v, intent)
				request := update.InstallRequest{Action: phase, IntentID: intent.ID, Binding: &binding, Preflight: &update.PreflightRequest{IntentID: intent.ID, Sequence: intent.Sequence, ManifestSHA256: intent.ManifestSHA256, ProbeID: p.Pause.Core.ProbeID, Pause: p.Pause, ExpectedSigner: p.Signer}}
				if _, e := v.Installation(request); e != nil {
					t.Fatal(phase, e)
				}
				v.Close()
				v = managedFixture(t, dir, "devnet")
				got, e := v.Installation(update.InstallRequest{Action: "status", IntentID: intent.ID})
				if e != nil || got.Phase != phase {
					t.Fatal(phase, got, e)
				}
				if *v.Status().Account != ref || !v.Status().Locked || !v.Status().RecoveryRequired {
					t.Fatal("key/hold changed")
				}
				b, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
				if !bytes.Equal(original, b) {
					t.Fatal("encrypted key rewritten")
				}
			}
			if rollback {
				if _, e := v.AbortUpdate(intent.ID); e != nil {
					t.Fatal(e)
				}
			}
			snap, e := v.UpdateInventory(update.NewProbe())
			if e != nil || snap.SequenceFloor != 1 || snap.ActiveIntentID != "" || !snap.RecoveryHold {
				t.Fatal(snap, e)
			}
			if rollback && snap.InstalledSequence != 0 || !rollback && snap.InstalledSequence != 1 {
				t.Fatal("wrong installed floor")
			}
			if _, e := v.PrepareUpdate(intentFixture(t, v, 1)); e == nil {
				t.Fatal("release sequence replay")
			}
			if _, e := os.Stat(filepath.Join(dir, "mock-receipts.log")); !os.IsNotExist(e) {
				t.Fatal("invented mock receipts")
			}

			if e := v.Unlock(fixturePassword, time.Minute); e != nil {
				t.Fatal(e)
			}
			backup, e := v.Backup(backupPassword)
			if e != nil {
				t.Fatal(e)
			}
			defer wipe(backup)
			v.Close()
			if e = os.Remove(filepath.Join(dir, "vault.json")); e != nil {
				t.Fatal(e)
			}
			v = managedFixture(t, dir, "devnet")
			if e = v.Restore(backup, backupPassword, fixturePassword); e != nil {
				t.Fatal(e)
			}
			after, e := v.UpdateInventory(update.NewProbe())
			if e != nil || after.SequenceFloor != snap.SequenceFloor || after.InstalledSequence != snap.InstalledSequence || after.AuthoritySHA256 == snap.AuthoritySHA256 || !after.RecoveryHold {
				t.Fatal("recovery reset maintenance history", after, e)
			}
			if _, e = v.Installation(update.InstallRequest{Action: "status", IntentID: intent.ID}); e == nil {
				t.Fatal("old authority receipt reused after recovery")
			}
			if e := v.ledger.append(v, journalRecord{Kind: "policy"}); e == nil {
				t.Fatal("financial record admitted")
			}
		})
	}
}
