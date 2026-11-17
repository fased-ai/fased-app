//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestInitialPackageRecoveryRequiresStoppedEmptySigner(t *testing.T) {
	_, m := packageGateInitialRecord(t)
	dir := filepath.Join(t.TempDir(), "signer-state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	check := func(path string, uid, gid int) error {
		if path != m.Root+"/signer-state" || uid != m.Signer.UID || gid != m.Signer.GID {
			return errors.New("foreign signer target")
		}
		return emptySignerDirectory(dir, os.Getuid(), os.Getgid())
	}
	ctl := func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) != 3 || args[0] != "show" || args[1] != instanceUnit(m, "signer") || args[2] != "--property=ActiveState,MainPID,ControlPID" {
			return nil, errors.New("foreign service")
		}
		return []byte("ActiveState=inactive\nMainPID=0\nControlPID=0\n"), nil
	}
	if err := verifyUnconfiguredPackageSigner(t.Context(), m, ctl, check); err != nil {
		t.Fatal("empty stopped signer refused", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := verifyUnconfiguredPackageSigner(cancelled, m, ctl, check); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled recovery continued: %v", err)
	}
	for _, value := range []string{
		"ActiveState=active\nMainPID=1\nControlPID=0\n",
		"ActiveState=inactive\nMainPID=1\nControlPID=0\n",
		"ActiveState=inactive\nMainPID=0\nControlPID=2\n",
		"ActiveState=inactive\nMainPID=0\n",
		"ActiveState=inactive\nMainPID=0\nControlPID=0\nMainPID=1\n",
	} {
		bad := func(context.Context, ...string) ([]byte, error) { return []byte(value), nil }
		if err := verifyUnconfiguredPackageSigner(t.Context(), m, bad, check); err == nil {
			t.Fatal("active, ambiguous or incomplete signer state was accepted", value)
		}
	}
	key := filepath.Join(dir, "preserve")
	if err := os.WriteFile(key, []byte("existing signer state"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyUnconfiguredPackageSigner(t.Context(), m, ctl, check); err == nil {
		t.Fatal("nonempty signer was accepted as unconfigured")
	}
	if got, err := os.ReadFile(key); err != nil || string(got) != "existing signer state" {
		t.Fatal("signer state was changed", err)
	}
	foreign := m
	foreign.Network = "devnet"
	if err := verifyUnconfiguredPackageSigner(t.Context(), foreign, ctl, check); err == nil {
		t.Fatal("configured signer used the empty-signer path")
	}
}

func TestEmptySignerDirectoryRejectsSymlinkAndWrongIdentity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := emptySignerDirectory(dir, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	if err := emptySignerDirectory(dir, os.Getuid()+1, os.Getgid()); err == nil {
		t.Fatal("foreign signer directory was accepted")
	}
	link := filepath.Join(filepath.Dir(dir), "linked")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := emptySignerDirectory(link, os.Getuid(), os.Getgid()); err == nil {
		t.Fatal("symlinked signer directory was accepted")
	}
}

func TestInitialServiceRecoveryRefusesRunningProcesses(t *testing.T) {
	for _, tc := range []struct {
		name, state, main, control string
		wantOK, wantReset          bool
	}{
		{"inactive", "inactive", "0", "0", true, false},
		{"failed without processes", "failed", "0", "0", true, true},
		{"failed with main process", "failed", "123", "0", false, false},
		{"failed with control process", "failed", "0", "456", false, false},
		{"active", "active", "123", "0", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, reset := tc.state, false
			ctl := func(_ context.Context, args ...string) ([]byte, error) {
				if len(args) == 3 && args[0] == "stop" {
					return nil, nil
				}
				if len(args) == 3 && args[0] == "reset-failed" {
					reset, state = true, "inactive"
					return nil, nil
				}
				if len(args) != 5 || args[0] != "show" {
					return nil, errors.New("unexpected command")
				}
				switch args[1] {
				case "--property=ActiveState":
					return []byte(state), nil
				case "--property=MainPID":
					return []byte(tc.main), nil
				case "--property=ControlPID":
					return []byte(tc.control), nil
				default:
					return nil, errors.New("unexpected property")
				}
			}
			err := ensureInitiallyStopped(t.Context(), "fixture.service", ctl)
			if (err == nil) != tc.wantOK || reset != tc.wantReset {
				t.Fatalf("state %s: reset=%v err=%v", tc.state, reset, err)
			}
		})
	}
}

func TestRefusedUpdateRequiresFreshUnheldCore(t *testing.T) {
	probe := strings.Repeat("1", 32)
	snapshot := update.CoreSnapshot{Version: 1, ProbeID: probe, BootID: strings.Repeat("2", 32), InstanceID: "fixture-instance", State: update.State{Name: "core-db", Version: 1, Anchor: strings.Repeat("a", 64)}}
	request := &update.CorePauseRequest{Version: 1, IntentID: strings.Repeat("3", 32), Sequence: 2, ManifestSHA256: strings.Repeat("b", 64), Expected: snapshot}
	idle := update.CorePauseReceipt{Version: 1, State: "idle", Core: snapshot}
	released := update.CorePauseReceipt{Version: 1, State: "released", PauseID: strings.Repeat("4", 32), Request: request, Core: snapshot}
	for _, tc := range []struct {
		name string
		p    update.CorePauseReceipt
		want bool
	}{
		{"idle", idle, true},
		{"released", released, true},
		{"still paused", func() update.CorePauseReceipt { p := released; p.State = "paused"; return p }(), false},
		{"stale probe", func() update.CorePauseReceipt { p := idle; p.Core.ProbeID = strings.Repeat("5", 32); return p }(), false},
		{"invalid boot", func() update.CorePauseReceipt { p := idle; p.Core.BootID = ""; return p }(), false},
		{"damaged release", func() update.CorePauseReceipt { p := released; p.Request = nil; return p }(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := unheldCorePause(tc.p, probe); got != tc.want {
				t.Fatalf("unheld=%t, want %t", got, tc.want)
			}
		})
	}
}

func initialMaintenanceFixture() update.MaintenanceRecord {
	m := fixtureManifest()
	m.Version, m.Mode, m.Profile = 3, signer.OrdinaryCustodyMode, signer.OwnerCustodyProfile
	m.Provenance, m.Network, m.Enrollment, m.Location = signer.ControlledCustodyProfile, "devnet", true, "local"
	p := &update.MaintenancePackage{
		Version: "0.1.0", Source: strings.Repeat("a", 40), Sequence: 7,
		ReleaseSHA256: strings.Repeat("b", 64), DevelopmentOnly: true,
		Components: []update.Component{
			{Version: "0.1.0", Contract: core.OrdinaryUpdateContract(), Artifact: update.Artifact{File: "fased-app", Size: 1, SHA256: m.CoreHash}},
			{Version: "0.1.0", Contract: signer.OrdinaryUpdateContract(), Artifact: update.Artifact{File: "fased-signer", Size: 1, SHA256: m.SignerHash}},
		},
	}
	return update.MaintenanceRecord{Version: 1, Owner: m.Owner, OperationID: m.ID, Phase: "idle", Start: "base", Base: mustJSON(m), Target: mustJSON(m), Package: p}
}

func TestInitialMaintenanceRecordRefusesForeignOrPendingState(t *testing.T) {
	if _, err := ValidateInitialMaintenanceRecord(initialMaintenanceFixture()); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*update.MaintenanceRecord){
		"foreign operation": func(r *update.MaintenanceRecord) { r.OperationID = strings.Repeat("e", 32) },
		"pending update":    func(r *update.MaintenanceRecord) { r.Phase = "prepared" },
		"target selected":   func(r *update.MaintenanceRecord) { r.Start = "target" },
		"approval retained": func(r *update.MaintenanceRecord) { r.Approval = &update.MaintenanceApproval{} },
		"signer hold":       func(r *update.MaintenanceRecord) { r.Installation = &update.Installation{} },
		"previous package":  func(r *update.MaintenanceRecord) { r.PreviousPackage = r.Package },
		"missing signer":    func(r *update.MaintenanceRecord) { r.Package.Components = r.Package.Components[:1] },
		"changed signer":    func(r *update.MaintenanceRecord) { r.Package.Components[1].Artifact.SHA256 = strings.Repeat("d", 64) },
		"old reader": func(r *update.MaintenanceRecord) {
			r.Package.Components[1].Contract.Features = []string{update.OrdinaryMaintenanceFeature}
		},
		"unsigned identity": func(r *update.MaintenanceRecord) { r.Package.ReleaseSHA256 = "not-a-hash" },
		"different target":  func(r *update.MaintenanceRecord) { r.Target = []byte(`{}`) },
	} {
		t.Run(name, func(t *testing.T) {
			r := initialMaintenanceFixture()
			change(&r)
			if _, err := ValidateInitialMaintenanceRecord(r); err == nil {
				t.Fatal("unsafe initial maintenance record accepted")
			}
		})
	}
}

