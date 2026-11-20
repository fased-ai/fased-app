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
	"github.com/fased-ai/fased-app/internal/privateaccess"
	"github.com/fased-ai/fased-app/internal/tailnet"
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

	hostplatform.AutomaticPredecessorMachineSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(string(machine)))))
	origin := "https://fixture.tail-test.ts.net"
	device := tailnet.Identity{UserID: 7, NodeID: "approved-device", Login: "fixture@example.test"}
	remote := base
	remote.Location, remote.PublicOrigin, remote.Listen = "remote", origin, "127.0.0.1:17482"
	view.View.Operation.Automatic = true
	if ControlledAutomaticOperation(remote, previous, view.View.Operation) {
		t.Fatal("Local fixture admitted a Remote")
	}
	t.Cleanup(func() {
		hostplatform.AutomaticPredecessorRemoteOrigin = ""
		hostplatform.AutomaticPredecessorRemoteDeviceSHA256 = ""
	})
	hostplatform.AutomaticPredecessorRemoteOrigin = origin
	hostplatform.AutomaticPredecessorRemoteDeviceSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte("7\x00approved-device")))
	approval := privateaccess.ProtectedInitialApproval{Version: 1, Owner: remote.Owner, Instance: remote.ID, Origin: origin, Listen: remote.Listen,
		Sequence: previous.Sequence, ReleaseSHA256: previous.ReleaseSHA256, CoreSHA256: remote.CoreHash, Device: device}
	remoteAdmission, err := controlledAutomaticAdmission(remote, previous, view)
	if err != nil || !matchesAutomaticRemoteApproval(remote, previous, approval) || !remoteAdmission.matchesRecord(remote, r) {
		t.Fatal("exact Remote fixture failed admission or recovery", err)
	}
	for _, mutate := range []func(*privateaccess.ProtectedInitialApproval){
		func(a *privateaccess.ProtectedInitialApproval) { a.Device.NodeID = "other" },
		func(a *privateaccess.ProtectedInitialApproval) { a.Device.UserID = 8 },
		func(a *privateaccess.ProtectedInitialApproval) { a.Origin = "https://other.tail-test.ts.net" },
		func(a *privateaccess.ProtectedInitialApproval) { a.Instance = strings.Repeat("f", 32) },
		func(a *privateaccess.ProtectedInitialApproval) { a.CoreSHA256 = strings.Repeat("f", 64) },
		func(a *privateaccess.ProtectedInitialApproval) { a.ReleaseSHA256 = strings.Repeat("f", 64) },
	} {
		changed := approval
		mutate(&changed)
		if matchesAutomaticRemoteApproval(remote, previous, changed) {
			t.Fatal("different protected Remote approval inherited Automatic")
		}
	}
	otherOrigin := remote
	otherOrigin.PublicOrigin = "https://other.tail-test.ts.net"
	otherPacket := view.View.Operation
	wrongRelease := release
	wrongRelease.ManifestSHA256 = strings.Repeat("f", 64)
	otherPacket.Release = &wrongRelease
	if ControlledAutomaticOperation(base, previous, view.View.Operation) ||
		ControlledAutomaticOperation(otherOrigin, previous, view.View.Operation) ||
		ControlledAutomaticOperation(remote, previous, otherPacket) {
		t.Fatal("Remote pin admitted Local, a foreign origin or a different update target")
	}
}
