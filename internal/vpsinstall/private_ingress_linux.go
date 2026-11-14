//go:build linux

package vpsinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/fased-ai/fased-app/internal/tailnet"
)

// prepareProtectedIngress publishes only fixed, root-owned service inputs.
// It runs under the existing installer lock and never changes a Serve route.
func prepareProtectedIngress(ctx context.Context, o hostOps, m Manifest) error {
	if m.validate() != nil || m.OwnerUID != o.uid || m.PublicOrigin == "" || o.protectedPublish == nil || o.ingressContract == nil {
		return errors.New("verified Remote installer and protected ingress required")
	}
	if err := o.ingressContract(ctx, m); err != nil {
		return err
	}
	name, socket, err := tailnet.ProtectedSocketUnit("vps", m.OwnerUID)
	if err != nil {
		return err
	}
	drop, body, err := tailnet.ProtectedSocketDropIn("vps", m.OwnerUID)
	if err != nil {
		return err
	}
	if err := parents(o, "/etc/systemd/system"); err != nil {
		return err
	}
	unit := "/etc/systemd/system/" + name
	dir := filepath.Dir(drop)
	dirExists := true
	if f, e := o.read(dir); errors.Is(e, os.ErrNotExist) {
		dirExists = false
	} else if e != nil || !f.Directory || f.UID != 0 || f.GID != 0 || f.Mode&os.ModeSymlink != 0 || f.Mode.Perm() != 0755 {
		return errors.New("foreign private ingress directory; preserving it")
	}
	present := map[string]bool{}
	for _, entry := range []struct {
		path string
		data []byte
	}{{unit, socket}, {drop, body}} {
		if entry.path == drop && !dirExists {
			continue
		}
		f, err := rootFile(o, entry.path)
		if err == nil && !known(f, entry.data, 0644) || err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("foreign protected ingress file; preserving it")
		}
		present[entry.path] = err == nil
	}
	fields, err := unitFields(ctx, o, m)
	if err != nil {
		return err
	}
	switch fields["ActiveState"] {
	case "", "inactive", "failed":
	case "active":
		found := false
		for _, path := range strings.Fields(fields["DropInPaths"]) {
			found = found || path == drop
		}
		if !found || !present[unit] || !present[drop] || fields["NeedDaemonReload"] != "no" {
			return errors.New("existing Remote service needs a guarded ingress transition")
		}
		return nil
	default:
		return errors.New("Remote service is changing; preserve its current ingress")
	}
	if err := directory(ctx, o, dir, 0, 0, 0755); err != nil {
		return err
	}
	// The listener precedes the drop-in; a crash between publications leaves
	// no service referencing an incomplete unit. Both writes are idempotent.
	if err := publish(ctx, o, unit, m.ID, socket, 0644); err != nil {
		return err
	}
	return publish(ctx, o, drop, m.ID, body, 0644)
}
