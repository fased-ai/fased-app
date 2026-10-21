//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type InspectionBridge struct {
	TLSCA       []byte                `json:"tlsCa"`
	Version     int                   `json:"version"`
	Profile     string                `json:"profile"`
	ID          string                `json:"id"`
	Base        Manifest              `json:"base"`
	Target      Manifest              `json:"target"`
	CatalogSHA  string                `json:"catalogSha256"`
	Inputs      map[string]string     `json:"inputs"`
	Maintenance []byte                `json:"maintenance"`
	Trust       []byte                `json:"trust"`
	ReviewedAt  int64                 `json:"reviewedAt"`
	Core        update.CoreSnapshot   `json:"core"`
	Signer      update.SignerSnapshot `json:"signer"`
}

func (t InspectionBridge) Dir() string {
	return t.Base.Root + "/fixture-inspection-v1/capsules/" + t.ID
}
func (t InspectionBridge) Digest() string { return sum(mustJSON(t)) }
func (t InspectionBridge) Plan() (update.BootstrapPlan, error) {
	var p update.BootstrapPlan
	if t.Version != 1 || t.Profile != "fased-app-fixture-reader-bridge:v1" || !update.IDOK(t.ID) || !validHex(t.CatalogSHA, 32) || t.Base.validate() != nil || t.Target.validate() != nil || t.Base.InspectionBridge != "" || t.Target.InspectionBridge != t.ID || len(t.Inputs) < 10 || len(t.Inputs) > 74 {
		return p, errors.New("invalid finite fixture bridge")
	}
	b := t.Target
	b.CoreHash = t.Base.CoreHash
	b.SignerHash = t.Base.SignerHash
	b.InspectionBridge = ""
	if b != t.Base {
		return p, errors.New("financial topology migration forbidden")
	}
	trust, e := update.ParseTrust(t.Trust, t.ReviewedAt)
	if e != nil {
		return p, e
	}
	m, h, e := update.Authenticate(t.Maintenance, trust, t.ReviewedAt)
	if e != nil {
		return p, e
	}
	if m.Target != (update.Target{OS: "linux", Arch: "amd64"}) || m.Sequence <= t.Signer.SequenceFloor || t.Signer.ActiveIntentID != "" || t.Signer.Unknown != 0 || t.Signer.Pending != 0 || t.Signer.RecoveryHold {
		return p, errors.New("unsupported bridge financial history")
	}
	for _, c := range m.Components {
		want := t.Base.CoreHash
		if c.Contract.Component == "signer" {
			want = t.Base.SignerHash
		}

		if c.Artifact.SHA256 != want {
			return p, errors.New("exact unchanged maintenance image required")
		}
		if c.Contract.Component == "core" {
			if c.Contract.Lifecycle == nil || c.Contract.Lifecycle.Entrypoint != 4 {
				return p, errors.New("exact lifecycle-4 owner/core required")
			}
		} else {
			// Signer contracts carry protocol/state support, not an owner lifecycle.
			completion := false
			for _, state := range c.Contract.State {
				if state.Name == "operations" {
					for _, feature := range state.Features {
						if feature == "install-completion-v1" {
							completion = true
						}
					}
				}
			}
			if c.Contract.Lifecycle != nil || !completion {
				return p, errors.New("exact completion-capable signer format required")
			}
		}
	}
	if _, e = update.JoinSnapshots(t.Core, t.Signer, m.Target); e != nil {
		return p, e
	}
	p.Maintenance = m
	p.MaintenanceSHA256 = h
	return p, nil
}
func (t InspectionBridge) Current() bool {
	_, e := update.ParseTrust(t.Trust, time.Now().Unix())
	if e != nil {
		return false
	}
	p, e := t.Plan()
	return e == nil && t.ReviewedAt <= time.Now().Unix() && time.Now().Unix() >= p.Maintenance.IssuedAt && time.Now().Unix() < p.Maintenance.ExpiresAt
}
func fixtureUnits(m Manifest, candidate bool) map[string][]byte {
	base := m
	base.InspectionBridge = ""
	units := definitions(base)
	for _, role := range []string{"app", "signer"} {
		name := "fased-" + role + ".service"
		b := string(units[name])
		old := "ExecStart=" + m.Root + "/fased-" + role + " "
		at := stringsIndex(b, old)
		end := stringsIndex(b[at:], "\n") + at
		executable := m.Root + "/fased-" + role
		if candidate {
			executable = m.Root + "/fixture-inspection-v1/capsules/" + m.InspectionBridge + "/candidate/fased-" + role
		}
		b = b[:at] + fmt.Sprintf("ExecStart=%s fixture-reader-guard --owner %d --disposable-fixture", executable, m.Owner) + b[end:]
		if role == "signer" {
			b = replaceText(b, "ReadWritePaths="+m.Root+"/signer-state "+m.Root+"/signer-ipc", "ReadWritePaths="+m.Root+"/signer-state "+m.Root+"/signer-ipc "+m.Root+"/fixture-inspection-v1/signer/state "+m.Root+"/fixture-inspection-v1/signer/anchor")
		}
		units[name] = []byte(b)
	}
	return units
}
func InspectionUnitDigest(m Manifest, candidate bool) string {
	u := fixtureUnits(m, candidate)
	return update.SnapshotDigest("fixture-unit-pair:v1", []string{string(u["fased-app.service"]), string(u["fased-signer.service"])})
}
func stringsIndex(s, part string) int   { return strings.Index(s, part) }
func replaceText(s, a, b string) string { return strings.ReplaceAll(s, a, b) }
func LoadInspectionBridge(uid int, id string) (InspectionBridge, error) {
	var t InspectionBridge
	if uid <= 0 || !update.IDOK(id) {
		return t, errors.New("exact owner/bridge required")
	}
	f, e := readRoot(Root(uid) + "/fixture-inspection-v1/capsules/" + id + "/transaction.json")
	if e != nil {
		return t, e
	}
	if strict(f.Data, &t) != nil || t.Base.Owner != uid || t.ID != id || !known(f, mustJSON(t), 0644) {
		return t, errors.New("immutable bridge changed")
	}
	_, e = t.Plan()
	return t, e
}
func PrepareInspectionBridge(ctx context.Context, t InspectionBridge, inputs, candidate string, s *System) error {
	if _, e := t.Plan(); e != nil {
		return e
	}
	if !t.Current() || os.Getuid() != t.Base.Owner || Verify(t.Base) != nil {
		return errors.New("verified installed owner required")
	}
	if e := noActiveController(t.Base); e != nil {
		return e
	}
	// Count actual three generations and immutable input bytes before allocation.
	needed := uint64(8 << 20)
	data := map[string][]byte{}
	for name, h := range t.Inputs {
		if !inspectionInputName(name) || !validHex(h, 32) {
			return errors.New("closed fixture input name required")
		}
		b, e := source(filepath.Join(inputs, name))
		if e != nil || sum(b) != h {
			return errors.New("fixture input producer changed")
		}
		needed += uint64(len(b))
		data[name] = b
	}
	var catalog update.FixtureInspectionCatalog
	if strict(data["catalog.json"], &catalog) != nil || sum(data["catalog.json"]) != t.CatalogSHA || catalog.Provision != t.ID || catalog.Installation != t.Base.ID || catalog.ManifestSHA != sum(mustJSON(t.Base)) || catalog.RootSHA != sum([]byte(t.Base.Root)) || catalog.BeforeImages != [2]string{t.Base.CoreHash, t.Base.SignerHash} || catalog.CandidateImages != [2]string{t.Target.CoreHash, t.Target.SignerHash} || catalog.GuardedUnits != InspectionUnitDigest(t.Target, true) || catalog.FinalUnits != InspectionUnitDigest(t.Target, false) {
		return errors.New("catalog does not bind actual bridge")
	}
	var rolePin update.FixtureInspectionRoleInput
	if strict(data["owner.json"], &rolePin) != nil {
		return errors.New("invalid independent owner descriptor")
	}
	downloaded, e := update.AcquireFixtureInspectionBundle(ctx, data["owner-seed.json"], t.TLSCA, rolePin.IndexSHA)
	if e != nil {
		return e
	}
	for name, b := range downloaded {
		if t.Inputs["bundle/"+name] != sum(b) {
			return errors.New("acquired bundle/capsule mismatch")
		}
		data["bundle/"+name] = b
	}
	if catalog.BeforeUnits != update.SnapshotDigest("fixture-unit-pair:v1", []string{string(definitions(t.Base)["fased-app.service"]), string(definitions(t.Base)["fased-signer.service"])}) {
		return errors.New("predecessor unit pin changed")
	}
	for _, role := range []string{"app", "signer"} {
		name := "fased-" + role
		from := candidate + "/" + name
		want := t.Target.CoreHash
		if role == "signer" {
			want = t.Target.SignerHash
		}
		b, e := source(from)
		if e != nil || sum(b) != want {
			return errors.New("candidate producer changed")
		}
		data["candidate/"+name] = b
		needed += 3 * uint64(len(b))
		b, e = source(t.Base.Root + "/" + name)
		if e != nil {
			return e
		}
		data["previous/"+name] = b
		needed += 3 * uint64(len(b))
	}
	var fs unix.Statfs_t
	if e := unix.Statfs(t.Base.Root, &fs); e != nil {
		return e
	}
	if fs.Bsize <= 0 || uint64(fs.Bavail)*uint64(fs.Bsize) < needed {
		return errors.New("insufficient bridge recovery space")
	}
	base := t.Base.Root + "/fixture-inspection-v1"
	for _, d := range []string{base, base + "/inputs", base + "/inputs/bundle", base + "/capsules", t.Dir(), t.Dir() + "/candidate", t.Dir() + "/previous", base + "/owner", base + "/signer"} {
		if e := directory(ctx, d, 0, 0, 0755, s); e != nil {
			return e
		}
	}
	for _, v := range []struct {
		name     string
		uid, gid int
	}{{"owner", t.Base.Owner, catalog.GIDs[0]}, {"signer", t.Base.Signer.UID, t.Base.Signer.GID}} {
		for _, d := range []string{"state", "anchor"} {
			if e := directory(ctx, base+"/"+v.name+"/"+d, v.uid, v.gid, 0700, s); e != nil {
				return e
			}
		}
	}
	for name, b := range data {
		path := base + "/inputs/" + name
		mode := os.FileMode(0644)
		if strings.HasPrefix(name, "candidate/") || strings.HasPrefix(name, "previous/") {
			path = t.Dir() + "/" + name
			mode = 0755
		}
		if e := publish(ctx, path, t.ID, b, mode, readRoot, s.Run); e != nil {
			return e
		}
	}
	// Authenticating output hashes precedes every candidate execution.
	for _, role := range []string{"app", "signer"} {
		identity := t.Base.App
		if role == "signer" {
			identity = t.Base.Signer
		}
		b, e := runAs(ctx, s, t.Dir()+"/candidate/fased-"+role, identity, "fixture-inspection-compatibility")
		var c update.FixtureInspectionReader
		if e != nil || update.StrictJSON(b, &c) != nil || c != update.FixtureInspectionContract() {
			return errors.New("candidate inspection capability refused")
		}
		actual, e := probeReader(ctx, t.Dir()+"/candidate/fased-"+role, role == "app", t.Base, s)
		if e != nil {
			return e
		}
		plan, e := t.Plan()
		if e != nil {
			return e
		}
		component := "core"
		if role == "signer" {
			component = "signer"
		}
		matched := false
		for _, c := range plan.Maintenance.Components {
			if c.Contract.Component == component {
				matched = update.SnapshotDigest("contract", actual) == update.SnapshotDigest("contract", c.Contract)
			}
		}
		if !matched {
			return errors.New("candidate changed financial reader contract")
		}

	}
	if e := update.ValidateProvisionedInspectionInputs(t.Base.Root, t.CatalogSHA); e != nil {
		return e
	}
	return publish(ctx, t.Dir()+"/transaction.json", t.ID, mustJSON(t), 0644, readRoot, s.Run)
}
func inspectionInputName(n string) bool { return update.FixtureInspectionInputName(n) }

