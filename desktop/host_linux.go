//go:build linux

package main

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"

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
	term, err := resolveTrustedLinuxTerminal(nil)
	if err != nil {
		return false, errors.New("system terminal unavailable; run this package's fased-app install --bundle . --interactive in your terminal")
	}
	cmdArgs := append(append([]string(nil), term.execArgs...), filepath.Join(bundle, localinstall.EngineName()), "install", "--bundle", bundle, "--prefix", p.Root, "--data-dir", p.Data, "--prepare-host")
	c := exec.CommandContext(ctx, term.path, cmdArgs...)
	if err = c.Start(); err != nil {
		return false, err
	}
	go func() { _ = c.Wait() }()
	return true, nil
}
