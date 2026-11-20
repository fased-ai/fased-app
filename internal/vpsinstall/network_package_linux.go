//go:build linux

package vpsinstall

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

const packageRemotePhase = "package-remote"

// PackageNetworkManifest projects an immutable format-4 Remote package instance
// into the network lockdown controller without altering legacy storage paths.
func PackageNetworkManifest(m custodyinstall.Manifest) (Manifest, error) {
	if m.Layout != custodyinstall.PackageLayout || m.Location != "remote" || m.Owner <= 0 || m.PublicOrigin == "" || m.Listen == "" {
		return Manifest{}, errors.New("exact Remote package installation required")
	}
	if err := custodyinstall.VerifyPackageInstance(m); err != nil {
		return Manifest{}, err
	}
	if os.Getuid() != m.Owner || os.Geteuid() != os.Getuid() {
		return Manifest{}, errors.New("Remote installation belongs to another OS owner")
	}
	unit, err := update.PackageInstanceUnit(m.Owner, "remote", m.ID, "app")
	if err != nil {
		return Manifest{}, err
	}
	root := filepath.Clean(m.Root)
	projected := Manifest{
		Version:      1,
		ID:           m.ID,
		Phase:        packageRemotePhase,
		OwnerUID:     m.Owner,
		OwnerGID:     os.Getgid(),
		AppUID:       m.App.UID,
		AppGID:       m.App.GID,
		AppUser:      m.App.Name,
		Root:         root,
		Data:         filepath.Join(root, "app-state"),
		Core:         filepath.Join(root, "fased-app"),
		Config:       filepath.Join(root, "install.json"),
		Unit:         unit,
		UnitPath:     "/etc/systemd/system/" + unit,
		Listen:       m.Listen,
		PublicOrigin: m.PublicOrigin,
		CoreSHA256:   m.CoreHash,
	}
	if err := projected.validatePackageNetwork(); err != nil {
		return Manifest{}, err
	}
	return projected, nil
}

// LoadPackageNetwork resolves an owner's immutable format-4 Remote package
// instance by explicit --data-dir or unique installed remote directory.
func LoadPackageNetwork(owner int, dataDir string) (Manifest, error) {
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
		return PackageNetworkManifest(pm)
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
	return PackageNetworkManifest(found[0])
}

func isPackageRemoteManifest(m Manifest) bool {
	return m.Phase == packageRemotePhase
}

func (m Manifest) validatePackageNetwork() error {
	if m.Version != 1 || m.Phase != packageRemotePhase || !canonicalHex(m.ID, 16) || !canonicalHex(m.CoreSHA256, 32) ||
		m.OwnerUID <= 0 || m.OwnerGID <= 0 || m.AppUID <= 0 || m.AppGID <= 0 || m.AppUID == m.OwnerUID || m.AppGID == m.OwnerGID || m.AppUser == "" {
		return errors.New("invalid Remote package network identity")
	}
	root, err := update.PackageInstanceRoot(m.OwnerUID, "remote", m.ID)
	if err != nil || m.Root != root || m.Data != root+"/app-state" || m.Core != root+"/fased-app" || m.Config != root+"/install.json" {
		return errors.New("invalid Remote package paths")
	}
	unit, err := update.PackageInstanceUnit(m.OwnerUID, "remote", m.ID, "app")
	if err != nil || m.Unit != unit || m.UnitPath != "/etc/systemd/system/"+unit || m.PublicOrigin == "" {
		return errors.New("invalid Remote package service binding")
	}
	return validateEndpoint(m.Listen, m.PublicOrigin)
}

func ownedByNetwork(o hostOps, m Manifest) error {
	if isPackageRemoteManifest(m) {
		if e := ordinary(o); e != nil {
			return e
		}
		if m.OwnerUID != o.uid {
			return errors.New("Remote installation belongs to another OS owner")
		}
		return m.validatePackageNetwork()
	}
	return ownedBy(o, m)
}

func verifyPackageNetworkInstallation(m Manifest) (custodyinstall.Manifest, error) {
	if err := m.validatePackageNetwork(); err != nil {
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
