//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

// Only the system package command selects a package instance automatically.
// Older commands and an explicitly selected data directory keep their target.
func defaultPackageLocalData(fallback string, explicit bool) (string, error) {
	if explicit {
		return fallback, nil
	}
	image, err := os.Executable()
	if err != nil {
		return "", err
	}
	if image != "/usr/lib/fased-app/fased-app" {
		return fallback, nil
	}
	if os.Getuid() <= 0 || os.Getuid() != os.Geteuid() || signer.VerifyExecutablePath(image) != nil {
		return "", errors.New("the installed package must be opened by its ordinary OS owner")
	}
	owner := os.Getuid()
	parent := filepath.Join("/var/lib/fased-app-package", strconv.Itoa(owner), "local")
	entries, err := os.ReadDir(parent)
	if errors.Is(err, os.ErrNotExist) {
		return "", errors.New("no Local package instance is installed")
	}
	if err != nil {
		return "", err
	}
	return selectPackageLocalData(owner, entries, custodyinstall.LoadPackageInstance)
}

// Never guess between instances or adopt a malformed, foreign or pending
// directory. The supplied loader reads only the protected instance manifest.
func selectPackageLocalData(owner int, entries []os.DirEntry, load func(int, string) (custodyinstall.Manifest, error)) (string, error) {
	if owner <= 0 || load == nil {
		return "", errors.New("ordinary package owner required")
	}
	var selected string
	for _, entry := range entries {
		root, err := update.PackageInstanceRoot(owner, "local", entry.Name())
		if err != nil || entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			return "", errors.New("unclassified Local package instance; preserve it")
		}
		m, err := load(owner, root)
		if err != nil || m.Layout != custodyinstall.PackageLayout || m.Root != root || m.Owner != owner || m.Location != "local" || m.ID != entry.Name() {
			return "", errors.New("Local package instance cannot be verified; preserve it")
		}
		if selected != "" {
			return "", errors.New("multiple Local package instances; select one with --data-dir")
		}
		selected = filepath.Join(root, "app-state")
	}
	if selected == "" {
		return "", errors.New("no Local package instance is installed")
	}
	return selected, nil
}
