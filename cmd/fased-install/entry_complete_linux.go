//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func completeInstalledEntry(profile string, in io.Reader, out, errOut io.Writer) error {
	if m, e := custodyinstall.Load(os.Getuid()); e == nil && m.Location == profile {
		_, e = fmt.Fprintln(out, "Fased updated. Open your existing workspace.")
		return e
	}
	var executable string
	args := []string{"install-command"}
	if profile == "remote" {
		return errors.New("legacy app-only Remote installation is retired; use Format-4 Remote setup")
	}
	paths, e := localinstall.DefaultPaths()
	if e != nil {
		return e
	}
	m, e := localinstall.Load(paths)
	if e != nil {
		return e
	}
	executable = filepath.Join(m.Root, localinstall.EngineName())
	c := exec.Command(executable, args...)
	c.Stdin, c.Stdout, c.Stderr = in, out, errOut
	return c.Run()
}

// Local predecessors retain their original terminal authentication. Remote
// authority comes from the verified current bootstrap's fixed dropped-AppUID
// bridge, before old-reader inventory; never invoke the old password login.
func signInForEntryUpgrade(ctx context.Context, profile string, in io.Reader, out, errOut io.Writer) error {
	if m, e := custodyinstall.Load(os.Getuid()); e == nil && m.Location == profile {
		return nil
	}
	if profile == "remote" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	fmt.Fprintln(errOut, "Sign in to update this workspace.")
	paths, e := localinstall.DefaultPaths()
	if e != nil {
		return e
	}
	m, e := localinstall.Load(paths)
	if e != nil {
		return e
	}
	c := exec.CommandContext(ctx, filepath.Join(m.Root, localinstall.EngineName()), "login", "--data-dir", m.Data)
	c.Stdin, c.Stdout, c.Stderr = in, out, errOut
	return c.Run()
}
