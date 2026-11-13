//go:build linux

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

func fixtureCustodyPause(id, manifest string) update.CorePauseReceipt {
	snapshot := update.CoreSnapshot{Version: 1, BootID: update.NewProbe(), ProbeID: update.NewProbe(), InstanceID: "fixture-instance", State: update.State{Name: "core-db", Version: 1, Anchor: strings.Repeat("d", 64)}}
	request := &update.CorePauseRequest{Version: 1, IntentID: id, Sequence: 8, ManifestSHA256: manifest, Expected: snapshot}
	return update.CorePauseReceipt{Version: 1, State: "paused", PauseID: update.NewProbe(), Request: request, Core: snapshot}
}

func TestCustodyUpdateStatusNeverRestartsWorker(t *testing.T) {
	id := update.NewProbe()
	release := &core.ApplicationRelease{Sequence: 8, ManifestSHA256: strings.Repeat("a", 64), EngineSHA256: strings.Repeat("b", 64)}
	op := core.ApplicationUpdateOperation{ID: id, Release: release}
	for _, tc := range []struct {
		name  string
		state custodyWorkerState
		want  string
	}{
		{"running", custodyWorkerState{load: "loaded", active: "activating"}, "applying"},
		{"queued", custodyWorkerState{load: "loaded", active: "inactive", job: "42"}, "applying"},
		{"failed", custodyWorkerState{load: "loaded", active: "failed"}, "recovery"},
		{"masked", custodyWorkerState{load: "masked", active: "inactive"}, "recovery"},
		{"stopped", custodyWorkerState{load: "loaded", active: "inactive"}, "recovery"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			starts := 0
			c := &custodyMaintenanceController{
				owner: 1800,
				readRecord: func(int) (update.MaintenanceRecord, error) {
					return update.MaintenanceRecord{OperationID: id, Phase: "ready", Package: &update.MaintenancePackage{Sequence: release.Sequence, ReleaseSHA256: release.ManifestSHA256}}, nil
				},
				startWorker: func(context.Context, string) error { starts++; return nil },
				workerState: func(context.Context, string) (custodyWorkerState, error) { return tc.state, nil },
			}
			receipt, err := c.Status(t.Context(), op)
			if err != nil || receipt.Phase != tc.want || starts != 0 {
				t.Fatalf("status=%q starts=%d err=%v", receipt.Phase, starts, err)
			}
		})
	}
}

