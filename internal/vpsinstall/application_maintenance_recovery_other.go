//go:build !linux

package vpsinstall

import (
	"context"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"io"
)

func RecoverApplicationMaintenance(context.Context, Manifest, string, *custodyinstall.System, io.Writer) error {
	return ErrApplicationMaintenanceAuthority
}
