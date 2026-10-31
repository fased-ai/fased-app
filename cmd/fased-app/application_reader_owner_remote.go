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
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

// Called only after fixed dropped-helper image/UID and strict stdin checks.
// Reader commands are never routed to the public/private HTTP owner endpoint.
func readerApplicationOwnerHelper(ctx context.Context, m vpsinstall.Manifest, image string, c core.ApplicationOwnerCommand) (core.ApplicationOwnerView, bool, error) {
	var out core.ApplicationOwnerView
	if c.Action != "reader-prepare" && c.Action != "reader-bootstrap" && c.Action != "reader-resume" {
		return out, false, nil
	}
	if c.Receipt != nil || image != m.Root+"-update-"+c.ID+"/next-core" {
		return out, true, errors.New("fixed admitted target reader helper required")
	}
	var r core.ApplicationReaderBootstrapRequest
	if c.ReaderBootstrap != nil {
		r = *c.ReaderBootstrap
	} else {
		var e error
		r, e = core.ReadApplicationReaderBootstrapRequest(m.Data, c.ID)
		if e != nil {
			if c.Action == "reader-prepare" && errors.Is(e, os.ErrNotExist) {
				v, oe := core.ReadApplicationOwnerOffline(ctx, m.Data, c.ID)
				if oe == nil && (c.Lease == "" || c.Lease == v.View.Operation.OwnerLease) {
					return v, true, nil
				}
			}
			return out, true, e
		}
	}
	if r.OperationID != c.ID || c.Lease != "" && c.Lease != r.OwnerLease {
		return out, true, errors.New("reader-bootstrap lease or operation changed")
	}
	if e := vpsinstall.ValidateApplicationReaderOwnerRequest(m, image, r); e != nil {
		return out, true, e
	}
	switch c.Action {
	case "reader-prepare":
		if c.ReaderBootstrap != nil {
			client, e := core.NewClient(ctx, m.Data)
			if e != nil {
				return out, true, e
			}
			defer client.Close()
			if e = client.LoadSession(ctx, m.Data); e != nil {
				return out, true, errors.New("sign in to the existing Remote CLI before reader upgrade")
			}
			var snapshot update.CoreSnapshot
			if e = client.Do(ctx, http.MethodGet, "/api/v1/updates/inventory?probe="+update.NewProbe(), nil, &snapshot); e != nil {
				return out, true, e
			}
			if e = core.CheckApplicationReaderPredecessor(ctx, m.Data, snapshot); e != nil {
				return out, true, e
			}
			pause := update.CorePauseRequest{Version: 1, IntentID: c.ID, Sequence: r.Release.Sequence, ManifestSHA256: r.Release.ManifestSHA256, Expected: snapshot}
			if e = client.Do(ctx, http.MethodPost, "/api/v1/updates/pause", update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()}, &r.Predecessor); e != nil {
				return out, true, e
			}
			if r.Predecessor.State == "paused" {
				p := r.Predecessor.Request
				if p == nil || p.IntentID != c.ID || p.Sequence != r.Release.Sequence || p.ManifestSHA256 != r.Release.ManifestSHA256 || !update.SameCoreMetadata(r.Predecessor.Core, snapshot) {
					return out, true, errors.New("another source metadata fence remains active")
				}
			} else if e = client.Do(ctx, http.MethodPost, "/api/v1/updates/pause", update.CorePauseCommand{Action: "pause", ProbeID: update.NewProbe(), Request: &pause}, &r.Predecessor); e != nil {
				return out, true, e
			}
			if e = core.StageApplicationReaderBootstrap(ctx, m.Data, r); e != nil {
				return out, true, e
			}
		}
	case "reader-bootstrap":
		if c.ReaderBootstrap == nil {
			return out, true, errors.New("exact staged reader-bootstrap request required")
		}
		if _, e := core.BootstrapApplicationReader(ctx, m.Data, r); e != nil {
			return out, true, e
		}
	case "reader-resume":
		if c.ReaderBootstrap != nil {
			return out, true, errors.New("resume cannot replace reader request")
		}
		if _, e := core.ResumeApplicationReaderBootstrap(ctx, m.Data, c.ID); e != nil {
			return out, true, e
		}
	}
	if c.Action == "reader-prepare" {
		out = core.ApplicationOwnerView{ReaderBootstrap: &r, BootID: r.Predecessor.Core.BootID, View: core.ApplicationUpdateView{Version: 1, InstanceID: r.Predecessor.Core.InstanceID, Schema: 33, Operation: core.ApplicationUpdateOperation{ID: r.OperationID, Phase: "applying", OwnerLease: r.OwnerLease, Release: &r.Release, BootID: r.Predecessor.Core.BootID}}}
		return out, true, nil
	}
	v, e := core.ReadApplicationOwnerOffline(ctx, m.Data, c.ID)
	v.ReaderBootstrap = &r
	return v, true, e
}

