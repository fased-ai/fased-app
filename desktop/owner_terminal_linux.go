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
	i, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || !i.Mode().IsRegular() || i.Mode().Perm()&0022 != 0 {
		return errors.New("system approval terminal is not trusted")
	}
	// No shell string or password enters the native process. Fased stays the
	// ordinary owner; the enrollment controller elevates fixed OS tools only.
	cmd := exec.CommandContext(ctx, resolved, append([]string{"-e", path}, args...)...)
	if err = cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
