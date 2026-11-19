//go:build linux && amd64 && fased_automatic_predecessor && !custody_acceptance_fixture && !fased_realhost_fixture

package custodyinstall

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestControlledAutomaticRecoveryBindsTheOriginalRecordAndPolicy(t *testing.T) {
	machine, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		t.Fatal(err)
	}
	instance, op := strings.Repeat("a", 32), strings.Repeat("b", 32)
	owner, machinePin, instancePin := hostplatform.AutomaticPredecessorOwnerUID, hostplatform.AutomaticPredecessorMachineSHA256, hostplatform.AutomaticPredecessorInstance
	t.Cleanup(func() {
		hostplatform.AutomaticPredecessorOwnerUID, hostplatform.AutomaticPredecessorMachineSHA256, hostplatform.AutomaticPredecessorInstance = owner, machinePin, instancePin
	})
	hostplatform.AutomaticPredecessorOwnerUID = "1400"
	hostplatform.AutomaticPredecessorMachineSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(string(machine)))))
	hostplatform.AutomaticPredecessorInstance = instance
	base := Manifest{Layout: PackageLayout, Owner: 1400, ID: instance, Location: "local", CoreHash: strings.Repeat("c", 64)}
	release := core.ApplicationRelease{EngineSHA256: strings.Repeat("d", 64), Version: "0.1.23", Sequence: 1000024, ManifestSHA256: automaticPredecessorTarget, Target: update.Target{OS: "linux", Arch: "amd64"}}
	policy := core.UpdatePolicy{Mode: "automatic", Pin: &core.UpdatePin{Sequence: "1000024", ManifestSHA256: automaticPredecessorTarget}}
	view := core.ApplicationOwnerView{RunningSHA256: base.CoreHash, View: core.ApplicationUpdateView{Policy: policy, Operation: core.ApplicationUpdateOperation{ID: op, Phase: "applying", Release: &release, PolicySHA256: update.SnapshotDigest("application-update-policy", policy), Automatic: true}}}
	previous := &update.MaintenancePackage{Version: "0.1.22", Sequence: 1000023, ReleaseSHA256: strings.Repeat("e", 64), Bundle: &update.LinuxBundle{}}
	r := update.MaintenanceRecord{Owner: base.Owner, OperationID: op, Phase: "prepared", PreviousPackage: previous, Package: &update.MaintenancePackage{
		Version: release.Version, Sequence: release.Sequence, ReleaseSHA256: release.ManifestSHA256,
		Components: []update.Component{{Contract: core.OrdinaryUpdateContract(), Artifact: update.Artifact{SHA256: release.EngineSHA256}}},
	}}
	if err := matchEmptyPackageOperation(view, r, base, nil); err == nil {
		t.Fatal("Automatic was accepted without the protected admission")
	}
	initial, err := controlledAutomaticAdmission(base, previous, view)
	if err != nil {
		t.Fatal(err)
	}
	var recovered automaticPredecessorAdmission
	if err := update.StrictJSON(mustJSON(initial), &recovered); err != nil {
		t.Fatal(err)
	}
	view.View.Operation.Phase = "recovery"
	view.View.Operation.OwnerLease = op
	if !recovered.matchesRecord(base, r) || matchEmptyPackageOperation(view, r, base, &recovered) != nil {
		t.Fatal("the original operation could not resume from protected admission")
	}
	for _, phase := range []string{"held", "replacing", "ready", "completed"} {
		r.Phase = phase
		if !recovered.matchesRecord(base, r) || !recovered.matchesView(view) {
			t.Fatalf("existing Automatic authority did not survive phase %s", phase)
		}
	}
	foreign := r
	foreign.OperationID = strings.Repeat("f", 32)
	if recovered.matchesRecord(base, foreign) {
		t.Fatal("the protected admission accepted a second operation")
	}
	changedBase := *previous
	changedBase.ReleaseSHA256 = strings.Repeat("8", 64)
	r.PreviousPackage = &changedBase
	if recovered.matchesRecord(base, r) {
		t.Fatal("a different predecessor packet inherited the admission")
	}
	r.PreviousPackage = previous
	changedPolicy := view
	changedPolicy.View.Policy.Notify = true
	if recovered.matchesView(changedPolicy) {
		t.Fatal("changed policy inherited the original admission")
	}
	view.View.Operation.Automatic = false
	if recovered.matchesView(view) {
		t.Fatal("an Automatic operation was silently relabeled as manual")
	}
	hostplatform.AutomaticPredecessorMachineSHA256 = strings.Repeat("1", 64)
	if recovered.matchesRecord(base, r) {
		t.Fatal("a different machine inherited test-only authority")
	}
}
