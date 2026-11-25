package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/fased-ai/fased-app/internal/update"
)

type packageInstanceRef struct {
	Location string
	ID       string
}

func resolveHostPackageInstance(uid int, location, instance string, readDir func(string) ([]os.DirEntry, error)) (packageInstanceRef, error) {
	if uid <= 0 {
		return packageInstanceRef{}, errors.New("ordinary installation owner required")
	}
	if location != "" && location != "remote" && location != "local" {
		return packageInstanceRef{}, errors.New("location must be local or remote")
	}
	if instance != "" && !update.IDOK(instance) {
		return packageInstanceRef{}, errors.New("exact 32-character hex instance ID required")
	}
	if location != "" && instance != "" {
		return packageInstanceRef{Location: location, ID: instance}, nil
	}
	locations := []string{"remote", "local"}
	if location != "" {
		locations = []string{location}
	}
	var matches []packageInstanceRef
	for _, loc := range locations {
		parent := fmt.Sprintf("/var/lib/fased-app-package/%d/%s", uid, loc)
		entries, err := readDir(parent)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return packageInstanceRef{}, err
		}
		for _, entry := range entries {
			if !entry.IsDir() || !update.IDOK(entry.Name()) {
				continue
			}
			if instance != "" && entry.Name() != instance {
				continue
			}
			matches = append(matches, packageInstanceRef{Location: loc, ID: entry.Name()})
		}
	}
	if len(matches) == 0 {
		return packageInstanceRef{}, errors.New("no installed Format-4 package instance found")
	}
	if len(matches) > 1 {
		return packageInstanceRef{}, errors.New("multiple Format-4 package instances exist; specify --location and --instance")
	}
	return matches[0], nil
}

func hostSubcommand(args []string, out, errOut io.Writer) error {
	if len(args) == 0 || args[0] != "migrate-units" {
		return errors.New("usage: fased-app host migrate-units [--location local|remote] [--instance ID]")
	}
	f := flag.NewFlagSet("host migrate-units", flag.ContinueOnError)
	f.SetOutput(errOut)
	location := f.String("location", "", "package location: local or remote")
	instance := f.String("instance", "", "exact 32-character hex package instance ID")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected arguments after host migrate-units flags")
	}
	if *location != "" && *location != "local" && *location != "remote" {
		return errors.New("location must be local or remote")
	}
	if *instance != "" && !update.IDOK(*instance) {
		return errors.New("exact 32-character hex instance ID required")
	}
	return runHostMigrateUnits(*location, *instance, out, errOut)
}
