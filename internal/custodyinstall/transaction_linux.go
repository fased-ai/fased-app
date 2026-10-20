//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/update"
)

type InstallTransaction struct {
	Version int                   `json:"version"`
	Base    Manifest              `json:"base"`
	Target  Manifest              `json:"target"`
	Binding update.InstallBinding `json:"binding"`
}

func (t InstallTransaction) dir() string {
	return t.Base.Root + "/updates/" + t.Binding.Index.Binding.Pause.Request.IntentID
}
func (t InstallTransaction) valid() bool {
	if t.Version != 1 || t.Base.validate() != nil || t.Target.validate() != nil || update.ValidateInstallBinding(t.Binding) != nil || t.Binding.Index.Binding.Pause.Request == nil {
		return false
	}
	b := t.Target
	b.CoreHash = t.Base.CoreHash
	b.SignerHash = t.Base.SignerHash
	return b == t.Base && t.Binding.InstallationID == t.Base.ID && t.Binding.PreviousCore == t.Base.CoreHash && t.Binding.PreviousSigner == t.Base.SignerHash && t.Binding.PreviousManifest == sum(mustJSON(t.Base)) && t.Binding.TargetCore == t.Target.CoreHash && t.Binding.TargetSigner == t.Target.SignerHash
}

type installOps struct {
	read       readHost
	run        runHost
	topology   func(Manifest) error
	health     func(context.Context, Manifest) error
	observe    func(context.Context, Manifest, update.InstallBinding) error
	hold       func(context.Context, string, update.InstallBinding) (*update.Installation, error)
	state      func(context.Context) (*update.Installation, error)
	checkpoint func(string) error
}
type phaseRecord struct {
	Version int    `json:"version"`
	Digest  string `json:"digest"`
	Phase   string `json:"phase"`
}

