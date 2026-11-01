//go:build linux

package main

import (
	"context"
	"fmt"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
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

// Reuse the installed reader's original terminal authentication. The verifier
// never reads its password or exports a cookie, including locked Remote owners.
func signInForEntryUpgrade(ctx context.Context, profile string, in io.Reader, out, errOut io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	fmt.Fprintln(errOut, "Sign in to update this workspace.")
	if profile == "remote" {
		m, e := vpsinstall.Load(os.Getuid())
		if e != nil {
			return e
		}
		return vpsinstall.ApplicationReaderSignIn(ctx, m, &custodyinstall.System{In: in, Out: errOut})
	}
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
