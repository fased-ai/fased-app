//go:build linux

package custodyinstall

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestPackageOwnerReleasesLockBeforeUpdateRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operation.lock")
	first, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	defer second.Close()
	if err = syscall.Flock(int(first.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		first.Close()
		t.Fatal(err)
	}
	called := false
	if err = packageOwnerAfterUnlock(first, func() error {
		called = true
		if err := syscall.Flock(int(second.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			return err
		}
		return syscall.Flock(int(second.Fd()), syscall.LOCK_UN)
	}); err != nil || !called {
		t.Fatal("owner request ran while the worker lock was held", err)
	}
	called = false
	if err = packageOwnerAfterUnlock(first, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("request ran after lock release failed", err)
	}
}

func TestPackageOwnerCommitBindsTheRunningLocalInstance(t *testing.T) {
	_, m := packageGateInitialRecord(t)
	instance := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
	health := core.Health{Status: "ok", APIVersion: core.APIVersion, InstanceID: instance,
		Connection: &core.ConnectionInfo{Location: "local", Transport: "loopback", OwnerUID: m.Owner}}
	commit, err := packageOwnerCommit(m, health)
	if err != nil || !validPackageOwnerCommit(m, commit) || commit.InstanceID != instance {
		t.Fatal("original Local owner was not bound", err)
	}
	for name, change := range map[string]func(*core.Health){
		"instance":  func(h *core.Health) { h.InstanceID = "other" },
		"owner":     func(h *core.Health) { h.Connection.OwnerUID++ },
		"location":  func(h *core.Health) { h.Connection.Location = "remote" },
		"transport": func(h *core.Health) { h.Connection.Transport = "private-https" },
		"API":       func(h *core.Health) { h.APIVersion = "different" },
		"status":    func(h *core.Health) { h.Status = "starting" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := health
			connection := *health.Connection
			bad.Connection = &connection
			change(&bad)
			if _, err := packageOwnerCommit(m, bad); err == nil {
				t.Fatal("foreign Local owner was accepted")
			}
		})
	}
	changed := commit
	changed.InstanceID = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("y", 32)))
	if validPackageOwnerCommit(m, changed) {
		t.Fatal("changed instance retained the original owner commitment")
	}
	other := m
	other.ID = strings.Repeat("f", 32)
	if validPackageOwnerCommit(other, commit) {
		t.Fatal("another installation retained the original owner commitment")
	}
}

func packageGateInitialRecord(t *testing.T) (update.MaintenanceRecord, Manifest) {
	t.Helper()
	r := initialMaintenanceFixture()
	var m Manifest
	if err := strict(r.Base, &m); err != nil {
		t.Fatal(err)
	}
	m.Layout, m.Provenance, m.Network = PackageLayout, signer.OwnerKeyProvenance, ""
	m.Root, _ = PackageRoot(m.Owner, m.Location, m.ID)
	m.App = Identity{Name: packageAccountName(m.Owner, m.Location, m.ID, "app"), UID: 2001, GID: 2001}
	m.Signer = Identity{Name: packageAccountName(m.Owner, m.Location, m.ID, "signer"), UID: 2002, GID: 2002}
	r.Package.DevelopmentOnly = false
	r.Package.ProvenanceSHA256 = strings.Repeat("e", 64)
	r.Base, r.Target = mustJSON(m), mustJSON(m)
	return r, m
}

