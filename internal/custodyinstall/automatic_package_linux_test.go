//go:build linux

package custodyinstall

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/privateaccess"
	"github.com/fased-ai/fased-app/internal/tailnet"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestAutomaticPackageAdmissionAndRecovery(t *testing.T) {
	t.Parallel()
	instance, op := strings.Repeat("a", 32), strings.Repeat("b", 32)
	base := Manifest{Layout: PackageLayout, Owner: 1400, ID: instance, Location: "local", CoreHash: strings.Repeat("c", 64)}
	target := update.Target{OS: "linux", Arch: runtime.GOARCH}

	// Sequential version bumps (0.1.24 -> 0.1.25 -> 0.1.26) work for both Local
	// and Remote without hardcoded versions, digests, or machine IDs.
	for _, tc := range []struct {
		name     string
		location string
		baseVer  string
		baseSeq  uint64
		nextVer  string
		nextSeq  uint64
	}{
		{"local 0.1.24->0.1.25", "local", "0.1.24", 1000025, "0.1.25", 1000026},
		{"local 0.1.25->0.1.26", "local", "0.1.25", 1000026, "0.1.26", 1000027},
		{"remote 0.1.24->0.1.25", "remote", "0.1.24", 1000025, "0.1.25", 1000026},
		{"remote 0.1.25->0.1.26", "remote", "0.1.25", 1000026, "0.1.26", 1000027},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			m.Location = tc.location
			if m.Location == "remote" {
				m.PublicOrigin = "https://owner.tail-test.ts.net"
				m.Listen = "127.0.0.1:7331"
			}
			previous := &update.MaintenancePackage{Version: tc.baseVer, Sequence: tc.baseSeq, ReleaseSHA256: strings.Repeat("e", 64), Bundle: &update.LinuxBundle{}}
			release := core.ApplicationRelease{EngineSHA256: strings.Repeat("d", 64), Version: tc.nextVer, Sequence: tc.nextSeq, ManifestSHA256: strings.Repeat("9", 64), Target: target}
			policy := core.UpdatePolicy{Mode: "automatic", Pin: &core.UpdatePin{Sequence: "1000026", ManifestSHA256: release.ManifestSHA256}}
			if tc.nextSeq != 1000026 {
				policy.Pin = nil
			}
			view := core.ApplicationOwnerView{
				RunningSHA256: m.CoreHash,
				View: core.ApplicationUpdateView{
					Policy: policy,
					Operation: core.ApplicationUpdateOperation{
						ID:           op,
						Phase:        "applying",
						Release:      &release,
						PolicySHA256: update.SnapshotDigest("application-update-policy", policy),
						Automatic:    true,
					},
				},
			}
			r := update.MaintenanceRecord{
				Owner:           m.Owner,
				OperationID:     op,
				Phase:           "prepared",
				PreviousPackage: previous,
				Package: &update.MaintenancePackage{
					Version:       release.Version,
					Sequence:      release.Sequence,
					ReleaseSHA256: release.ManifestSHA256,
					Components:    []update.Component{{Contract: core.OrdinaryUpdateContract(), Artifact: update.Artifact{SHA256: release.EngineSHA256}}},
				},
			}
			if err := matchEmptyPackageOperation(view, r, m, nil); err == nil {
				t.Fatal("Automatic was accepted without protected admission")
			}
			initial, err := packageAutomaticAdmissionFor(m, previous, view)
			if err != nil {
				t.Fatalf("packageAutomaticAdmissionFor failed: %v", err)
			}
			var recovered packageAutomaticAdmission
			if err := update.StrictJSON(mustJSON(initial), &recovered); err != nil {
				t.Fatal(err)
			}
			view.View.Operation.Phase = "recovery"
			view.View.Operation.OwnerLease = op
			if !recovered.matchesRecord(m, r) || matchEmptyPackageOperation(view, r, m, &recovered) != nil {
				t.Fatal("original Automatic operation failed recovery match")
			}
			for _, phase := range []string{"held", "replacing", "ready", "completed"} {
				r.Phase = phase
				if !recovered.matchesRecord(m, r) || !recovered.matchesView(view) {
					t.Fatalf("Automatic admission failed at phase %s", phase)
				}
			}
		})
	}

	previous := &update.MaintenancePackage{Version: "0.1.24", Sequence: 1000025, ReleaseSHA256: strings.Repeat("e", 64), NativePackage: &update.NativePackage{Family: "apt"}}
	release := core.ApplicationRelease{EngineSHA256: strings.Repeat("d", 64), Version: "0.1.25", Sequence: 1000026, ManifestSHA256: strings.Repeat("9", 64), Target: target}
	policy := core.UpdatePolicy{Mode: "automatic"}
	view := core.ApplicationOwnerView{
		RunningSHA256: base.CoreHash,
		View: core.ApplicationUpdateView{
			Policy: policy,
			Operation: core.ApplicationUpdateOperation{
				ID:           op,
				Phase:        "applying",
				Release:      &release,
				PolicySHA256: update.SnapshotDigest("application-update-policy", policy),
				Automatic:    true,
			},
		},
	}
	admission, err := packageAutomaticAdmissionFor(base, previous, view)
	if err != nil {
		t.Fatalf("native package base refused: %v", err)
	}
	r := update.MaintenanceRecord{
		Owner:           base.Owner,
		OperationID:     op,
		Phase:           "prepared",
		PreviousPackage: previous,
		Package: &update.MaintenancePackage{
			Version:       release.Version,
			Sequence:      release.Sequence,
			ReleaseSHA256: release.ManifestSHA256,
			Components:    []update.Component{{Contract: core.OrdinaryUpdateContract(), Artifact: update.Artifact{SHA256: release.EngineSHA256}}},
		},
	}

	// Safety boundaries: configured wallets, non-advancing sequences, pin
	// mismatches, policy mutations, and manual relabeling must all fail closed.
	walletBase := base
	walletBase.Network = "devnet"
	if AutomaticPackageOperation(walletBase, previous, view.View.Operation) {
		t.Fatal("wallet-configured package admitted Automatic")
	}
	devPrevious := *previous
	devPrevious.DevelopmentOnly = true
	if AutomaticPackageOperation(base, &devPrevious, view.View.Operation) {
		t.Fatal("development package admitted Automatic")
	}
	sameSeq := release
	sameSeq.Sequence = previous.Sequence
	sameOp := view.View.Operation
	sameOp.Release = &sameSeq
	if AutomaticPackageOperation(base, previous, sameOp) {
		t.Fatal("non-advancing sequence admitted Automatic")
	}
	wrongPin := view
	wrongPin.View.Policy.Pin = &core.UpdatePin{Sequence: "1000099", ManifestSHA256: release.ManifestSHA256}
	wrongPin.View.Operation.PolicySHA256 = update.SnapshotDigest("application-update-policy", wrongPin.View.Policy)
	if _, err := packageAutomaticAdmissionFor(base, previous, wrongPin); err == nil {
		t.Fatal("mismatched pin admitted Automatic")
	}
	changedPolicy := view
	changedPolicy.View.Policy.Notify = true
	if admission.matchesView(changedPolicy) {
		t.Fatal("mutated policy matched existing Automatic admission")
	}
	manualMode := view
	manualMode.View.Policy.Mode = "manual"
	if _, err := packageAutomaticAdmissionFor(base, previous, manualMode); err == nil {
		t.Fatal("manual policy mode admitted Automatic")
	}
	relabelled := view
	relabelled.View.Operation.Automatic = false
	if admission.matchesView(relabelled) {
		t.Fatal("Automatic admission accepted relabelled manual operation")
	}
	foreignOp := r
	foreignOp.OperationID = strings.Repeat("f", 32)
	if admission.matchesRecord(base, foreignOp) {
		t.Fatal("Automatic admission accepted another operation ID")
	}

	// Remote approval persists across upgrades even when initial-approval.json
	// retains the original fresh install Sequence and CoreHash.
	remote := base
	remote.Location = "remote"
	remote.PublicOrigin = "https://owner.tail-test.ts.net"
	remote.Listen = "127.0.0.1:7331"
	approval := privateaccess.ProtectedInitialApproval{
		Version:       1,
		Owner:         remote.Owner,
		Instance:      remote.ID,
		Origin:        remote.PublicOrigin,
		Listen:        remote.Listen,
		Sequence:      1000024, // Older install sequence must not block 1000025->1000026.
		ReleaseSHA256: strings.Repeat("1", 64),
		CoreSHA256:    strings.Repeat("2", 64),
		Device:        tailnet.Identity{UserID: 7, NodeID: "approved-node", Login: "owner@example.test"},
	}
	if err := verifyPackageAutomaticRemoteApprovalWith(remote, func(int, string) (privateaccess.ProtectedInitialApproval, error) {
		return approval, nil
	}); err != nil {
		t.Fatalf("valid Remote approval refused across upgrades: %v", err)
	}
	if err := verifyPackageAutomaticRemoteApprovalWith(remote, func(int, string) (privateaccess.ProtectedInitialApproval, error) {
		return privateaccess.ProtectedInitialApproval{}, errors.New("missing")
	}); err == nil {
		t.Fatal("missing Remote approval was accepted")
	}
	otherOrigin := approval
	otherOrigin.Origin = "https://other.tail-test.ts.net"
	if err := verifyPackageAutomaticRemoteApprovalWith(remote, func(int, string) (privateaccess.ProtectedInitialApproval, error) {
		return otherOrigin, nil
	}); err == nil {
		t.Fatal("foreign Remote origin was accepted")
	}
}
