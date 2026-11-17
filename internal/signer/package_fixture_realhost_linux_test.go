//go:build linux && custody_acceptance_fixture && fased_realhost_fixture

package signer

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestPackageFixturePreWalletIsBoundToItsRealHost(t *testing.T) {
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
	c := Config{Version: 3, Mode: OrdinaryCustodyMode, Profile: OwnerCustodyProfile, Provenance: ControlledCustodyProfile, SignerID: id, OwnerUID: owner, AppUID: owner + 1, SignerUID: owner + 2, StateDir: filepath.Join(root, "signer-state"), SocketDir: filepath.Join(root, "signer-ipc")}
	if err := c.Validate(); err != nil {
		t.Fatal("the exact pre-wallet package fixture was refused", err)
	}
	for _, change := range []func(*Config){
		func(v *Config) { v.OwnerUID++ },
		func(v *Config) { v.SignerID = strings.Repeat("b", 32) },
		func(v *Config) { v.StateDir = filepath.Join(root, "other-state") },
		func(v *Config) { v.SocketDir = filepath.Join(root, "other-socket") },
	} {
		bad := c
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("foreign pre-wallet fixture accepted")
		}
	}
	c.Network = "devnet"
	if err := c.Validate(); err != nil {
		t.Fatal("the exact networked package fixture was refused", err)
	}
	other := c
	other.SignerID = strings.Repeat("b", 32)
	otherRoot, err := update.PackageInstanceRoot(owner, "local", other.SignerID)
	if err != nil {
		t.Fatal(err)
	}
	other.StateDir, other.SocketDir = filepath.Join(otherRoot, "signer-state"), filepath.Join(otherRoot, "signer-ipc")
	if other.Validate() == nil {
		t.Fatal("a networked fixture accepted another package")
	}
	legacy := c
	legacyRoot := "/var/lib/fased-app-custody-" + strconv.Itoa(owner)
	legacy.StateDir, legacy.SocketDir = filepath.Join(legacyRoot, "signer-state"), filepath.Join(legacyRoot, "signer-ipc")
	if legacy.Validate() == nil {
		t.Fatal("a package-pinned fixture accepted a legacy signer")
	}
	hostplatform.FixtureSourceRoot = filepath.Join(t.TempDir(), "legacy-source")
	if err := legacy.Validate(); err != nil {
		t.Fatal("a separately pinned legacy fixture was refused", err)
	}
	hybrid := legacy
	hybrid.SocketDir = filepath.Join(root, "signer-ipc")
	if hybrid.Validate() == nil {
		t.Fatal("a legacy fixture accepted a package socket")
	}
}
