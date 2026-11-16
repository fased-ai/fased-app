//go:build linux && custody_acceptance_fixture && fased_realhost_fixture

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestRealHostFixtureSignerRefusesUnboundHostAndOwnerKeys(t *testing.T) {
	id, err := os.ReadFile("/etc/machine-id")
	if err != nil || os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
		t.Skip("ordinary host identity unavailable")
	}
	oldOwner, oldMachine, oldData := hostplatform.FixtureOwnerUID, hostplatform.FixtureMachineSHA256, hostplatform.FixtureSourceData
	t.Cleanup(func() {
		hostplatform.FixtureOwnerUID, hostplatform.FixtureMachineSHA256, hostplatform.FixtureSourceData = oldOwner, oldMachine, oldData
	})
	hostplatform.FixtureOwnerUID, hostplatform.FixtureMachineSHA256, hostplatform.FixtureSourceData = "", "", ""
	if err = run([]string{"ordinary-custody-version"}, nil, io.Discard, io.Discard); !errors.Is(err, signer.ErrUnavailable) {
		t.Fatal("unbound fixture signer was accepted", err)
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(string(id))))
	hostplatform.FixtureOwnerUID = strconv.Itoa(os.Getuid())
	hostplatform.FixtureMachineSHA256 = hex.EncodeToString(sum[:])
	hostplatform.FixtureSourceData = filepath.Join(t.TempDir(), "fixture-data")
	if !hostplatform.FixtureHostAllowed(os.Getuid()) {
		t.Skip("canonical Linux machine identity unavailable")
	}
	var out bytes.Buffer
	if err = run([]string{"ordinary-custody-version"}, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var version struct {
		Provenance   string `json:"provenance"`
		WalletSigner bool   `json:"walletSigner"`
	}
	if err = json.Unmarshal(out.Bytes(), &version); err != nil || version.Provenance != signer.ControlledCustodyProfile || version.WalletSigner {
		t.Fatal("fixture signer advertised real-wallet authority", err)
	}
	if compiledFixtureHost(os.Getuid() + 1) {
		t.Fatal("another owner was accepted")
	}
	if _, err = compiledFixtureServe(t.Context(), signer.Config{OwnerUID: os.Getuid(), Provenance: signer.OwnerKeyProvenance}); !errors.Is(err, signer.ErrUnavailable) {
		t.Fatal("owner-key custody was accepted by the fixture", err)
	}
}

func TestRealHostPackageFixtureSignerRefusesAnotherInstance(t *testing.T) {
	machine, err := os.ReadFile("/etc/machine-id")
	owner := os.Getuid()
	if owner <= 0 || err != nil {
		t.Skip("ordinary host identity unavailable")
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
	var compatibility bytes.Buffer
	if err := run([]string{"ordinary-custody-compatibility"}, nil, &compatibility, io.Discard); err != nil {
		t.Fatal("the fixture signer compatibility entry failed", err)
	}
	var contract update.Contract
	feature := hostplatform.FixturePackageSignerFeature()
	if err := json.Unmarshal(compatibility.Bytes(), &contract); err != nil || feature == "" || !slices.Contains(contract.Features, feature) || slices.Contains(contract.Features, update.OwnerKeyAdmissionFeature) {
		t.Fatal("the signer did not advertise its exact controlled fixture scope", err)
	}
	otherID := strings.Repeat("b", 32)
	otherRoot, err := update.PackageInstanceRoot(owner, "local", otherID)
	if err != nil {
		t.Fatal(err)
	}
	other := signer.Config{Version: 3, Mode: signer.OrdinaryCustodyMode, Profile: signer.OwnerCustodyProfile, Provenance: signer.ControlledCustodyProfile,
		Network: "devnet", SignerID: otherID, OwnerUID: owner, AppUID: owner + 1, SignerUID: owner + 2,
		StateDir: filepath.Join(otherRoot, "signer-state"), SocketDir: filepath.Join(otherRoot, "signer-ipc")}
	if _, err = compiledFixtureServe(t.Context(), other); !errors.Is(err, signer.ErrUnavailable) {
		t.Fatal("the package fixture did not refuse another signer instance", err)
	}
}
