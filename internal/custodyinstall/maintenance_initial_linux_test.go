//go:build linux

package custodyinstall

import (
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

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
