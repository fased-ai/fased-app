//go:build !linux

package localinstall

import (
	"context"
	"io"
)

func RecoverApplicationMaintenance(context.Context, Manifest, string, io.Reader, io.Writer, io.Writer) error {
	return errMaintenanceUnsupported
}
