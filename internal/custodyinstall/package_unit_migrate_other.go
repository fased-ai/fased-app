//go:build !linux

package custodyinstall

import "context"

// MigratePackageUnits upgrades legacy Format-4 systemd unit definitions in place.
func MigratePackageUnits(context.Context, int, string, string) (bool, error) {
	return false, unsupported
}
