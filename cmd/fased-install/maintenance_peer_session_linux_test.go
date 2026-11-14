//go:build linux

package main

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestMaintenanceClientUsesAuthenticatedPrivateCSRF(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	srv, err := core.Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close(context.Background()) })
	code, err := srv.Store.IssueClaimCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = srv.Store.Claim(t.Context(), code.Code, "fixture-only-owner-password"); err != nil {
		t.Fatal(err)
	}
	client, err := core.NewClient(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err = prepareMaintenanceClient(t.Context(), client, dir); err != nil {
		t.Fatal(err)
	}
	var receipt update.CorePauseReceipt
	err = client.Do(t.Context(), http.MethodPost, "/api/v1/updates/pause", update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()}, &receipt)
	if err != nil {
		t.Fatalf("authenticated maintenance status through private IPC: %v", err)
	}
	if receipt.State != "idle" {
		t.Fatalf("unexpected fixture pause state: %q", receipt.State)
	}
	if err = srv.Store.Logout(t.Context(), client.Token); err != nil {
		t.Fatal(err)
	}
	if err = client.Do(t.Context(), http.MethodPost, "/api/v1/updates/pause", update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()}, nil); err == nil {
		t.Fatal("revoked maintenance session was accepted")
	}
	if err = client.RefreshCSRF(t.Context()); !errors.Is(err, core.ErrUnauthorized) || client.CSRF != "" {
		t.Fatal("revoked session retained CSRF authority", err)
	}
}

type maintenanceReconcileController struct {
	release core.ApplicationRelease
	applies atomic.Int32
}

func (f *maintenanceReconcileController) Check(context.Context) (*core.ApplicationRelease, error) {
	r := f.release
	return &r, nil
}
func (f *maintenanceReconcileController) Apply(context.Context, core.ApplicationUpdateOperation) (core.ApplicationUpdateReceipt, error) {
	f.applies.Add(1)
	return core.ApplicationUpdateReceipt{}, errors.New("fixture interrupted after admission")
}
func (f *maintenanceReconcileController) Status(_ context.Context, o core.ApplicationUpdateOperation) (core.ApplicationUpdateReceipt, error) {
	return core.ApplicationUpdateReceipt{ID: o.ID, Sequence: o.Release.Sequence, ManifestSHA256: o.Release.ManifestSHA256, EngineSHA256: o.Release.EngineSHA256, Phase: "completed"}, nil
}

func TestMaintenanceReconcilesOnlyOriginalApplicationUpdate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	srv, err := core.Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close(context.Background()) })
	code, err := srv.Store.IssueClaimCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = srv.Store.Claim(t.Context(), code.Code, "fixture-only-owner-password"); err != nil {
		t.Fatal(err)
	}
	f := &maintenanceReconcileController{release: core.ApplicationRelease{Version: "0.1.11-test", Sequence: 8, ManifestSHA256: strings.Repeat("a", 64), EngineSHA256: strings.Repeat("b", 64), Target: update.Target{OS: runtime.GOOS, Arch: runtime.GOARCH}}}
	if err = srv.Store.BindApplicationUpdateController(f); err != nil {
		t.Fatal(err)
	}
	client, err := core.NewClient(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err = prepareMaintenanceClient(t.Context(), client, dir); err != nil {
		t.Fatal(err)
	}
	command := func(action string) {
		t.Helper()
		v, e := srv.Store.ApplicationUpdates(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		c := core.ApplicationUpdateCommand{Action: action, Revision: v.Revision, PolicyRevision: v.PolicyRevision}
		if action == "apply" {
			c.ManifestSHA256 = v.Operation.Release.ManifestSHA256
		}
		if _, e = srv.Store.CommandApplicationUpdate(t.Context(), c); e != nil {
			t.Fatal(e)
		}
	}
	wait := func(phase string) core.ApplicationUpdateView {
		t.Helper()
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) {
			v, e := srv.Store.ApplicationUpdates(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			if v.Operation.Phase == phase {
				return v
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("application update did not reach %s", phase)
		return core.ApplicationUpdateView{}
	}
	command("check")
	wait("available")
	command("apply")
	v := wait("recovery")
	r := update.MaintenanceRecord{OperationID: v.Operation.ID, Package: &update.MaintenancePackage{Sequence: f.release.Sequence, ReleaseSHA256: f.release.ManifestSHA256, Components: []update.Component{{Contract: update.Contract{Component: "core"}, Artifact: update.Artifact{SHA256: f.release.EngineSHA256}}}}}
	foreign := r
	foreign.OperationID = update.NewProbe()
	if err = reconcileMaintenanceApplication(t.Context(), client, foreign); err == nil {
		t.Fatal("accepted a different operation")
	}
	if got := wait("recovery"); got.Operation.ID != r.OperationID {
		t.Fatal("foreign request changed the operation")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	if err = reconcileMaintenanceApplication(ctx, client, r); err != nil {
		t.Fatal(err)
	}
	if err = reconcileMaintenanceApplication(ctx, client, r); err != nil {
		t.Fatal("completed recovery was not idempotent", err)
	}
	v = wait("completed")
	if v.Operation.ID != r.OperationID || f.applies.Load() != 1 {
		t.Fatal("recovery replaced or duplicated the original update")
	}
}