func runAs(ctx context.Context, s *System, path string, id Identity, args ...string) ([]byte, error) {
	cmd := []string{"/usr/bin/setpriv", "--reuid", strconv.Itoa(id.UID), "--regid", strconv.Itoa(id.GID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", path}
	return s.Run(ctx, nil, append(cmd, args...)...)
}

func RunInspectionBridge(ctx context.Context, t InspectionBridge, s *System, c BootstrapCallbacks) (BootstrapReceipt, error) {
	out := BootstrapReceipt{Version: 1, IntentID: t.ID, Digest: t.Digest()}
	if _, e := t.Plan(); e != nil {
		return out, e
	}
	if os.Getuid() != t.Base.Owner || os.Geteuid() != os.Getuid() {
		return out, errors.New("ordinary bridge owner required")
	}
	fixed, e := LoadInspectionBridge(t.Base.Owner, t.ID)
	if e != nil || fixed.Digest() != t.Digest() {
		return out, errors.New("bridge capsule changed")
	}
	for n, h := range t.Inputs {
		f, e := readRoot(t.Base.Root + "/fixture-inspection-v1/inputs/" + n)
		if e != nil || f.Mode != 0644 || sum(f.Data) != h {
			return out, errors.New("sealed input changed; held")
		}
	}
	o := installOps{read: readRoot, run: s.Run}
	base := t.Base.Root + "/fixture-inspection-v1"
	mark := func(phase string) error {
		if e := publish(ctx, t.Dir()+"/"+phase+".json", t.ID, mustJSON(phaseRecord{1, t.Digest(), phase}), 0644, readRoot, s.Run); e != nil {
			return e
		}
		fmt.Fprintln(s.Out, "Inspection bridge boundary:", phase)
		if s.installFault != nil {
			return s.installFault(phase)
		}
		return nil
	}
	has := func(phase string) (bool, error) {
		f, e := readRoot(t.Dir() + "/" + phase + ".json")
		if os.IsNotExist(e) {
			return false, nil
		}
		if e != nil || !known(f, mustJSON(phaseRecord{1, t.Digest(), phase}), 0644) {
			return false, errors.New("foreign bridge phase")
		}
		return true, nil
	}
	carrier := BootstrapTransaction{ID: t.ID, Base: t.Base, Target: t.Target, Core: t.Core, Signer: t.Signer, Trust: t.Trust, Bundle: update.BootstrapBundle{Maintenance: t.Maintenance}}
	var index update.RecoveryIndex
	f, e := readRoot(t.Dir() + "/recovery.json")
	if os.IsNotExist(e) {
		if !t.Current() {
			return out, errors.New("expired new bridge")
		}
		index, e = c.Maintain(ctx, carrier)
		if e != nil {
			return out, e
		}
		if e = publish(ctx, t.Dir()+"/recovery.json", t.ID, mustJSON(index), 0644, readRoot, s.Run); e != nil {
			return out, e
		}
	} else if e != nil || strict(f.Data, &index) != nil || !known(f, mustJSON(index), 0644) {
		return out, errors.New("invalid bridge recovery index")
	}
	plan, pe := t.Plan()
	if pe != nil || !update.ValidRecoveryIndex(index, index.Binding) || index.Binding.Pause.Request == nil || index.Binding.Pause.Request.IntentID != t.ID || index.Binding.Pause.Request.Sequence != plan.Maintenance.Sequence || index.Binding.Pause.Request.ManifestSHA256 != plan.MaintenanceSHA256 || !update.SameCoreMetadata(index.Binding.Pause.Request.Expected, t.Core) {
		return out, errors.New("maintenance backup binding changed")
	}
	if done, e := has("reader-ready"); e != nil {
		return out, e
	} else if done {
		if e = Verify(t.Target); e != nil {
			return out, e
		}
		if e = Runtime(t.Target); e != nil {
			return out, e
		}
		out.AlreadyCurrent = true
		out.Phase = "reader-ready"
		return out, nil
	}
	if e = c.Observe(ctx, carrier, index, false, true); e != nil {
		return out, e
	}
	if e = mark("maintenance-held"); e != nil {
		return out, e
	}
	if _, e = s.Run(ctx, nil, "/usr/bin/systemctl", "stop", "--", Unit(t.Base.Owner, "app"), Unit(t.Base.Owner, "signer")); e != nil {
		return out, e
	}
	if e = servicesStopped(ctx, t.Base, s.Run); e != nil {
		return out, e
	}
	if e = mark("stopped"); e != nil {
		return out, e
	}
	// Classify every existing unit as exact predecessor/guarded/final before replacing.
	installUnits := func(candidate bool) error {
		wanted := fixtureUnits(t.Target, candidate)
		old := definitions(t.Base)
		guard := fixtureUnits(t.Target, true)
		final := fixtureUnits(t.Target, false)
		for _, role := range []string{"app", "signer"} {
			name := "fased-" + role + ".service"
			for _, p := range []string{t.Base.Root + "/" + name, "/etc/systemd/system/" + Unit(t.Base.Owner, role)} {
				f, e := readRoot(p)
				if e != nil || (!known(f, old[name], 0644) && !known(f, guard[name], 0644) && !known(f, final[name], 0644)) {
					return errors.New("foreign service policy; held")
				}
				if e = replaceRecord(ctx, p, t.ID, wanted[name], o); e != nil {
					return e
				}
			}
		}
		_, e := s.Run(ctx, nil, "/usr/bin/systemctl", "daemon-reload")
		if e != nil {
			return e
		}
		for _, role := range []string{"app", "signer"} {
			image := t.Base.Root + "/fased-" + role
			if candidate {
				image = t.Dir() + "/candidate/fased-" + role
			}
			if e := loadedUnitPolicy(t.Target, role, wanted["fased-"+role+".service"], image); e != nil {
				return e
			}
		}
		return nil
	}
	forward, fe := readRoot(base + "/forward.json")
	fence := update.FixtureInspectionFence{Version: 1, Profile: update.FixtureInspectionProfile, Provision: t.ID, CatalogSHA: t.CatalogSHA, BridgeSHA: t.Digest()}
	if fe != nil && !os.IsNotExist(fe) {
		return out, fe
	}
	if fe == nil && !known(forward, mustJSON(fence), 0644) {
		return out, errors.New("foreign forward fence")
	}
	if os.IsNotExist(fe) {
		if e = installUnits(true); e != nil {
			return out, e
		}
		if e = mark("guarded-units"); e != nil {
			return out, e
		}
		if e = c.Observe(ctx, carrier, index, false, false); e != nil {
			return out, e
		}
		// Actual cold core inspection is read-only and refuses uncheckpointed WAL.
		b, e := runAs(ctx, s, t.Dir()+"/candidate/fased-app", t.Base.App, "fixture-inspection-cold", "--owner", strconv.Itoa(t.Base.Owner), "--disposable-fixture")
		var cold core.FixtureColdMetadata
		if e != nil || update.StrictJSON(b, &cold) != nil || !update.SameCoreMetadata(cold.Core, t.Core) || cold.Phase != "paused" || cold.PauseID != index.Binding.Pause.PauseID || cold.Request == nil || update.SnapshotDigest("pause-request", cold.Request) != update.SnapshotDigest("pause-request", index.Binding.Pause.Request) {
			return out, errors.New("cold core history changed or unreadable")
		}
		if e = mark("cold-verified"); e != nil {
			return out, e
		}
		if e = publish(ctx, base+"/forward.json", t.ID, mustJSON(fence), 0644, readRoot, s.Run); e != nil {
			return out, e
		}
		if e = mark("forward-only"); e != nil {
			return out, e
		}
	}
	// Nothing above the synced fence can initialize admission state. Everything
	// below is forward-only: no old binary or financial backup restore is called.
	if e = allowedInstallation(InstallTransaction{Base: t.Base, Target: t.Target}, readRoot); e != nil {
		return out, e
	}
	for _, name := range []string{"fased-app", "fased-signer"} {
		f, e := readRoot(t.Dir() + "/candidate/" + name)
		if e != nil {
			return out, e
		}
		want := t.Target.CoreHash
		if name == "fased-signer" {
			want = t.Target.SignerHash
		}
		if f.Mode != 0755 || sum(f.Data) != want {
			return out, errors.New("immutable target changed")
		}
		if e = replaceRecord(ctx, t.Base.Root+"/"+name, t.ID, f.Data, o); e != nil {
			return out, e
		}
		if e = mark(name + "-replaced"); e != nil {
			return out, e
		}
	}
	if e = replaceRecord(ctx, t.Base.Root+"/install.json", t.ID, mustJSON(t.Target), o); e != nil {
		return out, e
	}
	// Init uses the authenticated candidate and each independent private UID.
	for _, v := range []struct {
		role, path string
		identity   Identity
	}{{"signer", t.Dir() + "/candidate/fased-signer", t.Base.Signer}, {"owner", t.Dir() + "/candidate/fased-app", Identity{UID: t.Base.Owner, GID: os.Getgid()}}} {
		if _, e = runAs(ctx, s, v.path, v.identity, "fixture-inspection-init", "--owner", strconv.Itoa(t.Base.Owner), "--disposable-fixture"); e != nil {
			return out, e
		}
		if e = mark(v.role + "-initialized"); e != nil {
			return out, e
		}
	}
	if e = installUnits(false); e != nil {
		return out, e
	}
	if e = ownerCompletionRecord(InstallTransaction{Base: t.Base, Target: t.Target}, true); e != nil {
		return out, e
	}
	if e = replaceRecord(ctx, t.Base.Root+"/ready.json", t.ID, readyRecord(t.Target), o); e != nil {
		return out, e
	}
	if _, e = s.Run(ctx, nil, "/usr/bin/systemctl", "start", "--", Unit(t.Base.Owner, "signer"), Unit(t.Base.Owner, "app")); e != nil {
		return out, e
	}
	if e = waitReady(ctx, t.Target, s); e != nil {
		return out, e
	}
	if e = c.Observe(ctx, carrier, index, true, true); e != nil {
		return out, e
	}
	if e = mark("converged-held"); e != nil {
		return out, e
	}
	if e = c.Finish(ctx, carrier, index); e != nil {
		return out, e
	}
	if e = mark("maintenance-aborted"); e != nil {
		return out, e
	}
	if e = mark("reader-ready"); e != nil {
		return out, e
	}
	out.Phase = "reader-ready"
	return out, nil
}

// Rollback is explicit and only pre-fence. It never restores financial data.
func RollbackInspectionBridge(ctx context.Context, t InspectionBridge, s *System, c BootstrapCallbacks) (BootstrapReceipt, error) {
	out := BootstrapReceipt{Version: 1, IntentID: t.ID, Digest: t.Digest()}
	if os.Getuid() != t.Base.Owner || os.Geteuid() != os.Getuid() {
		return out, errors.New("ordinary owner required")
	}
	fixed, e := LoadInspectionBridge(t.Base.Owner, t.ID)
	if e != nil || fixed.Digest() != t.Digest() {
		return out, errors.New("immutable rollback capsule required")
	}
	if _, e := readRoot(t.Base.Root + "/fixture-inspection-v1/forward.json"); !os.IsNotExist(e) {
		return out, errors.New("forward fence forbids predecessor rollback")
	}
	carrier := BootstrapTransaction{ID: t.ID, Base: t.Base, Target: t.Target, Core: t.Core, Signer: t.Signer, Trust: t.Trust, Bundle: update.BootstrapBundle{Maintenance: t.Maintenance}}
	var index update.RecoveryIndex
	f, e := readRoot(t.Dir() + "/recovery.json")
	if e != nil || strict(f.Data, &index) != nil || !update.ValidRecoveryIndex(index, index.Binding) {
		return out, errors.New("verified maintenance recovery required")
	}
	if _, e = s.Run(ctx, nil, "/usr/bin/systemctl", "stop", "--", Unit(t.Base.Owner, "app"), Unit(t.Base.Owner, "signer")); e != nil {
		return out, e
	}
	if e = servicesStopped(ctx, t.Base, s.Run); e != nil {
		return out, e
	}
	if e = c.Observe(ctx, carrier, index, false, false); e != nil {
		return out, e
	}
	// Both private namespaces must positively report empty under their own UIDs.
	for _, v := range []struct {
		role, path string
		id         Identity
	}{{"owner", t.Dir() + "/candidate/fased-app", Identity{UID: t.Base.Owner, GID: os.Getgid()}}, {"signer", t.Dir() + "/candidate/fased-signer", t.Base.Signer}} {
		b, e := runAs(ctx, s, v.path, v.id, "fixture-inspection-empty", "--owner", strconv.Itoa(t.Base.Owner), "--disposable-fixture")
		var r struct {
			Empty bool `json:"empty"`
		}
		if e != nil || update.StrictJSON(b, &r) != nil || !r.Empty {
			return out, errors.New("initialized/unknown role state forbids rollback")
		}
	}
	// Actual predecessor cold parsing must succeed, independently of the new probe.
	b, e := runAs(ctx, s, t.Dir()+"/previous/fased-signer", t.Base.Signer, "fixture-update-inspect", "--config", t.Base.Root+"/identity.json", "--disposable-fixture", "--intent-id", t.ID)
	var previous signer.Response
	if e != nil || update.StrictJSON(b, &previous) != nil || previous.UpdateInventory == nil || !update.BootstrapHeld(index.Binding.Signer, *previous.UpdateInventory, false) {
		return out, errors.New("predecessor cannot read exact financial history")
	}
	if e = allowedInstallation(InstallTransaction{Base: t.Base, Target: t.Base}, readRoot); e != nil {
		return out, e
	}
	o := installOps{read: readRoot, run: s.Run}
	old := definitions(t.Base)
	guard := fixtureUnits(t.Target, true)
	for _, role := range []string{"app", "signer"} {
		n := "fased-" + role + ".service"
		for _, p := range []string{t.Base.Root + "/" + n, "/etc/systemd/system/" + Unit(t.Base.Owner, role)} {
			f, e := readRoot(p)
			if e != nil || (!known(f, old[n], 0644) && !known(f, guard[n], 0644)) {
				return out, errors.New("foreign rollback units")
			}
			if e = replaceRecord(ctx, p, t.ID, old[n], o); e != nil {
				return out, e
			}
		}
	}
	if _, e = s.Run(ctx, nil, "/usr/bin/systemctl", "daemon-reload"); e != nil {
		return out, e
	}
	if _, e = s.Run(ctx, nil, "/usr/bin/systemctl", "start", "--", Unit(t.Base.Owner, "signer"), Unit(t.Base.Owner, "app")); e != nil {
		return out, e
	}
	if e = waitReady(ctx, t.Base, s); e != nil {
		return out, e
	}
	if e = c.Observe(ctx, carrier, index, true, false); e != nil {
		return out, e
	}
	if e = c.Finish(ctx, carrier, index); e != nil {
		return out, e
	}
	if e = publish(ctx, t.Dir()+"/rolled-back.json", t.ID, mustJSON(phaseRecord{1, t.Digest(), "rolled-back"}), 0644, readRoot, s.Run); e != nil {
		return out, e
	}
	out.Phase = "rolled-back"
	return out, nil
}
