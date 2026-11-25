//go:build linux

package vpsnetwork

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

func networkPersistRequirementPaths(m Manifest, a networkAttempt) []string {
	name := networkUnit(m, a.ID, "persist")
	return []string{
		"/etc/systemd/system/" + a.sshSocket() + ".requires/" + name,
		"/etc/systemd/system/" + a.sshService() + ".requires/" + name,
	}
}

// Required dependencies gate both socket and daemon activation on a successful
// firewall boot job. A foreign link is never adopted or removed.
func (n networkController) persistRequirements(m Manifest, a networkAttempt, required bool) error {
	target := "/etc/systemd/system/" + networkUnit(m, a.ID, "persist")
	for _, path := range networkPersistRequirementPaths(m, a) {
		dir := filepath.Dir(path)
		if _, err := n.ops.read(dir); err == nil {
			if err = parents(n.ops, dir); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		} else if err = parents(n.ops, filepath.Dir(dir)); err != nil {
			return err
		}
		link, err := n.ops.read(path)
		if errors.Is(err, os.ErrNotExist) && !required {
			continue
		}
		if err != nil {
			return errors.New("owned SSH firewall dependency is absent or unreadable")
		}
		resolved := link.Link
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(dir, resolved)
		}
		if link.UID != 0 || link.GID != 0 || link.Mode&os.ModeSymlink == 0 || filepath.Clean(resolved) != target {
			return errors.New("foreign SSH firewall dependency; preserve it and inspect via console")
		}
	}
	return nil
}

// Remove only this attempt's verified dependencies. systemctl disable can also
// remove administrator-created links elsewhere. No persist/SSH stop is issued:
// stopping a required active persist unit could stop its requiring SSH units.
func (n networkController) removePersistRequirements(ctx context.Context, m Manifest, a networkAttempt) error {
	if err := n.persistRequirements(m, a, false); err != nil {
		return err
	}
	persistUnit := networkUnit(m, a.ID, "persist")
	persistPath := "/etc/systemd/system/" + persistUnit
	if err := enableLink(n.ops, persistUnit, persistPath); err != nil {
		return err
	}
	paths := append(networkPersistRequirementPaths(m, a), "/etc/systemd/system/multi-user.target.wants/"+persistUnit)
	for _, path := range paths {
		if _, err := n.ops.read(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if _, err := n.ops.run(ctx, nil, "/usr/bin/rm", "--", path); err != nil {
			return err
		}
		if _, err := n.ops.run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(path)); err != nil {
			return err
		}
	}
	if _, err := n.ops.run(ctx, nil, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return err
	}
	for _, path := range networkPersistRequirementPaths(m, a) {
		if _, err := n.ops.read(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("SSH firewall dependency removal remains incomplete")
		}
	}
	return nil
}
