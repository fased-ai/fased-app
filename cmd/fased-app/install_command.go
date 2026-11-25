package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"io"
	"os"
	"path/filepath"
)

// Verified older-helper upgrade completion installs ordinary entry/maintenance
// support, preserving the instance. It is not a public HTTP/privilege endpoint.
func installedCommandCommand(args []string, out, errOut io.Writer) error {
	f := flag.NewFlagSet("install-command", flag.ContinueOnError)
	f.SetOutput(errOut)
	printPath := f.Bool("path", false, "print the verified installed command")
	remote := f.Bool("remote", false, "existing managed Remote instance")
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return errors.New("unexpected command installation arguments")
	}
	if *remote {
		return errors.New("legacy app-only Remote install-command has been retired")
	}
	paths, e := localinstall.DefaultPaths()
	if e != nil {
		return e
	}
	m, e := localinstall.Load(paths)
	if e != nil {
		return e
	}
	if *printPath {
		return printInstalledCommand(filepath.Join(m.Root, localinstall.EngineName()), m.Root, m.Data, false, out)
	}
	if e = installCommand(filepath.Join(m.Root, localinstall.EngineName()), m.Root, m.Data, false, errOut); e != nil {
		return e
	}
	if e = localinstall.InstallApplicationMaintenance(context.Background(), m, localinstall.ApplicationMaintenanceRunner(m)); e != nil {
		return e
	}
	return localinstall.PrepareApplicationMaintenanceOwner(context.Background(), m, os.Stdin, errOut)
}

func printInstalledCommand(corePath, root, data string, remote bool, out io.Writer) error {
	home, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	path, e := verifiedInstalledCommand(home, corePath, root, data, remote)
	if e != nil {
		return e
	}
	_, e = fmt.Fprintln(out, path)
	return e
}
