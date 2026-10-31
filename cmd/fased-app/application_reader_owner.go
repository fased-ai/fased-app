package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

// Independent publisher binding for an old engine which has no saved release
// record. No CLI/environment source override exists; nil keeps activation held.
var compiledApplicationReaderSource func(context.Context, string) (*bootstrap.Prepared, error)

func readerLocalCallbacks(m localinstall.Manifest, request *core.ApplicationReaderBootstrapRequest) localinstall.ApplicationUpdateCallbacks {
	ipc := func() *applicationOwnerIPC {
		return &applicationOwnerIPC{directory: m.Data, instance: request.Predecessor.Core.InstanceID, id: request.OperationID, lease: request.OwnerLease}
	}
	return localinstall.ApplicationUpdateCallbacks{
		BeforeStop: func(ctx context.Context, previous, target localinstall.Manifest) error {
			if request.Version == 2 && request.Predecessor.State == "prepared" {
				if e := stageLocalReader35(ctx, m.Data, request); e != nil {
					return e
				}
			}
			if previous.Files["fased-app"] != request.PreviousEngineSHA256 || target.Files["fased-app"] != request.Release.EngineSHA256 {
				return errors.New("reader-bootstrap installed code identity changed")
			}
			if _, e := core.ReadApplicationOwnerOffline(ctx, m.Data, request.OperationID); e == nil {
				return nil
			}
			return core.StageApplicationReaderBootstrap(ctx, m.Data, *request)
		},
		AfterStop: func(ctx context.Context, _, _ localinstall.Manifest) error {
			_, e := core.BootstrapApplicationReader(ctx, m.Data, *request)
			return e
		},
		Health: func(ctx context.Context, target localinstall.Manifest) error {
			v, e := ipc().call(ctx, "inspect", nil)
			if e != nil {
				return e
			}
			if v.View.Schema != core.StateSchema || v.View.Operation.OwnerLease != request.OwnerLease || v.View.Operation.Release == nil || update.SnapshotDigest("reader-release", v.View.Operation.Release) != update.SnapshotDigest("reader-release", &request.Release) || v.RunningSHA256 != target.Files["fased-app"] || v.BootID == request.Predecessor.Core.BootID {
				return errors.New("new reader health differs from authenticated handoff")
			}
			return nil
		},
		RollbackAllowed: func(ctx context.Context, _, _ localinstall.Manifest) error {
			return core.CheckApplicationRollbackMetadata(ctx, m.Data, request.OperationID, request.Predecessor.Core.State.Version)
		},
		Complete: func(ctx context.Context, target localinstall.Manifest, s localinstall.ApplicationUpdateStatus) error {
			proof := core.ApplicationUpdateReceipt{ID: s.ID, Sequence: s.Sequence, ManifestSHA256: s.ReleaseSHA256, EngineSHA256: target.Files["fased-app"], Phase: s.Phase}
			if proof.Phase == "healthy-held" {
				proof.Phase = "completed"
			}
			if proof.Phase == "rolled-back-held" {
				proof.Phase = "rolled-back"
			}
			_, e := ipc().call(ctx, "finish", &proof)
			return e
		},
	}
}