func remoteReaderUpdateApplicable(ctx context.Context, m vpsinstall.Manifest, id string, system *custodyinstall.System) (bool, error) {
	if journal, e := vpsinstall.ApplicationUpdateState(m.OwnerUID); e == nil && journal.ID == id {
		return journal.ReaderBootstrap, nil
	}
	snapshot, e := vpsinstall.ApplicationReaderInventory(ctx, m, id, system)
	if e != nil {
		return true, errors.New("cannot verify the installed source reader; sign in with fased-app vps login or inspect the preserved transaction")
	}
	switch snapshot.State.Version {
	case 33:
		return true, nil
	case core.StateSchema:
		return false, nil
	case 34:
		return true, errors.New("schema34 belongs to the separately preserved recovery branch; installed data is unchanged")
	default:
		return true, errors.New("unsupported installed source reader; preserve original state")
	}
}

// Durable controller classification is selected before touching target IPC.
// A failed candidate/helper cannot turn an ordinary rollback into a reader
// migration, nor turn a migrated reader into an old-schema rollback.
func routeRemoteApplicationMaintenance(status vpsinstall.ApplicationUpdateStatus, statusErr error, id string, ordinary, reader func() error) error {
	if statusErr != nil && !errors.Is(statusErr, os.ErrNotExist) {
		return statusErr
	}
	if statusErr == nil {
		terminal := status.Phase == "completed" || status.Phase == "rolled-back"
		if status.ID != id && !terminal {
			return errors.New("another Remote installation transaction requires recovery")
		}
		if status.ID == id && status.ReaderBootstrap && !terminal {
			return reader()
		}
	}
	return ordinary()
}

