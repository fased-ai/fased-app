//go:build linux

package main

import (
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
