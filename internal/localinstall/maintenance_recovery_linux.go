//go:build linux

package localinstall

import (
	"context"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
	"os"
)

// Explicit OS-owner recovery only. The core bus has no reset/restart operation.
// The same verified actor inspects the original IPC/lease/journal and resumes or
// refuses it. Never manufacture a result or create a replacement operation.
func RecoverApplicationMaintenance(ctx context.Context, m Manifest, id string, in io.Reader, out, errOut io.Writer) error {
	if !applicationUpdateID(id) || os.Getuid() <= 0 || os.Getuid() != os.Geteuid() {
		return errors.New("ordinary owner and exact recovery operation required")
	}
	if e := maintenanceIdentity(m); e != nil {
		return e
	}
	runner := ApplicationMaintenanceRunner(m)
	p, e := maintenanceManager(ctx, runner, maintenanceUnit(id))
	if e != nil {
		return e
	}
	if e = checkMaintenanceTemplate(m, p); e != nil {
		return e
	}
	switch p["ActiveState"] {
	case "failed", "inactive":
	case "active":
		if p["SubState"] != "exited" {
			return errors.New("maintenance is still running; preserve its current operation")
		}
	default:
		return errors.New("maintenance is still running or unknown; preserve it")
	}
	if e = recoverIsolatedMaintenance(ctx, m, id, update.NewProbe(), &custodyinstall.System{In: in, Out: errOut, Authorization: "Authorize recovery of this existing Fased update through its isolated ordinary-user service. No Fased code runs as root."}); e != nil {
		return e
	}
	status, e := ApplicationUpdateState(Paths{Root: m.Root, Data: m.Data})
	if e != nil {
		return e
	}
	_, e = fmt.Fprintln(out, status.Phase)
	return e
}