func maintainLocalReaderUpdate(ctx context.Context, m localinstall.Manifest, id string, rollback bool, source func(context.Context, string) (*bootstrap.Prepared, error), out io.Writer) error {
	if rollback {
		return errors.New("reader-bootstrap never downgrades migrated metadata; resume the original transaction")
	}
	paths := localinstall.Paths{Root: m.Root, Data: m.Data}
	if request, e := core.ReadApplicationReaderBootstrapRequest(m.Data, id); e == nil {
		state, e := localinstall.ApplicationUpdateState(paths)
		if errors.Is(e, os.ErrNotExist) {
			if source == nil {
				return errors.New("original reader lease is staged; identical signed release must be reverified before initial code staging")
			}
			p, e := source(ctx, id)
			if e != nil {
				return e
			}
			defer p.Close()
			driver, e := p.LocalApplicationController(paths, readerLocalCallbacks(m, &request))
			if e != nil {
				return e
			}
			release, e := driver.Check(ctx)
			if e != nil {
				return e
			}
			if release == nil || update.SnapshotDigest("reader-release", release) != update.SnapshotDigest("reader-release", &request.Release) {
				return errors.New("signed release differs from staged reader lease")
			}
			_, e = driver.Apply(ctx, core.ApplicationUpdateOperation{ID: id, Phase: "applying", OwnerLease: request.OwnerLease, Release: &request.Release, BootID: request.Predecessor.Core.BootID})
			if e != nil {
				return e
			}
			return finishLocalReaderUpdate(ctx, paths, out)
		}
		if e != nil || state.ID != id || state.Sequence != request.Release.Sequence || state.ReleaseSHA256 != request.Release.ManifestSHA256 || state.EngineSHA256 != request.Release.EngineSHA256 {
			return errors.New("reader-bootstrap request differs from original installation journal")
		}
		h := readerLocalCallbacks(m, &request)
		if _, e = localinstall.ResumeApplication(ctx, paths, id, h); e != nil {
			return e
		}
		return finishLocalReaderUpdate(ctx, paths, out)
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	client, e := core.NewClient(ctx, m.Data)
	if e != nil {
		return e
	}
	defer client.Close()
	if e = client.LoadSession(ctx, m.Data); e != nil {
		return errors.New("sign in to the existing Local instance before upgrading its reader")
	}
	var snapshot update.CoreSnapshot
	if e = client.Do(ctx, http.MethodGet, "/api/v1/updates/inventory?probe="+update.NewProbe(), nil, &snapshot); e != nil {
		return e
	}
	if snapshot.State.Version != 33 && snapshot.State.Version != 35 {
		return errors.New("unsupported installed predecessor; preserve state")
	}
	if e = core.CheckApplicationReaderPredecessor(ctx, m.Data, snapshot); e != nil {
		return e
	}
	if source == nil {
		return errors.New("trusted reader-bootstrap release binding is not configured; activation remains disabled")
	}
	p, e := source(ctx, id)
	if e != nil {
		return e
	}
	defer p.Close()
	var request core.ApplicationReaderBootstrapRequest
	h := readerLocalCallbacks(m, &request)
	driver, e := p.LocalApplicationController(paths, h)
	if e != nil {
		return e
	}
	release, e := driver.Check(ctx)
	if e != nil {
		return e
	}
	if release == nil {
		return errors.New("independently authenticated newer reader release required")
	}
	pauseRequest := update.CorePauseRequest{Version: 1, IntentID: id, Sequence: release.Sequence, ManifestSHA256: release.ManifestSHA256, Expected: snapshot}
	if snapshot.State.Version == 35 {
		prepared := update.CorePauseReceipt{Version: 1, State: "prepared", Core: snapshot, Request: &pauseRequest}
		request, e = p.ApplicationReaderBootstrapRequest(id, update.NewProbe(), m.Files["fased-app"], prepared)
		if e != nil {
			return e
		}
		if e = core.PrepareApplicationReaderBootstrap(ctx, m.Data, request); e != nil {
			return e
		}
		if e = stageLocalReader35(ctx, m.Data, &request); e != nil {
			return e
		}
	} else {
		var paused update.CorePauseReceipt
		if e = client.Do(ctx, http.MethodPost, "/api/v1/updates/pause", update.CorePauseCommand{Action: "pause", ProbeID: update.NewProbe(), Request: &pauseRequest}, &paused); e != nil {
			return e
		}
		request, e = p.ApplicationReaderBootstrapRequest(id, update.NewProbe(), m.Files["fased-app"], paused)
		if e != nil {
			return e
		}
		if e = core.StageApplicationReaderBootstrap(ctx, m.Data, request); e != nil {
			return e
		}
	}
	_, e = driver.Apply(ctx, core.ApplicationUpdateOperation{ID: id, Phase: "applying", OwnerLease: request.OwnerLease, Release: &request.Release, BootID: snapshot.BootID})
	if e != nil {
		return e
	}
	return finishLocalReaderUpdate(ctx, paths, out)
}

func finishLocalReaderUpdate(ctx context.Context, paths localinstall.Paths, out io.Writer) error {
	return finishLocalReaderUpdateWith(ctx, paths, out, localinstall.PrepareInstallationMaintenance)
}

func finishLocalReaderUpdateWith(ctx context.Context, paths localinstall.Paths, out io.Writer, prepare func(context.Context, localinstall.Manifest, bool, io.Writer) string) error {
	current, e := localinstall.Load(paths)
	if e != nil {
		return e
	}
	status, e := localinstall.ApplicationUpdateState(paths)
	if e != nil || status.Phase != "completed" {
		return errors.New("reader update is not yet completed; preserve the original transaction")
	}
	prepare(ctx, current, true, out)
	fmt.Fprintln(out, "completed")
	return nil
}

// Finalize only the durable35 intent recorded before the old source paused.
func stageLocalReader35(ctx context.Context, directory string, r *core.ApplicationReaderBootstrapRequest) error {
	client, e := core.NewClient(ctx, directory)
	if e != nil {
		return e
	}
	defer client.Close()
	if e = client.LoadSession(ctx, directory); e != nil {
		return errors.New("sign in to the original instance before reader handoff")
	}
	var current update.CoreSnapshot
	if e = client.Do(ctx, http.MethodGet, "/api/v1/updates/inventory?probe="+update.NewProbe(), nil, &current); e != nil {
		return e
	}
	if !update.SameCoreMetadata(current, r.Predecessor.Core) {
		return errors.New("original prepared reader state changed")
	}
	var paused update.CorePauseReceipt
	if e = client.Do(ctx, http.MethodPost, "/api/v1/updates/pause", update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()}, &paused); e != nil {
		return e
	}
	if paused.State != "paused" {
		fresh := *r.Predecessor.Request
		fresh.Expected = current
		if e = client.Do(ctx, http.MethodPost, "/api/v1/updates/pause", update.CorePauseCommand{Action: "pause", ProbeID: update.NewProbe(), Request: &fresh}, &paused); e != nil {
			return e
		}
	}
	final := *r
	final.Predecessor = paused
	if e = core.StageApplicationReaderBootstrap(ctx, directory, final); e != nil {
		return e
	}
	*r = final
	return nil
}
