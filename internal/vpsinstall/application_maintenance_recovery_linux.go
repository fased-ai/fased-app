//go:build linux

package vpsinstall

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

// Explicit OS-owner recovery of the original operation. No reset/restart
// capability is added to the app or its Polkit grant.
func RecoverApplicationMaintenance(ctx context.Context, m Manifest, id string, s *custodyinstall.System, out io.Writer) error {
	o := liveOps(s)
	if s == nil || s.NonInteractive || !canonicalHex(id, 16) {
		return errors.New("owner terminal and exact recovery operation required")
	}
	if e := ownedBy(o, m); e != nil {
		return e
	}
	g, e := readMaintenanceGrant(o, m)
	if e != nil {
		return e
	}
	if e = verifyMaintenancePolicies(ctx, o, m); e != nil {
		return e
	}
	state, e := applicationMaintenanceStatus(ctx, o, m, id)
	if e != nil {
		return e
	}
	if state.Phase == "running" {
		return errors.New("inspect the current owner operation before recovery")
	}
	command := exec.CommandContext(ctx, maintenanceImagePath(m.OwnerUID), "maintain-update", "--remote", "--unattended", "--operation", id)
	command.Dir = g.OwnerHome
	command.Env = []string{"HOME=" + g.OwnerHome, "XDG_CONFIG_HOME=" + strings.TrimSuffix(g.OwnerControl, "/fased-app-vps-installer"), "PATH=/usr/bin:/bin", "LANG=C"}
	command.Stdin, command.Stdout, command.Stderr = s.In, out, s.Out
	return command.Run()
}
