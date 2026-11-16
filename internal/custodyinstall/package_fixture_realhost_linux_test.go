//go:build linux && custody_acceptance_fixture && fased_realhost_fixture

package custodyinstall

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

func TestPackageFixtureProvisionBindsOneHostAndInstance(t *testing.T) {
	owner := os.Getuid()
	machine, err := os.ReadFile("/etc/machine-id")
	if owner <= 0 || err != nil {
		t.Skip("ordinary Linux host identity unavailable")
	}
	id := strings.Repeat("a", 32)
	root, err := PackageRoot(owner, "local", id)
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
	m := Manifest{Layout: PackageLayout, Version: 3, Mode: signer.OrdinaryCustodyMode, Profile: signer.OwnerCustodyProfile, Provenance: signer.ControlledCustodyProfile, Enrollment: true, Location: "local", Owner: owner, ID: id, Root: root, Listen: "127.0.0.1:17331", CoreHash: strings.Repeat("b", 64), SignerHash: strings.Repeat("c", 64)}
	m.App = Identity{Name: packageAccountName(owner, "local", id, "app"), UID: owner + 1, GID: owner + 1}
	m.Signer = Identity{Name: packageAccountName(owner, "local", id, "signer"), UID: owner + 2, GID: owner + 2}
	if err := m.validatePackage(); err != nil {
		t.Fatal("the exact disposable package was refused", err)
	}
	r, _ := packageGateInitialRecord(t)
	p := *r.Package
	p.Components = append([]update.Component(nil), p.Components...)
	for i := range p.Components {
		switch p.Components[i].Contract.Component {
		case "core":
			p.Components[i].Artifact.SHA256 = m.CoreHash
		case "signer":
			p.Components[i].Artifact.SHA256 = m.SignerHash
			p.Components[i].Contract = signer.OrdinaryUpdateContract()
		}
	}
	if err := CheckPackageWalletSigner(m, &p); err != nil {
		t.Fatal("the pinned disposable signer was refused", err)
	}
	wrongSigner := p
	wrongSigner.Components = append([]update.Component(nil), p.Components...)
	for i := range wrongSigner.Components {
		if wrongSigner.Components[i].Contract.Component == "signer" {
			wrongSigner.Components[i].Contract.Features = []string{update.OrdinaryMaintenanceFeature, update.JournalBasePackageFeature, update.NativePackageJournalFeature, update.NativeDesktopJournalFeature}
		}
	}
	if err := CheckPackageWalletSigner(m, &wrongSigner); err == nil {
		t.Fatal("a signer without the exact disposable pins was admitted")
	}
	if provenance, err := packageProvisionProvenance(owner, "local", id); err != nil || provenance != signer.ControlledCustodyProfile {
		t.Fatal("the disposable installation lost its controlled provenance", err)
	}
	other := m
	other.ID = strings.Repeat("d", 32)
	other.Root, _ = PackageRoot(owner, "local", other.ID)
	other.App.Name = packageAccountName(owner, "local", other.ID, "app")
	other.Signer.Name = packageAccountName(owner, "local", other.ID, "signer")
	if other.validatePackage() == nil {
		t.Fatal("another package instance received fixture authority")
	}
	if err := CheckPackageWalletSigner(other, &p); err == nil {
		t.Fatal("another package instance received signer admission")
	}
	if _, err := packageProvisionProvenance(owner, "local", other.ID); err == nil {
		t.Fatal("another package instance could be provisioned by the fixture")
	}
}
