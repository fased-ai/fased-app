//go:build linux && amd64 && fased_automatic_predecessor && !custody_acceptance_fixture && !fased_realhost_fixture

package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestControlledAutomaticWorkerRequiresThePinnedReceiverAndPacket(t *testing.T) {
	const owner = 1400
	const packet = "f009a6ad164403688e062e384f23c4ead7d994f1f94d339b8fdf4ee82ffb4725"
	instance, operation := strings.Repeat("1", 32), strings.Repeat("2", 32)
	machine, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		t.Fatal(err)
	}
	oldOwner := hostplatform.AutomaticPredecessorOwnerUID
	oldMachine := hostplatform.AutomaticPredecessorMachineSHA256
	oldInstance := hostplatform.AutomaticPredecessorInstance
	t.Cleanup(func() {
		hostplatform.AutomaticPredecessorOwnerUID = oldOwner
		hostplatform.AutomaticPredecessorMachineSHA256 = oldMachine
		hostplatform.AutomaticPredecessorInstance = oldInstance
	})
	hostplatform.AutomaticPredecessorOwnerUID = "1400"
	hostplatform.AutomaticPredecessorMachineSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(string(machine)))))
	hostplatform.AutomaticPredecessorInstance = instance
	manifest := custodyinstall.Manifest{Layout: custodyinstall.PackageLayout, Owner: owner, ID: instance, Location: "local"}
	installed := update.MaintenancePackage{Version: "0.1.22", Sequence: 1000023, ReleaseSHA256: strings.Repeat("a", 64), Bundle: &update.LinuxBundle{}}
	release := core.ApplicationRelease{EngineSHA256: strings.Repeat("b", 64), Version: "0.1.23", Sequence: 1000024, ManifestSHA256: packet, Target: update.Target{OS: "linux", Arch: "amd64"}}
	policy := core.UpdatePolicy{Mode: "automatic", Pin: &core.UpdatePin{Sequence: "1000024", ManifestSHA256: packet}}
	view := core.ApplicationOwnerView{View: core.ApplicationUpdateView{Policy: policy, Operation: core.ApplicationUpdateOperation{ID: operation, Phase: "applying", Release: &release, PolicySHA256: update.SnapshotDigest("application-update-policy", policy), Automatic: true}}}
	candidate := maintenance.CandidateReceipt{Version: 1, Owner: owner, Scope: "update", PacketSHA256: packet, InstallerSHA256: strings.Repeat("d", 64), EngineSHA256: release.EngineSHA256, ProductVersion: release.Version, Sequence: release.Sequence, ReleaseSHA256: packet, Target: release.Target, BaseSequence: installed.Sequence, BaseReleaseSHA256: installed.ReleaseSHA256, BaseInstallation: instance, BaseManifestSHA: update.SnapshotDigest("candidate-base-manifest:v1", manifest), ExpiresAt: 200}
	now := time.Unix(100, 0)
	if q, err := approvedPackageCandidate(view, operation, installed, manifest, candidate, nil, now); err != nil || q.OperationID != operation {
		t.Fatal("the scoped Automatic release was refused", err)
	}
	for name, change := range map[string]func(*custodyinstall.Manifest, *maintenance.CandidateReceipt){
		"Remote":      func(m *custodyinstall.Manifest, _ *maintenance.CandidateReceipt) { m.Location = "remote" },
		"other owner": func(m *custodyinstall.Manifest, _ *maintenance.CandidateReceipt) { m.Owner++ },
		"other packet": func(_ *custodyinstall.Manifest, c *maintenance.CandidateReceipt) {
			c.PacketSHA256 = strings.Repeat("c", 64)
		},
		"expired": func(_ *custodyinstall.Manifest, c *maintenance.CandidateReceipt) { c.ExpiresAt = now.Unix() },
	} {
		t.Run(name, func(t *testing.T) {
			m, c := manifest, candidate
			change(&m, &c)
			if _, err := approvedPackageCandidate(view, operation, installed, m, c, nil, now); err == nil {
				t.Fatal("an unapproved Automatic update was admitted")
			}
		})
	}
}
