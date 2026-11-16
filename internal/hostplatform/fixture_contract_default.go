//go:build !linux || !custody_acceptance_fixture || !fased_realhost_fixture

package hostplatform

func FixturePackageSignerFeature() string { return "" }
