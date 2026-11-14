//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

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