func TestInitialPackageJournalBindsInstanceAndPair(t *testing.T) {
	makeRecord := func() update.MaintenanceRecord {
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
		return r
	}
	r := makeRecord()
	var m Manifest
	if err := strict(r.Base, &m); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateInitialPackageMaintenanceRecord(r, m.Owner, m.Location, m.ID); err != nil {
		t.Fatal("exact package journal refused", err)
	}
	if _, err := ValidateInitialMaintenanceRecord(r); err == nil {
		t.Fatal("legacy owner-wide controller accepted a package journal")
	}
	for name, change := range map[string]func(*update.MaintenanceRecord){
		"operation": func(v *update.MaintenanceRecord) { v.OperationID = strings.Repeat("d", 32) },
		"held":      func(v *update.MaintenanceRecord) { v.Phase = "held"; v.Installation = &update.Installation{} },
		"configured signer": func(v *update.MaintenanceRecord) {
			var configured Manifest
			if err := strict(v.Base, &configured); err != nil {
				t.Fatal(err)
			}
			configured.Network = "devnet"
			v.Base, v.Target = mustJSON(configured), mustJSON(configured)
		},
		"target":  func(v *update.MaintenanceRecord) { v.Target = []byte(`{}`) },
		"signer":  func(v *update.MaintenanceRecord) { v.Package.Components[1].Artifact.SHA256 = strings.Repeat("d", 64) },
		"fixture": func(v *update.MaintenanceRecord) { v.Package.DevelopmentOnly = true },
	} {
		t.Run(name, func(t *testing.T) {
			bad := makeRecord()
			change(&bad)
			if _, err := ValidateInitialPackageMaintenanceRecord(bad, m.Owner, m.Location, m.ID); err == nil {
				t.Fatal("foreign or held initial journal accepted")
			}
		})
	}
	if _, err := ValidateInitialPackageMaintenanceRecord(r, m.Owner, "remote", m.ID); err == nil {
		t.Fatal("another profile accepted")
	}
}

