//go:build linux

package main

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
	"os"
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
	if profile == "server" {
		if action != "install" {
			return true, errors.New("use fased server uninstall or status to manage an installed server")
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
	if action == "install" {
		return false, nil
	}
	return false, errors.New("no installed workspace for this operator")
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
