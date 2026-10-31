//go:build !linux

package vpsinstall

import (
	"context"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/localinstall"
)

func ApplicationMaintenanceManifest(int) (Manifest, error) { return Manifest{}, unsupported }

func InstallApplicationMaintenance(context.Context, Manifest, *custodyinstall.System) error {
	return unsupported
}
func LaunchApplicationMaintenance(context.Context, Manifest, string, *custodyinstall.System) (localinstall.MaintenanceStatus, error) {
	return localinstall.MaintenanceStatus{}, unsupported
}
func ApplicationMaintenanceStatus(context.Context, Manifest, string) (localinstall.MaintenanceStatus, error) {
	return localinstall.MaintenanceStatus{}, unsupported
}
func ValidateApplicationMaintenanceEntry(Manifest, string) error { return unsupported }
func BeginApplicationMaintenance(context.Context, Manifest, string, *custodyinstall.System) error {
	return unsupported
}
func CompleteApplicationMaintenance(context.Context, Manifest, string, string, *custodyinstall.System) error {
	return unsupported
}
