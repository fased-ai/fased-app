//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

type ordinaryBootstrapOps struct {
	raw    func(context.Context) (string, error)
	cold   func(context.Context) (update.CoreSnapshot, update.SignerSnapshot, error)
	commit func(context.Context, update.OrdinaryHandoffAuthority) (update.SignerSnapshot, error)
}

// These callbacks are compiled fixed UID/path operations, never RPC input.
func ordinaryBootstrapCallbacks(t BootstrapTransaction, s *System) ordinaryBootstrapOps {
	call := func(ctx context.Context, i Identity, name string, args ...string) ([]byte, error) {
		cmd := []string{"/usr/bin/setpriv", "--reuid", strconv.Itoa(i.UID), "--regid", strconv.Itoa(i.GID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", t.Dir() + "/candidate/" + name}
		return s.Run(ctx, nil, append(cmd, args...)...)
	}
	cold := func(ctx context.Context) (update.CoreSnapshot, update.SignerSnapshot, error) {
		var c core.FixtureColdMetadata
		var r signer.Response
		b, e := call(ctx, t.Base.App, "fased-app", "fixture-inspection-cold", "--owner", strconv.Itoa(t.Base.Owner), "--disposable-fixture")
		if e != nil {
			return c.Core, update.SignerSnapshot{}, e
		}
		if update.StrictJSON(b, &c) != nil || c.Phase != "idle" && c.Phase != "released" {
			return c.Core, update.SignerSnapshot{}, errors.New("cold ordinary metadata has an active pause")
		}
		b, e = call(ctx, t.Base.Signer, "fased-signer", "fixture-update-inspect", "--config", t.Base.Root+"/identity.json", "--disposable-fixture")
		if e != nil {
			return c.Core, update.SignerSnapshot{}, e
		}
		if update.StrictJSON(b, &r) != nil || r.Error != "" || r.UpdateInventory == nil || !r.Status.Locked {
			return c.Core, update.SignerSnapshot{}, errors.New("cold held ordinary signer required")
		}
		return c.Core, *r.UpdateInventory, nil
	}
	raw := func(ctx context.Context) (string, error) {
		var hashes []string
		for _, group := range []struct {
			i     Identity
			files []string
		}{{t.Base.App, []string{t.Base.Root + "/app-state/core.sqlite"}}, {t.Base.Signer, []string{t.Base.Root + "/signer-state/vault.json", t.Base.Root + "/signer-state/operations.log"}}} {
			args := []string{"/usr/bin/setpriv", "--reuid", strconv.Itoa(group.i.UID), "--regid", strconv.Itoa(group.i.GID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", "/usr/bin/sha256sum", "--"}
			b, e := s.Run(ctx, nil, append(args, group.files...)...)
			if e != nil {
				return "", e
			}
			lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
			if len(lines) != len(group.files) {
				return "", errors.New("incomplete cold file hashes")
			}
			for n, line := range lines {
				h, ok := strings.CutSuffix(line, "  "+group.files[n])
				if !ok || !validHex(h, 32) {
					return "", errors.New("invalid cold file hash")
				}
				hashes = append(hashes, h)
			}
		}
		return update.SnapshotDigest("ordinary-source-files", hashes), nil
	}
	return ordinaryBootstrapOps{raw: raw, cold: cold, commit: func(ctx context.Context, a update.OrdinaryHandoffAuthority) (update.SignerSnapshot, error) {
		b, e := call(ctx, t.Base.Signer, "fased-signer", "ordinary-reader-handoff", "--config", t.Base.Root+"/identity.json", "--disposable-fixture", "--intent-id", t.ID)
		var r update.SignerSnapshot
		if e == nil {
			e = update.StrictJSON(b, &r)
		}
		return r, e
	}}
}

func ordinaryAuthority(t BootstrapTransaction, s update.SignerSnapshot, files string) (update.OrdinaryHandoffAuthority, error) {
	p, e := t.Plan()
	if e != nil {
		return update.OrdinaryHandoffAuthority{}, e
	}
	s.ProbeID = t.Core.ProbeID
	a := update.OrdinaryHandoffAuthority{SourceFilesSHA256: files, Version: 1, Digest: t.Digest(), ConfigSHA256: update.SnapshotDigest("ordinary-handoff-config", identityConfig(t.Base)), Root: t.Base.Root, Owner: t.Base.Owner, App: t.Base.App.UID, Signer: t.Base.Signer.UID, SignerID: t.Base.ID, Network: t.Base.Network, Before: s, Receipt: update.OrdinaryHandoffReceipt{Version: 1, ID: t.ID, Sequence: p.Target.Sequence, ManifestSHA256: p.TargetSHA256, AuthoritySHA256: s.AuthoritySHA256, PreviousCore: t.Base.CoreHash, PreviousSigner: t.Base.SignerHash, TargetCore: t.Target.CoreHash, TargetSigner: t.Target.SignerHash}}
	if len(s.States) == 2 {
		a.Receipt.GenesisSHA256 = s.States[1].Anchor
	}
	if e = a.VerifyTransaction(mustJSON(t)); e != nil {
		return a, e
	}
	return a, nil
}

// The old signer cannot create an intent. This finite branch instead performs
// a zero-write cold trial under stopped services. It fences BEFORE extending
// genesis or replacing either executable, then recovery is forward-only.
func runOrdinaryBootstrap(ctx context.Context, t BootstrapTransaction, o installOps, c ordinaryBootstrapOps, cancel ...bool) (BootstrapReceipt, error) {
	r := BootstrapReceipt{Version: 1, IntentID: t.ID, Digest: t.Digest()}
	fail := func(e error) (BootstrapReceipt, error) { r.ResumeAllowed = true; return r, e }
	if _, e := t.Plan(); e != nil {
		return fail(e)
	}
	if t.Version != 2 || c.cold == nil || c.commit == nil || c.raw == nil {
		return fail(errors.New("ordinary cold handoff required"))
	}
	if e := allowedInstallation(InstallTransaction{Base: t.Base, Target: t.Target}, o.read); e != nil {
		return fail(e)
	}
	mark := func(phase string) error {
		if e := publish(ctx, t.Dir()+"/"+phase+".json", t.Base.ID, mustJSON(phaseRecord{1, t.Digest(), phase}), 0644, o.read, o.run); e != nil {
			return e
		}
		if o.checkpoint != nil {
			return o.checkpoint(phase)
		}
		return nil
	}
	has := func(phase string) (bool, error) {
		f, e := o.read(t.Dir() + "/" + phase + ".json")
		if errors.Is(e, os.ErrNotExist) {
			return false, nil
		}
		if e != nil {
			return false, e
		}
		if !known(f, mustJSON(phaseRecord{1, t.Digest(), phase}), 0644) {
			return false, errors.New("foreign ordinary handoff phase")
		}
		return true, nil
	}
	forward, e := has("forward-only")
	if e != nil {
		return fail(e)
	}
	cancelled, e := has("cancelled")
	if e != nil {
		return fail(e)
	}
	cancelling := len(cancel) > 0 && cancel[0]
	if cancelling && forward {
		return fail(errors.New("ordinary handoff is forward-only; resume the same target"))
	}
	if cancelled {
		if forward {
			return fail(errors.New("conflicting ordinary cancellation and forward fence"))
		}
		if e = ordinaryBaseExact(t, o); e != nil {
			return fail(e)
		}
		if _, e = o.run(ctx, nil, "/usr/bin/systemctl", "start", "--", Unit(t.Base.Owner, "signer"), Unit(t.Base.Owner, "app")); e != nil {
			return fail(e)
		}
		if e = o.health(ctx, t.Base); e != nil {
			return fail(e)
		}
		if e = clearBootstrapActive(ctx, t, o); e != nil {
			return fail(e)
		}
		r.Phase = "cancelled"
		r.ResumeAllowed = false
		return r, nil
	}
	done, e := has("completed")
	if e != nil {
		return fail(e)
	}
	if done {
		if !forward {
			return fail(errors.New("completed handoff lacks forward fence"))
		}
		if e = ordinaryBootstrapRecords(t, o, true); e != nil {
			return fail(e)
		}
		if e = o.health(ctx, t.Target); e != nil {
			return fail(e)
		}
		if e = clearBootstrapActive(ctx, t, o); e != nil {
			return fail(e)
		}
		r.Phase = "reader-upgraded"
		r.AlreadyCurrent = true
		return r, nil
	}
	if !forward {
		if e = ordinaryBaseExact(t, o); e != nil {
			return fail(e)
		}
	}
	active := t.Base.Root + "/update-active.json"
	want := mustJSON(phaseRecord{1, t.Digest(), t.ID})
	if f, e := o.read(active); e == nil {
		if !known(f, want, 0644) {
			return fail(errors.New("another controller owns maintenance"))
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return fail(e)
	} else if e = publish(ctx, active, t.Base.ID, want, 0644, o.read, o.run); e != nil {
		return fail(e)
	}
	if _, e = o.run(ctx, nil, "/usr/bin/systemctl", "stop", "--", Unit(t.Base.Owner, "app"), Unit(t.Base.Owner, "signer")); e != nil {
		return fail(e)
	}
	if e = servicesStopped(ctx, t.Base, o.run); e != nil {
		return fail(e)
	}
	if e = mark("ordinary-stopped"); e != nil {
		return fail(e)
	}
	// Enrollment identity, native access, root and owner records must still be
	// original/target generations. No foreign recovery record is overwritten.
	if e = ordinaryBootstrapRecords(t, o, false); e != nil {
		return fail(e)
	}
	var a update.OrdinaryHandoffAuthority
	f, e := o.read(t.Dir() + "/ordinary-signer.json")
	if e == nil {
		if update.StrictJSON(f.Data, &a) != nil || !known(f, mustJSON(a), 0644) || a.VerifyTransaction(mustJSON(t)) != nil {
			return fail(errors.New("foreign ordinary handoff snapshot"))
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return fail(e)
	} else {
		if forward {
			return fail(errors.New("forward handoff lost original snapshot"))
		}
		files, ce := c.raw(ctx)
		if ce != nil {
			return fail(ce)
		}
		cold, s, ce := c.cold(ctx)
		if ce != nil {
			return fail(ce)
		}
		if !update.SameCoreMetadata(cold, t.Core) {
			return fail(errors.New("predecessor core metadata changed"))
		}
		a, e = ordinaryAuthority(t, s, files)
		if e != nil {
			return fail(e)
		}
		if e = publish(ctx, t.Dir()+"/ordinary-signer.json", t.Base.ID, mustJSON(a), 0644, o.read, o.run); e != nil {
			return fail(e)
		}
	}
	if !forward {
		files, ce := c.raw(ctx)
		if ce != nil {
			return fail(ce)
		}
		if files != a.SourceFilesSHA256 {
			return fail(errors.New("raw source files changed during cold handoff"))
		}
		// Repeat both cold reads under stopped roles. No partial publication, WAL
		// recovery, signer repair or source drift is accepted before the fence.
		cold, s, ce := c.cold(ctx)
		if ce != nil {
			return fail(ce)
		}
		filesAfter, re := c.raw(ctx)
		if re != nil {
			return fail(re)
		}
		if filesAfter != files || !update.SameCoreMetadata(cold, t.Core) || !update.SameOrdinarySnapshot(s, a.Before) {
			return fail(errors.New("cold handoff source changed; preserve stopped state"))
		}
		if cancelling {
			if e = mark("cancelled"); e != nil {
				return fail(e)
			}
			if _, e = o.run(ctx, nil, "/usr/bin/systemctl", "start", "--", Unit(t.Base.Owner, "signer"), Unit(t.Base.Owner, "app")); e != nil {
				return fail(e)
			}
			if e = o.health(ctx, t.Base); e != nil {
				return fail(e)
			}
			if e = clearBootstrapActive(ctx, t, o); e != nil {
				return fail(e)
			}
			r.Phase = "cancelled"
			r.ResumeAllowed = false
			return r, nil
		}
		if !t.AdmissionCurrent() {
			return fail(errors.New("ordinary handoff expired before forward commitment"))
		}
		if e = mark("ordinary-trial-verified"); e != nil {
			return fail(e)
		}
		if e = mark("forward-only"); e != nil {
			return fail(e)
		}
	}
	// After this point failures stay stopped/held. NEVER restart the old reader.
	s, e := c.commit(ctx, a)
	if e != nil {
		return fail(e)
	}
	if s.AuthoritySHA256 != a.Before.AuthoritySHA256 || s.SequenceFloor != a.Receipt.Sequence || s.InstalledSequence != 0 || s.ActiveIntentID != "" || !s.RecoveryHold || len(s.States) != 2 || s.States[0].Anchor != a.Before.States[0].Anchor {
		return fail(errors.New("ordinary handoff signer receipt changed"))
	}
	if e = mark("ordinary-reader-reserved"); e != nil {
		return fail(e)
	}
	for _, v := range []struct{ name, hash, phase string }{{"fased-app", t.Target.CoreHash, "core-replaced"}, {"fased-signer", t.Target.SignerHash, "pair-replaced"}} {
		f, e := o.read(t.Dir() + "/candidate/" + v.name)
		if e != nil || !f.Regular || f.UID != 0 || f.Mode != 0755 || sum(f.Data) != v.hash {
			return fail(errors.New("ordinary handoff reader changed"))
		}
		if e = replaceRecord(ctx, t.Base.Root+"/"+v.name, t.Base.ID, f.Data, o); e != nil {
			return fail(e)
		}
		if e = mark(v.phase); e != nil {
			return fail(e)
		}
	}
	if e = replaceRecord(ctx, t.Base.Root+"/install.json", t.Base.ID, mustJSON(t.Target), o); e != nil {
		return fail(e)
	}
	if e = mark("inventory-replaced"); e != nil {
		return fail(e)
	}
	if e = bootstrapRecords(ctx, t, o, true); e != nil {
		return fail(e)
	}
	if _, e = o.run(ctx, nil, "/usr/bin/systemctl", "start", "--", Unit(t.Base.Owner, "signer"), Unit(t.Base.Owner, "app")); e != nil {
		return fail(e)
	}
	if e = mark("started"); e != nil {
		return fail(e)
	}
	if e = o.health(ctx, t.Target); e != nil {
		return fail(e)
	}
	if e = mark("ordinary-healthy"); e != nil {
		return fail(e)
	}
	if e = mark("completed"); e != nil {
		return fail(e)
	}
	if e = clearBootstrapActive(ctx, t, o); e != nil {
		return fail(e)
	}
	r.Phase = "reader-upgraded"
	return r, nil
}

func ordinaryBootstrapRecords(t BootstrapTransaction, o installOps, converged bool) error {
	f, e := o.read(t.Base.Root + "/enrollment-ready.json")
	var c EnrollmentCommit
	if e != nil || !f.Regular || f.UID != 0 || f.Mode != 0644 || strict(f.Data, &c) != nil || c.Version != 1 || c.ManifestSHA256 != sum(mustJSON(EnrollmentIdentity(t.Base))) || c.InstanceID != t.Core.InstanceID || !validHex(c.Digest, 32) {
		return errors.New("ordinary enrollment identity changed")
	}
	for _, v := range []struct {
		name      string
		old, next []byte
	}{{"intent.json", mustJSON(installerIntent(t.Base)), mustJSON(installerIntent(t.Target))}, {"ready.json", readyRecord(t.Base), readyRecord(t.Target)}} {
		f, e = o.read(t.Base.Root + "/" + v.name)
		if e != nil {
			return e
		}
		if !known(f, v.next, 0644) && (converged || !known(f, v.old, 0644)) {
			return errors.New("ordinary installer metadata changed")
		}
	}
	if converged {
		if o.ownerCurrent == nil {
			return errors.New("exact ordinary owner record checker required")
		}
		return o.ownerCurrent(t.Target)
	}
	return o.ownerRecord(false)
}

func ordinaryBaseExact(t BootstrapTransaction, o installOps) error {
	for _, v := range []struct{ name, hash string }{{"fased-app", t.Base.CoreHash}, {"fased-signer", t.Base.SignerHash}} {
		f, e := o.read(t.Base.Root + "/" + v.name)
		if e != nil || !f.Regular || f.UID != 0 || f.Mode != 0755 || sum(f.Data) != v.hash {
			return errors.New("pre-fence handoff requires exact predecessor readers")
		}
	}
	for _, v := range []struct {
		name string
		data []byte
	}{{"install.json", mustJSON(t.Base)}, {"intent.json", mustJSON(installerIntent(t.Base))}, {"ready.json", readyRecord(t.Base)}} {
		f, e := o.read(t.Base.Root + "/" + v.name)
		if e != nil || !known(f, v.data, 0644) {
			return errors.New("pre-fence handoff inventory changed")
		}
	}
	if o.ownerCurrent == nil {
		return errors.New("owner generation checker required")
	}
	return o.ownerCurrent(t.Base)
}