func TestPackageGateSelectsOnlyBoundStartState(t *testing.T) {
	r, m := packageGateInitialRecord(t)
	selected, err := packageStartSelection(r, m.Owner, m.Location, m.ID, nil)
	if err != nil || selected != m {
		t.Fatal("matching idle installation refused", err)
	}
	target := m
	target.CoreHash, target.SignerHash = strings.Repeat("a", 64), strings.Repeat("b", 64)
	previous := *r.Package
	packageNext := previous
	packageNext.Sequence++
	packageNext.Components = append([]update.Component(nil), previous.Components...)
	packageNext.Components[0].Artifact.SHA256 = target.CoreHash
	packageNext.Components[1].Artifact.SHA256 = target.SignerHash
	r.OperationID = strings.Repeat("f", 32)
	r.Phase, r.Start, r.Target = "prepared", "base", mustJSON(target)
	r.PreviousPackage, r.Package = &previous, &packageNext
	selected, err = packageStartSelection(r, m.Owner, m.Location, m.ID, nil)
	if err != nil || selected != m {
		t.Fatal("unchanged prepared base refused", err)
	}
	for name, change := range map[string]func(*update.MaintenanceRecord){
		"stopped":            func(v *update.MaintenanceRecord) { v.Phase, v.Start = "quiescing", "" },
		"unheld target":      func(v *update.MaintenanceRecord) { v.Phase, v.Start = "ready", "target" },
		"prepared with hold": func(v *update.MaintenanceRecord) { v.Staged = &update.Intent{} },
		"different target identity": func(v *update.MaintenanceRecord) {
			x := target
			x.App.GID++
			v.Target = mustJSON(x)
		},
		"different previous pair": func(v *update.MaintenanceRecord) {
			p := *v.PreviousPackage
			p.Components = append([]update.Component(nil), p.Components...)
			p.Components[0].Artifact.SHA256 = strings.Repeat("c", 64)
			v.PreviousPackage = &p
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := r
			change(&bad)
			if _, err := packageStartSelection(bad, m.Owner, m.Location, m.ID, nil); err == nil {
				t.Fatal("unbound start was accepted")
			}
		})
	}
	if _, err := packageStartSelection(r, m.Owner, "remote", m.ID, nil); err == nil {
		t.Fatal("another location's record was accepted")
	}
}

func TestPackageGateRequiresOriginalEmptySignerCheckpoint(t *testing.T) {
	r, base := packageGateInitialRecord(t)
	r.Package.NativePackage = &update.NativePackage{Family: "dnf", Artifact: update.Artifact{File: "fased-app-linux-amd64.rpm", Size: 1, SHA256: strings.Repeat("6", 64)}, InstallerSHA256: strings.Repeat("7", 64)}
	target := base
	target.CoreHash, target.SignerHash = strings.Repeat("a", 64), strings.Repeat("b", 64)
	previous := *r.Package
	p := previous
	p.Sequence++
	p.ReleaseSHA256 = strings.Repeat("c", 64)
	p.Components = append([]update.Component(nil), previous.Components...)
	p.Components[0].Artifact.SHA256, p.Components[1].Artifact.SHA256 = target.CoreHash, target.SignerHash
	r.OperationID, r.Phase, r.Start = strings.Repeat("f", 32), "prepared", "base"
	r.PreviousPackage, r.Package, r.Target, r.OwnerLease = &previous, &p, mustJSON(target), r.OperationID
	if selected, err := packageStartSelection(r, base.Owner, base.Location, base.ID, nil); err != nil || selected != base {
		t.Fatal("prepared base was refused", err)
	}
	snapshot := update.CoreSnapshot{Version: 1, BootID: strings.Repeat("1", 32), ProbeID: strings.Repeat("2", 32), InstanceID: "fixture-instance", State: update.State{Name: "core-db", Version: core.StateSchema, Anchor: strings.Repeat("3", 64)}}
	request := &update.CorePauseRequest{Version: 1, IntentID: r.OperationID, Sequence: p.Sequence, ManifestSHA256: p.ReleaseSHA256, Expected: snapshot}
	checkpoint := &update.ApplicationCheckpoint{Version: 1, OperationID: r.OperationID, ManifestSHA256: p.ReleaseSHA256, PreviousEngineSHA256: base.CoreHash, Pause: update.CorePauseReceipt{Version: 1, State: "paused", PauseID: strings.Repeat("4", 32), Request: request, Core: snapshot}, Snapshot: update.MetadataSnapshot{Size: 64, SHA256: strings.Repeat("5", 64)}}
	checkpoint.Snapshot.File = "application-" + checkpoint.Snapshot.SHA256 + ".sqlite"
	r.Application, r.Phase, r.Start = checkpoint, "held", ""
	if _, err := packageStartSelection(r, base.Owner, base.Location, base.ID, nil); err == nil {
		t.Fatal("held replacement started a service")
	}
	r.Phase, r.Start = "ready", "target"
	if selected, err := packageStartSelection(r, base.Owner, base.Location, base.ID, nil); err != nil || selected != target {
		t.Fatal("original checkpoint did not select its target", err)
	}
	for name, change := range map[string]func(*update.MaintenanceRecord){
		"other lease":        func(v *update.MaintenanceRecord) { v.OwnerLease = strings.Repeat("9", 32) },
		"missing checkpoint": func(v *update.MaintenanceRecord) { v.Application = nil },
		"different predecessor": func(v *update.MaintenanceRecord) {
			c := *v.Application
			c.PreviousEngineSHA256 = strings.Repeat("9", 64)
			v.Application = &c
		},
		"fake signer hold": func(v *update.MaintenanceRecord) { v.Installation = &update.Installation{} },
		"different release": func(v *update.MaintenanceRecord) {
			c := *v.Application
			c.ManifestSHA256 = strings.Repeat("9", 64)
			v.Application = &c
		},
		"unreadable metadata": func(v *update.MaintenanceRecord) {
			c := *v.Application
			request := *c.Pause.Request
			request.Expected.State.Version = 999
			c.Pause.Request = &request
			c.Pause.Core.State.Version = 999
			v.Application = &c
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := r
			change(&bad)
			if _, err := packageStartSelection(bad, base.Owner, base.Location, base.ID, nil); err == nil {
				t.Fatal("foreign or fabricated recovery was accepted")
			}
		})
	}
	r.Phase = "completed"
	if selected, err := packageStartSelection(r, base.Owner, base.Location, base.ID, nil); err != nil || selected != target {
		t.Fatal("completed target did not survive restart", err)
	}
}