func TestCustodyRecoveryRefusesUnreleasedPauseAndForeignFence(t *testing.T) {
	id, root := update.NewProbe(), t.TempDir()
	coreHash, signerHash, releaseHash := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	bound := fixtureCustodyPause(id, releaseHash)
	files := []update.StageFile{
		{Artifact: update.Artifact{File: "fased-app", Size: 1, SHA256: coreHash}, Bytes: 1},
		{Artifact: update.Artifact{File: "fased-signer", Size: 1, SHA256: signerHash}, Bytes: 1},
	}
	recoveryBinding := update.RecoveryBinding{Version: 1, Pause: bound, Files: files}
	index := update.RecoveryIndex{Version: 1, Phase: "metadata-backed-up", Binding: recoveryBinding, BindingSHA256: update.RecoveryDigest(recoveryBinding), Snapshot: update.MetadataSnapshot{File: "metadata.sqlite", Size: 1, SHA256: coreHash}}
	binding := update.InstallBinding{Version: 1, Contracts: []update.Contract{{Component: "core"}, {Component: "signer"}}, Index: index, InstallationID: update.NewProbe(), PreviousCore: coreHash, PreviousSigner: signerHash, PreviousManifest: releaseHash, TargetCore: coreHash, TargetSigner: signerHash}
	installation := &update.Installation{Version: 1, Binding: binding, Digest: update.InstallDigest(binding), Phase: "completed", InstalledSequenceCommitted: true}
	if !update.ValidInstallation(*installation) {
		t.Fatal("fixture does not satisfy the real signer receipt contract")
	}
	target, err := json.Marshal(custodyinstall.Manifest{Root: root, CoreHash: coreHash})
	if err != nil {
		t.Fatal(err)
	}
	release := &core.ApplicationRelease{Sequence: 8, ManifestSHA256: releaseHash, EngineSHA256: coreHash}
	op := core.ApplicationUpdateOperation{ID: id, Phase: "applying", Release: release}
	r := update.MaintenanceRecord{OperationID: id, Phase: "completed", Installation: installation, Target: target, Package: &update.MaintenancePackage{Sequence: release.Sequence, ReleaseSHA256: releaseHash}}
	actual, starts := bound, 0
	c := &custodyMaintenanceController{
		owner:        1800,
		readRecord:   func(int) (update.MaintenanceRecord, error) { return r, nil },
		pauseStatus:  func(context.Context) (update.CorePauseReceipt, error) { return actual, nil },
		verifyTarget: func(custodyinstall.Manifest) error { return nil },
		startWorker:  func(context.Context, string) error { starts++; return nil },
		workerState: func(context.Context, string) (custodyWorkerState, error) {
			return custodyWorkerState{load: "loaded", active: "failed"}, nil
		},
	}
	marker := filepath.Join(root, "update-active.json")
	if err = os.WriteFile(marker, update.MaintenanceFence(id), 0600); err != nil {
		t.Fatal(err)
	}
	if err = c.Recover(t.Context(), op); err == nil || starts != 0 {
		t.Fatal("recovery tried to clear a held pause", starts, err)
	}
	actual.State = "released"
	actual.PauseID = update.NewProbe()
	if err = c.Recover(t.Context(), op); err == nil || starts != 0 {
		t.Fatal("recovery accepted another pause", starts, err)
	}
	actual = bound
	actual.State = "released"
	actual.Core.InstanceID = "foreign-instance"
	if err = c.Recover(t.Context(), op); err == nil || starts != 0 {
		t.Fatal("recovery accepted another core instance", starts, err)
	}
	actual = bound
	actual.State = "released"
	if err = os.WriteFile(marker, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = c.Recover(t.Context(), op); err == nil || starts != 0 {
		t.Fatal("recovery accepted a foreign fence", starts, err)
	}
	if err = os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err = syscall.Mkfifo(marker, 0600); err != nil {
		t.Fatal(err)
	}
	refused := make(chan error, 1)
	go func() { refused <- c.Recover(t.Context(), op) }()
	select {
	case err = <-refused:
		if err == nil || starts != 0 {
			t.Fatal("recovery accepted a FIFO fence", starts, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FIFO fence blocked recovery")
	}
	if err = os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(marker, update.MaintenanceFence(id), 0600); err != nil {
		t.Fatal(err)
	}
	if err = c.Recover(t.Context(), op); err != nil || starts != 1 {
		t.Fatal("exact released fence could not be cleaned", starts, err)
	}
}

func TestCustodyCompletionWaitsForFenceAndOriginalPause(t *testing.T) {
	root := t.TempDir()
	id := update.NewProbe()
	bound := fixtureCustodyPause(id, strings.Repeat("c", 64))
	installation := &update.Installation{}
	installation.Binding.Index.Binding.Pause = bound
	r := update.MaintenanceRecord{OperationID: id, Installation: installation}
	actual := bound
	actual.State = "released"
	c := &custodyMaintenanceController{pauseStatus: func(context.Context) (update.CorePauseReceipt, error) { return actual, nil }}
	marker := filepath.Join(root, "update-active.json")
	if err := os.WriteFile(marker, update.MaintenanceFence(id), 0600); err != nil {
		t.Fatal(err)
	}
	if ready, err := c.completionReady(t.Context(), r, root); err != nil || ready {
		t.Fatal("completed with original fence still present", ready, err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	actual.State = "paused"
	if ready, err := c.completionReady(t.Context(), r, root); err == nil || ready {
		t.Fatal("completed while original pause remained active", ready, err)
	}
	actual.State = "released"
	actual.PauseID = update.NewProbe()
	if ready, err := c.completionReady(t.Context(), r, root); err == nil || ready {
		t.Fatal("accepted a different released pause", ready, err)
	}
	actual = bound
	actual.State = "released"
	if ready, err := c.completionReady(t.Context(), r, root); err != nil || !ready {
		t.Fatal("original release and absent fence were not accepted", ready, err)
	}
}

func TestCustodyUpdateStatusObservesQueuedSuccessorWithoutStartingIt(t *testing.T) {
	id := update.NewProbe()
	release := &core.ApplicationRelease{Sequence: 8, ManifestSHA256: strings.Repeat("a", 64), EngineSHA256: strings.Repeat("b", 64)}
	starts := 0
	c := &custodyMaintenanceController{
		owner: 1800,
		readRecord: func(int) (update.MaintenanceRecord, error) {
			return update.MaintenanceRecord{OperationID: update.NewProbe(), Phase: "completed"}, nil
		},
		startWorker: func(context.Context, string) error { starts++; return nil },
		workerState: func(context.Context, string) (custodyWorkerState, error) {
			return custodyWorkerState{load: "loaded", active: "inactive", job: "42"}, nil
		},
	}
	receipt, err := c.Status(t.Context(), core.ApplicationUpdateOperation{ID: id, Release: release})
	if err != nil || receipt.Phase != "applying" || starts != 0 {
		t.Fatal("queued successor was restarted", receipt, starts, err)
	}
}

func TestCustodyExplicitRecoveryStartsOnlyStoppedOriginalWorker(t *testing.T) {
	id := update.NewProbe()
	release := &core.ApplicationRelease{Sequence: 8, ManifestSHA256: strings.Repeat("a", 64), EngineSHA256: strings.Repeat("b", 64)}
	op := core.ApplicationUpdateOperation{ID: id, Phase: "applying", Release: release}
	for _, tc := range []struct {
		name       string
		state      custodyWorkerState
		foreign    bool
		wantStarts int
		wantError  bool
	}{
		{"failed", custodyWorkerState{load: "loaded", active: "failed"}, false, 1, false},
		{"already queued", custodyWorkerState{load: "loaded", active: "inactive", job: "42"}, false, 0, false},
		{"already running", custodyWorkerState{load: "loaded", active: "activating"}, false, 0, false},
		{"masked", custodyWorkerState{load: "masked", active: "inactive"}, false, 0, true},
		{"foreign transaction", custodyWorkerState{load: "loaded", active: "failed"}, true, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			starts := 0
			c := &custodyMaintenanceController{
				owner: 1800,
				readRecord: func(int) (update.MaintenanceRecord, error) {
					recordID := id
					if tc.foreign {
						recordID = update.NewProbe()
					}
					return update.MaintenanceRecord{OperationID: recordID, Phase: "ready", Package: &update.MaintenancePackage{Sequence: release.Sequence, ReleaseSHA256: release.ManifestSHA256}}, nil
				},
				startWorker: func(_ context.Context, got string) error {
					if got != id {
						t.Fatal("started a different update", got)
					}
					starts++
					return nil
				},
				workerState: func(context.Context, string) (custodyWorkerState, error) { return tc.state, nil },
			}
			err := c.Recover(t.Context(), op)
			if (err != nil) != tc.wantError || starts != tc.wantStarts {
				t.Fatal("unexpected recovery action", starts, err)
			}
		})
	}
}

func TestCustodyWorkerSystemdStatusParsing(t *testing.T) {
	state, err := parseCustodyWorkerState([]byte("LoadState=loaded\nActiveState=failed\nJob=\n"))
	if err != nil || state.load != "loaded" || state.active != "failed" || state.job != "" {
		t.Fatal("failed systemd worker not recognized", state, err)
	}
	for _, malformed := range []string{
		"LoadState=loaded\nActiveState=inactive\n",
		"LoadState=loaded\nActiveState=failed\nJob=\nJob=42\n",
		"LoadState=loaded\nActiveState=failed\nJob\n",
	} {
		if _, err = parseCustodyWorkerState([]byte(malformed)); err == nil {
			t.Fatal("ambiguous worker status accepted", malformed)
		}
	}
}
