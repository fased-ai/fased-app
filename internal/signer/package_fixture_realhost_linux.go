//go:build linux && custody_acceptance_fixture && fased_realhost_fixture

package signer

import (
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/update"
)

// Only an explicitly compiled disposable build can use this package signer.
// The machine, OS owner, package root and app data are all compile-time pins.
func controlledPackageFixturePreWallet(c Config) bool {
	root := filepath.Dir(c.StateDir)
	for _, location := range []string{"local", "remote"} {
		expected, err := update.PackageInstanceRoot(c.OwnerUID, location, c.SignerID)
		if err == nil && root == expected && c.StateDir == filepath.Join(root, "signer-state") && c.SocketDir == filepath.Join(root, "signer-ipc") && hostplatform.FixtureSourceLocationAllowed(c.OwnerUID, root, filepath.Join(root, "app-state")) {
			return true
		}
	}
	return false
}

func controlledPackageFixtureRequired(c Config) bool {
	return packageFixturePath(hostplatform.FixtureSourceRoot) || packageFixturePath(c.StateDir) || packageFixturePath(c.SocketDir)
}
