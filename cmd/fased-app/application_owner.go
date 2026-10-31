package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

// Publisher-reviewed Go binding only. No environment/CLI/file/HTTP source override
// or production credentials are accepted. Nil keeps public activation unapplied.
var compiledApplicationUpdateSource func(context.Context, core.ApplicationRelease, string) (*bootstrap.Prepared, error)

type applicationOwnerIPC struct{ directory, instance, id, lease string }

func (c *applicationOwnerIPC) call(ctx context.Context, action string, receipt *core.ApplicationUpdateReceipt) (core.ApplicationOwnerView, error) {
	client, e := core.NewClient(ctx, c.directory)
	if e != nil {
		return core.ApplicationOwnerView{}, e
	}
	defer client.Close()
	if c.instance != "" && client.Endpoint.InstanceID != c.instance {
		return core.ApplicationOwnerView{}, errors.New("maintenance destination changed")
	}
	var out core.ApplicationOwnerView
	e = client.Do(ctx, http.MethodPost, "/api/v1/updates/application/owner", core.ApplicationOwnerCommand{Action: action, ID: c.id, Lease: c.lease, Receipt: receipt}, &out)
	if e == nil {
		if c.instance != "" && out.View.InstanceID != c.instance {
			return out, errors.New("owner receipt belongs to another instance")
		}
		c.instance = out.View.InstanceID
	}
	return out, e
}
func maintainUpdateCommand(args []string, out, errOut io.Writer) error {
	if os.Geteuid() == 0 {
		return errors.New("maintenance must run as the existing ordinary OS owner")
	}
	f := flag.NewFlagSet("maintain-update", flag.ContinueOnError)
	f.SetOutput(errOut)
	remote := f.Bool("remote", false, "use the installed Remote maintenance path")
	unattended := f.Bool("unattended", false, "fixed installed Remote maintenance entry only")
	id := f.String("operation", "", "existing opaque operation")
	prefix := f.String("prefix", "", "installer-owned application directory")
	data := f.String("data-dir", "", "same installed instance directory")
	rollback := f.Bool("rollback", false, "explicit owner recovery; never restore data")
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 || !update.IDOK(*id) || (!*remote && (!filepath.IsAbs(*prefix) || !filepath.IsAbs(*data))) {
		return errors.New("fixed installed owner entry and exact operation required")
	}
	if *remote {
		if *prefix != "" || *data != "" {
			return errors.New("Remote uses its fixed installed identity, not custom directories")
		}
		m, e := vpsinstall.Load(os.Getuid())
		if e != nil {
			m, e = vpsinstall.LoadApplicationMaintenance(os.Getuid(), *id)
			if e != nil {
				return e
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
		defer cancel()
		if *unattended {
			image, err := os.Executable()
			if err != nil {
				return err
			}
			if err = vpsinstall.ValidateApplicationMaintenanceEntry(m, image); err != nil {
				return err
			}
			return maintainRemoteUpdateWithSystem(ctx, m, *id, *rollback, compiledApplicationUpdateSource, out, &custodyinstall.System{NonInteractive: true, Out: errOut})
		}
		return maintainRemoteUpdate(ctx, m, *id, *rollback, compiledApplicationUpdateSource, out)
	}
	if *unattended {
		return errors.New("unattended entry requires the fixed installed Remote actor")
	}
	paths := localinstall.Paths{Root: *prefix, Data: *data}
	m, e := localinstall.Load(paths)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
	defer cancel()
	return maintainLocalUpdate(ctx, m, *id, *rollback, compiledApplicationUpdateSource, out)
}
func maintainLocalUpdate(ctx context.Context, m localinstall.Manifest, id string, rollback bool, source func(context.Context, core.ApplicationRelease, string) (*bootstrap.Prepared, error), out io.Writer) error {
	if _, e := core.ReadApplicationReaderBootstrapRequest(m.Data, id); e == nil {
		return maintainLocalReaderUpdate(ctx, m, id, rollback, compiledApplicationReaderSource, out)
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	ipc := &applicationOwnerIPC{directory: m.Data, id: id}
	state, e := ipc.call(ctx, "inspect", nil)
	if e != nil {
		// Only a validated existing filesystem journal and frozen metadata can
		// recover without IPC. Missing state never authorizes a new installation.
		journal, je := localinstall.ApplicationUpdateState(localinstall.Paths{Root: m.Root, Data: m.Data})
		if je != nil || journal.ID != id {
			return maintainLocalReaderUpdate(ctx, m, id, rollback, compiledApplicationReaderSource, out)
		}
		state, e = core.ReadApplicationOwnerOffline(ctx, m.Data, id)
		if e != nil {
			return e
		}
		if state.View.Operation.Release.Sequence != journal.Sequence || state.View.Operation.Release.ManifestSHA256 != journal.ReleaseSHA256 {
			return errors.New("offline controller and metadata identities differ")
		}
		if journal.Phase == "completed" || journal.Phase == "rolled-back" {
			if e = verifyLocalTerminalOutcome(ctx, m, state, journal); e != nil {
				return e
			}
			// Both durable records and installed bytes already prove completion.
			// Only disable this exact actor; no IPC, restart or new source is needed.
			if e = localinstall.CompleteApplicationMaintenance(ctx, m, id, journal.Phase, localinstall.ApplicationMaintenanceRunner(m)); e != nil {
				return e
			}
			fmt.Fprintln(out, journal.Phase)
			return nil
		}
		ipc.instance = state.View.InstanceID
		if state.Checkpoint == nil || journal.Phase == "healthy-held" || journal.Phase == "rolled-back-held" {
			if journal.Phase != "prepared" && journal.Phase != "healthy-held" && journal.Phase != "rolled-back-held" {
				return errors.New("replacement without a durable checkpoint is held")
			}
			// Start only the already verified unchanged current generation. Start
			// validates its full inventory before any manager mutation.
			if e = localinstall.Start(ctx, localinstall.Paths{Root: m.Root, Data: m.Data}); e != nil {
				return e
			}
			deadline := time.NewTimer(10 * time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(100 * time.Millisecond)
			defer tick.Stop()
			for {
				fresh, err := ipc.call(ctx, "inspect", nil)
				if err == nil {
					state = fresh
					break
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-deadline.C:
					return errors.New("current core did not recover; preserve the transaction")
				case <-tick.C:
				}
			}
		}
	}
	o := state.View.Operation
	if o.Release == nil {
		return errors.New("admitted release missing")
	}
	ipc.lease = o.OwnerLease
	callbacks := localinstall.ApplicationUpdateCallbacks{
		BeforeStage: func(ctx context.Context) error {
			fresh, err := ipc.call(ctx, "inspect", nil)
			if err != nil {
				return err
			}
			return applicationOwnerMayStage(o, fresh.View.Operation)
		},
		BeforeStop: func(ctx context.Context, _, _ localinstall.Manifest) error {
			_, e := ipc.call(ctx, "prepare", nil)
			if e != nil && state.Checkpoint != nil {
				return core.CheckApplicationRollbackMetadata(ctx, m.Data, id, state.View.Schema)
			}
			return e
		},
		Health: func(ctx context.Context, target localinstall.Manifest) error {
			fresh, e := ipc.call(ctx, "inspect", nil)
			if e != nil {
				return e
			}
			if fresh.View.Schema != state.View.Schema {
				return errors.New("metadata reader changed; explicit compatible migration required")
			}
			if fresh.View.InstanceID != state.View.InstanceID || fresh.RunningSHA256 != target.Files["fased-app"] || fresh.BootID == o.BootID || localinstall.ManifestSHA256(target) == "" {
				return errors.New("update health identity mismatch")
			}
			return nil
		},
		RollbackAllowed: func(ctx context.Context, _, _ localinstall.Manifest) error {
			return core.CheckApplicationRollbackMetadata(ctx, m.Data, id, state.View.Schema)
		},
		Complete: func(ctx context.Context, target localinstall.Manifest, s localinstall.ApplicationUpdateStatus) error {
			phase := s.Phase
			if phase == "healthy-held" {
				phase = "completed"
			}
			if phase == "rolled-back-held" {
				phase = "rolled-back"
			}
			proof := core.ApplicationUpdateReceipt{ID: s.ID, Sequence: s.Sequence, ManifestSHA256: s.ReleaseSHA256, EngineSHA256: target.Files["fased-app"], Phase: phase}
			_, e := ipc.call(ctx, "finish", &proof)
			return e
		},
	}
	paths := localinstall.Paths{Root: m.Root, Data: m.Data}
	journal, journalErr := localinstall.ApplicationUpdateState(paths)
	staging, e := applicationOwnerStaging(state, journal, journalErr, id, localinstall.ManifestSHA256(m))
	if e != nil {
		return e
	}
	if staging == ownerReadmitPrepared {
		needed, err := localinstall.ApplicationUpdateNeedsSource(paths, id)
		if err != nil {
			return err
		}
		if !needed {
			staging = ownerResume
		}
	}
	if staging == ownerRejectUnstaged {
		if rollback {
			return errors.New("no staged transaction exists to roll back")
		}
		if state.RunningSHA256 != m.Files["fased-app"] {
			return errors.New("unstaged running application differs from installed baseline")
		}
		// Core independently refuses any paused fence. An absent filesystem
		// journal never proves a held operation may be discarded.
		if e = localinstall.RejectUnstagedApplication(ctx, paths, id, o.Release.Sequence, localinstall.ManifestSHA256(m), func() error {
			_, err := ipc.call(ctx, "reject", nil)
			return err
		}); e != nil {
			return e
		}
		if e = localinstall.CompleteApplicationMaintenance(ctx, m, id, "deferred", localinstall.ApplicationMaintenanceRunner(m)); e != nil {
			return e
		}
		fmt.Fprintln(out, "Update was not started. Check for updates and approve again.")
		return nil
	}
	if ipc.lease != "" && staging != ownerReadmitPrepared {
		if rollback {
			_, e = localinstall.RecoverApplication(ctx, paths, id, callbacks)
		} else {
			_, e = localinstall.ResumeApplication(ctx, paths, id, callbacks)
		}
	} else {
		if rollback {
			return errors.New("no previously claimed transaction to roll back")
		}
		freshClaim := ipc.lease == ""
		if source == nil {
			if freshClaim {
				_, _ = ipc.call(ctx, "reject", nil)
				_ = localinstall.CompleteApplicationMaintenance(ctx, m, id, "deferred", localinstall.ApplicationMaintenanceRunner(m))
			}
			return errors.New("trusted application release binding is not configured; activation remains disabled")
		}
		p, err := source(ctx, *o.Release, id)
		if err != nil {
			if freshClaim {
				_, _ = ipc.call(ctx, "reject", nil)
				_ = localinstall.CompleteApplicationMaintenance(ctx, m, id, "deferred", localinstall.ApplicationMaintenanceRunner(m))
			}
			return err
		}
		defer p.Close()
		driver, err := p.LocalApplicationController(paths, callbacks)
		if err != nil {
			return err
		}
		if freshClaim {
			release, err := driver.Check(ctx)
			if err != nil {
				return err
			}
			if release == nil || update.SnapshotDigest("application-release", release) != update.SnapshotDigest("application-release", o.Release) {
				return errors.New("owner independently admitted a different release")
			}
			ipc.lease = update.NewProbe()
			claimed, err := ipc.call(ctx, "claim", nil)
			if err != nil {
				return err
			}
			if !claimed.MayApply {
				if applicationOwnerClaimDeferred(o, claimed) {
					if err = localinstall.CompleteApplicationMaintenance(ctx, m, id, "deferred", localinstall.ApplicationMaintenanceRunner(m)); err != nil {
						return err
					}
					fmt.Fprintln(out, "Update deferred.")
					return nil
				}
				return errors.New("update deferred or already claimed; inspect the original transaction")
			}
			o = claimed.View.Operation
		}
		// Prepared.Apply independently binds the exact release and original
		// manifest. Re-admission finishes this lease; it never claims again.
		_, e = driver.Apply(ctx, o)
		if freshClaim && errors.Is(e, core.ErrApplicationUpdateNotStarted) {
			if _, rejectErr := ipc.call(ctx, "reject", nil); rejectErr != nil {
				return errors.Join(e, rejectErr)
			}
			if cleanupErr := localinstall.CompleteApplicationMaintenance(ctx, m, id, "deferred", localinstall.ApplicationMaintenanceRunner(m)); cleanupErr != nil {
				return errors.Join(e, cleanupErr)
			}
		}
	}
	if e != nil {
		return e
	}
	final, e := ipc.call(ctx, "inspect", nil)
	if e != nil {
		return e
	}
	if final.View.Operation.Phase != "completed" && final.View.Operation.Phase != "rolled-back" {
		return errors.New("owner outcome is held for recovery")
	}
	fmt.Fprintf(out, "%s\n", final.View.Operation.Phase)
	return localinstall.CompleteApplicationMaintenance(ctx, m, id, final.View.Operation.Phase, localinstall.ApplicationMaintenanceRunner(m))
}

type ownerStagingAction int

const (
	ownerResume ownerStagingAction = iota
	ownerRejectUnstaged
	ownerReadmitPrepared
)

// This classification creates no authority. Reject still needs unchanged
// inventory/running proof and core's independent no-paused-fence check; prepared
// re-admission still needs a fresh authenticated Prepared and the original lease.
func applicationOwnerStaging(state core.ApplicationOwnerView, journal localinstall.ApplicationUpdateStatus, journalErr error, id, currentManifest string) (ownerStagingAction, error) {
	o := state.View.Operation
	if o.OwnerLease == "" && o.Phase != "check-failed" {
		return ownerResume, nil
	}
	if o.ID != id || o.OwnerLease != "" && !update.IDOK(o.OwnerLease) || o.Release == nil || (o.Phase != "applying" && o.Phase != "recovery" && o.Phase != "completed" && o.Phase != "rolled-back" && o.Phase != "check-failed") {
		return ownerResume, errors.New("exact original owner dispatch required")
	}
	if errors.Is(journalErr, os.ErrNotExist) {
		if o.Phase == "completed" || o.Phase == "rolled-back" {
			return ownerResume, errors.New("terminal owner outcome has no installation receipt")
		}
		return ownerRejectUnstaged, nil
	}
	if journalErr != nil {
		return ownerResume, journalErr
	}
	if journal.ID != id {
		expected := journal.TargetManifestSHA256
		if journal.Phase == "rolled-back" {
			expected = journal.PreviousManifestSHA256
		}
		if (o.Phase == "applying" || o.Phase == "recovery" || o.Phase == "check-failed") && (journal.Phase == "completed" || journal.Phase == "rolled-back") && journal.Sequence < o.Release.Sequence && expected == currentManifest {
			return ownerRejectUnstaged, nil
		}
		return ownerResume, errors.New("foreign installation transaction remains held")
	}
	if journal.Sequence != o.Release.Sequence || journal.ReleaseSHA256 != o.Release.ManifestSHA256 || journal.EngineSHA256 != o.Release.EngineSHA256 {
		return ownerResume, errors.New("owner dispatch and installation journal differ")
	}
	if journal.Phase == "prepared" {
		if journal.ReaderBootstrap || (o.Phase != "applying" && o.Phase != "recovery") || journal.PreviousManifestSHA256 != currentManifest {
			return ownerResume, errors.New("prepared installation no longer has its original baseline")
		}
		return ownerReadmitPrepared, nil
	}
	return ownerResume, nil
}

func applicationOwnerMayStage(expected, current core.ApplicationUpdateOperation) error {
	if !update.IDOK(expected.OwnerLease) || current.ID != expected.ID || current.OwnerLease != expected.OwnerLease || current.Release == nil || expected.Release == nil || (current.Phase != "applying" && current.Phase != "recovery") || update.SnapshotDigest("application-release", current.Release) != update.SnapshotDigest("application-release", expected.Release) {
		return errors.New("original owner dispatch changed before staging")
	}
	return nil
}

// Policy deferral finishes only this unclaimed actor. The core remains
// available; no reject, pause release, checkpoint deletion or new work occurs.
func applicationOwnerClaimDeferred(expected core.ApplicationUpdateOperation, reply core.ApplicationOwnerView) bool {
	current := reply.View.Operation
	return !reply.MayApply && expected.Phase == "applying" && expected.OwnerLease == "" && update.IDOK(expected.ID) && current.ID == expected.ID && current.Phase == "available" && current.OwnerLease == "" && expected.Release != nil && current.Release != nil && update.SnapshotDigest("application-release", expected.Release) == update.SnapshotDigest("application-release", current.Release)
}

func verifyLocalTerminalOutcome(ctx context.Context, m localinstall.Manifest, state core.ApplicationOwnerView, journal localinstall.ApplicationUpdateStatus) error {
	o := state.View.Operation
	r := o.Receipt
	if (journal.Phase != "completed" && journal.Phase != "rolled-back") || o.Phase != journal.Phase || o.ID != journal.ID || o.Release == nil || r == nil || r.ID != journal.ID || r.Phase != journal.Phase || r.Sequence != journal.Sequence || r.ManifestSHA256 != journal.ReleaseSHA256 || o.Release.Sequence != journal.Sequence || o.Release.ManifestSHA256 != journal.ReleaseSHA256 || r.EngineSHA256 != m.Files["fased-app"] {
		return errors.New("offline terminal installation and metadata outcomes differ")
	}
	refuse := func() error { return errors.New("terminal verification cannot mutate an installation") }
	_, e := localinstall.ResumeApplication(ctx, localinstall.Paths{Root: m.Root, Data: m.Data}, journal.ID, localinstall.ApplicationUpdateCallbacks{
		BeforeStop:      func(context.Context, localinstall.Manifest, localinstall.Manifest) error { return refuse() },
		Health:          func(context.Context, localinstall.Manifest) error { return refuse() },
		RollbackAllowed: func(context.Context, localinstall.Manifest, localinstall.Manifest) error { return refuse() },
		Complete: func(context.Context, localinstall.Manifest, localinstall.ApplicationUpdateStatus) error {
			return refuse()
		},
	})
	return e
}
