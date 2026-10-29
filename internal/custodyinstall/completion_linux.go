//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
)

func installerIntent(m Manifest) journal {
	return journal{Version: m.Version, Network: m.Network, ID: m.ID, Owner: m.Owner, CoreHash: m.CoreHash, SignerHash: m.SignerHash, Listen: m.Listen, Enrollment: m.Enrollment, Location: m.Location, PublicOrigin: m.PublicOrigin}
}
func readyRecord(m Manifest) []byte {
	return mustJSON(map[string]any{"schema": 1, "manifest": sum(mustJSON(m)), "fixtureOnly": true})
}

// All records must be classified before commit. The owner journal is metadata,
// never authority; a saved host receipt cannot substitute for the live signer.
func ownerCompletionRecord(t InstallTransaction, write bool) error {
	dir, e := ownerJournal()
	if e != nil {
		return e
	}
	p := dir + "/intent.json"
	b, e := core.ReadPrivateFile(p)
	if e != nil {
		return e
	}
	var j journal
	if strict(b, &j) != nil || (j != installerIntent(t.Base) && j != installerIntent(t.Target)) {
		return errors.New("foreign owner installer record; preserve it")
	}
	if !write {
		return nil
	}
	if j != installerIntent(t.Target) {
		if e = core.WritePrivateJSON(p, installerIntent(t.Target)); e != nil {
			return e
		}
	}
	d, e := os.Open(dir)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}

func runCompletion(ctx context.Context, t InstallTransaction, o installOps) (*update.Installation, error) {
	if !t.valid() || o.release == nil || o.ownerRecord == nil {
		return nil, errors.New("complete protected installation context required")
	}
	if e := o.topology(t.Target); e != nil {
		return nil, e
	}
	// Completion never repairs executables or silently rolls back a committed pair.
	for _, p := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{"fased-app", nil, 0755}, {"fased-signer", nil, 0755}, {"install.json", mustJSON(t.Target), 0644},
	} {
		f, e := o.read(t.Target.Root + "/" + p.name)
		if e != nil {
			return nil, e
		}
		hash := t.Target.CoreHash
		if p.name == "fased-signer" {
			hash = t.Target.SignerHash
		}
		if (p.data != nil && !known(f, p.data, p.mode)) || (p.data == nil && (!f.Regular || f.UID != 0 || f.Mode != p.mode || sum(f.Data) != hash)) {
			return nil, errors.New("completion requires exact installed candidate pair and inventory")
		}
	}
	records := []struct {
		name      string
		old, next []byte
	}{
		{"intent.json", mustJSON(installerIntent(t.Base)), mustJSON(installerIntent(t.Target))},
		{"ready.json", readyRecord(t.Base), readyRecord(t.Target)},
	}
	for _, p := range records {
		f, e := o.read(t.Base.Root + "/" + p.name)
		if e != nil {
			return nil, e
		}
		if !known(f, p.old, 0644) && !known(f, p.next, 0644) {
			return nil, errors.New("foreign installer completion record; preserve it")
		}
	}
	if e := o.ownerRecord(false); e != nil {
		return nil, e
	}
	current, e := o.state(ctx)
	if e != nil {
		return nil, e
	}
	digest := update.InstallDigest(t.Binding)
	if current == nil || !update.ValidInstallation(*current) || current.Digest != digest || (current.Phase != "healthy-held" && current.Phase != "committed-held" && current.Phase != "completed") {
		return nil, errors.New("verified healthy or committed current signer installation required")
	}
	active, e := o.read(t.Base.Root + "/update-active.json")
	if e != nil && !(errors.Is(e, os.ErrNotExist) && current.Phase == "completed") {
		return nil, e
	}
	if e == nil && !known(active, mustJSON(phaseRecord{1, digest, t.Binding.Index.Binding.Pause.Request.IntentID}), 0644) {
		return nil, errors.New("foreign active installation marker")
	}
	// Exact pair/topology and actual live/cold signer authority precede start.
	if _, e = o.run(ctx, nil, "/usr/bin/systemctl", "start", "--", Unit(t.Base.Owner, "signer"), Unit(t.Base.Owner, "app")); e != nil {
		return nil, e
	}
	if e = o.health(ctx, t.Target); e != nil {
		return nil, e
	}
	mark := func(phase string) error {
		if e := publish(ctx, t.dir()+"/"+phase+".json", t.Base.ID, mustJSON(phaseRecord{1, digest, phase}), 0644, o.read, o.run); e != nil {
			return e
		}
		if o.checkpoint != nil {
			return o.checkpoint(phase)
		}
		return nil
	}
	if current.Phase == "healthy-held" {
		if e = o.observe(ctx, t.Target, t.Binding); e != nil {
			return nil, e
		}
		current, e = o.hold(ctx, "committed-held", t.Binding)
		if e != nil {
			return nil, e
		}
	}
	if current == nil || !update.ValidInstallation(*current) || current.Digest != digest || !current.InstalledSequenceCommitted {
		return nil, errors.New("actual signer installed-sequence receipt required")
	}
	if e = mark("sequence-committed"); e != nil {
		return nil, e
	}
	for _, p := range records {
		if e = replaceRecord(ctx, t.Base.Root+"/"+p.name, t.Base.ID, p.next, o); e != nil {
			return nil, e
		}
		if e = mark("completion-" + p.name); e != nil {
			return nil, e
		}
	}
	if e = o.ownerRecord(true); e != nil {
		return nil, e
	}
	if e = mark("completion-owner"); e != nil {
		return nil, e
	}
	if current.Phase != "completed" {
		if e = o.observe(ctx, t.Target, t.Binding); e != nil {
			return nil, e
		}
		current, e = o.hold(ctx, "completed", t.Binding)
		if e != nil {
			return nil, e
		}
	}
	if current == nil || !update.ValidInstallation(*current) || current.Digest != digest || current.Phase != "completed" {
		return nil, errors.New("actual signer completion receipt required")
	}
	if e = mark("signer-completed"); e != nil {
		return nil, e
	}
	// Read the signer again before releasing even an already-released core fence.
	actual, e := o.state(ctx)
	if e != nil {
		return nil, e
	}
	if actual == nil || update.SnapshotDigest("completion", *actual) != update.SnapshotDigest("completion", *current) {
		return nil, errors.New("signer completion changed")
	}
	if e = o.release(ctx, *actual); e != nil {
		return nil, e
	}
	if e = mark("core-released"); e != nil {
		return nil, e
	}
	if e = clearActive(ctx, t, o); e != nil {
		return nil, e
	}
	if e = mark("completed"); e != nil {
		return nil, e
	}
	return actual, nil
}

