//go:build linux

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func launchOwnerEnrollment(ctx context.Context, path string, args []string) error {
	resolved, err := filepath.EvalSymlinks("/usr/bin/x-terminal-emulator")
	if err != nil {
		return errors.New("a system terminal is required for initial OS approval")
	}
	base := filepath.Base(resolved)
	if base == "gnome-terminal.wrapper" {
		resolved, base = "/usr/bin/gnome-terminal", "gnome-terminal"
	}
	i, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || !i.Mode().IsRegular() || i.Mode().Perm()&0022 != 0 {
		return errors.New("system approval terminal is not trusted")
	}
	// Wait for the delegated controller, not merely a terminal launcher. No
	// shell string, private input or password enters this process.
	var terminalArgs []string
	switch base {
	case "xterm", "uxterm", "lxterm":
		terminalArgs = []string{"-e"}
	case "gnome-terminal":
		terminalArgs = []string{"--wait", "--"}
	case "konsole":
		terminalArgs = []string{"--nofork", "-e"}
	default:
		return errors.New("wallet setup requires xterm, GNOME Terminal or Konsole")
	}
	cmd := exec.CommandContext(ctx, resolved, append(append(terminalArgs, path), args...)...)
	if err = cmd.Run(); err != nil {
		return errors.New("wallet setup did not complete in the system window; reconnect to resume")
	}
	// Some terminals return zero even after a failed delegated command. The
	// caller must inspect the existing enrollment's readiness after this exit.
	return nil
}
