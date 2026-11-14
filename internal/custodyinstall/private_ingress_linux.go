//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/privateaccess"
	"github.com/fased-ai/fased-app/internal/tailnet"
)

// ProtectedRemoteIngressReady is called only inside the installed helper. It
// binds the live protected listener to the original enrollment instance.
func ProtectedRemoteIngressReady(ctx context.Context, owner int) (string, error) {
	a, err := maintenance.ValidateProcess(owner)
	if err != nil || a.Profile != "remote" {
		return "", errors.New("protected Remote authority required")
	}
	m, err := Load(owner)
	if err != nil || m.Location != "remote" || m.PublicOrigin == "" || Verify(m) != nil {
		return "", errors.New("original Remote custody installation required")
	}
	commit, err := ReadEnrollmentCommit(m)
	if err != nil || commit.InstanceID == "" {
		return "", errors.New("original enrollment instance required")
	}
	if err = Runtime(m); err != nil {
		return "", err
	}
	if err = verifyProtectedLoadedServices(ctx, a, m); err != nil {
		return "", err
	}
	fields, err := loadedUnitPolicyFields(m, "app", definitions(m)["fased-app.service"], m.Root+"/fased-app")
	if err != nil {
		return "", err
	}
	drop, _, err := tailnet.ProtectedSocketDropIn("custody", owner)
	if err != nil {
		return "", err
	}
	found := false
	for _, path := range strings.Fields(fields["DropInPaths"]) {
		found = found || path == drop
	}
	if !found || fields["NeedDaemonReload"] != "no" || !tailnet.ProtectedSocketReady("custody", owner) {
		return "", errors.New("protected custody listener is not active")
	}
	path, err := tailnet.ProtectedSocket("custody", owner)
	if err != nil {
		return "", err
	}
	if err = privateaccess.ProbeProtectedIngress(ctx, m.PublicOrigin, owner, commit.InstanceID, func(ctx context.Context) (net.Conn, error) { return (&net.Dialer{}).DialContext(ctx, "unix", path) }); err != nil {
		return "", err
	}
	return commit.InstanceID, nil
}

type protectedIngressOps struct {
	probe   func(context.Context, Manifest) error
	active  func(context.Context, Manifest) (bool, error)
	read    readHost
	ensure  func(string) error
	publish func(string, []byte, os.FileMode) error
}

// prepareProtectedEnrollmentIngress publishes only the fixed socket and
// service inputs. It never changes a Serve route or an active installation.
func prepareProtectedEnrollmentIngress(ctx context.Context, m Manifest, o protectedIngressOps) error {
	if m.Owner <= 0 || m.Location != "remote" || m.PublicOrigin == "" || o.probe == nil || o.active == nil || o.read == nil || o.ensure == nil || o.publish == nil {
		return errors.New("exact Remote enrollment ingress required")
	}
	if err := o.probe(ctx, m); err != nil {
		return err
	}
	name, socket, err := tailnet.ProtectedSocketUnit("custody", m.Owner)
	if err != nil {
		return err
	}
	drop, body, err := tailnet.ProtectedSocketDropIn("custody", m.Owner)
	if err != nil {
		return err
	}
	unit := "/etc/systemd/system/" + name
	present := map[string]bool{}
	for _, entry := range []struct {
		path string
		body []byte
	}{{unit, socket}, {drop, body}} {
		f, err := o.read(entry.path)
		if err == nil && !known(f, entry.body, 0644) || err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("foreign protected custody ingress; preserving it")
		}
		present[entry.path] = err == nil
	}
	active, err := o.active(ctx, m)
	if err != nil {
		return err
	}
	if active {
		if !present[unit] || !present[drop] {
			return errors.New("active custody service requires a guarded ingress transition")
		}
		return nil
	}
	if err = o.ensure(filepath.Dir(drop)); err != nil {
		return err
	}
	if err = o.publish(unit, socket, 0644); err != nil {
		return err
	}
	return o.publish(drop, body, 0644)
}

func protectedEnrollmentIngressOps() protectedIngressOps {
	return protectedIngressOps{
		read: readRoot, ensure: maintenance.EnsureProtectedDirectory, publish: maintenance.PublishProtected,
		probe: func(ctx context.Context, m Manifest) error {
			child, err := startMaintenanceChild(ctx, m, "app", m.Root+"/fased-app", "private-ingress-capability")
			if err != nil {
				return err
			}
			_ = child.in.Close()
			b, readErr := child.out.ReadSlice('\n')
			if err = errors.Join(readErr, child.close()); err != nil || !bytes.Equal(b, []byte("fased-protected-ingress-v1\n")) {
				return errors.New("verified custody core cannot accept the protected listener")
			}
			return nil
		},
		active: func(ctx context.Context, m Manifest) (bool, error) {
			b, err := protectedSystemctl(ctx, "show", Unit(m.Owner, "app"), "--property=LoadState,ActiveState")
			if err != nil {
				return false, err
			}
			state := ""
			for _, line := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(line, "ActiveState=") {
					if state != "" {
						return false, errors.New("ambiguous custody service state")
					}
					state = strings.TrimPrefix(line, "ActiveState=")
				}
			}
			switch state {
			case "inactive", "failed":
				return false, nil
			case "active", "activating", "reloading", "deactivating":
				return true, nil
			default:
				return false, errors.New("custody service state is unavailable")
			}
		},
	}
}
