//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

type BootstrapTransaction struct {
	Repair     *InstallTransaction    `json:"repair,omitempty"`
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
	p, e := t.pairPlan()
	if e != nil {
		return p, e
	}
	ordinary := t.Version == 2 && t.Bundle.Version == 2 && t.Base.Version == 3
	if ordinary {
		if update.SnapshotDigest("empty-signer", t.Signer) != update.SnapshotDigest("empty-signer", update.SignerSnapshot{}) || t.Core.Version != 1 || !update.IDOK(t.Core.ProbeID) || !update.IDOK(t.Core.BootID) || t.Core.State.Name != "core-db" {
			return p, errors.New("ordinary handoff requires old core snapshot and cold-only signer capture")
		}
	} else if t.Version == 3 {
		if e := t.repairPlan(p); e != nil {
			return p, e
		}
	} else if _, e := update.JoinSnapshots(t.Core, t.Signer, p.Maintenance.Target); e != nil {
		return p, e
	}
	return p, nil
}

// Pair validation also precedes the narrowly scoped custody inventory read. A
// loadable transaction still requires the real snapshot through Plan above.
func (t BootstrapTransaction) pairPlan() (update.BootstrapPlan, error) {
	p, e := update.VerifyBootstrap(t.Bundle, t.Trust, t.ReviewedAt)
	if e != nil {
		return p, e
	}
	ordinary := (t.Version == 2 || t.Version == 3) && t.Bundle.Version == t.Version && t.Base.Version == 3
	b := t.Target
	b.CoreHash = t.Base.CoreHash
	b.SignerHash = t.Base.SignerHash
	if (!ordinary && (t.Version != 1 || t.Base.Version != 1 || t.Bundle.Version != 1)) || !update.IDOK(t.ID) || t.Base.validate() != nil || t.Target.validate() != nil || b != t.Base || (t.Version != 3 && (t.Repair != nil || t.Signer.ActiveIntentID != "" || t.Signer.InstalledSequence != 0 || p.Target.Sequence <= t.Signer.SequenceFloor)) {
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

// This stages only an inert signed core. No transaction, intent, active marker
// or forward fence exists until the owner reviews the resulting snapshot.
func (s *System) BootstrapInventory(ctx context.Context, t BootstrapTransaction, artifacts, probe string, out io.Writer) error {
	_, e := t.pairPlan()
	if e != nil {
		return e
	}
	m := t.Base
	if t.Version != 2 || t.Bundle.Version != 2 || !bootstrapInventoryProfile(m) || m.Owner != os.Getuid() || os.Geteuid() != m.Owner || !update.IDOK(probe) || Verify(m) != nil {
		return errors.New("exact enrolled custody predecessor required")
	}
	lock, e := OwnerLock()
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = noActiveController(m); e != nil {
		return e
	}
	if _, e = ReadEnrollmentCommit(m); e != nil {
		return e
	}
	if m.Location == "local" {
		if e = verifyNativePolicy(ctx, m, s); e != nil {
			return e
		}
	}
	if e = Runtime(m); e != nil {
		return errors.New("open the existing custody instance before bootstrap review")
	}
	return stageBootstrapInventory(ctx, t, artifacts, probe, out, bootstrapInventoryOps{
		read: readRoot, run: s.Run,
		mkdir: func(ctx context.Context, path string) error { return directory(ctx, path, 0, 0, 0755, s) },
	})
}

type bootstrapInventoryOps struct {
	read  readHost
	run   runHost
	mkdir func(context.Context, string) error
}

func stageBootstrapInventory(ctx context.Context, t BootstrapTransaction, artifacts, probe string, out io.Writer, ops bootstrapInventoryOps) error {
	p, e := t.pairPlan()
	if e != nil {
		return e
	}
	m := t.Base
	if t.Version != 2 || t.Bundle.Version != 2 || !bootstrapInventoryProfile(m) || !update.IDOK(probe) {
		return errors.New("exact enrolled custody predecessor required")
	}
	for _, d := range []string{m.Root + "/bootstraps", t.Dir(), t.Dir() + "/candidate"} {
		if e = ops.mkdir(ctx, d); e != nil {
			return e
		}
	}
	for _, c := range p.Target.Components {
		if c.Contract.Component != "core" {
			continue
		}
		b, e := source(filepath.Join(artifacts, c.Artifact.File))
		if e != nil {
			return e
		}
		if int64(len(b)) != c.Artifact.Size || sum(b) != c.Artifact.SHA256 {
			return errors.New("custody inventory core differs from signed target")
		}
		path := t.Dir() + "/candidate/fased-app"
		if e = publish(ctx, path, m.ID, b, 0755, ops.read, ops.run); e != nil {
			return e
		}
		contract, e := probeReaderWithRun(ctx, path, true, m, ops.run, true)
		if e != nil {
			return e
		}
		if update.SnapshotDigest("contract", contract) != update.SnapshotDigest("contract", c.Contract) {
			return errors.New("custody inventory core contract changed")
		}
		args := []string{"/usr/bin/setpriv", "--reuid", strconv.Itoa(m.App.UID), "--regid", strconv.Itoa(m.App.GID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", path, "update-inventory", "--data-dir", m.Root + "/app-state", "--probe-id", probe, "--bootstrap-owner", strconv.Itoa(m.Owner), "--bootstrap-id", t.ID, "--bootstrap-core-sha256", c.Artifact.SHA256}
		result, e := ops.run(ctx, nil, args...)
		if e != nil {
			return e
		}
		_, e = out.Write(result)
		return e
	}
	return errors.New("signed custody inventory core missing")
}

// The staged child independently checks its OS role and immutable selected image
// before touching protected IPC. It grants neither signer nor setup authority.
func BootstrapInventoryIdentity(owner int, id, hash, data string) (Manifest, string, error) {
	m, e := Load(owner)
	if e != nil {
		return Manifest{}, "", e
	}
	executable, e := os.Executable()
	if e != nil {
		return Manifest{}, "", e
	}
	if e = validateBootstrapInventory(m, os.Getuid(), os.Geteuid(), os.Getgid(), executable, data, id, hash); e != nil {
		return Manifest{}, "", e
	}
	if e = Verify(m); e != nil {
		return Manifest{}, "", e
	}
	if e = noActiveController(m); e != nil {
		return Manifest{}, "", e
	}
	if e = signer.CheckApplication(m.Root+"/identity.json", m.Root, data); e != nil {
		return Manifest{}, "", e
	}
	f, e := readRoot(executable)
	if e != nil || f.Mode != 0755 || sum(f.Data) != hash {
		return Manifest{}, "", errors.New("signed staged custody inventory core changed")
	}
	c, e := ReadEnrollmentCommit(m)
	return m, c.InstanceID, e
}

// Local retains native-owner authentication; Remote uses only its own enrolled
// custody identity. A separately installed VPS app is never an identity source.
func bootstrapInventoryProfile(m Manifest) bool {
	return m.Version == 3 && m.Enrollment && ((m.Location == "local" && m.NativeOwnerAccess) || (m.Location == "remote" && !m.NativeOwnerAccess && m.PublicOrigin != ""))
}

func validateBootstrapInventory(m Manifest, uid, euid, gid int, executable, data, id, hash string) error {
	if m.validate() != nil || !bootstrapInventoryProfile(m) || uid != m.App.UID || euid != uid || gid != m.App.GID || !update.IDOK(id) || !validHex(hash, 32) || data != m.Root+"/app-state" || executable != m.Root+"/bootstraps/"+id+"/candidate/fased-app" {
		return errors.New("exact staged custody inventory role required")
	}
	return nil
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
			contract, e := probeReader(ctx, t.Dir()+"/"+group.name+"/"+c.Artifact.File, c.Contract.Component == "core", t.Base, s, t.Version == 3 || t.Version == 2 && group.name == "candidate")
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
func probeReader(ctx context.Context, path string, isCore bool, m Manifest, s *System, ordinaryTarget ...bool) (update.Contract, error) {
	return probeReaderWithRun(ctx, path, isCore, m, s.Run, ordinaryTarget...)
}

func probeReaderWithRun(ctx context.Context, path string, isCore bool, m Manifest, run runHost, ordinaryTarget ...bool) (update.Contract, error) {
	identity := m.Signer
	args := []string{"fixture-compatibility"}
	if isCore {
		identity = m.App
		args = []string{"updates", "compatibility"}
	}
	if m.Version == 3 && (!isCore || len(ordinaryTarget) > 0 && ordinaryTarget[0]) {
		args[len(args)-1] = "ordinary-custody-compatibility"
	}
	cmd := []string{"/usr/bin/setpriv", "--reuid", strconv.Itoa(identity.UID), "--regid", strconv.Itoa(identity.GID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", path}
	cmd = append(cmd, args...)
	b, e := run(ctx, nil, cmd...)
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
	CancelOrdinary bool // Protected owner CLI only; never decoded from RPC.
	Maintain       func(context.Context, BootstrapTransaction) (update.RecoveryIndex, error)
	Observe        func(context.Context, BootstrapTransaction, update.RecoveryIndex, bool, bool) error // require core, allow aborted
	Finish         func(context.Context, BootstrapTransaction, update.RecoveryIndex) error
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
		if t.Version == 2 || t.Version == 3 {
			return ownerCompletionRecord(InstallTransaction{Base: t.Base, Target: t.Target}, false)
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
	o.ownerCurrent = func(m Manifest) error {
		d, e := ownerJournal()
		if e != nil {
			return e
		}
		b, e := core.ReadPrivateFile(d + "/intent.json")
		var j journal
		if e != nil || strict(b, &j) != nil || j != installerIntent(m) {
			return errors.New("ordinary owner record is not the exact installed generation")
		}
		return nil
	}
	if t.Version == 2 || t.Version == 3 {
		return runOrdinaryBootstrap(ctx, t, o, ordinaryBootstrapCallbacks(t, s), c.CancelOrdinary)
	}
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
	if _, e := t.Plan(); e != nil {
		return false
	}
	_, e := update.VerifyBootstrap(t.Bundle, t.Trust, time.Now().Unix())
	return e == nil
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
