package localinstall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
)

// PrepareInstallationMaintenance is shared by terminal/native first launch.
// Optional readiness cannot undo a healthy app install or enable an update mode.
func PrepareInstallationMaintenance(ctx context.Context, m Manifest, serviceReady bool, out io.Writer) string {
	return prepareInstallationMaintenance(ctx, m, serviceReady, out, func(ctx context.Context, m Manifest) error {
		if e := InstallApplicationMaintenance(ctx, m, ApplicationMaintenanceRunner(m)); e != nil {
			return e
		}
		return PrepareApplicationMaintenanceOwner(ctx, m, os.Stdin, out)
	})
}
func prepareInstallationMaintenance(ctx context.Context, m Manifest, serviceReady bool, out io.Writer, prepare func(context.Context, Manifest) error) string {
	if runtime.GOOS != "linux" {
		return "manual-only"
	}
	if !serviceReady {
		if out != nil {
			fmt.Fprintln(out, "Updates: automatic maintenance setup is deferred until the Local service starts.")
		}
		return "deferred"
	}
	if e := prepare(ctx, m); e != nil {
		if out != nil {
			fmt.Fprintf(out, "Updates need maintenance setup: %v. The app and data are preserved.\n", e)
		}
		return "needs-setup"
	}
	if out != nil {
		fmt.Fprintln(out, "Updates: maintenance is ready. Automatic updates remain off.")
	}
	return "ready"
}

// No removal may discard a still-authorized actor/transaction. A finished
// marker was committed only after disabling its exact persistent instance.
func checkMaintenanceRemoval(m Manifest) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	if e := legacyMaintenanceIdle(m); e != nil {
		return e
	}
	if j, e := readApplicationJournal(Paths{Root: m.Root, Data: m.Data}); e == nil {
		if j.Status.Phase != "completed" && j.Status.Phase != "rolled-back" {
			return errors.New("recover the pending application update before removing the application")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	r, e := readMaintenanceRequestForRemoval(m)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	if r == "" {
		return errors.New("finish or recover the existing owner maintenance operation before removing the application")
	}
	return nil
}
