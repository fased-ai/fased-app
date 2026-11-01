//go:build !linux

package localinstall

import (
	"context"
	"errors"
	"io"
)

var errMaintenanceUnsupported = errors.New("owner maintenance launch currently supports Linux user services")

func PrepareApplicationMaintenanceOwner(context.Context, Manifest, io.Reader, io.Writer) error {
	return errMaintenanceUnsupported
}

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

func ApplicationMaintenanceReady(context.Context, Manifest, string, MaintenanceRunner) error {
	return errMaintenanceUnsupported
}
func StartApplicationMaintenance(context.Context, Manifest, string, MaintenanceRunner) error {
	return errMaintenanceUnsupported
}
func PrepareApplicationMaintenanceOperation(context.Context, Manifest, string, MaintenanceRunner) error {
	return errMaintenanceUnsupported
}

func ApplicationAcquisitionParent(Manifest) (string, error) { return "", errMaintenanceUnsupported }

func IsApplicationMaintenanceImage(Manifest, string) bool { return false }
func ValidateApplicationMaintenanceProcess() error        { return errMaintenanceUnsupported }

func PrepareApplicationMaintenanceHost(context.Context, Manifest, func(context.Context, []byte, ...string) ([]byte, error)) error {
	return errMaintenanceUnsupported
}
func DelegateApplicationMaintenance(context.Context, Manifest, string) error {
	return errMaintenanceUnsupported
}

func legacyMaintenanceIdle(Manifest) error { return nil }

func CheckApplicationMaintenanceHost(context.Context, Manifest, string) error {
	return errMaintenanceUnsupported
}
