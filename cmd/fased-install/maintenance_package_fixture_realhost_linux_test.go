//go:build linux && custody_acceptance_fixture && wallet_payload && fased_realhost_fixture

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestPackageFixtureCandidateBindsExactInstance(t *testing.T) {
	owner := os.Getuid()
	machine, err := os.ReadFile("/etc/machine-id")
	if owner <= 0 || err != nil {
		t.Skip("ordinary Linux host identity unavailable")
	}
	id := strings.Repeat("a", 32)
	root, err := update.PackageInstanceRoot(owner, "local", id)
	if err != nil {
		t.Fatal(err)
	}
	oldOwner, oldMachine, oldRoot, oldData := hostplatform.FixtureOwnerUID, hostplatform.FixtureMachineSHA256, hostplatform.FixtureSourceRoot, hostplatform.FixtureSourceData
	t.Cleanup(func() {
		hostplatform.FixtureOwnerUID, hostplatform.FixtureMachineSHA256, hostplatform.FixtureSourceRoot, hostplatform.FixtureSourceData = oldOwner, oldMachine, oldRoot, oldData
	})
	sum := sha256.Sum256([]byte(strings.TrimSpace(string(machine))))
	hostplatform.FixtureOwnerUID, hostplatform.FixtureMachineSHA256 = strconv.Itoa(owner), hex.EncodeToString(sum[:])
	hostplatform.FixtureSourceRoot, hostplatform.FixtureSourceData = root, filepath.Join(root, "app-state")
	if !hostplatform.FixtureHostAllowed(owner) {
		t.Skip("canonical Linux machine identity unavailable")
	}
	if !fixturePackageCandidateAllowed(owner, "local", id) || fixturePackageCandidateAllowed(owner+1, "local", id) || fixturePackageCandidateAllowed(owner, "remote", id) || fixturePackageCandidateAllowed(owner, "local", strings.Repeat("b", 32)) {
		t.Fatal("candidate fixture escaped its pinned package instance")
	}
	args := []string{"--owner", strconv.Itoa(owner), "--location", "local", "--instance", id}
	if _, _, _, err := packageMaintenanceArguments(args); err != nil {
		t.Fatal("the pinned fixture helper entry was refused", err)
	}
	args[5] = strings.Repeat("b", 32)
	if _, _, _, err := packageMaintenanceArguments(args); err == nil {
		t.Fatal("the fixture helper accepted an unpinned package instance")
	}
	if err := requireControlledFixtureManifest(custodyinstall.Manifest{Provenance: signer.ControlledCustodyProfile}); err != nil {
		t.Fatal("the controlled package was refused", err)
	}
	if err := requireControlledFixtureManifest(custodyinstall.Manifest{Provenance: signer.OwnerKeyProvenance}); err == nil {
		t.Fatal("the fixture accepted an owner-key package")
	}
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		if err := requireFixturePackage(owner, "local", id, true); err != nil {
			t.Fatal("the missing fresh fixture was refused", err)
		}
		if err := requireFixturePackage(owner, "local", id, false); err == nil {
			t.Fatal("an update accepted a missing fixture package")
		}
	}
	oldBinding := compiledFirstInstallBinding
	t.Cleanup(func() { compiledFirstInstallBinding = oldBinding })
	seed, err := json.Marshal(bootstrap.Seed{Binding: bootstrap.Binding{Version: 4, Purpose: bootstrap.AttestedPurpose, Repository: "fased-ai/fased-app", Channel: "stable"}, SigningProfile: bootstrap.GitHubAttestationProfile})
	if err != nil {
		t.Fatal(err)
	}
	seedSum := sha256.Sum256(seed)
	bound := &firstInstallBinding{Seed: seed, SHA256: hex.EncodeToString(seedSum[:])}
	compiledFirstInstallBinding = bound
	if got, err := packageCandidateTrust(owner, "local", id); err != nil || got != bound {
		t.Fatal("the pinned package candidate did not use its compiled trust", err)
	}
	if _, err := packageCandidateTrust(owner, "local", strings.Repeat("b", 32)); err == nil {
		t.Fatal("a foreign instance acquired the fixture candidate trust")
	}
	wrongPin := *bound
	wrongPin.SHA256 = strings.Repeat("d", 64)
	compiledFirstInstallBinding = &wrongPin
	if _, err := packageCandidateTrust(owner, "local", id); err == nil {
		t.Fatal("a different seed pin acquired fixture candidate trust")
	}
	oldSeed, err := json.Marshal(bootstrap.Seed{Binding: bootstrap.Binding{Version: 1, Purpose: bootstrap.Purpose, DevelopmentOnly: true, Channel: "development"}})
	if err != nil {
		t.Fatal(err)
	}
	oldSum := sha256.Sum256(oldSeed)
	compiledFirstInstallBinding = &firstInstallBinding{Seed: oldSeed, SHA256: hex.EncodeToString(oldSum[:])}
	if _, err := packageCandidateTrust(owner, "local", id); err == nil {
		t.Fatal("the legacy development seed acquired native package trust")
	}
	compiledFirstInstallBinding = nil
	if _, err := packageCandidateTrust(owner, "local", id); err == nil {
		t.Fatal("an unbound package candidate acquired fixture trust")
	}
}
