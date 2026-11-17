//go:build linux

package custodyinstall

import (
	"errors"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestSuccessorOwnerPathRequiresOriginalLock(t *testing.T) {
	a := maintenance.Authority{ImageSHA256: strings.Repeat("1", 64)}
	successor := maintenance.ImagePath(strings.Repeat("2", 64))
	if _, err := protectedOwnerPeerImage(t.Context(), a, Manifest{}, successor, nil, signer.Request{}); err == nil {
		t.Fatal("successor owner peer started without the original lock")
	}
	if err := completeProtectedMetadata(t.Context(), a, update.MaintenanceRecord{}, Manifest{}, successor, nil); err == nil {
		t.Fatal("successor metadata update accepted without the original lock")
	}
}

func TestOriginalSignerConfigRejectsChangedIdentityAndPaths(t *testing.T) {
	tx, _ := modelTransaction(t)
	i := &update.Installation{Version: 1, Binding: tx.Binding, Digest: update.InstallDigest(tx.Binding), Phase: "completed", InstalledSequenceCommitted: true}
	r := update.MaintenanceRecord{Owner: tx.Base.Owner, Base: mustJSON(tx.Base), Target: mustJSON(tx.Target), Installation: i}
	want := identityConfig(tx.Target)
	if err := VerifyOriginalSignerConfig(r, want); err != nil {
		t.Fatal(err)
	}
	r.OperationID = tx.Binding.Index.Binding.Pause.Request.IntentID
	r.Phase, r.Start = "signer-completed", "target"
	h := maintenance.Successor{OperationID: r.OperationID, InstallationDigest: i.Digest}
	if err := VerifyOriginalCompletedSignerRecord(r, h); err != nil {
		t.Fatal("committed signer before root completion was refused", err)
	}
	wrong := h
	wrong.OperationID = strings.Repeat("9", 32)
	if VerifyOriginalCompletedSignerRecord(r, wrong) == nil {
		t.Fatal("another operation's signer receipt was accepted")
	}
	r.Phase = "ready"
	if VerifyOriginalCompletedSignerRecord(r, h) == nil {
		t.Fatal("uncommitted root phase was accepted")
	}
	r.Phase = "signer-completed"
	for name, change := range map[string]func(*signer.Config){
		"state":        func(c *signer.Config) { c.StateDir += "-other" },
		"socket":       func(c *signer.Config) { c.SocketDir += "-other" },
		"app identity": func(c *signer.Config) { c.AppUID++ },
		"owner":        func(c *signer.Config) { c.OwnerUID++ },
		"network":      func(c *signer.Config) { c.Network = "devnet" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := want
			change(&changed)
			if err := VerifyOriginalSignerConfig(r, changed); err == nil {
				t.Fatal("changed signer configuration was accepted")
			}
		})
	}
	foreignBase, foreignTarget := tx.Base, tx.Target
	foreignBase.ID, foreignTarget.ID = strings.Repeat("e", 32), strings.Repeat("e", 32)
	foreign := r
	foreign.Base, foreign.Target = mustJSON(foreignBase), mustJSON(foreignTarget)
	if VerifyOriginalSignerConfig(foreign, identityConfig(foreignTarget)) == nil {
		t.Fatal("a self-consistent foreign signer identity was accepted")
	}
}

