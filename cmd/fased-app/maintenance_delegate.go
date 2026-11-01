package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

func maintenanceDelegateCommand(args []string, out io.Writer) error {
	return isolatedMaintenanceCommand(args, out, false)
}
func maintenanceRecoveryCommand(args []string, out io.Writer) error {
	return isolatedMaintenanceCommand(args, out, true)
}
func isolatedMaintenanceCommand(args []string, out io.Writer, recovery bool) error {
	f := flag.NewFlagSet("maintenance-delegate", flag.ContinueOnError)
	f.SetOutput(out)
	id := f.String("operation", "", "existing operation")
	attempt := f.String("attempt", "", "fixed recovery invocation")
	root := f.String("prefix", "", "installed root")
	data := f.String("data-dir", "", "installed data")
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 || !filepath.IsAbs(*root) || !filepath.IsAbs(*data) {
		return errors.New("fixed installed maintenance delegate required")
	}
	m, e := localinstall.Load(localinstall.Paths{Root: *root, Data: *data})
	if e != nil {
		return e
	}
	image, e := os.Executable()
	if e != nil {
		return e
	}
	entry, e := localinstall.ApplicationMaintenanceEntry(m)
	if e != nil {
		return e
	}
	if image != entry {
		return errors.New("fixed installed maintenance image required")
	}
	if recovery {
		parts := strings.Split(*attempt, "-")
		if len(parts) != 2 || !update.IDOK(parts[0]) || !update.IDOK(parts[1]) || *id != "" {
			return errors.New("exact recovery invocation required")
		}
		if e = localinstall.ValidateApplicationMaintenanceProcess(); e != nil {
			return e
		}
		return maintainUpdateCommand([]string{"--operation", parts[0], "--prefix", *root, "--data-dir", *data}, out, out)
	}
	if *attempt != "" {
		return errors.New("recovery is an owner action")
	}
	return localinstall.DelegateApplicationMaintenance(context.Background(), m, *id)
}