func TestInitialMaintenanceRecordBindsProtectedDesktop(t *testing.T) {
	r := initialMaintenanceFixture()
	var m Manifest
	if err := strict(r.Base, &m); err != nil {
		t.Fatal(err)
	}
	m.NativeOwnerAccess = true
	r.Base, r.Target = mustJSON(m), mustJSON(m)
	if _, err := ValidateInitialMaintenanceRecord(r); err != nil {
		t.Fatal("legacy Local journal was not preserved", err)
	}
	r.Package.NativeDesktop = &update.Artifact{File: "fased-desktop", SHA256: strings.Repeat("f", 64), Size: 100}
	if _, err := ValidateInitialMaintenanceRecord(r); err != nil {
		t.Fatal("matching protected desktop was refused", err)
	}
	features := r.Package.Components[1].Contract.Features
	for i, feature := range features {
		if feature == update.NativeDesktopJournalFeature {
			r.Package.Components[1].Contract.Features = append(features[:i:i], features[i+1:]...)
			break
		}
	}
	if _, err := ValidateInitialMaintenanceRecord(r); err == nil {
		t.Fatal("an older signer that cannot read the desktop record was accepted")
	}
	r.Package.Components[1].Contract = signer.OrdinaryUpdateContract()
	r.Package.NativeDesktop.File = "foreign-desktop"
	if _, err := ValidateInitialMaintenanceRecord(r); err == nil {
		t.Fatal("foreign desktop filename was accepted")
	}
}

func TestProtectedWorkerRefusesIdleInstallation(t *testing.T) {
	r := initialMaintenanceFixture()
	if err := validateProtectedUpdateRecord(r, r.OperationID); err == nil {
		t.Fatal("initial idle journal was accepted as an update transaction")
	}
	r.Phase = "prepared"
	if err := validateProtectedUpdateRecord(r, r.OperationID); err != nil {
		t.Fatalf("matching prepared update refused: %v", err)
	}
	if err := validateProtectedUpdateRecord(r, strings.Repeat("e", 32)); err == nil {
		t.Fatal("worker accepted another operation")
	}
	r.Package = nil
	if err := validateProtectedUpdateRecord(r, r.OperationID); err == nil {
		t.Fatal("worker accepted a missing package")
	}
}

func TestInitialMaintenancePreservesInstalledReleaseDuringRecovery(t *testing.T) {
	r := initialMaintenanceFixture()
	base := *r.Package
	target := base
	target.Sequence++
	target.ReleaseSHA256 = strings.Repeat("f", 64)
	r.PreviousPackage, r.Package, r.Phase = &base, &target, "prepared"
	installed, err := r.InstalledPackage()
	if err != nil || installed.Sequence != base.Sequence {
		t.Fatal("pending update lost the installed release floor", err)
	}
	r.Phase, r.Start = "completed", "target"
	installed, err = r.InstalledPackage()
	if err != nil || installed.Sequence != target.Sequence {
		t.Fatal("completed target did not become the installed release", err)
	}
	r.Phase, r.Start = "refused", "base"
	installed, err = r.InstalledPackage()
	if err != nil || installed.Sequence != base.Sequence {
		t.Fatal("base recovery lost its original release floor", err)
	}
	r.Package.Sequence = base.Sequence
	if _, err = r.InstalledPackage(); err == nil {
		t.Fatal("non-advancing release transition was accepted")
	}
}