func CompleteController(ctx context.Context, t InstallTransaction, s *System, c InstallationCallbacks) (*update.Installation, error) {
	if os.Getuid() != t.Base.Owner || os.Geteuid() != os.Getuid() {
		return nil, errors.New("ordinary installation owner required")
	}
	if _, e := ControllerRunner(t.Base.Owner, t.Binding.Index.Binding.Pause.Request.IntentID); e != nil {
		return nil, e
	}
	return runCompletion(ctx, t, installOps{read: readRoot, run: s.Run, topology: func(m Manifest) error {
		if e := Verify(m); e != nil {
			return e
		}
		for _, role := range []string{"app", "signer"} {
			if e := loadedUnit(m, role); e != nil {
				return e
			}
		}
		return nil
	}, health: func(ctx context.Context, m Manifest) error { return waitReady(ctx, m, s) }, observe: c.Observe, hold: c.Hold, state: func(ctx context.Context) (*update.Installation, error) {
		return currentInstallation(ctx, t, s, c.State)
	}, release: c.Release, ownerRecord: func(write bool) error { return ownerCompletionRecord(t, write) }, checkpoint: func(phase string) error {
		if s.Out != nil {
			fmt.Fprintln(s.Out, "Installation boundary:", phase)
		}
		if s.installFault != nil {
			return s.installFault(phase)
		}
		return nil
	}})
}

// CompletionState lets the immutable owner review/retry the same installation
// with stopped services. It never falls back to an owner-supplied executable.
func CompletionState(ctx context.Context, t InstallTransaction, s *System, state func(context.Context) (*update.Installation, error)) (*update.Installation, error) {
	if !t.valid() || os.Getuid() != t.Base.Owner || os.Geteuid() != os.Getuid() {
		return nil, errors.New("ordinary owner and exact installation transaction required")
	}
	if _, e := ControllerRunner(t.Base.Owner, t.Binding.Index.Binding.Pause.Request.IntentID); e != nil {
		return nil, e
	}
	return currentInstallation(ctx, t, s, state)
}
