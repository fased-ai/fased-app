//go:build linux

package custodyinstall

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestProvisionedPackageReleasesLockBeforeRecoveryServiceStarts(t *testing.T) {
	t.Parallel()
	_, m := packageGateInitialRecord(t)
	path := filepath.Join(t.TempDir(), "operation.lock")
	first, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err = syscall.Flock(int(first.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	waited := false
	start := func(_ context.Context, args ...string) ([]byte, error) {
		if strings.Join(args, " ") != "start -- "+instanceUnit(m, "app") {
			return nil, errors.New("the wrong instance was started")
		}
		if err := syscall.Flock(int(second.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			return nil, err
		}
		return nil, syscall.Flock(int(second.Fd()), syscall.LOCK_UN)
	}
	ready := func(_ context.Context, got Manifest) error {
		waited = true
		if got != m {
			return errors.New("the selected instance changed")
		}
		return nil
	}
	bind := func(context.Context, Manifest) error { return nil }
	if err = startProvisionedPackage(t.Context(), m, first, start, ready, bind); err != nil || !waited {
		t.Fatal("initial recovery could not acquire the instance lock before startup", err)
	}
	called := false
	if err = startProvisionedPackage(t.Context(), m, first, func(context.Context, ...string) ([]byte, error) {
		called = true
		return nil, nil
	}, ready, bind); err == nil || called {
		t.Fatal("startup ran without releasing the original lock", err)
	}
	retry, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer retry.Close()
	if err = syscall.Flock(int(retry.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	startError := errors.New("service start failed")
	waited = false
	if err = startProvisionedPackage(t.Context(), m, retry, func(context.Context, ...string) ([]byte, error) {
		return nil, startError
	}, ready, bind); !errors.Is(err, startError) || waited {
		t.Fatal("failed startup was treated as healthy", err)
	}
	if err = syscall.Flock(int(second.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal("failed startup kept the recovery lock", err)
	}
	if err = syscall.Flock(int(second.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionedPackageBindsOwnerBeforeInstallerReturns(t *testing.T) {
	t.Parallel()
	_, m := packageGateInitialRecord(t)
	lock, err := os.OpenFile(filepath.Join(t.TempDir(), "operation.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	ready, bound := false, false
	err = startProvisionedPackage(t.Context(), m, lock,
		func(context.Context, ...string) ([]byte, error) { return nil, nil },
		func(_ context.Context, got Manifest) error { ready = got == m; return nil },
		func(_ context.Context, got Manifest) error { bound = ready && got == m; return nil })
	if err != nil || !ready || !bound {
		t.Fatal("installer returned before binding the healthy package owner", err)
	}
	lock, err = os.OpenFile(filepath.Join(t.TempDir(), "operation.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	bindError := errors.New("owner commitment unavailable")
	err = startProvisionedPackage(t.Context(), m, lock,
		func(context.Context, ...string) ([]byte, error) { return nil, nil },
		func(context.Context, Manifest) error { return nil },
		func(context.Context, Manifest) error { return bindError })
	if !errors.Is(err, bindError) {
		t.Fatal("installer reported success without the protected owner", err)
	}
}

func TestPackageOwnerReleasesLockBeforeUpdateRequest(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestInstalledPackageOwnerHealthWithEnrolledWallet(t *testing.T) {
	t.Parallel()
	_, m := packageGateInitialRecord(t)
	health := core.Health{Status: "ok", APIVersion: core.APIVersion,
		InstanceID: base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", 32))),
		Connection: &core.ConnectionInfo{Location: m.Location, Transport: "loopback", OwnerUID: m.Owner}}
	fresh, err := packageOwnerCommit(m, health)
	if err != nil {
		t.Fatal(err)
	}
	m.Network = "devnet"
	if _, err = packageOwnerCommit(m, health); err == nil {
		t.Fatal("fresh provisioning accepted an enrolled signer")
	}
	if installedPackageOwnerHealthy(m, fresh, health) {
		t.Fatal("wallet enrollment must use the recorded configured owner commitment")
	}
	original := packageWalletCommit(m, health.InstanceID)
	if !installedPackageOwnerHealthy(m, original, health) {
		t.Fatal("healthy wallet-bearing package was rejected despite the original owner commitment")
	}
	health.InstanceID = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("y", 32)))
	if installedPackageOwnerHealthy(m, original, health) {
		t.Fatal("a different healthy app was accepted")
	}
}

func TestPackageOwnerCommitBindsTheRunningRemoteInstance(t *testing.T) {
	t.Parallel()
	_, m := packageGateInitialRecord(t)
	m.Location, m.PublicOrigin = "remote", "https://node.example.ts.net:8443"
	m.Root, _ = PackageRoot(m.Owner, m.Location, m.ID)
	m.App.Name = packageAccountName(m.Owner, m.Location, m.ID, "app")
	m.Signer.Name = packageAccountName(m.Owner, m.Location, m.ID, "signer")
	instance := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
	health := core.Health{Status: "ok", APIVersion: core.APIVersion, InstanceID: instance,
		Connection: &core.ConnectionInfo{Location: "remote", Transport: "loopback", OwnerUID: m.Owner}}
	commit, err := packageOwnerCommit(m, health)
	if err != nil || !validPackageOwnerCommit(m, commit) {
		t.Fatal("running Remote instance did not receive its own owner binding", err)
	}
	other := m
	other.ID = strings.Repeat("f", 32)
	other.Root, _ = PackageRoot(other.Owner, other.Location, other.ID)
	other.App.Name = packageAccountName(other.Owner, other.Location, other.ID, "app")
	other.Signer.Name = packageAccountName(other.Owner, other.Location, other.ID, "signer")
	if validPackageOwnerCommit(other, commit) {
		t.Fatal("another Remote package accepted the owner binding")
	}
	health.Connection.Location = "local"
	if _, err = packageOwnerCommit(m, health); err == nil {
		t.Fatal("Local health was accepted for the Remote package")
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

func TestLocalPackageJournalRetainsVerifiedWalletClient(t *testing.T) {
	t.Parallel()
	r, m := packageGateInitialRecord(t)
	r.Package.NativeDesktop = &update.Artifact{File: "fased-desktop", Size: 20, SHA256: strings.Repeat("d", 64)}
	r.Package.NativePackage = &update.NativePackage{
		Family: "dnf", Artifact: update.Artifact{File: "fased-app-linux-amd64.rpm", Size: 30, SHA256: strings.Repeat("6", 64)},
		InstallerSHA256: strings.Repeat("7", 64),
	}
	if _, err := ValidateIdlePackageMaintenanceRecord(r, m.Owner, m.Location, m.ID); err != nil {
		t.Fatal("the authenticated Local wallet client was refused by its journal", err)
	}
	if !packagePairMatches(*r.Package, m) {
		t.Fatal("the Local wallet client was refused by the package gate")
	}
	files, err := nativePackageFiles(*r.Package)
	if err != nil || len(files) != 5 {
		t.Fatal("wallet client metadata changed the fixed OS package surface", err)
	}
	if _, ok := files["usr/lib/fased-app/fased-desktop"]; ok {
		t.Fatal("instance-owned wallet client entered the shared OS package")
	}
	remote := m
	remote.Location = "remote"
	if packagePairMatches(*r.Package, remote) {
		t.Fatal("a Local wallet client was accepted for a Remote instance")
	}
	bad := *r.Package
	artifact := *bad.NativeDesktop
	artifact.SHA256 = "invalid"
	bad.NativeDesktop = &artifact
	if packagePairMatches(bad, m) {
		t.Fatal("a wallet client without a valid digest was accepted")
	}
}

func TestLegacyLocalImportBindsIdleJournalWithoutPermittingStart(t *testing.T) {
	t.Parallel()
	r, m := packageGateInitialRecord(t)
	p := r.Package
	p.Version, p.Source, p.ReleaseSHA256 = "0.1.23", maintenance.LegacyLocalRetainedSource, maintenance.LegacyLocalRetainedPacket
	p.Bundle = &update.LinuxBundle{
		Archive:        update.Artifact{File: "fased-app-linux-amd64.tar.gz", Size: 30, SHA256: strings.Repeat("a", 64)},
		ManifestSHA256: maintenance.LegacyLocalRetainedBundle, InstallerSHA256: maintenance.LegacyLocalRetainedInstaller,
	}
	for i := range p.Components {
		p.Components[i].Version = p.Version
	}
	source := PackageLocalAdoption{
		LegacyManifestSHA256: strings.Repeat("1", 64), StateSHA256: strings.Repeat("2", 64),
		InstanceID: base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("s", 32))),
	}
	commit, proof, err := bindLegacyLocalImport(m, *p, r, source)
	if err != nil || commit.InstanceID != source.InstanceID || !validPackageOwnerCommit(m, commit) ||
		proof.Source != source || proof.ApplyAllowed || proof.JournalSHA256 != update.SnapshotDigest("legacy-local-adoption-journal:v1", r) {
		t.Fatal("source owner and state did not bind to the exact held package", err)
	}
	blocked := hostFile{UID: 0, Regular: true, Mode: 0644, Data: source.fence()}
	if err := verifyPackageStartFences(m, r, func(string) (hostFile, error) { return blocked, nil }); err == nil {
		t.Fatal("the ordinary app start gate admitted the held import")
	}
	if nextCommit, nextProof, err := bindLegacyLocalImport(m, *p, r, source); err != nil || nextCommit != commit || nextProof != proof {
		t.Fatal("same import did not yield the original commitment after retry", err)
	}
	pending := r
	pending.Phase = "prepared"
	if _, _, err := bindLegacyLocalImport(m, *p, pending, source); err == nil {
		t.Fatal("existing recovery was accepted as an initial import")
	}
	other := *p
	other.Sequence++
	if _, _, err := bindLegacyLocalImport(m, other, r, source); err == nil {
		t.Fatal("foreign package journal accepted")
	}
}

func TestPackageGateSelectsOnlyBoundStartState(t *testing.T) {
	t.Parallel()
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

func TestPackageOfflineRecoverySelectsOnlyItsRecordedOperation(t *testing.T) {
	t.Parallel()
	r, base := packageGateInitialRecord(t)
	previous := *r.Package
	previous.NativePackage = &update.NativePackage{Family: "dnf", Artifact: update.Artifact{File: "fased-app-linux-amd64.rpm", Size: 1, SHA256: strings.Repeat("1", 64)}, InstallerSHA256: strings.Repeat("2", 64)}
	next := previous
	next.Sequence++
	next.ReleaseSHA256 = strings.Repeat("3", 64)
	next.Components = append([]update.Component(nil), previous.Components...)
	target := base
	target.CoreHash, target.SignerHash = strings.Repeat("4", 64), strings.Repeat("5", 64)
	next.Components[0].Artifact.SHA256, next.Components[1].Artifact.SHA256 = target.CoreHash, target.SignerHash
	r.OperationID, r.Phase, r.Start = strings.Repeat("f", 32), "prepared", "base"
	r.Base, r.Target, r.PreviousPackage, r.Package = mustJSON(base), mustJSON(target), &previous, &next
	if got, err := PackageRecoveryOperation(base, r); err != nil || got != r.OperationID {
		t.Fatal("the original package recovery was refused", got, err)
	}
	if _, err := PackageRecoveryOperation(target, r); err == nil {
		t.Fatal("the target was accepted while the journal still selects the base")
	}
	for name, change := range map[string]func(*update.MaintenanceRecord){
		"idle":                       func(v *update.MaintenanceRecord) { v.Phase = "idle" },
		"other owner":                func(v *update.MaintenanceRecord) { v.Owner++ },
		"bootstrap":                  func(v *update.MaintenanceRecord) { v.Bootstrap = true },
		"no predecessor":             func(v *update.MaintenanceRecord) { v.PreviousPackage = nil },
		"same operation as instance": func(v *update.MaintenanceRecord) { v.OperationID = base.ID },
		"foreign target": func(v *update.MaintenanceRecord) {
			wrong := target
			wrong.Network = "devnet"
			v.Target = mustJSON(wrong)
		},
		"missing hold": func(v *update.MaintenanceRecord) { v.Phase, v.Start = "held", "" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := r
			change(&bad)
			if _, err := PackageRecoveryOperation(base, bad); err == nil {
				t.Fatal("foreign or incomplete package recovery was accepted")
			}
		})
	}
	foreign := base
	foreign.CoreHash = strings.Repeat("6", 64)
	if _, err := PackageRecoveryOperation(foreign, r); err == nil {
		t.Fatal("an unrelated installed pair was accepted")
	}
	// During the protected stop, only the recorded pair and exact fence
	// qualify as a temporary outage. Other refusals remain terminal.
	r.Phase, r.Start = "quiescing", ""
	for _, tc := range []struct {
		name  string
		m     Manifest
		fence []byte
		block bool
		want  bool
	}{
		{"original stopped update", base, update.MaintenanceFence(r.OperationID), false, true},
		{"same installation after manifest swap", target, update.MaintenanceFence(r.OperationID), false, true},
		{"no fence", base, nil, false, false},
		{"foreign fence", base, update.MaintenanceFence(strings.Repeat("a", 32)), false, false},
		{"separate recovery fence", base, update.MaintenanceFence(r.OperationID), true, false},
		{"foreign pair", foreign, update.MaintenanceFence(r.OperationID), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read := func(path string) (hostFile, error) {
				switch {
				case path == base.Root+"/update-active.json" && tc.fence != nil:
					return hostFile{UID: 0, Regular: true, Mode: 0644, Data: tc.fence}, nil
				case path == base.Root+"/update-start-blocked.json" && tc.block:
					return hostFile{UID: 0, Regular: true, Mode: 0644}, nil
				default:
					return hostFile{}, os.ErrNotExist
				}
			}
			if got := packageOwnerUpdateStopped(tc.m, r, read); got != tc.want {
				t.Fatalf("temporary outage = %t, want %t", got, tc.want)
			}
		})
	}

}

func TestPackageGateRequiresOriginalEmptySignerCheckpoint(t *testing.T) {
	t.Parallel()
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
	initial, err := walletInitialRecord(r, base.Owner, base.Location, base.ID)
	if !signer.OrdinaryOwnerAdmissionEnabled() {
		if !errors.Is(err, signer.ErrUnavailable) || r.Phase != "completed" {
			t.Fatal("a disabled owner signer must preserve the completed update", err)
		}
		// A later verified signer may support owner admission even when the
		// original pinned helper was built while admission was disabled.
		supported := r
		pkg := *r.Package
		pkg.Components = append([]update.Component(nil), pkg.Components...)
		for i := range pkg.Components {
			if pkg.Components[i].Contract.Component == "signer" {
				pkg.Components[i].Contract.Features = append(append([]string(nil), pkg.Components[i].Contract.Features...), update.OwnerKeyAdmissionFeature)
			}
		}
		supported.Package = &pkg
		initial, err = walletInitialRecord(supported, base.Owner, base.Location, base.ID)
		if err != nil {
			t.Fatal("a supported recorded signer was refused by the older helper", err)
		}
		if selected, err := ValidateInitialPackageMaintenanceRecord(initial, base.Owner, base.Location, base.ID); err != nil || selected != target || supported.Phase != "completed" {
			t.Fatal("wallet setup did not preserve the supported completed target", err)
		}
	} else {
		if err != nil {
			t.Fatal("a completed empty-signer update blocked wallet setup", err)
		}
		if selected, err := ValidateInitialPackageMaintenanceRecord(initial, base.Owner, base.Location, base.ID); err != nil || selected != target || r.Phase != "completed" {
			t.Fatal("wallet setup did not preserve the completed target", err)
		}
	}
	for _, phase := range []string{"held", "ready", "replacing"} {
		unfinished := r
		unfinished.Phase = phase
		if _, err := walletInitialRecord(unfinished, base.Owner, base.Location, base.ID); err == nil {
			t.Fatal("unfinished update became a wallet setup", phase)
		}
	}
}

func TestPackageStagingRequiresProtectedNativeRecovery(t *testing.T) {
	t.Parallel()
	_, m := packageGateInitialRecord(t)
	p := update.MaintenancePackage{NativePackage: &update.NativePackage{InstallerSHA256: strings.Repeat("a", 64)}}
	if validateNativePackageUpdateScope(m, p, false) == nil {
		t.Fatal("disabled native package update was admitted")
	}
	if err := validateNativePackageUpdateScope(m, p, true); err != nil {
		t.Fatal("empty signer was refused after enabling native recovery", err)
	}
	m.Network = "mainnet-beta"
	if validateNativePackageUpdateScope(m, p, false) == nil {
		t.Fatal("key-bearing package update bypassed the disabled recovery controller")
	}
	if err := validateNativePackageUpdateScope(m, p, true); err != nil {
		t.Fatal("key-bearing package update could not enter the protected signer transaction", err)
	}
	p.NativePackage = nil
	if validateNativePackageUpdateScope(m, p, true) == nil {
		t.Fatal("a package installation admitted a signer update without its OS package")
	}
	legacy := m
	legacy.Layout = ""
	if err := validateNativePackageUpdateScope(legacy, p, false); err != nil {
		t.Fatal("an existing legacy signer recovery was changed", err)
	}
	m.Network = ""
	if validateNativePackageUpdateScope(m, p, true) == nil {
		t.Fatal("empty-signer update without native package was admitted")
	}
}

func TestBundleJournalKeepsTheOriginalPairAndRecoveryIdentity(t *testing.T) {
	t.Parallel()
	r, m := packageGateInitialRecord(t)
	r.Package.Bundle = &update.LinuxBundle{Archive: update.Artifact{File: "fased-app-linux-amd64.tar.gz", Size: 1, SHA256: strings.Repeat("6", 64)}, ManifestSHA256: strings.Repeat("7", 64), InstallerSHA256: strings.Repeat("8", 64)}
	if err := r.Package.Validate(); err != nil {
		t.Fatal("matching bundle pair refused", err)
	}
	if selected, err := ValidateInitialPackageMaintenanceRecord(r, m.Owner, m.Location, m.ID); err != nil || selected != m {
		t.Fatal("initial bundle record lost its original identity", err)
	}
	bad := *r.Package
	bad.Components = append([]update.Component(nil), r.Package.Components...)
	bad.Components[0].Contract.Features = nil
	if err := bad.Validate(); err == nil {
		t.Fatal("a reader without the bundle journal feature was accepted")
	}
	bad = *r.Package
	bad.NativePackage = &update.NativePackage{InstallerSHA256: strings.Repeat("8", 64)}
	if err := bad.Validate(); err == nil || bad.InstallerDigest() != "" {
		t.Fatal("two competing distribution identities were accepted")
	}
	b := nativeBootstrap{Version: 1, Base: m, Package: *r.Package}
	if _, err := retainedNativeBootstrap(hostFile{UID: 0, Regular: true, Mode: 0644, Data: mustJSON(b)}); err != nil {
		t.Fatal("the original bundle bootstrap could not be resumed", err)
	}
	previous := *r.Package
	next := previous
	next.Sequence++
	next.ReleaseSHA256 = strings.Repeat("c", 64)
	next.Components = append([]update.Component(nil), previous.Components...)
	target := m
	target.CoreHash, target.SignerHash = strings.Repeat("a", 64), strings.Repeat("b", 64)
	next.Components[0].Artifact.SHA256, next.Components[1].Artifact.SHA256 = target.CoreHash, target.SignerHash
	r.OperationID, r.Phase, r.Start = strings.Repeat("f", 32), "prepared", "base"
	r.PreviousPackage, r.Package, r.Target = &previous, &next, mustJSON(target)
	if got, err := PackageRecoveryOperation(m, r); err != nil || got != r.OperationID {
		t.Fatal("original bundle recovery was not selected", got, err)
	}
	otherArch := next
	changedBundle := *next.Bundle
	changedBundle.Archive.File = "fased-app-linux-arm64.tar.gz"
	otherArch.Bundle = &changedBundle
	r.Package = &otherArch
	if _, err := PackageRecoveryOperation(m, r); err == nil {
		t.Fatal("bundle recovery crossed architectures")
	}
	r.Package = &next
	r.Target = mustJSON(m)
	if _, err := PackageRecoveryOperation(m, r); err == nil {
		t.Fatal("bundle recovery selected a different target")
	}
}

func TestPackageWalletAdmissionPreservesIdleJournalWhenOwnerSignerHeld(t *testing.T) {
	t.Parallel()
	r, m := packageGateInitialRecord(t)
	old := *r.Package
	old.Components = append([]update.Component(nil), old.Components...)
	for i := range old.Components {
		if old.Components[i].Contract.Component == "signer" {
			old.Components[i].Contract.Features = slices.DeleteFunc(slices.Clone(old.Components[i].Contract.Features), func(f string) bool { return f == update.OwnerKeyAdmissionFeature })
		}
	}
	r.Package = &old
	before := mustJSON(r)
	if err := CheckPackageWalletSigner(m, r.Package); !errors.Is(err, signer.ErrUnavailable) {
		t.Fatal("the disabled owner signer was offered wallet initialization", err)
	}
	if _, err := walletInitialRecord(r, m.Owner, m.Location, m.ID); !errors.Is(err, signer.ErrUnavailable) {
		t.Fatal("the idle journal was admitted with a disabled signer", err)
	}
	if string(mustJSON(r)) != string(before) {
		t.Fatal("the original idle journal changed")
	}
}

func TestPackageWalletAdmissionBindsSignerContract(t *testing.T) {
	t.Parallel()
	r, m := packageGateInitialRecord(t)
	p := *r.Package
	p.Components = append([]update.Component(nil), p.Components...)
	for i := range p.Components {
		if p.Components[i].Contract.Component == "signer" {
			features := slices.DeleteFunc(slices.Clone(p.Components[i].Contract.Features), func(f string) bool { return f == update.OwnerKeyAdmissionFeature })
			p.Components[i].Contract.Features = append(features, update.OwnerKeyAdmissionFeature)
		}
	}
	if err := CheckPackageWalletSigner(m, &p); err != nil {
		t.Fatal("a matching signer advertising owner admission was refused", err)
	}
	wrongHash := m
	wrongHash.SignerHash = strings.Repeat("9", 64)
	if err := CheckPackageWalletSigner(wrongHash, &p); err == nil {
		t.Fatal("a different signer was admitted")
	}
	coreOnly := *r.Package
	coreOnly.Components = append([]update.Component(nil), coreOnly.Components...)
	for i := range coreOnly.Components {
		if coreOnly.Components[i].Contract.Component == "signer" {
			coreOnly.Components[i].Contract.Features = slices.DeleteFunc(slices.Clone(coreOnly.Components[i].Contract.Features), func(f string) bool { return f == update.OwnerKeyAdmissionFeature })
		} else if coreOnly.Components[i].Contract.Component == "core" {
			coreOnly.Components[i].Contract.Features = append(append([]string(nil), coreOnly.Components[i].Contract.Features...), update.OwnerKeyAdmissionFeature)
		}
	}
	if err := CheckPackageWalletSigner(m, &coreOnly); err == nil {
		t.Fatal("the core cannot claim signer admission")
	}
}

func TestPackageGateAdmitsOnlyTheOriginalHeldTarget(t *testing.T) {
	t.Parallel()
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
	snapshot := update.CoreSnapshot{Version: 1, BootID: strings.Repeat("1", 32), ProbeID: strings.Repeat("2", 32), InstanceID: "fixture-instance", State: update.State{Name: "core-db", Version: 1, Anchor: strings.Repeat("3", 64)}}
	approval := update.MaintenanceApproval{Version: 1, Owner: base.Owner, InstallationID: base.ID, OperationID: op, HelperSHA256: strings.Repeat("4", 64), Source: p.Source, ProductVersion: p.Version, ReleaseSHA256: p.ReleaseSHA256, ProvenanceSHA256: p.ProvenanceSHA256, PreviousCore: base.CoreHash, PreviousSigner: base.SignerHash, PreviousManifest: sum(mustJSON(base)), Core: snapshot,
		Plan: update.Manifest{Version: 1, Product: "fased-app-maintenance", Channel: "protected-installer", Sequence: p.Sequence, IssuedAt: 1, ExpiresAt: 2, Target: update.Target{OS: "linux", Arch: "amd64"}, Components: p.Components}}
	if err := approval.Validate(); err != nil {
		t.Fatal("invalid approval fixture", err)
	}
	request := &update.CorePauseRequest{Version: 1, IntentID: op, Sequence: p.Sequence, ManifestSHA256: approval.Digest(), Expected: snapshot}
	index := update.RecoveryIndex{Version: 1, Phase: "metadata-backed-up", Binding: update.RecoveryBinding{Version: 1, Pause: update.CorePauseReceipt{Version: 1, State: "paused", PauseID: strings.Repeat("5", 32), Request: request, Core: snapshot}, Signer: update.SignerSnapshot{Version: 3, ActiveIntentID: op, RecoveryHold: true, SequenceFloor: p.Sequence}}, Snapshot: update.MetadataSnapshot{File: "metadata.sqlite", Size: 16, SHA256: strings.Repeat("6", 64)}}
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
	t.Run("owner polls after fence removal before final journal write", func(t *testing.T) {
		completed := *installation
		completed.Phase, completed.InstalledSequenceCommitted = "completed", true
		current, durable := r, r
		current.Installation = &completed
		released := index.Binding.Pause
		released.State = "released"
		missing := func(string) (hostFile, error) { return hostFile{}, os.ErrNotExist }
		if err := verifyPackageOwnerFences(target, r, missing); err == nil || errors.Is(err, core.ErrCoreUnavailable) {
			t.Fatal("an earlier missing fence must remain fatal", err)
		}
		err := completeVerifiedSignerReceipt(&current, &completed,
			func(next update.MaintenanceRecord) error { durable = next; return nil },
			func(*update.Installation) (update.CorePauseReceipt, error) { return released, nil },
			func() error {
				selected, err := packageStartSelection(durable, base.Owner, base.Location, base.ID, read)
				if err != nil || selected != target || durable.Phase != "signer-completed" {
					t.Fatal("unexpected protected completion phase", err)
				}
				if err := verifyPackageStartFences(target, durable, missing); err == nil {
					t.Fatal("the service-start gate accepted a missing fence")
				}
				for _, path := range []string{"/update-active.json", "/update-start-blocked.json"} {
					foreignFence := func(name string) (hostFile, error) {
						if name == target.Root+path {
							return hostFile{UID: 0, Regular: true, Mode: 0644, Data: update.MaintenanceFence(strings.Repeat("9", 32))}, nil
						}
						return missing(name)
					}
					if err := verifyPackageOwnerFences(target, durable, foreignFence); err == nil || errors.Is(err, core.ErrCoreUnavailable) {
						t.Fatal("a foreign fence became retryable", path, err)
					}
				}
				if err := verifyPackageOwnerFences(target, durable, missing); !errors.Is(err, core.ErrCoreUnavailable) {
					t.Fatal("the owner cannot wait for the original final journal write", err)
				}
				return nil
			})
		if err != nil || durable.OperationID != op || durable.Phase != "completed" {
			t.Fatal("the same operation did not complete", err)
		}
		if err = verifyPackageOwnerFences(target, durable, missing); err != nil {
			t.Fatal("the completed operation is unreadable", err)
		}
	})
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
	t.Parallel()
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
	t.Parallel()
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

func TestKnownUnitDefinitionAcceptsHardenedAndLegacyPackageUnits(t *testing.T) {
	t.Parallel()
	_, m := packageGateInitialRecord(t)
	hardened := definitions(m)
	legacy := legacyPackageDefinitions(m)
	for _, role := range []string{"app", "signer"} {
		name := "fased-" + role + ".service"
		if !knownUnitDefinition(hostFile{UID: 0, Regular: true, Mode: 0644, Data: hardened[name]}, m, role) {
			t.Fatalf("hardened %s unit was rejected", name)
		}
		if !knownUnitDefinition(hostFile{UID: 0, Regular: true, Mode: 0644, Data: legacy[name]}, m, role) {
			t.Fatalf("legacy %s unit was rejected", name)
		}
		if knownUnitDefinition(hostFile{UID: 0, Regular: true, Mode: 0644, Data: append([]byte("# mutated\n"), hardened[name]...)}, m, role) {
			t.Fatalf("mutated %s unit was accepted", name)
		}
	}
}
