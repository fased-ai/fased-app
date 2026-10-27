package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/fased-ai/fased-app/internal/localinstall"
)

func hostPreparationNotice() string {
	if localinstall.RequiresBubblewrap() && localinstall.CheckHostRuntime() != nil {
		return " Ubuntu host preparation will open a system terminal for one sudo authorization to install OS packages and the reviewed restrictive sandbox policy. Existing policies are preserved."
	}
	return ""
}
func prepareDesktopHost(ctx context.Context, bundle string, p localinstall.Paths) (bool, error) {
	if hostPreparationNotice() == "" {
		return false, nil
	}
	i, e := os.Stat("/usr/bin/x-terminal-emulator")
	if e != nil {
		return false, errors.New("system terminal unavailable; run this package's fased-app install --bundle . --interactive in your terminal")
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != 0 || !i.Mode().IsRegular() || i.Mode().Perm()&0022 != 0 {
		return false, errors.New("system terminal must be a trusted root-owned OS executable")
	}
	c := exec.CommandContext(ctx, "/usr/bin/x-terminal-emulator", "-e", filepath.Join(bundle, localinstall.EngineName()), "install", "--bundle", bundle, "--prefix", p.Root, "--data-dir", p.Data, "--prepare-host")
	if e = c.Start(); e != nil {
		return false, e
	}
	go func() { _ = c.Wait() }()
	return true, nil
}
