//go:build linux && custody_acceptance_fixture && fased_realhost_fixture

package core

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestPackageFixtureCoreKeepsEmptySignerUnconfigured(t *testing.T) {
	app := os.Getuid()
	machine, err := os.ReadFile("/etc/machine-id")
	if app <= 0 || err != nil {
		t.Skip("ordinary Linux host identity unavailable")
	}
	owner, id := app+1, strings.Repeat("a", 32)
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
	c := signer.Config{Version: 3, Mode: signer.OrdinaryCustodyMode, Profile: signer.OwnerCustodyProfile, Provenance: signer.ControlledCustodyProfile, SignerID: id, OwnerUID: owner, AppUID: app, SignerUID: app + 2, StateDir: filepath.Join(root, "signer-state"), SocketDir: filepath.Join(root, "signer-ipc")}
	s, err := OpenStore(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.configureVerifiedSigner(c); err != nil || !s.unconfiguredProtectedSigner || s.protectedOwnerUID != owner || s.custodyNetwork != "" {
		t.Fatal("the empty fixture was not kept unconfigured", err)
	}
	if view := s.fixtureView(t.Context()); view.Available {
		t.Fatal("an empty signer was reported as an available wallet")
	}
	s.installationSource.Store(1)
	if view := s.walletSetup(s.fixtureView(t.Context())); !view.PackageOwner || !view.OwnerSupported || view.State != "configuration-required" || !view.FixtureOnly {
		t.Fatal("the actual protected package configuration did not route Local Wallets", view)
	}
}