func runTransaction(ctx context.Context, t InstallTransaction, rollback bool, o installOps) (*update.Installation, error) {
	if !t.valid() {
		return nil, errors.New("invalid fixed-path installation transaction")
	}
	d := t.dir()
	digest := update.InstallDigest(t.Binding)
	if e := o.topology(t.Base); e != nil {
		return nil, e
	}
	// Only exact old/target executables and inventories are classified. Unknown
	// bytes are never repaired over, even when a phase record says otherwise.
	if e := allowedInstallation(t, o.read); e != nil {
		return nil, e
	}
	mark := func(name string) error {
		b := mustJSON(phaseRecord{1, digest, name})
		if e := publish(ctx, d+"/"+name+".json", t.Base.ID, b, 0644, o.read, o.run); e != nil {
			return e
		}
		if o.checkpoint != nil {
			return o.checkpoint(name)
		}
		return nil
	}
	have := func(name string) (bool, error) {
		f, e := o.read(d + "/" + name + ".json")
		if errors.Is(e, os.ErrNotExist) {
			return false, nil
		}
		if e != nil {
			return false, e
		}
		if !known(f, mustJSON(phaseRecord{1, digest, name}), 0644) {
			return false, errors.New("conflicting installation phase; preserve state")
		}
		return true, nil
	}
	decision, e := have("rollback")
	if e != nil {
		return nil, e
	}
	var held update.Installation
	f, e := o.read(d + "/held.json")
	if e == nil {
		if strict(f.Data, &held) != nil || !known(f, mustJSON(held), 0644) || !update.ValidInstallation(held) || held.Digest != digest || held.Phase != "active" {
			return nil, errors.New("invalid root-bound signer hold receipt")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	} else {
		if rollback || decision {
			return nil, errors.New("no durable installation hold to roll back")
		}
		p, e := o.hold(ctx, "active", t.Binding)
		if e != nil {
			return nil, e
		}
		if p == nil || !update.ValidInstallation(*p) || p.Digest != digest || p.Phase != "active" {
			return nil, errors.New("signer did not acquire the exact durable installation hold")
		}
		held = *p
		if e = publish(ctx, d+"/held.json", t.Base.ID, mustJSON(held), 0644, o.read, o.run); e != nil {
			return nil, e
		}
		if o.checkpoint != nil {
			if e = o.checkpoint("held"); e != nil {
				return nil, e
			}
		}
	}
	// This durable receipt permits cold retry while the signer service is down.
	// It never permits financial work. The old/new binaries must both understand
	// its signer-owned hold, and live authority is checked again before success.
	current, e := o.state(ctx)
	if e != nil {
		return nil, fmt.Errorf("installation hold revalidation failed: %w", e)
	}
	if current == nil || !update.ValidInstallation(*current) || current.Digest != digest {
		return nil, errors.New("current signer does not own this installation")
	}
	if current.Phase == "rolled-back-held" && !decision {
		return nil, errors.New("rollback completion lacks its durable host decision")
	}
	active := t.Base.Root + "/update-active.json"
	if e = publish(ctx, active, t.Base.ID, mustJSON(phaseRecord{1, digest, t.Binding.Index.Binding.Pause.Request.IntentID}), 0644, o.read, o.run); e != nil {
		return nil, e
	}
	if rollback && !decision {
		if e = mark("rollback"); e != nil {
			return nil, e
		}
		decision = true
	}
	if done, e := have("healthy-held"); e != nil {
		return nil, e
	} else if done && !decision {
		if _, e = o.run(ctx, nil, "/usr/bin/systemctl", "start", "--", Unit(t.Base.Owner, "signer"), Unit(t.Base.Owner, "app")); e != nil {
			return nil, e
		}
		if e = o.health(ctx, t.Target); e != nil {
			return nil, e
		}
		if e = o.observe(ctx, t.Target, t.Binding); e != nil {
			return nil, e
		}
		return o.hold(ctx, "healthy-held", t.Binding)
	}
	if done, e := have("rolled-back-held"); e != nil {
		return nil, e
	} else if done {
		if _, e = o.run(ctx, nil, "/usr/bin/systemctl", "start", "--", Unit(t.Base.Owner, "signer"), Unit(t.Base.Owner, "app")); e != nil {
			return nil, e
		}
		if e = o.health(ctx, t.Base); e != nil {
			return nil, e
		}
		if e = o.observe(ctx, t.Base, t.Binding); e != nil {
			return nil, e
		}
		p, e := o.hold(ctx, "rolled-back-held", t.Binding)
		if e != nil {
			return nil, e
		}
		return p, clearActive(ctx, t, o)
	}
	apply := func(target Manifest, prefix string) error {
		if _, e := o.run(ctx, nil, "/usr/bin/systemctl", "stop", "--", Unit(t.Base.Owner, "app"), Unit(t.Base.Owner, "signer")); e != nil {
			return e
		}
		if e := servicesStopped(ctx, t.Base, o.run); e != nil {
			return e
		}
		if e := mark(prefix + "stopped"); e != nil {
			return e
		}
		coreName, signerName := "next-core", "next-signer"
		if prefix != "" {
			coreName, signerName = "runner", "previous-signer"
		}
		for _, p := range []struct{ name, source, want, old, phase string }{{"fased-app", coreName, target.CoreHash, t.Base.CoreHash, prefix + "core-replaced"}, {"fased-signer", signerName, target.SignerHash, t.Base.SignerHash, prefix + "pair-replaced"}} {
			if e := replaceExecutable(ctx, t, p.name, d+"/"+p.source, p.want, o); e != nil {
				return e
			}
			if e := mark(p.phase); e != nil {
				return e
			}
		}
		if e := replaceRecord(ctx, t.Base.Root+"/install.json", t.Base.ID, mustJSON(target), o); e != nil {
			return e
		}
		if e := mark(prefix + "inventory-replaced"); e != nil {
			return e
		}
		if _, e := o.run(ctx, nil, "/usr/bin/systemctl", "start", "--", Unit(t.Base.Owner, "signer"), Unit(t.Base.Owner, "app")); e != nil {
			return e
		}
		return mark(prefix + "started")
	}
	if !decision {
		if e = apply(t.Target, ""); e != nil {
			return nil, e
		}
		e = o.health(ctx, t.Target)
		if e == nil {
			e = o.observe(ctx, t.Target, t.Binding)
		}
		if e == nil {
			p, e := o.hold(ctx, "healthy-held", t.Binding)
			if e != nil {
				return nil, e
			}
			if e = mark("healthy-held"); e != nil {
				return nil, e
			}
			return p, nil
		}
		// Persist the rollback decision before touching either executable. It
		// survives helper death and never starts another candidate on retry.
		if e = mark("rollback"); e != nil {
			return nil, e
		}
		decision = true
	}
	if e = apply(t.Base, "rollback-"); e != nil {
		return nil, e
	}
	if e = o.health(ctx, t.Base); e != nil {
		return nil, fmt.Errorf("rollback health failed; financial hold retained: %w", e)
	}
	if e = o.observe(ctx, t.Base, t.Binding); e != nil {
		return nil, e
	}
	p, e := o.hold(ctx, "rolled-back-held", t.Binding)
	if e != nil {
		return nil, e
	}
	if e = mark("rolled-back-held"); e != nil {
		return nil, e
	}
	return p, clearActive(ctx, t, o)
}

func clearActive(ctx context.Context, t InstallTransaction, o installOps) error {
	p := t.Base.Root + "/update-active.json"
	f, e := o.read(p)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	want := mustJSON(phaseRecord{1, update.InstallDigest(t.Binding), t.Binding.Index.Binding.Pause.Request.IntentID})
	if !known(f, want, 0644) {
		return errors.New("foreign active installation marker")
	}
	if _, e = o.run(ctx, nil, "/usr/bin/rm", "--", p); e != nil {
		return e
	}
	_, e = o.run(ctx, nil, "/usr/bin/sync", "-f", t.Base.Root)
	return e
}
func servicesStopped(ctx context.Context, m Manifest, run runHost) error {
	for _, role := range []string{"app", "signer"} {
		b, e := run(ctx, nil, "/usr/bin/systemctl", "show", Unit(m.Owner, role), "--property=ActiveState,MainPID")
		if e != nil {
			return e
		}
		if string(b) != "ActiveState=inactive\nMainPID=0\n" && string(b) != "MainPID=0\nActiveState=inactive\n" {
			return errors.New("both managed processes must be inactive before replacement")
		}
	}
	return nil
}
func allowedInstallation(t InstallTransaction, read readHost) error {
	for _, p := range []struct{ name, a, b string }{{"fased-app", t.Base.CoreHash, t.Target.CoreHash}, {"fased-signer", t.Base.SignerHash, t.Target.SignerHash}} {
		f, e := read(t.Base.Root + "/" + p.name)
		if e != nil {
			return e
		}
		if f.UID != 0 || !f.Regular || f.Mode != 0755 || (sum(f.Data) != p.a && sum(f.Data) != p.b) {
			return errors.New("foreign installed executable; preserving it")
		}
	}
	f, e := read(t.Base.Root + "/install.json")
	if e != nil {
		return e
	}
	if !known(f, mustJSON(t.Base), 0644) && !known(f, mustJSON(t.Target), 0644) {
		return errors.New("foreign installed inventory; preserving it")
	}
	return nil
}
func replaceExecutable(ctx context.Context, t InstallTransaction, name, source, want string, o installOps) error {
	f, e := o.read(source)
	if e != nil || f.Mode != 0755 || sum(f.Data) != want {
		return errors.New("immutable recovery/candidate executable changed")
	}
	current, e := o.read(t.Base.Root + "/" + name)
	if e != nil {
		return e
	}
	if sum(current.Data) == want {
		return nil
	}
	// allowedInstallation already classified both possible generations.
	a, b := t.Base.CoreHash, t.Target.CoreHash
	if name == "fased-signer" {
		a, b = t.Base.SignerHash, t.Target.SignerHash
	}
	if sum(current.Data) != a && sum(current.Data) != b {
		return errors.New("unexpected executable before replacement")
	}
	return replaceRecord(ctx, t.Base.Root+"/"+name, t.Base.ID, f.Data, o)
}
func replaceRecord(ctx context.Context, path, id string, data []byte, o installOps) error {
	mode := os.FileMode(0644)
	if filepath.Base(path) == "fased-app" || filepath.Base(path) == "fased-signer" {
		mode = 0755
	}
	f, e := o.read(path)
	if e != nil {
		return e
	}
	if known(f, data, mode) {
		return nil
	}
	stage := filepath.Dir(path) + "/.replace-" + id + "-" + filepath.Base(path) + "-" + sum(data)
	if e = publish(ctx, stage, id, data, mode, o.read, o.run); e != nil {
		return e
	}
	if _, e = o.run(ctx, nil, "/usr/bin/mv", "--no-target-directory", "--", stage, path); e != nil {
		return e
	}
	if _, e = o.run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(path)); e != nil {
		return e
	}
	f, e = o.read(path)
	if e != nil || !known(f, data, mode) {
		return errors.New("replacement failed verification")
	}
	return nil
}
