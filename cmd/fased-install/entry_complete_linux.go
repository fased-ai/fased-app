//go:build linux

package main

import (
	"context"
	"fmt"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func completeInstalledEntry(profile string, in io.Reader, out, errOut io.Writer) error {
	var executable string
	args := []string{"install-command"}
	if profile == "remote" {
		m, e := vpsinstall.Load(os.Getuid())
		if e != nil {
			return e
		}
		executable = m.Core
		args = append(args, "--remote")
	} else {
		paths, e := localinstall.DefaultPaths()
		if e != nil {
			return e
		}
		m, e := localinstall.Load(paths)
		if e != nil {
			return e
		}
		executable = filepath.Join(m.Root, localinstall.EngineName())
	}
	c := exec.Command(executable, args...)
	c.Stdin, c.Stdout, c.Stderr = in, out, errOut
	return c.Run()
}

// Local predecessors retain their original terminal authentication. Remote
// authority comes from the verified current bootstrap's fixed dropped-AppUID
// bridge, before old-reader inventory; never invoke the old password login.
func signInForEntryUpgrade(ctx context.Context, profile string, in io.Reader, out, errOut io.Writer) error {
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
