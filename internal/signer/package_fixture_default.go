//go:build !linux || !custody_acceptance_fixture || !fased_realhost_fixture

package signer

func controlledPackageFixturePreWallet(Config) bool { return false }

func controlledPackageFixtureRequired(c Config) bool {
	return packageFixturePath(c.StateDir) || packageFixturePath(c.SocketDir)
}
