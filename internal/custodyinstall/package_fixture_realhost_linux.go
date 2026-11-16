//go:build linux && custody_acceptance_fixture && fased_realhost_fixture

package custodyinstall

import (
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/signer"
)

func controlledPackageFixture(m Manifest) bool {
	root, err := PackageRoot(m.Owner, m.Location, m.ID)
	return err == nil && root == m.Root && hostplatform.FixtureSourceLocationAllowed(m.Owner, root, filepath.Join(root, "app-state"))
}

func packageProvisionProvenance(owner int, location, instance string) (string, error) {
	root, err := PackageRoot(owner, location, instance)
	if err != nil || !hostplatform.FixtureSourceLocationAllowed(owner, root, filepath.Join(root, "app-state")) {
		return "", signer.ErrUnavailable
	}
	return signer.ControlledCustodyProfile, nil
}
