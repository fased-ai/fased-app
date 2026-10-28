//go:build linux

package vpsinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

func networkPersistRequirementPaths(m Manifest, a networkAttempt) []string {
	name := networkUnit(m, a.ID, "persist")
	return []string{"/etc/systemd/system/ssh.socket.requires/" + name, "/etc/systemd/system/ssh.service.requires/" + name}
}

// Required dependencies gate both socket and daemon activation on a successful
// firewall boot job. A foreign link is never adopted or removed.
func (n networkController) persistRequirements(m Manifest, a networkAttempt, required bool) error {
	target := "/etc/systemd/system/" + networkUnit(m, a.ID, "persist")
	for _, path := range networkPersistRequirementPaths(m, a) {
		dir := filepath.Dir(path)
		if _, e := n.ops.read(dir); e == nil {
			if e = parents(n.ops, dir); e != nil {
				return e
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		} else if e = parents(n.ops, filepath.Dir(dir)); e != nil {
			return e
		}
		link, e := n.ops.read(path)
		if errors.Is(e, os.ErrNotExist) && !required {
			continue
		}
		if e != nil {
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
	if e := n.persistRequirements(m, a, false); e != nil {
		return e
	}
	copy := m
	copy.Unit = networkUnit(m, a.ID, "persist")
	copy.UnitPath = "/etc/systemd/system/" + copy.Unit
	if e := enableLink(n.ops, copy); e != nil {
		return e
	}
	paths := append(networkPersistRequirementPaths(m, a), "/etc/systemd/system/multi-user.target.wants/"+copy.Unit)
	for _, path := range paths {
		if _, e := n.ops.read(path); errors.Is(e, os.ErrNotExist) {
			continue
		} else if e != nil {
			return e
		}
		if _, e := n.ops.run(ctx, nil, "/usr/bin/rm", "--", path); e != nil {
			return e
		}
		if _, e := n.ops.run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(path)); e != nil {
			return e
		}
	}
	if _, e := n.ops.run(ctx, nil, "/usr/bin/systemctl", "daemon-reload"); e != nil {
		return e
	}
	for _, path := range networkPersistRequirementPaths(m, a) {
		if _, e := n.ops.read(path); !errors.Is(e, os.ErrNotExist) {
			return errors.New("SSH firewall dependency removal remains incomplete")
		}
	}
	return nil
}
