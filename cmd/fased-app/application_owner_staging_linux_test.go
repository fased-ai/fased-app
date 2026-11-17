//go:build linux

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestApplicationOwnerCheckpointFallbackUsesTheOriginalOperation(t *testing.T) {
	id, lease := update.NewProbe(), update.NewProbe()
	release := &core.ApplicationRelease{Version: "0.1.12", Sequence: 12, ManifestSHA256: strings.Repeat("a", 64), EngineSHA256: strings.Repeat("b", 64)}
	op := core.ApplicationUpdateOperation{ID: id, OwnerLease: lease, Phase: "applying", Release: release}
	state := core.ApplicationOwnerView{View: core.ApplicationUpdateView{InstanceID: "original-instance", Schema: 37, Operation: op}}
	checkpoint := &core.ApplicationCheckpoint{Version: 1, OperationID: id, ManifestSHA256: release.ManifestSHA256, PreviousEngineSHA256: strings.Repeat("c", 64)}
	offline := state
	offline.Checkpoint = checkpoint
	lost := errors.New("owner reply lost")
	for name, change := range map[string]func(*core.ApplicationOwnerView){
		"missing backup":  func(v *core.ApplicationOwnerView) { v.Checkpoint = nil },
		"other instance":  func(v *core.ApplicationOwnerView) { v.View.InstanceID = "other" },
		"other schema":    func(v *core.ApplicationOwnerView) { v.View.Schema++ },
		"other operation": func(v *core.ApplicationOwnerView) { v.View.Operation.ID = update.NewProbe() },
		"other lease":     func(v *core.ApplicationOwnerView) { v.View.Operation.OwnerLease = update.NewProbe() },
		"other policy":    func(v *core.ApplicationOwnerView) { v.View.Operation.PolicySHA256 = strings.Repeat("f", 64) },
		"automatic mode":  func(v *core.ApplicationOwnerView) { v.View.Operation.Automatic = true },
		"other release": func(v *core.ApplicationOwnerView) {
			changed := *v.View.Operation.Release
			changed.EngineSHA256 = strings.Repeat("d", 64)
			v.View.Operation.Release = &changed
		},
		"terminal operation": func(v *core.ApplicationOwnerView) { v.View.Operation.Phase = "completed" },
		"different checkpoint": func(v *core.ApplicationOwnerView) {
			changed := *v.Checkpoint
			changed.PreviousEngineSHA256 = strings.Repeat("e", 64)
			v.Checkpoint = &changed
		},
	} {
		t.Run(name, func(t *testing.T) {
			expected, current := state, offline
			if name == "different checkpoint" {
				expected.Checkpoint = checkpoint
			}
			change(&current)
			err := prepareApplicationCheckpoint(t.Context(), expected, op,
				func(context.Context) error { return lost },
				func(context.Context) (core.ApplicationOwnerView, error) { return current, nil })
			if !errors.Is(err, lost) {
				t.Fatal("foreign or missing checkpoint authorized replacement", err)
			}
		})
	}
	reads := 0
	read := func(context.Context) (core.ApplicationOwnerView, error) { reads++; return offline, nil }
	if err := prepareApplicationCheckpoint(t.Context(), state, op, func(context.Context) error { return nil }, read); err != nil || reads != 0 {
		t.Fatal("successful preparation performed unnecessary offline recovery", err, reads)
	}
	if err := prepareApplicationCheckpoint(t.Context(), state, op, func(context.Context) error { return lost }, read); err != nil || reads != 1 {
		t.Fatal("the exact checkpoint could not recover a lost response", err, reads)
	}
	if err := prepareApplicationCheckpoint(t.Context(), state, op, func(context.Context) error { return lost }, func(context.Context) (core.ApplicationOwnerView, error) { return offline, errors.New("backup changed") }); !errors.Is(err, lost) {
		t.Fatal("unreadable checkpoint authorized replacement", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := prepareApplicationCheckpoint(cancelled, state, op, func(context.Context) error { return nil }, read); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled update authorized replacement", err)
	}
}

func TestApplicationOwnerCheckpointFallbackRechecksTheRealBackup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "core")
	store, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "core.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	op := core.ApplicationUpdateOperation{ID: update.NewProbe(), OwnerLease: update.NewProbe(), Phase: "applying", Release: &core.ApplicationRelease{Version: "0.1.12", Sequence: 12, ManifestSHA256: strings.Repeat("a", 64), EngineSHA256: strings.Repeat("b", 64), Target: update.Target{OS: runtime.GOOS, Arch: runtime.GOARCH}}}
	body, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE application_updates SET document=? WHERE id=1", string(body)); err != nil {
		t.Fatal(err)
	}
	state, err := store.ApplicationOwnerCommand(t.Context(), core.ApplicationOwnerCommand{Action: "prepare", ID: op.ID, Lease: op.OwnerLease})
	if err != nil || state.Checkpoint == nil {
		t.Fatal("real application checkpoint was not prepared", err)
	}
	lost := errors.New("owner reply lost")
	prepare := func(context.Context) error { return lost }
	read := func(ctx context.Context) (core.ApplicationOwnerView, error) {
		return core.ReadApplicationOwnerOffline(ctx, dir, op.ID)
	}
	if err = prepareApplicationCheckpoint(t.Context(), state, op, prepare, read); err != nil {
		t.Fatal("original retained backup was refused", err)
	}
	backup := filepath.Join(dir, "lifecycle", op.ID, state.Checkpoint.Snapshot.File)
	if err = os.WriteFile(backup, []byte("damaged backup"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = core.CheckApplicationRollbackMetadata(t.Context(), dir, op.ID, state.View.Schema); err != nil {
		t.Fatal("the prior metadata-only fallback did not reproduce", err)
	}
	if err = prepareApplicationCheckpoint(t.Context(), state, op, prepare, read); !errors.Is(err, lost) {
		t.Fatal("a changed backup authorized replacement", err)
	}
}

func TestApplicationOwnerPrestageRecoveryDecision(t *testing.T) {
	id, lease := strings.Repeat("1", 32), strings.Repeat("2", 32)
	state := core.ApplicationOwnerView{View: core.ApplicationUpdateView{Operation: core.ApplicationUpdateOperation{ID: id, OwnerLease: lease, Phase: "applying", Release: &core.ApplicationRelease{Sequence: 8, ManifestSHA256: strings.Repeat("a", 64), EngineSHA256: strings.Repeat("b", 64)}}}}
	manifest := strings.Repeat("c", 64)
	prepared := localinstall.ApplicationUpdateStatus{ID: id, Phase: "prepared", Sequence: 8, ReleaseSHA256: state.View.Operation.Release.ManifestSHA256, EngineSHA256: state.View.Operation.Release.EngineSHA256, PreviousManifestSHA256: manifest}
	for _, tc := range []struct {
		name   string
		j      localinstall.ApplicationUpdateStatus
		err    error
		want   ownerStagingAction
		refuse bool
	}{
		{"claimed-no-journal", localinstall.ApplicationUpdateStatus{}, os.ErrNotExist, ownerRejectUnstaged, false},
		{"prepared-requires-independent-readmission", prepared, nil, ownerReadmitPrepared, false},
		{"later-offline-resume", func() localinstall.ApplicationUpdateStatus { j := prepared; j.Phase = "applying"; return j }(), nil, ownerResume, false},
		{"prior-completed-receipt-preserved", localinstall.ApplicationUpdateStatus{ID: strings.Repeat("3", 32), Phase: "completed", Sequence: 7, TargetManifestSHA256: manifest}, nil, ownerRejectUnstaged, false},
		{"prior-rolled-back-floor-preserved", localinstall.ApplicationUpdateStatus{ID: strings.Repeat("3", 32), Phase: "rolled-back", Sequence: 7, PreviousManifestSHA256: manifest}, nil, ownerRejectUnstaged, false},
		{"foreign-active-held", localinstall.ApplicationUpdateStatus{ID: strings.Repeat("3", 32), Phase: "prepared", Sequence: 7}, nil, ownerResume, true},
		{"consumed-floor-held", localinstall.ApplicationUpdateStatus{ID: strings.Repeat("3", 32), Phase: "completed", Sequence: 8, TargetManifestSHA256: manifest}, nil, ownerResume, true},
		{"prior-terminal-changed-baseline", localinstall.ApplicationUpdateStatus{ID: strings.Repeat("3", 32), Phase: "completed", Sequence: 7, TargetManifestSHA256: strings.Repeat("d", 64)}, nil, ownerResume, true},
		{"changed-release-held", func() localinstall.ApplicationUpdateStatus {
			j := prepared
			j.ReleaseSHA256 = strings.Repeat("d", 64)
			return j
		}(), nil, ownerResume, true},
		{"changed-engine-held", func() localinstall.ApplicationUpdateStatus {
			j := prepared
			j.EngineSHA256 = strings.Repeat("d", 64)
			return j
		}(), nil, ownerResume, true},
		{"reader-bootstrap-never-ordinary-readmitted", func() localinstall.ApplicationUpdateStatus { j := prepared; j.ReaderBootstrap = true; return j }(), nil, ownerResume, true},
		{"unknown-journal-held", prepared, errors.New("corrupt"), ownerResume, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, e := applicationOwnerStaging(state, tc.j, tc.err, id, manifest)
			if (e != nil) != tc.refuse || e == nil && got != tc.want {
				t.Fatal(got, e)
			}
			if state.View.Operation.OwnerLease != lease || state.View.Operation.ID != id {
				t.Fatal("original consent/lease changed")
			}
		})
	}
	for _, lease := range []string{"", strings.Repeat("2", 32)} {
		rejected := state
		rejected.View.Operation.Phase = "check-failed"
		rejected.View.Operation.OwnerLease = lease
		if action, e := applicationOwnerStaging(rejected, localinstall.ApplicationUpdateStatus{}, os.ErrNotExist, id, manifest); e != nil || action != ownerRejectUnstaged {
			t.Fatal("already-rejected cleanup cannot resume", action, e)
		}
		if _, e := applicationOwnerStaging(rejected, prepared, nil, id, manifest); e == nil {
			t.Fatal("rejected metadata discarded a prepared journal")
		}
	}
}