func maintainRemoteReaderUpdate(ctx context.Context, m vpsinstall.Manifest, id string, rollback bool, source func(context.Context, string) (*bootstrap.Prepared, error), out io.Writer, system *custodyinstall.System) error {
	if rollback {
		return errors.New("reader-bootstrap preserves forward recovery; it never downgrades schema35")
	}
	actor := &remoteApplicationOwner{m: m, id: id, system: system}
	var request core.ApplicationReaderBootstrapRequest
	callbacks := vpsinstall.ApplicationUpdateCallbacks{
		BeforeStop: func(ctx context.Context, a, b vpsinstall.Manifest) error {
			if a.CoreSHA256 != request.PreviousEngineSHA256 || b.CoreSHA256 != request.Release.EngineSHA256 {
				return errors.New("reader bootstrap code identity changed")
			}
			c := core.ApplicationOwnerCommand{Action: "reader-prepare", ID: id, Lease: request.OwnerLease}
			if request.Predecessor.Request == nil {
				c.ReaderBootstrap = &request
			}
			v, e := vpsinstall.ApplicationOwnerCall(ctx, actor.m, c, system)
			if e != nil {
				return e
			}
			if v.ReaderBootstrap == nil {
				return errors.New("predecessor pause proof missing")
			}
			request = *v.ReaderBootstrap
			actor.lease = request.OwnerLease
			actor.instance = request.Predecessor.Core.InstanceID
			return nil
		},
		AfterStop: func(ctx context.Context, _, _ vpsinstall.Manifest) error {
			_, e := vpsinstall.ApplicationOwnerCall(ctx, actor.m, core.ApplicationOwnerCommand{Action: "reader-bootstrap", ID: id, Lease: request.OwnerLease, ReaderBootstrap: &request}, system)
			return e
		},
		Health: func(ctx context.Context, target vpsinstall.Manifest) error {
			actor.m = target
			v, e := actor.call(ctx, "inspect", nil)
			if e != nil {
				return e
			}
			if v.View.Schema != core.StateSchema || v.RunningSHA256 != target.CoreSHA256 || v.BootID == request.Predecessor.Core.BootID {
				return errors.New("new Remote reader health mismatch")
			}
			return nil
		},
		RollbackAllowed: func(context.Context, vpsinstall.Manifest, vpsinstall.Manifest) error {
			return errors.New("schema33 reader bootstrap requires forward recovery")
		},
		Complete: func(ctx context.Context, target vpsinstall.Manifest, s vpsinstall.ApplicationUpdateStatus) error {
			actor.m = target
			phase := s.Phase
			if phase == "healthy-held" {
				phase = "completed"
			}
			proof := core.ApplicationUpdateReceipt{ID: id, Sequence: s.Sequence, ManifestSHA256: s.ReleaseSHA256, EngineSHA256: target.CoreSHA256, Phase: phase}
			_, e := actor.call(ctx, "finish", &proof)
			return e
		},
	}
	if journal, e := vpsinstall.ApplicationUpdateState(m.OwnerUID); e == nil && journal.ID == id {
		v, e := vpsinstall.ApplicationOwnerCall(ctx, m, core.ApplicationOwnerCommand{Action: "reader-prepare", ID: id}, system)
		if e == nil && v.ReaderBootstrap != nil {
			request = *v.ReaderBootstrap
			actor.lease = request.OwnerLease
			actor.instance = request.Predecessor.Core.InstanceID
			if journal.Sequence != request.Release.Sequence || journal.ReleaseSHA256 != request.Release.ManifestSHA256 || journal.EngineSHA256 != request.Release.EngineSHA256 {
				return errors.New("reader request differs from retained installation")
			}
			_, e = vpsinstall.ResumeApplication(ctx, m, id, callbacks, system)
			return e
		}
		// No old33 lease was saved yet. Reuse only the admitted retained target,
		// authenticate the source session again, and persist its first lease.
		template, te := vpsinstall.ApplicationReaderBootstrapTemplate(m, id, update.NewProbe())
		if te != nil {
			return te
		}
		v, e = vpsinstall.ApplicationOwnerCall(ctx, m, core.ApplicationOwnerCommand{Action: "reader-prepare", ID: id, Lease: template.OwnerLease, ReaderBootstrap: &template}, system)
		if e != nil || v.ReaderBootstrap == nil {
			return errors.New("existing Remote transaction has no authenticated staged reader request; preserve originals")
		}
		request = *v.ReaderBootstrap
		actor.lease = request.OwnerLease
		actor.instance = request.Predecessor.Core.InstanceID
		_, e = vpsinstall.ResumeApplication(ctx, m, id, callbacks, system)
		return e
	}
	snapshot, e := vpsinstall.ApplicationReaderInventory(ctx, m, id, system)
	if e != nil {
		return e
	}
	if snapshot.State.Version != 33 {
		return errors.New("only installed schema33 may enter reader-bootstrap; schema34 stays unchanged")
	}
	if source == nil {
		return errors.New("trusted reader-bootstrap release binding is not configured; activation remains disabled")
	}
	p, e := source(ctx, id)
	if e != nil {
		return e
	}
	defer p.Close()
	driver, e := p.RemoteApplicationController(m, callbacks, system)
	if e != nil {
		return e
	}
	release, e := driver.Check(ctx)
	if e != nil {
		return e
	}
	if release == nil {
		return errors.New("new independently verified reader release required")
	}
	request = core.ApplicationReaderBootstrapRequest{Version: 1, OperationID: id, OwnerLease: update.NewProbe(), PreviousEngineSHA256: m.CoreSHA256, Release: *release}
	_, e = driver.Apply(ctx, core.ApplicationUpdateOperation{ID: id, Phase: "applying", OwnerLease: request.OwnerLease, Release: release, BootID: snapshot.BootID})
	if e == nil {
		fmt.Fprintln(out, "completed")
	}
	return e
}
