//go:build linux

package vpsnetwork

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

// FromPackageManifest projects an verified Format-4 Remote package instance
// into the network lockdown controller without altering live storage paths.
func FromPackageManifest(pm custodyinstall.Manifest) (Manifest, error) {
	if pm.Layout != custodyinstall.PackageLayout || pm.Location != "remote" || pm.Owner <= 0 || pm.PublicOrigin == "" || pm.Listen == "" {
		return Manifest{}, errors.New("exact Remote package installation required")
	}
	if err := custodyinstall.VerifyPackageInstance(pm); err != nil {
		return Manifest{}, err
	}
	if os.Getuid() != pm.Owner || os.Geteuid() != os.Getuid() {
		return Manifest{}, errors.New("Remote installation belongs to another OS owner")
	}
	unit, err := update.PackageInstanceUnit(pm.Owner, "remote", pm.ID, "app")
	if err != nil {
		return Manifest{}, err
	}
	root := filepath.Clean(pm.Root)
	projected := Manifest{
		Version:      1,
		ID:           pm.ID,
		Phase:        packageRemotePhase,
		OwnerUID:     pm.Owner,
		OwnerGID:     os.Getgid(),
		AppUID:       pm.App.UID,
		AppGID:       pm.App.GID,
		AppUser:      pm.App.Name,
		Root:         root,
		Data:         filepath.Join(root, "app-state"),
		Core:         filepath.Join(root, "fased-app"),
		Config:       filepath.Join(root, "install.json"),
		Unit:         unit,
		UnitPath:     "/etc/systemd/system/" + unit,
		Listen:       pm.Listen,
		PublicOrigin: pm.PublicOrigin,
		CoreSHA256:   pm.CoreHash,
	}
	if err := projected.validate(); err != nil {
		return Manifest{}, err
	}
	return projected, nil
}

// Load resolves an owner's immutable Format-4 Remote package instance by
// explicit --data-dir or unique installed remote directory.
func Load(owner int, dataDir string) (Manifest, error) {
	if owner <= 0 || os.Getuid() != owner || os.Geteuid() != owner {
		return Manifest{}, errors.New("ordinary installation owner required")
	}
	if dataDir != "" {
		abs, err := filepath.Abs(dataDir)
		if err != nil || filepath.Base(abs) != "app-state" {
			return Manifest{}, errors.New("exact Remote package app-state directory required")
		}
		pm, err := custodyinstall.LoadPackageInstance(owner, filepath.Dir(abs))
		if err != nil {
			return Manifest{}, err
		}
		return FromPackageManifest(pm)
	}
	parent := fmt.Sprintf("/var/lib/fased-app-package/%d/remote", owner)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return Manifest{}, err
	}
	var found []custodyinstall.Manifest
	for _, entry := range entries {
		if !entry.IsDir() || !update.IDOK(entry.Name()) {
			continue
		}
		pm, err := custodyinstall.LoadPackageInstance(owner, filepath.Join(parent, entry.Name()))
		if err != nil {
			return Manifest{}, err
		}
		found = append(found, pm)
	}
	if len(found) == 0 {
		return Manifest{}, os.ErrNotExist
	}
	if len(found) > 1 {
		return Manifest{}, errors.New("multiple Remote package instances exist; select one with --data-dir")
	}
	return FromPackageManifest(found[0])
}

func verifyPackageInstallation(m Manifest) (custodyinstall.Manifest, error) {
	if err := m.validate(); err != nil {
		return custodyinstall.Manifest{}, err
	}
	pm, err := custodyinstall.LoadPackageInstance(m.OwnerUID, m.Root)
	if err != nil {
		return custodyinstall.Manifest{}, err
	}
	if err := custodyinstall.VerifyPackageInstance(pm); err != nil {
		return custodyinstall.Manifest{}, err
	}
	if pm.Layout != custodyinstall.PackageLayout || pm.Location != "remote" || pm.ID != m.ID ||
		pm.PublicOrigin != m.PublicOrigin || pm.Listen != m.Listen ||
		pm.App.UID != m.AppUID || pm.App.GID != m.AppGID || pm.App.Name != m.AppUser || pm.CoreHash != m.CoreSHA256 {
		return custodyinstall.Manifest{}, errors.New("Remote package installation changed; preserve it")
	}
	return pm, nil
}