func TestPackageStagingRefusesUnprotectedNativeAndKeyBearingUpdates(t *testing.T) {
	_, m := packageGateInitialRecord(t)
	p := update.MaintenancePackage{NativePackage: &update.NativePackage{}}
	if validateNativePackageUpdateScope(m, p, false) == nil {
		t.Fatal("disabled native package update was admitted")
	}
	if err := validateNativePackageUpdateScope(m, p, true); err != nil {
		t.Fatal("empty signer was refused after enabling native recovery", err)
	}
	m.Network = "mainnet-beta"
	if validateNativePackageUpdateScope(m, p, true) == nil {
		t.Fatal("key-bearing signer was sent through app-only native recovery")
	}
	p.NativePackage = nil
	if err := validateNativePackageUpdateScope(m, p, false); err != nil {
		t.Fatal("existing legacy signer recovery was changed", err)
	}
	m.Network = ""
	if validateNativePackageUpdateScope(m, p, true) == nil {
		t.Fatal("empty-signer update without native package was admitted")
	}
}

func TestPackageGateAdmitsOnlyTheOriginalHeldTarget(t *testing.T) {
	r, base := packageGateInitialRecord(t)
	base.Network = "devnet"
	r.Base = mustJSON(base)
	target := base
	target.CoreHash, target.SignerHash = strings.Repeat("a", 64), strings.Repeat("b", 64)
	previous := *r.Package
	p := previous
	p.Sequence++
	p.Components = append([]update.Component(nil), previous.Components...)
	p.Components[0].Artifact.SHA256, p.Components[1].Artifact.SHA256 = target.CoreHash, target.SignerHash
	op := strings.Repeat("f", 32)
	core := update.CoreSnapshot{Version: 1, BootID: strings.Repeat("1", 32), ProbeID: strings.Repeat("2", 32), InstanceID: "fixture-instance", State: update.State{Name: "core-db", Version: 1, Anchor: strings.Repeat("3", 64)}}
	approval := update.MaintenanceApproval{Version: 1, Owner: base.Owner, InstallationID: base.ID, OperationID: op, HelperSHA256: strings.Repeat("4", 64), Source: p.Source, ProductVersion: p.Version, ReleaseSHA256: p.ReleaseSHA256, ProvenanceSHA256: p.ProvenanceSHA256, PreviousCore: base.CoreHash, PreviousSigner: base.SignerHash, PreviousManifest: sum(mustJSON(base)), Core: core,
		Plan: update.Manifest{Version: 1, Product: "fased-app-maintenance", Channel: "protected-installer", Sequence: p.Sequence, IssuedAt: 1, ExpiresAt: 2, Target: update.Target{OS: "linux", Arch: "amd64"}, Components: p.Components}}
	if err := approval.Validate(); err != nil {
		t.Fatal("invalid approval fixture", err)
	}
	request := &update.CorePauseRequest{Version: 1, IntentID: op, Sequence: p.Sequence, ManifestSHA256: approval.Digest(), Expected: core}
	index := update.RecoveryIndex{Version: 1, Phase: "metadata-backed-up", Binding: update.RecoveryBinding{Version: 1, Pause: update.CorePauseReceipt{Version: 1, State: "paused", PauseID: strings.Repeat("5", 32), Request: request, Core: core}, Signer: update.SignerSnapshot{Version: 3, ActiveIntentID: op, RecoveryHold: true, SequenceFloor: p.Sequence}}, Snapshot: update.MetadataSnapshot{File: "metadata.sqlite", Size: 16, SHA256: strings.Repeat("6", 64)}}
	staged := update.Intent{ID: op, Sequence: p.Sequence, ManifestSHA256: approval.Digest(), State: "staged", Lifecycle: p.Components[0].Contract.Lifecycle}
	var contracts []update.Contract
	for _, component := range p.Components {
		staged.Artifacts = append(staged.Artifacts, component.Artifact)
		contracts = append(contracts, component.Contract)
		index.Binding.Files = append(index.Binding.Files, update.StageFile{Artifact: component.Artifact, Bytes: component.Artifact.Size})
	}
	staged.StageSHA256 = update.StageDigest(staged)
	index.BindingSHA256 = update.RecoveryDigest(index.Binding)
	binding := update.InstallBinding{Version: 1, Contracts: contracts, Index: index, InstallationID: base.ID, PreviousCore: base.CoreHash, PreviousSigner: base.SignerHash, PreviousManifest: approval.PreviousManifest, TargetCore: target.CoreHash, TargetSigner: target.SignerHash}
	installation := &update.Installation{Version: 1, Binding: binding, Digest: update.InstallDigest(binding), Phase: "active"}
	if !update.ValidInstallation(*installation) {
		t.Fatal("invalid signer hold fixture")
	}
	transaction := InstallTransaction{Version: 1, Base: base, Target: target, Binding: binding}
	if !transaction.valid() {
		t.Fatal("the existing recovery transaction refused the exact scoped package pair")
	}
	noWallet := transaction
	noWallet.Base.Network, noWallet.Target.Network = "", ""
	noWallet.Binding.PreviousManifest = sum(mustJSON(noWallet.Base))
	if noWallet.valid() {
		t.Fatal("an unconfigured signer was treated as a real held installation")
	}
	r.OperationID, r.Phase, r.Start = op, "ready", "target"
	r.Target, r.PreviousPackage, r.Package = mustJSON(target), &previous, &p
	r.Approval, r.Staged, r.Recovery, r.Installation = &approval, &staged, &index, installation
	read := func(string) (update.MaintenanceApproval, error) { return approval, nil }
	selected, err := packageStartSelection(r, base.Owner, base.Location, base.ID, read)
	if err != nil || selected != target {
		t.Fatal("the matching held target was refused", err)
	}
	for name, change := range map[string]func(*update.MaintenanceRecord){
		"foreign approval": func(v *update.MaintenanceRecord) {
			a := *v.Approval
			a.HelperSHA256 = strings.Repeat("7", 64)
			v.Approval = &a
		},
		"foreign backup": func(v *update.MaintenanceRecord) {
			b := *v.Recovery
			b.Snapshot.SHA256 = strings.Repeat("8", 64)
			v.Recovery = &b
		},
		"missing held signer": func(v *update.MaintenanceRecord) { v.Installation = nil },
		"foreign signer contracts": func(v *update.MaintenanceRecord) {
			i := *v.Installation
			i.Binding.Contracts = append([]update.Contract(nil), i.Binding.Contracts...)
			i.Binding.Contracts[0].Protocol++
			i.Digest = update.InstallDigest(i.Binding)
			if !update.ValidInstallation(i) {
				t.Fatal("test needs a structurally valid but foreign signer hold")
			}
			v.Installation = &i
		},
		"foreign artifact sizes": func(v *update.MaintenanceRecord) {
			stage := *v.Staged
			stage.Artifacts = append([]update.Artifact(nil), stage.Artifacts...)
			stage.Artifacts[0].Size++
			stage.StageSHA256 = update.StageDigest(stage)
			backup := *v.Recovery
			backup.Binding.Files = append([]update.StageFile(nil), backup.Binding.Files...)
			backup.Binding.Files[0].Artifact.Size++
			backup.Binding.Files[0].Bytes++
			backup.BindingSHA256 = update.RecoveryDigest(backup.Binding)
			i := *v.Installation
			i.Binding.Index = backup
			i.Digest = update.InstallDigest(i.Binding)
			if !update.ValidInstallation(i) {
				t.Fatal("test needs a structurally valid but foreign artifact pair")
			}
			v.Staged, v.Recovery, v.Installation = &stage, &backup, &i
		},
		"rolled back signer": func(v *update.MaintenanceRecord) {
			i := *v.Installation
			i.Phase = "rolled-back-held"
			v.Installation = &i
		},
		"early signer phase": func(v *update.MaintenanceRecord) { v.Phase = "signer-healthy-held" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := r
			change(&bad)
			if _, err := packageStartSelection(bad, base.Owner, base.Location, base.ID, read); err == nil {
				t.Fatal("foreign or incomplete target was accepted")
			}
		})
	}
}

