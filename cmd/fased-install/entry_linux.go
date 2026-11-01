//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
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
		return installedLocalEntry(ctx, action, in, out, errOut)
	}
	if profile != "remote" {
		return true, errors.New("choose Local or Remote")
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
	system := &custodyinstall.System{In: in, Out: errOut}
	// Stop/recover must work even when a running core cannot answer health.
	// Their existing implementations enforce immutable owner/unit/UID checks.
	switch action {
	case "update":
		if !installedUpdateEntrySupported(ctx, m.Core) {
			return false, errEntryUpgradeRequired
		}
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
