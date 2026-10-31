//go:build !linux

package localinstall

import (
	"context"
	"errors"
)

var errMaintenanceUnsupported = errors.New("owner maintenance launch currently supports Linux user services")

func ApplicationMaintenanceRunner(Manifest) MaintenanceRunner {
	return func(context.Context, ...string) ([]byte, error) { return nil, errMaintenanceUnsupported }
}
func InstallApplicationMaintenance(context.Context, Manifest, MaintenanceRunner) error {
	return errMaintenanceUnsupported
}
func LaunchApplicationMaintenance(context.Context, Manifest, string, MaintenanceRunner) (MaintenanceStatus, error) {
	return MaintenanceStatus{}, errMaintenanceUnsupported
}
func ApplicationMaintenanceStatus(context.Context, Manifest, string, MaintenanceRunner) (MaintenanceStatus, error) {
	return MaintenanceStatus{}, errMaintenanceUnsupported
}

func CompleteApplicationMaintenance(context.Context, Manifest, string, string, MaintenanceRunner) error {
	return errMaintenanceUnsupported
}
func ApplicationMaintenanceEntry(Manifest) (string, error)      { return "", errMaintenanceUnsupported }
func readMaintenanceRequestForRemoval(Manifest) (string, error) { return "", nil }
