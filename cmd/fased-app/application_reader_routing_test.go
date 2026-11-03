package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

func TestRemoteApplicationReaderRestartRoutesBeforeHealthyTargetIPC(t *testing.T) {
	status := vpsinstall.ApplicationUpdateStatus{Version: 1, ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Phase: "recovery", ReaderBootstrap: true}
	// Persist/readback models the actor restart after the target is already35.
	b, _ := json.Marshal(status)
	var restarted vpsinstall.ApplicationUpdateStatus
	if e := json.Unmarshal(b, &restarted); e != nil {
		t.Fatal(e)
	}
	ordinary, reader := 0, 0
	e := routeRemoteApplicationMaintenance(restarted, nil, status.ID, func() error { ordinary++; return nil /* healthy new35 IPC */ }, func() error { reader++; return nil })
	if e != nil || ordinary != 0 || reader != 1 {
		t.Fatal("healthy new target erased old-reader recovery classification", ordinary, reader, e)
	}
}

func TestRemotePreparedReaderRecoveryPrecedesIdleSourceIPC(t *testing.T) {
	for _, phase := range []string{"prepared", "applying", "recovery", "healthy-held"} {
		t.Run(phase, func(t *testing.T) {
			status := vpsinstall.ApplicationUpdateStatus{Version: 1, ID: strings.Repeat("a", 32), Phase: phase, ReaderBootstrap: true}
			calls := 0
			err := routeRemoteApplicationMaintenance(status, nil, status.ID, func() error {
				t.Fatal("idle predecessor IPC hid the retained reader transaction")
				return nil
			}, func() error { calls++; return nil })
			if err != nil || calls != 1 {
				t.Fatal("did not resume the exact retained reader operation", calls, err)
			}
		})
	}
}

func TestApplicationReaderCompletionPreparesOptionalMaintenance(t *testing.T) {
	root := t.TempDir()
	image := filepath.Join(root, "inert-core")
	if e := os.WriteFile(image, []byte("inert fixture"), 0700); e != nil {
		t.Fatal(e)
	}
	paths := localinstall.Paths{Root: filepath.Join(root, "installed"), Data: filepath.Join(root, "data")}
	previous, e := localinstall.Install(t.Context(), paths, localinstall.Options{Engine: image})
	if e != nil {
		t.Fatal(e)
	}
	target := previous
	target.Version = "reader-target"
	target.ApplicationSequence = 7
	target.ApplicationReleaseSHA256 = strings.Repeat("a", 64)
	if e = core.WritePrivateJSON(filepath.Join(paths.Root, "install.json"), target); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(root, ".installed-updates")
	if e = os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	status := localinstall.ApplicationUpdateStatus{ReaderBootstrap: true, Version: 1, ID: strings.Repeat("1", 32), Phase: "completed", Sequence: 7, ReleaseSHA256: target.ApplicationReleaseSHA256, EngineSHA256: target.Files["fased-app"], PreviousManifestSHA256: localinstall.ManifestSHA256(previous), TargetManifestSHA256: localinstall.ManifestSHA256(target)}
	write := func() {
		t.Helper()
		if e = core.WritePrivateJSON(filepath.Join(dir, "current.json"), map[string]any{"status": status, "previous": previous, "target": target}); e != nil {
			t.Fatal(e)
		}
	}
	write()
	calls := 0
	prepare := func(_ context.Context, m localinstall.Manifest, ready bool, _ io.Writer) string {
		calls++
		if !ready || m.ApplicationSequence != 7 {
			t.Fatal("wrong readiness identity")
		}
		return "manual-only"
	}
	if e = finishLocalReaderUpdateWith(t.Context(), paths, io.Discard, prepare); e != nil || calls != 1 {
		t.Fatal("optional/deferred readiness changed completion", e, calls)
	}
	status.Phase = "applying"
	write()
	if e = finishLocalReaderUpdateWith(t.Context(), paths, io.Discard, prepare); e == nil || calls != 1 {
		t.Fatal("maintenance prepared before completion", e, calls)
	}
}

func TestRemoteApplicationOrdinaryRollbackDoesNotProbeReaderHelper(t *testing.T) {
	status := vpsinstall.ApplicationUpdateStatus{Version: 1, ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Phase: "recovery"}
	want := errors.New("ordinary offline rollback selected")
	e := routeRemoteApplicationMaintenance(status, nil, status.ID, func() error { return want }, func() error { t.Fatal("ordinary rollback depended on crashed target helper"); return nil })
	if !errors.Is(e, want) {
		t.Fatal(e)
	}
	if e = routeRemoteApplicationMaintenance(status, os.ErrNotExist, status.ID, func() error { return nil }, func() error { t.Fatal("missing journal became migration authority"); return nil }); e != nil {
		t.Fatal(e)
	}
}

func TestCurrentRemoteReaderRequiresPublicUpdaterHandoff(t *testing.T) {
	id := update.NewProbe()
	snapshot := update.CoreSnapshot{InstanceID: "existing", State: update.State{Version: core.StateSchema}}
	view := core.ApplicationUpdateView{Version: 1, Schema: core.StateSchema, InstanceID: "existing", Operation: core.ApplicationUpdateOperation{Phase: "idle"}}
	required, err := currentReaderNeedsBootstrap(snapshot, view, id, true)
	if err != nil || !required {
		t.Fatal("same schema without public updater skipped handoff", required, err)
	}
	view.ControllerAvailable = true
	if required, err = currentReaderNeedsBootstrap(snapshot, view, id, true); err != nil || required {
		t.Fatal("configured controller bypassed", err)
	}
	if required, err = currentReaderNeedsBootstrap(snapshot, view, id, false); err != nil || !required {
		t.Fatal("configured older packet reader skipped verified bootstrap", required, err)
	}
	view.Operation.Phase = "check-failed"
	if required, err = currentReaderNeedsBootstrap(snapshot, view, id, false); err != nil || !required {
		t.Fatal("failed older format check skipped bootstrap", required, err)
	}
	view.Operation.Phase = "recovery"
	if _, err = currentReaderNeedsBootstrap(snapshot, view, id, false); err == nil {
		t.Fatal("older packet capability bypassed unresolved recovery")
	}
	view.Operation.Phase = "idle"
	view.ControllerAvailable = false
	view.Operation.ID = id
	if required, err = currentReaderNeedsBootstrap(snapshot, view, id, true); err != nil || required {
		t.Fatal("existing owner operation bypassed", err)
	}
	view.Operation.ID = ""
	view.Operation.Phase = "recovery"
	if _, err = currentReaderNeedsBootstrap(snapshot, view, id, true); err == nil {
		t.Fatal("unresolved update replaced")
	}
	view.InstanceID = "foreign"
	if _, err = currentReaderNeedsBootstrap(snapshot, view, id, true); err == nil {
		t.Fatal("foreign workspace adopted")
	}
}