func TestApplicationOwnerPrestageDispatchRecheck(t *testing.T) {
	o := core.ApplicationUpdateOperation{ID: update.NewProbe(), OwnerLease: update.NewProbe(), Phase: "applying", Release: &core.ApplicationRelease{Sequence: 8, Version: "0.0.0-next", ManifestSHA256: strings.Repeat("a", 64), EngineSHA256: strings.Repeat("b", 64)}}
	if e := applicationOwnerMayStage(o, o); e != nil {
		t.Fatal(e)
	}
	for _, change := range []string{"phase", "id", "lease", "release"} {
		current := o
		switch change {
		case "phase":
			current.Phase = "check-failed"
		case "id":
			current.ID = update.NewProbe()
		case "lease":
			current.OwnerLease = update.NewProbe()
		case "release":
			r := *o.Release
			r.EngineSHA256 = strings.Repeat("c", 64)
			current.Release = &r
		}
		if e := applicationOwnerMayStage(o, current); e == nil {
			t.Fatal("changed owner dispatch staged", change)
		}
	}
}

func TestApplicationOwnerPrestagePolicyDeferralActorOnly(t *testing.T) {
	expected := core.ApplicationUpdateOperation{ID: update.NewProbe(), Phase: "applying", Release: &core.ApplicationRelease{Sequence: 8, Version: "0.0.0-next", ManifestSHA256: strings.Repeat("a", 64), EngineSHA256: strings.Repeat("b", 64)}}
	deferred := core.ApplicationOwnerView{View: core.ApplicationUpdateView{Operation: expected}, Checkpoint: &core.ApplicationCheckpoint{OperationID: expected.ID}}
	deferred.View.Operation.Phase = "available"
	if !applicationOwnerClaimDeferred(expected, deferred) {
		t.Fatal("exact unclaimed policy deferral stranded actor")
	}
	if deferred.View.Operation.Phase != "available" || deferred.Checkpoint == nil {
		t.Fatal("actor classification changed core/fence")
	}
	for _, change := range []string{"may-apply", "claimed", "id", "phase", "release"} {
		reply := deferred
		switch change {
		case "may-apply":
			reply.MayApply = true
		case "claimed":
			reply.View.Operation.OwnerLease = update.NewProbe()
		case "id":
			reply.View.Operation.ID = update.NewProbe()
		case "phase":
			reply.View.Operation.Phase = "applying"
		case "release":
			r := *reply.View.Operation.Release
			r.Sequence++
			reply.View.Operation.Release = &r
		}
		if applicationOwnerClaimDeferred(expected, reply) {
			t.Fatal("active or foreign reply retired actor", change)
		}
	}
}

