package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
	"io"
	"os"
	"time"
)

// Fixed dropped-AppUID metadata client. No source, path, cookie or credential is
// accepted on stdin; config and executable identity are rechecked after drop.
func applicationOwnerClient(args []string, in *os.File, out, errOut io.Writer) error {
	f := flag.NewFlagSet("application-owner-client", flag.ContinueOnError)
	f.SetOutput(errOut)
	config := f.String("config", "", "fixed root-owned Remote identity")
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 || in == nil {
		return errors.New("fixed owner metadata helper required")
	}
	image, e := os.Executable()
	if e != nil {
		return e
	}
	m, e := vpsinstall.ValidateApplicationOwnerHelper(*config, image, os.Getuid())
	if e != nil {
		return e
	}
	b, e := io.ReadAll(io.LimitReader(in, 16385))
	if e != nil || len(b) > 16384 {
		return errors.New("bounded public owner command required")
	}
	var command core.ApplicationOwnerCommand
	if update.StrictJSON(b, &command) != nil || !update.IDOK(command.ID) {
		return errors.New("invalid owner command")
	}
	reader := command.Action == "reader-prepare" || command.Action == "reader-bootstrap" || command.Action == "reader-resume"
	if !reader && (len(b) > 4096 || command.ReaderBootstrap != nil) {
		return errors.New("bounded ordinary owner command required")
	}
	if image != m.Core && image != m.Root+"-update-"+command.ID+"/previous-core" && !(reader && image == m.Root+"-update-"+command.ID+"/next-core") {
		return errors.New("retained helper belongs to another operation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if result, handled, err := readerApplicationOwnerHelper(ctx, m, image, command); handled {
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(result)
	}
	var result core.ApplicationOwnerView
	if command.Action == "offline" {
		if command.Lease != "" || command.Receipt != nil {
			return errors.New("offline inspection cannot change authority")
		}
		result, e = core.ReadApplicationOwnerOffline(ctx, m.Data, command.ID)
	} else {
		client, err := core.NewClient(ctx, m.Data)
		if err != nil {
			return err
		}
		defer client.Close()
		e = client.Do(ctx, "POST", "/api/v1/updates/application/owner", command, &result)
	}
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(result)
}

type remoteApplicationOwner struct {
	m                   vpsinstall.Manifest
	system              *custodyinstall.System
	id, lease, instance string
}

func (c *remoteApplicationOwner) call(ctx context.Context, action string, p *core.ApplicationUpdateReceipt) (core.ApplicationOwnerView, error) {
	lease := c.lease
	if action == "offline" {
		lease = ""
	}
	v, e := vpsinstall.ApplicationOwnerCall(ctx, c.m, core.ApplicationOwnerCommand{Action: action, ID: c.id, Lease: lease, Receipt: p}, c.system)
	if e == nil {
		if c.instance != "" && v.View.InstanceID != c.instance {
			return v, errors.New("Remote instance changed")
		}
		c.instance = v.View.InstanceID
	}
	return v, e
}
func maintainRemoteUpdate(ctx context.Context, m vpsinstall.Manifest, id string, rollback bool, source func(context.Context, core.ApplicationRelease, string) (*bootstrap.Prepared, error), out io.Writer) error {
	return maintainRemoteUpdateWithSystem(ctx, m, id, rollback, source, out, &custodyinstall.System{In: os.Stdin, Out: os.Stderr})
}
func maintainRemoteUpdateWithSystem(ctx context.Context, m vpsinstall.Manifest, id string, rollback bool, source func(context.Context, core.ApplicationRelease, string) (*bootstrap.Prepared, error), out io.Writer, system *custodyinstall.System) error {
	status, statusErr := vpsinstall.ApplicationUpdateState(m.OwnerUID)
	return routeRemoteApplicationMaintenance(status, statusErr, id, func() error { return maintainSameReaderRemoteUpdate(ctx, m, id, rollback, source, out, system) }, func() error {
		return maintainRemoteReaderUpdate(ctx, m, id, rollback, compiledApplicationReaderSource, out, system)
	})
}

func maintainSameReaderRemoteUpdate(ctx context.Context, m vpsinstall.Manifest, id string, rollback bool, source func(context.Context, core.ApplicationRelease, string) (*bootstrap.Prepared, error), out io.Writer, system *custodyinstall.System) error {
	actor := &remoteApplicationOwner{m: m, id: id, system: system}
	state, e := actor.call(ctx, "inspect", nil)
	if e != nil {
		state, e = actor.call(ctx, "offline", nil)
		if e != nil {
			if handled, err := remoteReaderUpdateApplicable(ctx, m, id, system); handled {
				if err != nil {
					return err
				}
				return maintainRemoteReaderUpdate(ctx, m, id, rollback, compiledApplicationReaderSource, out, system)
			}
			return e
		}
	}
	o := state.View.Operation
	if system.NonInteractive {
		if e = vpsinstall.BeginApplicationMaintenance(ctx, m, id, system); e != nil {
			return e
		}
	}
	if o.Release == nil {
		return errors.New("admitted Remote release missing")
	}
	actor.lease = o.OwnerLease
	callbacks := vpsinstall.ApplicationUpdateCallbacks{
		BeforeStage: func(ctx context.Context) error {
			fresh, err := actor.call(ctx, "inspect", nil)
			if err != nil {
				return err
			}
			return applicationOwnerMayStage(o, fresh.View.Operation)
		},
		BeforeStop: func(ctx context.Context, _, _ vpsinstall.Manifest) error {
			_, e := actor.call(ctx, "prepare", nil)
			if e == nil {
				return nil
			}
			offline, oe := actor.call(ctx, "offline", nil)
			if oe == nil && offline.Checkpoint != nil && offline.View.Schema == state.View.Schema {
				return nil
			}
			return e
		},
		Health: func(ctx context.Context, target vpsinstall.Manifest) error {
			actor.m = target
			fresh, e := actor.call(ctx, "inspect", nil)
			if e != nil {
				return e
			}
			if fresh.View.Schema != state.View.Schema || fresh.RunningSHA256 != target.CoreSHA256 || fresh.BootID == o.BootID {
				return errors.New("Remote target health/reader identity mismatch")
			}
			return nil
		},
		RollbackAllowed: func(ctx context.Context, _, _ vpsinstall.Manifest) error {
			offline, e := actor.call(ctx, "offline", nil)
			if e != nil {
				return e
			}
			if offline.View.Schema != state.View.Schema {
				return errors.New("Remote old reader cannot restore migrated metadata")
			}
			return nil
		},
		Complete: func(ctx context.Context, target vpsinstall.Manifest, s vpsinstall.ApplicationUpdateStatus) error {
			actor.m = target
			phase := s.Phase
			if phase == "healthy-held" {
				phase = "completed"
			}
			if phase == "rolled-back-held" {
				phase = "rolled-back"
			}
			proof := core.ApplicationUpdateReceipt{ID: s.ID, Sequence: s.Sequence, ManifestSHA256: s.ReleaseSHA256, EngineSHA256: target.CoreSHA256, Phase: phase}
			_, e := actor.call(ctx, "finish", &proof)
			return e
		},
	}
	journal, journalErr := vpsinstall.ApplicationUpdateState(m.OwnerUID)
	staging, e := applicationOwnerStaging(state, journal, journalErr, id, vpsinstall.ManifestSHA256(m))
	if e != nil {
		return e
	}
	if staging == ownerReadmitPrepared {
		needed, err := vpsinstall.ApplicationUpdateNeedsSource(m.OwnerUID, id)
		if err != nil {
			return err
		}
		if !needed {
			staging = ownerResume
		}
	}
	if staging == ownerRejectUnstaged {
		if rollback || state.RunningSHA256 != m.CoreSHA256 {
			return errors.New("unstaged Remote dispatch cannot be rolled back or its running baseline changed")
		}
		if e = vpsinstall.RejectUnstagedApplication(ctx, m, id, o.Release.Sequence, func() error {
			_, err := actor.call(ctx, "reject", nil)
			return err
		}, system); e != nil {
			return e
		}
		if system.NonInteractive {
			if e = vpsinstall.CompleteApplicationMaintenance(ctx, m, id, "deferred", system); e != nil {
				return e
			}
		}
		fmt.Fprintln(out, "Update was not started. Check for updates and approve again.")
		return nil
	}
	if actor.lease != "" && staging != ownerReadmitPrepared {
		if rollback {
			_, e = vpsinstall.RecoverApplication(ctx, m, id, callbacks, actor.system)
		} else {
			_, e = vpsinstall.ResumeApplication(ctx, m, id, callbacks, actor.system)
		}
	} else {
		freshClaim := actor.lease == ""
		if rollback || source == nil {
			if freshClaim && !rollback && system.NonInteractive {
				if _, rejectErr := actor.call(ctx, "reject", nil); rejectErr == nil {
					if cleanupErr := vpsinstall.CompleteApplicationMaintenance(ctx, m, id, "deferred", system); cleanupErr != nil {
						return cleanupErr
					}
				}
			}
			return errors.New("trusted Remote release binding is unavailable; no installation started")
		}
		p, err := source(ctx, *o.Release, id)
		if err != nil {
			if freshClaim && system.NonInteractive {
				if _, rejectErr := actor.call(ctx, "reject", nil); rejectErr == nil {
					_ = vpsinstall.CompleteApplicationMaintenance(ctx, m, id, "deferred", system)
				}
			}
			return err
		}
		defer p.Close()
		driver, err := p.RemoteApplicationController(m, callbacks, actor.system)
		if err != nil {
			return err
		}
		if freshClaim {
			release, err := driver.Check(ctx)
			if err != nil || release == nil || update.SnapshotDigest("application-release", release) != update.SnapshotDigest("application-release", o.Release) {
				return errors.New("Remote independently admitted a different release")
			}
			actor.lease = update.NewProbe()
			claim, err := actor.call(ctx, "claim", nil)
			if err != nil {
				return err
			}
			if !claim.MayApply {
				if applicationOwnerClaimDeferred(o, claim) {
					if system.NonInteractive {
						if err = vpsinstall.CompleteApplicationMaintenance(ctx, m, id, "deferred", system); err != nil {
							return err
						}
					}
					fmt.Fprintln(out, "Update deferred.")
					return nil
				}
				return errors.New("Remote owner operation already claimed or deferred")
			}
			o = claim.View.Operation
		}
		_, e = driver.Apply(ctx, o)
		if freshClaim && errors.Is(e, core.ErrApplicationUpdateNotStarted) {
			if _, rejectErr := actor.call(ctx, "reject", nil); rejectErr != nil {
				return errors.Join(e, rejectErr)
			}
			if system.NonInteractive {
				if cleanupErr := vpsinstall.CompleteApplicationMaintenance(ctx, m, id, "deferred", system); cleanupErr != nil {
					return errors.Join(e, cleanupErr)
				}
			}
		}
	}
	if e != nil {
		return e
	}
	if system.NonInteractive {
		final, err := actor.call(ctx, "inspect", nil)
		if err != nil {
			final, err = actor.call(ctx, "offline", nil)
		}
		if err != nil {
			return err
		}
		if e = vpsinstall.CompleteApplicationMaintenance(ctx, actor.m, id, final.View.Operation.Phase, system); e != nil {
			return e
		}
	}
	_, e = io.Copy(out, bytes.NewBufferString("Remote update completed; financial permissions remain unchanged.\n"))
	return e
}
