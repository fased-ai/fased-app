//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/maintenance"
)

type unitMigrationStep struct {
	Path       string
	LegacyHash string
	Hardened   []byte
}

func planPackageUnitMigration(m Manifest, read func(string) (hostFile, error)) ([]unitMigrationStep, error) {
	if err := m.validatePackage(); err != nil {
		return nil, err
	}
	oldDefs := legacyPackageDefinitions(m)
	newDefs := definitions(m)
	targets := []struct {
		path, roleFile string
	}{
		{filepath.Join(m.Root, "fased-app.service"), "fased-app.service"},
		{filepath.Join(m.Root, "fased-signer.service"), "fased-signer.service"},
		{filepath.Join("/etc/systemd/system", instanceUnit(m, "app")), "fased-app.service"},
		{filepath.Join("/etc/systemd/system", instanceUnit(m, "signer")), "fased-signer.service"},
	}
	var steps []unitMigrationStep
	for _, item := range targets {
		f, err := read(item.path)
		if err != nil {
			return nil, err
		}
		if known(f, newDefs[item.roleFile], 0644) {
			continue
		}
		if known(f, oldDefs[item.roleFile], 0644) {
			steps = append(steps, unitMigrationStep{
				Path:       item.path,
				LegacyHash: sum(oldDefs[item.roleFile]),
				Hardened:   bytesClone(newDefs[item.roleFile]),
			})
			continue
		}
		return nil, errors.New("existing unit differs from both legacy and hardened definitions; preserve it")
	}
	return steps, nil
}

func bytesClone(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// MigratePackageUnits upgrades pre-0.1.37 legacyPackageDefinitions unit files
// in place to hardened Format-4 definitions(m) while preserving state and keys.
// It returns migrated=false without restarting services when units are already hardened.
func MigratePackageUnits(ctx context.Context, owner int, location, instance string) (bool, error) {
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		return false, errors.New("administrator package unit migration required")
	}
	lock, err := maintenance.PackageLock(owner, location, instance)
	if err != nil {
		return false, err
	}
	migrated, err := migratePackageUnitsLocked(ctx, owner, location, instance)
	return migrated, errors.Join(err, lock.Close())
}

func migratePackageUnitsLocked(ctx context.Context, owner int, location, instance string) (bool, error) {
	if _, err := maintenance.ValidatePackageProcess(owner, location, instance); err != nil {
		return false, err
	}
	root, err := PackageRoot(owner, location, instance)
	if err != nil {
		return false, err
	}
	for _, guard := range []string{"update-active.json", "update-start-blocked.json"} {
		if _, err := os.Lstat(filepath.Join(root, guard)); !errors.Is(err, os.ErrNotExist) {
			return false, errors.New("finish pending update or recovery before migrating units")
		}
	}
	m, err := LoadPackageInstance(owner, root)
	if err != nil {
		return false, err
	}
	if err := VerifyPackageInstance(m); err != nil {
		return false, err
	}
	steps, err := planPackageUnitMigration(m, readRoot)
	if err != nil {
		return false, err
	}
	if len(steps) == 0 {
		return false, nil
	}
	stopUnits := []string{instanceUnit(m, "app"), instanceUnit(m, "signer")}
	if _, err := protectedSystemctl(ctx, append([]string{"stop", "--"}, stopUnits...)...); err != nil {
		return false, err
	}
	for _, step := range steps {
		if err := maintenance.ReplaceProtected(step.Path, step.Hardened, 0644, step.LegacyHash); err != nil {
			return false, err
		}
	}
	if _, err := protectedSystemctl(ctx, "daemon-reload"); err != nil {
		return false, err
	}
	if err := VerifyPackageInstance(m); err != nil {
		return false, err
	}
	original, commitErr := ReadEnrollmentCommit(m)
	if commitErr != nil && !errors.Is(commitErr, os.ErrNotExist) {
		return false, commitErr
	}
	if _, err := protectedSystemctl(ctx, append([]string{"start", "--"}, protectedInstanceUnits(m)...)...); err != nil {
		return false, err
	}
	healthCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	err = waitPackageHealth(healthCtx, m, func(h core.Health) bool {
		if commitErr == nil {
			return installedPackageOwnerHealthy(m, original, h)
		}
		return h.Status == "ok" && h.InstanceID != ""
	})
	return true, err
}
