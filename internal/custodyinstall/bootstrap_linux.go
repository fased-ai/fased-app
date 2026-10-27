//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

type BootstrapTransaction struct {
	Version    int                    `json:"version"`
	ID         string                 `json:"id"`
	Base       Manifest               `json:"base"`
	Target     Manifest               `json:"target"`
	Bundle     update.BootstrapBundle `json:"bundle"`
	Trust      []byte                 `json:"trust"`
	ReviewedAt int64                  `json:"reviewedAt"`
	Core       update.CoreSnapshot    `json:"core"`
	Signer     update.SignerSnapshot  `json:"signer"`
}

func (t BootstrapTransaction) Dir() string    { return t.Base.Root + "/bootstraps/" + t.ID }
func (t BootstrapTransaction) Digest() string { return update.SnapshotDigest("reader-bootstrap:v1", t) }
func (t BootstrapTransaction) Plan() (update.BootstrapPlan, error) {
	p, e := update.VerifyBootstrap(t.Bundle, t.Trust, t.ReviewedAt)
	if e != nil {
		return p, e
	}
	if _, e := update.JoinSnapshots(t.Core, t.Signer, p.Maintenance.Target); e != nil {
		return p, e
	}
	b := t.Target
	b.CoreHash = t.Base.CoreHash
	b.SignerHash = t.Base.SignerHash
	if t.Version != 1 || t.Base.Version != 1 || !update.IDOK(t.ID) || t.Base.validate() != nil || t.Target.validate() != nil || b != t.Base || t.Signer.ActiveIntentID != "" || t.Signer.InstalledSequence != 0 || p.Target.Sequence <= t.Signer.SequenceFloor {
		return p, errors.New("unsupported bootstrap identity or financial floor")
	}
	for _, c := range p.Maintenance.Components {
		hash := t.Base.CoreHash
		if c.Contract.Component == "signer" {
			hash = t.Base.SignerHash
		}
		if c.Artifact.SHA256 != hash {
			return p, errors.New("maintenance manifest differs from installed predecessor")
		}
	}
	for _, c := range p.Target.Components {
		hash := t.Target.CoreHash
		if c.Contract.Component == "signer" {
			hash = t.Target.SignerHash
		}
		if c.Artifact.SHA256 != hash {
			return p, errors.New("bootstrap target differs from signed pair")
		}
	}
	return p, nil
}
func LoadBootstrap(uid int, id string) (BootstrapTransaction, error) {
	var t BootstrapTransaction
	if uid <= 0 || !update.IDOK(id) {
		return t, errors.New("ordinary owner and exact bootstrap id required")
	}
	f, e := readRoot(Root(uid) + "/bootstraps/" + id + "/transaction.json")
	if e != nil {
		return t, e
	}
	if update.StrictJSON(f.Data, &t) != nil || t.Base.Owner != uid || t.ID != id || !known(f, mustJSON(t), 0644) {
		return t, errors.New("foreign bootstrap transaction")
	}
	_, e = t.Plan()
	return t, e
}
func BootstrapRunner(uid int, id string) (string, error) {
	t, e := LoadBootstrap(uid, id)
	if e != nil {
		return "", e
	}
	if e = verifyTopology(t.Base); e != nil {
		return "", e
	}
	for _, p := range []struct{ path, hash string }{{t.Dir() + "/candidate/fased-app", t.Target.CoreHash}, {t.Dir() + "/candidate/fased-signer", t.Target.SignerHash}, {t.Dir() + "/previous/fased-app", t.Base.CoreHash}, {t.Dir() + "/previous/fased-signer", t.Base.SignerHash}} {
		f, e := readRoot(p.path)
		if e != nil || !f.Regular || f.Mode != 0755 || sum(f.Data) != p.hash {
			return "", errors.New("immutable bootstrap reader changed")
		}
	}
	if e = allowedInstallation(InstallTransaction{Base: t.Base, Target: t.Target}, readRoot); e != nil {
		return "", e
	}
	return t.Dir() + "/candidate/fased-app", nil
}
func PrepareBootstrap(ctx context.Context, t BootstrapTransaction, dir string, s *System) error {
	plan, e := t.Plan()
	if e != nil {
		return e
	}
	if t.Base.Owner != os.Getuid() || os.Geteuid() != os.Getuid() || Verify(t.Base) != nil {
		return errors.New("verified predecessor and ordinary owner required")
	}
	if e = noActiveController(t.Base); e != nil {
		return e
	}
	var fs unix.Statfs_t
	if e = unix.Statfs(t.Base.Root, &fs); e != nil {
		return e
	}
	needed := uint64(8 << 20)
	for _, c := range append(append([]update.Component{}, plan.Maintenance.Components...), plan.Target.Components...) {
		if c.Artifact.Size > update.MaxMetadataBackup {
			return errors.New("bounded reader pair required")
		}
		needed += uint64(c.Artifact.Size) * 3
	}
	if fs.Bsize <= 0 || fs.Bavail < uint64((needed+uint64(fs.Bsize)-1)/uint64(fs.Bsize)) {
		return errors.New("insufficient bootstrap recovery space")
	}
	for _, d := range []string{t.Base.Root + "/bootstraps", t.Dir(), t.Dir() + "/previous", t.Dir() + "/candidate"} {
		if e = directory(ctx, d, 0, 0, 0755, s); e != nil {
			return e
		}
	}
	for _, group := range []struct {
		name, source string
		manifest     update.Manifest
	}{{"previous", t.Base.Root, plan.Maintenance}, {"candidate", dir, plan.Target}} {
		for _, c := range group.manifest.Components {
			data, e := source(filepath.Join(group.source, c.Artifact.File))
			if e != nil {
				return e
			}
			if int64(len(data)) != c.Artifact.Size || sum(data) != c.Artifact.SHA256 {
				return errors.New("bootstrap artifact differs from signed bytes")
			}
			if e = publish(ctx, t.Dir()+"/"+group.name+"/"+c.Artifact.File, t.Base.ID, data, 0755, readRoot, s.Run); e != nil {
				return e
			}
			contract, e := probeReader(ctx, t.Dir()+"/"+group.name+"/"+c.Artifact.File, c.Contract.Component == "core", t.Base, s)
			if e != nil {
				return e
			}
			if update.SnapshotDigest("contract", contract) != update.SnapshotDigest("contract", c.Contract) {
				return errors.New("signed bootstrap contract differs from executable")
			}
		}
	}
	return publish(ctx, t.Dir()+"/transaction.json", t.Base.ID, mustJSON(t), 0644, readRoot, s.Run)
}
func probeReader(ctx context.Context, path string, isCore bool, m Manifest, s *System) (update.Contract, error) {
	identity := m.Signer
	args := []string{"fixture-compatibility"}
	if isCore {
		identity = m.App
		args = []string{"updates", "compatibility"}
	}
	cmd := []string{"/usr/bin/setpriv", "--reuid", strconv.Itoa(identity.UID), "--regid", strconv.Itoa(identity.GID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", path}
	cmd = append(cmd, args...)
	b, e := s.Run(ctx, nil, cmd...)
	var c update.Contract
	if e != nil {
		return c, e
	}
	if !isCore {
		e = update.StrictJSON(b, &c)
		return c, e
	}
	var r struct {
		Version            int             `json:"version"`
		Contract           update.Contract `json:"contract"`
		Target             update.Target   `json:"target"`
		WalletSigner       *bool           `json:"walletSigner"`
		FinancialExecution *bool           `json:"financialExecution"`
		UpdaterEnabled     *bool           `json:"updaterEnabled"`
	}
	if e = update.StrictJSON(b, &r); e != nil {
		return c, e
	}
	if r.WalletSigner == nil || *r.WalletSigner || r.FinancialExecution == nil || *r.FinancialExecution || r.UpdaterEnabled == nil || *r.UpdaterEnabled {
		return c, errors.New("bootstrap requires disabled real capabilities")
	}
	return r.Contract, nil
}

type BootstrapCallbacks struct {
	Maintain func(context.Context, BootstrapTransaction) (update.RecoveryIndex, error)
	Observe  func(context.Context, BootstrapTransaction, update.RecoveryIndex, bool, bool) error // require core, allow aborted
	Finish   func(context.Context, BootstrapTransaction, update.RecoveryIndex) error
}
type BootstrapReceipt struct {
	Version                    int    `json:"version"`
	IntentID                   string `json:"intentId"`
	Digest                     string `json:"digest"`
	Phase                      string `json:"phase"`
	AlreadyCurrent             bool   `json:"alreadyCurrent"`
	InstalledSequenceCommitted bool   `json:"installedSequenceCommitted"`
	ResumeAllowed              bool   `json:"resumeAllowed"`
	FinancialExecution         bool   `json:"financialExecution"`
}

func RunBootstrap(ctx context.Context, t BootstrapTransaction, s *System, c BootstrapCallbacks) (BootstrapReceipt, error) {
	if os.Getuid() != t.Base.Owner || os.Geteuid() != os.Getuid() {
		return BootstrapReceipt{}, errors.New("ordinary bootstrap owner required")
	}
	if _, e := BootstrapRunner(t.Base.Owner, t.ID); e != nil {
		return BootstrapReceipt{}, e
	}
	for _, role := range []string{"app", "signer"} {
		if e := loadedUnit(t.Base, role); e != nil {
			return BootstrapReceipt{}, e
		}
	}
	o := installOps{read: readRoot, run: s.Run, health: func(ctx context.Context, m Manifest) error { return waitReady(ctx, m, s) }, ownerRecord: func(write bool) error {
		if write {
			return ownerCompletionRecord(InstallTransaction{Base: t.Base, Target: t.Target}, true)
		}
		dir, e := ownerJournal()
		if e != nil {
			return e
		}
		b, e := core.ReadPrivateFile(dir + "/intent.json")
		if e != nil {
			return e
		}
		var j journal
		if strict(b, &j) != nil || j != installerIntent(t.Target) {
			return errors.New("bootstrap owner record is not current")
		}
		return nil
	}, checkpoint: func(phase string) error {
		fmt.Fprintln(s.Out, "Bootstrap boundary:", phase)
		if s.installFault != nil {
			return s.installFault(phase)
		}
		return nil
	}}
	return runBootstrap(ctx, t, c, o)
}

// A bootstrap switches readers using only predecessor-readable prepare/stage/
// abort records. No installation journal record or installed release is created.
func runBootstrap(ctx context.Context, t BootstrapTransaction, c BootstrapCallbacks, o installOps) (BootstrapReceipt, error) {
	result := BootstrapReceipt{Version: 1, IntentID: t.ID, Digest: t.Digest()}
	fail := func(e error) (BootstrapReceipt, error) { return result, e }
	plan, e := t.Plan()
	if e != nil {
		return fail(e)
	}
	if e = allowedInstallation(InstallTransaction{Base: t.Base, Target: t.Target}, o.read); e != nil {
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
			return false, errors.New("foreign bootstrap phase")
		}
		return true, nil
	}
	var index update.RecoveryIndex
	f, e := o.read(t.Dir() + "/recovery.json")
	if errors.Is(e, os.ErrNotExist) {
		index, e = c.Maintain(ctx, t)
		if e != nil {
			return fail(e)
		}
		if e = publish(ctx, t.Dir()+"/recovery.json", t.Base.ID, mustJSON(index), 0644, o.read, o.run); e != nil {
			return fail(e)
		}
	} else if e != nil {
		return fail(e)
	} else if strict(f.Data, &index) != nil || !known(f, mustJSON(index), 0644) {
		return fail(errors.New("foreign bootstrap recovery index"))
	}
	q := index.Binding.Pause.Request
	if q == nil || !update.ValidRecoveryIndex(index, index.Binding) || q.IntentID != t.ID || q.Sequence != plan.Maintenance.Sequence || q.ManifestSHA256 != plan.MaintenanceSHA256 || !update.SameCoreMetadata(q.Expected, t.Core) {
		return fail(errors.New("unbound bootstrap maintenance recovery"))
	}
	rollbackVerified, e := has("rollback-verified")
	if e != nil {
		return fail(e)
	}
	rolledBack, e := has("rolled-back")
	if e != nil {
		return fail(e)
	}
	forward, e := has("forward-only")
	if e != nil {
		return fail(e)
	}
	done, e := has("completed")
	if e != nil {
		return fail(e)
	}
	if e = c.Observe(ctx, t, index, false, forward || rollbackVerified || rolledBack); e != nil {
		return fail(e)
	}
	active := t.Base.Root + "/update-active.json"
	want := mustJSON(phaseRecord{1, t.Digest(), t.ID})
	if f, e := o.read(active); e == nil {
		if !known(f, want, 0644) {
			return fail(errors.New("foreign active bootstrap"))
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return fail(e)
	} else if !done && !rolledBack {
		if e = publish(ctx, active, t.Base.ID, want, 0644, o.read, o.run); e != nil {
			return fail(e)
		}
	}
	if rolledBack {
		if e = o.health(ctx, t.Base); e != nil {
			return fail(e)
		}
		result.Phase = "rolled-back"
		return result, nil
	}
	if done {
		if e = o.health(ctx, t.Target); e != nil {
			return fail(e)
		}
		if e = bootstrapRecords(ctx, t, o, false); e != nil {
			return fail(e)
		}
		result.Phase = "reader-upgraded"
		result.AlreadyCurrent = true
		return result, nil
	}
	rollback, e := has("rollback")
	if e != nil {
		return fail(e)
	}
	apply := func(m Manifest, group, prefix string) error {
		if _, e := o.run(ctx, nil, "/usr/bin/systemctl", "stop", "--", Unit(t.Base.Owner, "app"), Unit(t.Base.Owner, "signer")); e != nil {
			return e
		}
		if e := servicesStopped(ctx, t.Base, o.run); e != nil {
			return e
		}
		// The new reader alone performs cold validation. Old executables are never
		// restarted if any unexpected record, floor or financial state appeared.
		if e := c.Observe(ctx, t, index, false, false); e != nil {
			return e
		}
		for _, v := range []struct{ name, hash, phase string }{{"fased-app", m.CoreHash, "core-replaced"}, {"fased-signer", m.SignerHash, "pair-replaced"}} {
			f, e := o.read(t.Dir() + "/" + group + "/" + v.name)
			if e != nil || sum(f.Data) != v.hash || f.Mode != 0755 {
				return errors.New("bootstrap executable changed")
			}
			if e = replaceRecord(ctx, m.Root+"/"+v.name, t.Base.ID, f.Data, o); e != nil {
				return e
			}
			if e = mark(prefix + v.phase); e != nil {
				return e
			}
		}
		if e := replaceRecord(ctx, m.Root+"/install.json", t.Base.ID, mustJSON(m), o); e != nil {
			return e
		}
		if _, e := o.run(ctx, nil, "/usr/bin/systemctl", "start", "--", Unit(t.Base.Owner, "signer"), Unit(t.Base.Owner, "app")); e != nil {
			return e
		}
		return mark(prefix + "started")
	}
	if !forward && !rollback {
		if e = apply(t.Target, "candidate", ""); e != nil {
			return fail(e)
		}
		e = o.health(ctx, t.Target)
		if e == nil {
			e = c.Observe(ctx, t, index, true, false)
		}
		if e != nil {
			if e = mark("rollback"); e != nil {
				return fail(e)
			}
			rollback = true
		}
	}
	if rollback {
		if forward {
			return fail(errors.New("committed reader handoff cannot roll back"))
		}
		if !rollbackVerified {
			if e = apply(t.Base, "previous", "rollback-"); e != nil {
				return fail(e)
			}
			if e = o.health(ctx, t.Base); e != nil {
				return fail(e)
			}
			if e = c.Observe(ctx, t, index, true, false); e != nil {
				return fail(e)
			}
			if e = mark("rollback-verified"); e != nil {
				return fail(e)
			}
		}
		if e = c.Finish(ctx, t, index); e != nil {
			return fail(e)
		}
		if e = clearBootstrapActive(ctx, t, o); e != nil {
			return fail(e)
		}
		if e = mark("rolled-back"); e != nil {
			return fail(e)
		}
		result.Phase = "rolled-back"
		return result, nil
	}
	if !forward {
		if e = bootstrapRecords(ctx, t, o, true); e != nil {
			return fail(e)
		}
		if e = mark("forward-only"); e != nil {
			return fail(e)
		}
	} else {
		if _, e = o.run(ctx, nil, "/usr/bin/systemctl", "start", "--", Unit(t.Base.Owner, "signer"), Unit(t.Base.Owner, "app")); e != nil {
			return fail(e)
		}
		if e = o.health(ctx, t.Target); e != nil {
			return fail(e)
		}
		if e = bootstrapRecords(ctx, t, o, false); e != nil {
			return fail(e)
		}
	}
	if e = c.Finish(ctx, t, index); e != nil {
		return fail(e)
	}
	if e = mark("released"); e != nil {
		return fail(e)
	}
	if e = clearBootstrapActive(ctx, t, o); e != nil {
		return fail(e)
	}
	if e = mark("completed"); e != nil {
		return fail(e)
	}
	result.Phase = "reader-upgraded"
	return result, nil
}
func bootstrapRecords(ctx context.Context, t BootstrapTransaction, o installOps, write bool) error {
	for _, v := range []struct {
		name      string
		old, next []byte
	}{{"intent.json", mustJSON(installerIntent(t.Base)), mustJSON(installerIntent(t.Target))}, {"ready.json", readyRecord(t.Base), readyRecord(t.Target)}} {
		f, e := o.read(t.Base.Root + "/" + v.name)
		if e != nil {
			return e
		}
		if !known(f, v.next, 0644) && (!write || !known(f, v.old, 0644)) {
			return errors.New("foreign or unconverged bootstrap installer record")
		}
		if write {
			if e = replaceRecord(ctx, t.Base.Root+"/"+v.name, t.Base.ID, v.next, o); e != nil {
				return e
			}
			if o.checkpoint != nil {
				if e = o.checkpoint("installer-" + v.name); e != nil {
					return e
				}
			}
		}
	}
	if e := o.ownerRecord(write); e != nil {
		return e
	}
	if write && o.checkpoint != nil {
		return o.checkpoint("installer-owner")
	}
	return nil
}

// Expiry is checked again when the predecessor has not yet acquired its hold;
// retained exact recovery remains possible after the original validity window.
func (t BootstrapTransaction) AdmissionCurrent() bool {
	p, e := t.Plan()
	return e == nil && time.Now().Unix() < p.Maintenance.ExpiresAt
}

func clearBootstrapActive(ctx context.Context, t BootstrapTransaction, o installOps) error {
	p := t.Base.Root + "/update-active.json"
	f, e := o.read(p)
	if e == nil {
		if !known(f, mustJSON(phaseRecord{1, t.Digest(), t.ID}), 0644) {
			return errors.New("foreign bootstrap marker")
		}
		if _, e = o.run(ctx, nil, "/usr/bin/rm", "--", p); e != nil {
			return e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	_, e = o.run(ctx, nil, "/usr/bin/sync", "-f", t.Base.Root)
	return e
}