func TestPackageGateRequiresTheOriginalFence(t *testing.T) {
	r, m := packageGateInitialRecord(t)
	files := map[string]hostFile{}
	read := func(path string) (hostFile, error) {
		if f, ok := files[path]; ok {
			return f, nil
		}
		return hostFile{}, os.ErrNotExist
	}
	if err := verifyPackageStartFences(m, r, read); err != nil {
		t.Fatal("idle without fence refused", err)
	}
	r.OperationID, r.Phase = strings.Repeat("f", 32), "ready"
	if err := verifyPackageStartFences(m, r, read); err == nil {
		t.Fatal("pending target started without its fence")
	}
	files[m.Root+"/update-active.json"] = hostFile{UID: 0, Regular: true, Mode: 0644, Data: update.MaintenanceFence(r.OperationID)}
	if err := verifyPackageStartFences(m, r, read); err != nil {
		t.Fatal("matching fence refused", err)
	}
	files[m.Root+"/update-start-blocked.json"] = hostFile{UID: 0, Regular: true, Mode: 0644}
	if err := verifyPackageStartFences(m, r, read); err == nil {
		t.Fatal("another recovery fence was ignored")
	}
	delete(files, m.Root+"/update-start-blocked.json")
	files[m.Root+"/update-active.json"] = hostFile{UID: 0, Regular: true, Mode: 0644, Data: update.MaintenanceFence(strings.Repeat("a", 32))}
	if err := verifyPackageStartFences(m, r, read); err == nil {
		t.Fatal("another operation's fence was accepted")
	}
	if err := verifyPackageStartFences(m, r, func(string) (hostFile, error) { return hostFile{}, errors.New("unreadable") }); err == nil {
		t.Fatal("an unreadable fence was treated as absent")
	}
}

func TestPackageGateRejectsInvalidPhysicalEntry(t *testing.T) {
	r, m := packageGateInitialRecord(t)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(r.Base, &raw); err != nil {
		t.Fatal(err)
	}
	delete(raw, "signer")
	r.Base, _ = json.Marshal(raw)
	if _, err := packageStartSelection(r, m.Owner, m.Location, m.ID, nil); err == nil {
		t.Fatal("missing signer identity was accepted")
	}
	if err := VerifyPackageStart(m.Owner, m.Location, m.ID, "unknown"); err == nil {
		t.Fatal("unknown service role was accepted")
	}
}