func TestApplicationOwnerClaimWithoutJournalProtectedReject(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsent", true: "fenced"}[held], func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "core")
			s, e := core.OpenStore(dir)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			db, e := sql.Open("sqlite", "file:"+filepath.Join(dir, "core.sqlite"))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			op := core.ApplicationUpdateOperation{ID: update.NewProbe(), OwnerLease: update.NewProbe(), Phase: "applying", Release: &core.ApplicationRelease{Version: "0.0.0-test.2", Sequence: 7, ManifestSHA256: strings.Repeat("a", 64), EngineSHA256: strings.Repeat("b", 64), Target: update.Target{OS: runtime.GOOS, Arch: runtime.GOARCH}}}
			b, _ := json.Marshal(op)
			if _, e = db.Exec("UPDATE application_updates SET document=? WHERE id=1", string(b)); e != nil {
				t.Fatal(e)
			}
			if held {
				if _, e = db.Exec("UPDATE update_pause SET phase='paused' WHERE id=1"); e != nil {
					t.Fatal(e)
				}
			}
			if _, e = s.ApplicationOwnerCommand(t.Context(), core.ApplicationOwnerCommand{Action: "reject", ID: op.ID, Lease: update.NewProbe()}); e == nil {
				t.Fatal("foreign lease rejected dispatch")
			}
			v, e := s.ApplicationOwnerCommand(t.Context(), core.ApplicationOwnerCommand{Action: "reject", ID: op.ID, Lease: op.OwnerLease})
			if held {
				if e == nil {
					t.Fatal("crossed fence rejected as unsent")
				}
				var phase string
				if e = db.QueryRow("SELECT phase FROM update_pause WHERE id=1").Scan(&phase); e != nil || phase != "paused" {
					t.Fatal("hold changed", phase, e)
				}
				return
			}
			if e != nil || v.View.Operation.Phase != "check-failed" || v.View.Operation.ID != op.ID || v.View.Operation.OwnerLease != op.OwnerLease {
				t.Fatal(v, e)
			}
			if _, e = s.ApplicationOwnerCommand(t.Context(), core.ApplicationOwnerCommand{Action: "reject", ID: op.ID, Lease: op.OwnerLease}); e != nil {
				t.Fatal("post-reject cleanup retry lost exact authority", e)
			}
		})
	}
}