func TestProtectedSignerCompletionPersistsBeforeReleaseAndReplays(t *testing.T) {
	tx, _ := modelTransaction(t)
	snapshot := update.CoreSnapshot{Version: 1, BootID: strings.Repeat("1", 32), ProbeID: strings.Repeat("2", 32), InstanceID: "fixture-instance", State: update.State{Name: "core-db", Version: 1, Anchor: strings.Repeat("3", 64)}}
	tx.Binding.Index.Binding.Pause.Request.Expected = snapshot
	tx.Binding.Index.Binding.Pause.Core = snapshot
	tx.Binding.Index.BindingSHA256 = update.RecoveryDigest(tx.Binding.Index.Binding)
	actual := &update.Installation{Version: 1, Binding: tx.Binding, Digest: update.InstallDigest(tx.Binding), Phase: "completed", InstalledSequenceCommitted: true}
	if !update.ValidInstallation(*actual) {
		t.Fatal("invalid completed fixture")
	}
	id := tx.Binding.Index.Binding.Pause.Request.IntentID
	released := tx.Binding.Index.Binding.Pause
	released.State = "released"
	released.Core = snapshot
	if !update.SameReleasedPause(id, tx.Binding.Index.Binding.Pause, released) {
		t.Fatal("invalid released pause fixture")
	}
	base := update.MaintenanceRecord{OperationID: id, Phase: "ready", Start: "target", Installation: actual}
	blocked := base
	releases, removals := 0, 0
	errWrite := errors.New("interrupted write")
	if err := completeVerifiedSignerReceipt(&blocked, actual, func(update.MaintenanceRecord) error { return errWrite }, func(*update.Installation) (update.CorePauseReceipt, error) { releases++; return released, nil }, func() error { removals++; return nil }); !errors.Is(err, errWrite) || releases != 0 || removals != 0 {
		t.Fatal("failed receipt write released the original hold", err)
	}
	if blocked.Phase != "ready" {
		t.Fatal("failed receipt write changed the in-memory phase")
	}
	stores := 0
	if err := completeVerifiedSignerReceipt(&blocked, actual, func(update.MaintenanceRecord) error { stores++; return nil }, func(*update.Installation) (update.CorePauseReceipt, error) {
		return update.CorePauseReceipt{}, errors.New("pause remains held")
	}, func() error { t.Fatal("held pause removed the fence"); return nil }); err == nil || stores != 1 || blocked.Phase != "signer-completed" {
		t.Fatal("same-process retry skipped the receipt write", err)
	}
	var durable update.MaintenanceRecord
	interrupted := base
	errRelease := errors.New("interrupted before pause release")
	if err := completeVerifiedSignerReceipt(&interrupted, actual, func(r update.MaintenanceRecord) error { durable = r; return nil }, func(*update.Installation) (update.CorePauseReceipt, error) {
		return update.CorePauseReceipt{}, errRelease
	}, func() error { removals++; return nil }); !errors.Is(err, errRelease) || durable.Phase != "signer-completed" || removals != 0 {
		t.Fatal("interrupted completion lost its durable signer phase", err)
	}
	if err := completeVerifiedSignerReceipt(&durable, actual, func(r update.MaintenanceRecord) error { durable = r; return nil }, func(*update.Installation) (update.CorePauseReceipt, error) { releases++; return released, nil }, func() error { removals++; return nil }); err != nil || durable.Phase != "completed" || durable.OperationID != id || releases != 1 || removals != 1 {
		t.Fatal("same-operation completion did not recover", err)
	}
	finalWrite := base
	var lastDurable update.MaintenanceRecord
	finalReleases, finalRemovals := 0, 0
	errFinal := errors.New("interrupted final write")
	err := completeVerifiedSignerReceipt(&finalWrite, actual, func(r update.MaintenanceRecord) error {
		if r.Phase == "completed" {
			return errFinal
		}
		lastDurable = r
		return nil
	}, func(*update.Installation) (update.CorePauseReceipt, error) { finalReleases++; return released, nil }, func() error { finalRemovals++; return nil })
	if !errors.Is(err, errFinal) || finalWrite.Phase != "signer-completed" || lastDurable.Phase != "signer-completed" || finalReleases != 1 || finalRemovals != 1 {
		t.Fatal("failed final write changed the durable phase", err)
	}
	err = completeVerifiedSignerReceipt(&finalWrite, actual, func(r update.MaintenanceRecord) error { lastDurable = r; return nil }, func(*update.Installation) (update.CorePauseReceipt, error) { finalReleases++; return released, nil }, func() error { finalRemovals++; return nil })
	if err != nil || finalWrite.Phase != "completed" || lastDurable.OperationID != id || finalReleases != 2 || finalRemovals != 2 {
		t.Fatal("same-operation final-write retry did not recover", err)
	}
	for _, failedPhase := range []string{"signer-completed", "completed"} {
		t.Run("ambiguous-"+failedPhase, func(t *testing.T) {
			current, disk := base, base
			failed := false
			persist := func(r update.MaintenanceRecord) error {
				disk = r // Model rename succeeding before directory sync fails.
				if r.Phase == failedPhase && !failed {
					failed = true
					return errWrite
				}
				return nil
			}
			release := func(*update.Installation) (update.CorePauseReceipt, error) { return released, nil }
			remove := func() error { return nil }
			if err := completeVerifiedSignerReceipt(&current, actual, persist, release, remove); !errors.Is(err, errWrite) || disk.Phase != failedPhase {
				t.Fatal("ambiguous write did not retain the observed record", err)
			}
			if err := completeVerifiedSignerReceipt(&current, actual, persist, release, remove); err != nil || disk.Phase != "completed" || disk.OperationID != id {
				t.Fatal("ambiguous write did not reconcile the same operation", err)
			}
		})
	}
	foreign := base
	foreign.OperationID = strings.Repeat("f", 32)
	if err := completeVerifiedSignerReceipt(&foreign, actual, func(update.MaintenanceRecord) error { t.Fatal("foreign receipt changed the journal"); return nil }, func(*update.Installation) (update.CorePauseReceipt, error) { return released, nil }, func() error { t.Fatal("foreign pause removed the fence"); return nil }); err == nil {
		t.Fatal("another operation's pause was accepted")
	}
}
