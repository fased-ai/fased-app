//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
	"io"
	"os"
	"os/exec"
	"time"
)

// Existing-instance actions reuse the verified installed owner's CLI. They grant
// no replacement/update authority and never turn missing/foreign data into defaults.
func installedEntry(ctx context.Context, profile, action string, in io.Reader, out, errOut io.Writer) (bool, error) {
	switch action {
	case "install", "open", "start", "stop", "status", "recover", "update", "setup":
	default:
		return false, errors.New("unknown installed entry action")
	}
	if os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
		return false, errors.New("entry verifier requires an ordinary OS owner")
	}
	if profile == "connect" {
		if action != "install" {
			return true, errors.New("Connect does not manage installation")
		}
		return false, nil
	}
	if profile == "local" {
		if handled, err := installedCustodyEntry(ctx, profile, action, errOut); handled || err != nil {
			return handled, err
		}
		return installedLocalEntry(ctx, action, in, out, errOut)
	}
	if profile != "remote" {
		return true, errors.New("choose Local or Remote")
	}
	if handled, err := installedCustodyEntry(ctx, profile, action, errOut); handled || err != nil {
		return handled, err
	}
	budget := 2 * time.Minute
	if action == "update" || action == "setup" {
		budget = 16 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	m, e := vpsinstall.Load(os.Getuid())
	if errors.Is(e, os.ErrNotExist) {
		if action == "install" {
			return false, nil
		}
		return false, errors.New("no installed workspace for this operator")
	}
	if e != nil {
		return false, e
	}
	if action == "install" || action == "update" {
		if err := entryUpgradeDecision(action, installedUpdateEntrySupported(ctx, m.Core), in, errOut); err != nil {
			return false, err
		}
	}
	system := &custodyinstall.System{In: in, Out: errOut}
	// Stop/recover must work even when a running core cannot answer health.
	// Their existing implementations enforce immutable owner/unit/UID checks.
	switch action {
	case "update":
		command := exec.CommandContext(ctx, m.Core, "vps", "updates", "wizard")
		command.Stdin, command.Stdout, command.Stderr = in, out, errOut
		return true, command.Run()
	case "stop":
		return true, vpsinstall.Stop(ctx, m, system)
	case "recover":
		return true, vpsinstall.Claim(ctx, m, system, true, out)
	case "status":
		state, e := vpsinstall.Status(ctx, m, system)
		if e != nil {
			return true, e
		}
		return true, json.NewEncoder(out).Encode(state)
	}
	state, statusErr := vpsinstall.Status(ctx, m, system)
	if statusErr == nil && !state.Installed {
		if action == "install" {
			return false, nil
		}
		return true, errors.New("Remote installation is incomplete; rerun installation")
	}
	// Start revalidates immutable identity independently; failed health cannot
	// bypass those checks, and cannot prevent an authorized restart attempt.
	if e = vpsinstall.Start(ctx, m, system); e != nil {
		return true, e
	}
	fmt.Fprintln(errOut, "Using the installed Fased workspace; no update.")
	args := []string{"vps", "access"}
	if action == "setup" {
		args = append(args, "--configure")
	}
	command := exec.CommandContext(ctx, m.Core, args...)
	command.Stdin, command.Stdout, command.Stderr = in, out, errOut
	return true, command.Run()
}

func installedCustodyEntry(ctx context.Context, profile, action string, diagnostic io.Writer) (bool, error) {
	m, err := custodyinstall.Load(os.Getuid())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if m.Location != profile {
		return false, errors.New("retain the installed custody profile")
	}
	if action != "update" && action != "install" {
		return true, errors.New("use the installed custody client for this action")
	}
	if r, e := update.ReadMaintenanceRecord(m.Owner); e == nil && r.Phase != "completed" && r.Package != nil {
		_, e = maintenance.Call(ctx, maintenance.Request{Version: 1, Action: "custody-update", Custody: &maintenance.CustodyUpdateRequest{OperationID: r.OperationID, Sequence: r.Package.Sequence, ReleaseSHA256: r.Package.ReleaseSHA256, Bootstrap: r.Bootstrap}}, diagnostic)
		return true, e
	} else if e != nil && !errors.Is(e, os.ErrNotExist) {
		return false, e
	}
	return false, errEntryUpgradeRequired
}
