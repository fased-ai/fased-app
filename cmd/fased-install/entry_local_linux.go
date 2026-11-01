//go:build linux

package main

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

func installedLocalEntry(ctx context.Context, action string, in io.Reader, out, errOut io.Writer) (bool, error) {
	paths, e := localinstall.DefaultPaths()
	if e != nil {
		return true, e
	}
	m, e := localinstall.Load(paths)
	if errors.Is(e, os.ErrNotExist) && action == "install" {
		return false, nil
	}
	if e != nil {
		return true, e
	}
	var args []string
	switch action {
	case "install", "open", "start", "setup":
		args = []string{"open", "--prefix", m.Root, "--data-dir", m.Data}
	case "stop":
		args = []string{"service", "stop", "--prefix", m.Root, "--data-dir", m.Data}
	case "status":
		args = []string{"status", "--data-dir", m.Data}
	case "update":
		if !installedUpdateEntrySupported(ctx, filepath.Join(m.Root, localinstall.EngineName())) {
			return false, errEntryUpgradeRequired
		}
		args = []string{"update", "--data-dir", m.Data}
	case "recover":
		return true, errors.New("use the existing stopped-instance recovery flow; no implicit password reset")
	default:
		return true, errors.New("unknown installed action")
	}
	c := exec.CommandContext(ctx, filepath.Join(m.Root, localinstall.EngineName()), args...)
	c.Stdin, c.Stdout, c.Stderr = in, out, errOut
	return true, c.Run()
}
