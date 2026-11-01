//go:build linux

package localinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/godbus/dbus/v5"
)

func rootMaintenanceName(uid int, id string) string {
	return fmt.Sprintf("fased-local-maintenance-%d@%s.service", uid, id)
}
func PrepareApplicationMaintenanceOwner(ctx context.Context, m Manifest, in io.Reader, out io.Writer) error {
	system := &custodyinstall.System{In: in, Out: out, Authorization: "Authorize Local update maintenance: a restricted system service running as your ordinary user. Fased never runs as root. Automatic updates remain off."}
	return prepareMaintenanceOwner(ctx, m, func(ctx context.Context, m Manifest) error {
		return CheckApplicationMaintenanceHost(ctx, m, strings.Repeat("0", 32))
	}, func(ctx context.Context, m Manifest) error {
		return PrepareApplicationMaintenanceHost(ctx, m, system.Run)
	})
}
func prepareMaintenanceOwner(ctx context.Context, m Manifest, check, prepare func(context.Context, Manifest) error) error {
	_, e := core.ReadPrivateFile(maintenanceHostProofPath(m))
	if e == nil {
		return check(ctx, m)
	}
	if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return prepare(ctx, m)
}
func rootMaintenancePath(uid int) string {
	return "/etc/systemd/system/" + rootMaintenanceName(uid, "")
}
func rootRecoveryPath(uid int) string {
	return fmt.Sprintf("/etc/systemd/system/fased-local-recovery-%d@.service", uid)
}
func rootRecoveryTemplate(m Manifest, uid, gid int, home, config string) []byte {
	return bytes.Replace(rootMaintenanceTemplate(m, uid, gid, home, config), []byte("maintain-update --operation %i"), []byte("maintenance-recover --attempt %i"), 1)
}
func rootMaintenancePolicyPath(uid int) string {
	return fmt.Sprintf("/etc/polkit-1/rules.d/60-fased-local-%d-updates.rules", uid)
}
func rootMaintenanceTemplate(m Manifest, uid, gid int, home, config string) []byte {
	control := applicationUpdateDirectory(Paths{Root: m.Root, Data: m.Data})
	return []byte(fmt.Sprintf("[Unit]\nDescription=Fased Local update maintenance\nAfter=network.target polkit.service\n\n[Service]\nType=oneshot\nRemainAfterExit=yes\nUser=%d\nGroup=%d\nExecStart=%s maintain-update --operation %%i --prefix %s --data-dir %s\nTimeoutStartSec=15min\nRestart=no\nUMask=0077\nNoNewPrivileges=yes\nCapabilityBoundingSet=\nAmbientCapabilities=\nRestrictSUIDSGID=yes\nLockPersonality=yes\nPrivateUsers=no\nPrivateTmp=yes\nProtectSystem=strict\nProtectHome=read-only\nReadWritePaths=%s %s %s\nWorkingDirectory=/\nEnvironment=%s\nEnvironment=%s\nEnvironment=XDG_RUNTIME_DIR=/run/user/%d\nEnvironment=DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/%d/bus\n", uid, gid, quote(maintenanceExecutable(m)), quote(m.Root), quote(m.Data), quote(m.Root), quote(m.Data), quote(control), quote("HOME="+home), quote("XDG_CONFIG_HOME="+config), uid, uid))
}
func rootMaintenancePolicy(uid int, name string) []byte {
	return []byte(fmt.Sprintf("polkit.addRule(function(action, subject) {\n  if (subject.user === %q && action.id === 'org.freedesktop.systemd1.manage-units' && action.lookup('verb') === 'start' && /^fased-local-maintenance-%d@[0-9a-f]{32}\\.service$/.test(action.lookup('unit') || '')) return polkit.Result.YES;\n});\n", name, uid))
}

// Explicit owner/installer preparation only. OS tools publish fixed policy;
// systemd drops to the ordinary owner before executing any Fased image.
func PrepareApplicationMaintenanceHost(ctx context.Context, m Manifest, run func(context.Context, []byte, ...string) ([]byte, error)) error {
	if run == nil || os.Getuid() <= 0 || os.Getuid() != os.Geteuid() {
		return errors.New("ordinary installer owner and OS authorization required")
	}
	if e := maintenanceIdentity(m); e != nil {
		return e
	}
	if e := checkMaintenanceImage(m); e != nil {
		return e
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	config, e := os.UserConfigDir()
	if e != nil {
		return e
	}
	u, e := user.Current()
	if e != nil || !regexp.MustCompile(`^[a-z_][a-z0-9_-]*\$?$`).MatchString(u.Username) {
		return errors.New("fixed ordinary owner account required")
	}
	if _, e = run(ctx, nil, "/usr/bin/apt-get", "install", "--yes", "--no-install-recommends", "polkitd"); e != nil {
		return e
	}
	for _, p := range []struct {
		path string
		body []byte
	}{{rootMaintenancePath(os.Getuid()), rootMaintenanceTemplate(m, os.Getuid(), os.Getgid(), home, config)}, {rootRecoveryPath(os.Getuid()), rootRecoveryTemplate(m, os.Getuid(), os.Getgid(), home, config)}, {rootMaintenancePolicyPath(os.Getuid()), rootMaintenancePolicy(os.Getuid(), u.Username)}} {
		if e = publishRootMaintenanceFile(ctx, p.path, p.body, run); e != nil {
			return e
		}
	}
	if _, e = run(ctx, nil, "/usr/bin/systemctl", "daemon-reload"); e != nil {
		return e
	}
	if _, e = run(ctx, nil, "/usr/bin/pkcheck", "--action-id", "org.freedesktop.systemd1.manage-units", "--process", strconv.Itoa(os.Getpid()), "--detail", "unit", rootMaintenanceName(os.Getuid(), strings.Repeat("0", 32)), "--detail", "verb", "start"); e != nil {
		return errors.New("Local maintenance permission verification failed; preserve installation and rerun owner setup")
	}
	proof, e := expectedMaintenanceHostProof(m)
	if e != nil {
		return e
	}
	return core.WritePrivateJSON(maintenanceHostProofPath(m), proof)
}
func publishRootMaintenanceFile(ctx context.Context, path string, body []byte, run func(context.Context, []byte, ...string) ([]byte, error)) error {
	if e := rootMaintenanceParents(filepath.Dir(path)); e != nil {
		return e
	}
	// rules.d may be root:polkitd0750. Inspect only this exact public file.
	meta, e := run(ctx, nil, "/usr/bin/find", filepath.Dir(path), "-maxdepth", "1", "-name", filepath.Base(path), "-printf", "%U:%G:%m:%y")
	if e != nil {
		return e
	}
	if len(meta) > 0 {
		if string(meta) != "0:0:644:f" {
			return errors.New("foreign Local maintenance policy; preserve it")
		}
		b, e := run(ctx, nil, "/usr/bin/cat", "--", path)
		if e != nil {
			return e
		}
		if !bytes.Equal(b, body) {
			return errors.New("Local maintenance policy differs; preserve it")
		}
		return nil
	}
	// Exclusive hard-link publication; never overwrite an existing destination.
	pending := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+"-"+bytesDigest(body)[:32]+".pending")
	meta, e = run(ctx, nil, "/usr/bin/find", filepath.Dir(path), "-maxdepth", "1", "-name", filepath.Base(pending), "-printf", "%U:%G:%m:%y")
	if e != nil {
		return e
	}
	if len(meta) > 0 {
		if string(meta) != "0:0:644:f" {
			return errors.New("foreign Local maintenance staging")
		}
		b, e := run(ctx, nil, "/usr/bin/cat", "--", pending)
		if e != nil || !bytes.Equal(b, body) {
			return errors.New("changed Local maintenance staging")
		}
	} else if _, e = run(ctx, body, "/usr/bin/install", "-m", "644", "-o", "root", "-g", "root", "--", "/dev/stdin", pending); e != nil {
		return e
	}
	if _, e = run(ctx, nil, "/usr/bin/sync", "-f", pending); e != nil {
		return e
	}
	if _, e = run(ctx, nil, "/usr/bin/ln", "--no-target-directory", "--", pending, path); e != nil {
		return e
	}
	if _, e = run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(path)); e != nil {
		return e
	}
	_, e = run(ctx, nil, "/usr/bin/rm", "--", pending)
	return e
}
func rootMaintenanceParents(path string) error {
	for p := path; ; p = filepath.Dir(p) {
		i, e := os.Lstat(p)
		if e != nil {
			return e
		}
		s, ok := i.Sys().(*syscall.Stat_t)
		if !ok || s.Uid != 0 || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm()&0022 != 0 {
			return errors.New("maintenance policy ancestors must be protected and root-owned")
		}
		if p == "/" {
			return nil
		}
	}
}

// The user unit is the persistent delegate; only its exact root-manager actor
// performs maintenance. No unit-file, transient-unit or property grant exists.
func DelegateApplicationMaintenance(ctx context.Context, m Manifest, id string) error {
	if !applicationUpdateID(id) {
		return errors.New("exact existing update operation required")
	}
	if e := maintenanceIdentity(m); e != nil {
		return e
	}
	if e := checkMaintenanceImage(m); e != nil {
		return e
	}
	if e := ValidateApplicationMaintenanceProcess(); e != nil {
		return e
	}
	return runRootMaintenance(ctx, m, id, "", nil, false)
}
func CheckApplicationMaintenanceHost(ctx context.Context, m Manifest, id string) error {
	return runRootMaintenance(ctx, m, id, "", nil, true)
}
func recoverIsolatedMaintenance(ctx context.Context, m Manifest, id, attempt string, system *custodyinstall.System) error {
	return runRootMaintenance(ctx, m, id, attempt, func(unit string) error {
		_, e := system.Run(ctx, nil, "/usr/bin/systemctl", "start", "--", unit)
		return e
	}, false)
}
func runRootMaintenance(ctx context.Context, m Manifest, id, attempt string, authorizedStart func(string) error, checkOnly bool) error {
	if os.Getuid() <= 0 || os.Getuid() != os.Geteuid() || !applicationUpdateID(id) || attempt != "" && !applicationUpdateID(attempt) {
		return errors.New("ordinary owner and exact maintenance invocation required")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	dialCtx, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	raw, e := (&net.Dialer{}).DialContext(dialCtx, "unix", "/run/dbus/system_bus_socket")
	if e != nil {
		return e
	}
	deadline, _ := dialCtx.Deadline()
	if e = raw.SetDeadline(deadline); e != nil {
		raw.Close()
		return e
	}
	conn, e := dbus.NewConn(raw, dbus.WithContext(ctx))
	if e != nil {
		raw.Close()
		return e
	}
	defer conn.Close()
	if e = conn.Auth([]dbus.Auth{dbus.AuthExternal(strconv.Itoa(os.Getuid()))}); e != nil {
		return e
	}
	if e = conn.Hello(); e != nil {
		return e
	}
	if e = raw.SetDeadline(time.Time{}); e != nil {
		return e
	}
	manager := conn.Object("org.freedesktop.systemd1", "/org/freedesktop/systemd1")
	if e = manager.CallWithContext(dialCtx, "org.freedesktop.systemd1.Manager.Subscribe", 0).Err; e != nil {
		return e
	}
	var path dbus.ObjectPath
	unit := rootMaintenanceName(os.Getuid(), id)
	if attempt != "" {
		unit = fmt.Sprintf("fased-local-recovery-%d@%s-%s.service", os.Getuid(), id, attempt)
	}
	if e = manager.CallWithContext(dialCtx, "org.freedesktop.systemd1.Manager.LoadUnit", 0, unit).Store(&path); e != nil {
		return e
	}
	object := conn.Object("org.freedesktop.systemd1", path)
	var props, service map[string]dbus.Variant
	inspect := func() error {
		q, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if e := object.CallWithContext(q, "org.freedesktop.DBus.Properties.GetAll", 0, "org.freedesktop.systemd1.Unit").Store(&props); e != nil {
			return e
		}
		if e := object.CallWithContext(q, "org.freedesktop.DBus.Properties.GetAll", 0, "org.freedesktop.systemd1.Service").Store(&service); e != nil {
			return e
		}
		if e := validateRootMaintenanceRuntime(m, id, attempt, props, service); e != nil {
			return e
		}
		return checkRootMaintenanceDefinition(m, attempt != "")
	}
	if e = inspect(); e != nil {
		return e
	}
	if attempt == "" {
		if e = checkPreparedMaintenanceHost(m); e != nil {
			return e
		}
	}
	if checkOnly {
		return nil
	}
	// RemainAfterExit makes a repeated same-ID StartUnit a no-op. Never reset a
	// failed/completed root actor automatically; owner recovery is explicit.
	if props["ActiveState"].Value() == "failed" || service["Result"].Value() != "success" {
		return errors.New("Local owner maintenance requires explicit recovery")
	}
	if authorizedStart != nil {
		if e = authorizedStart(unit); e != nil {
			return e
		}
		if e = inspect(); e != nil {
			return e
		}
		if props["ActiveState"].Value() != "active" || props["SubState"].Value() != "exited" || service["Result"].Value() != "success" {
			return errors.New("isolated owner recovery did not complete normally")
		}
		return nil
	}
	var sender string
	if e = conn.BusObject().CallWithContext(dialCtx, "org.freedesktop.DBus.GetNameOwner", 0, "org.freedesktop.systemd1").Store(&sender); e != nil {
		return e
	}
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	if e = conn.AddMatchSignal(dbus.WithMatchSender("org.freedesktop.systemd1"), dbus.WithMatchObjectPath("/org/freedesktop/systemd1"), dbus.WithMatchInterface("org.freedesktop.systemd1.Manager"), dbus.WithMatchMember("JobRemoved")); e != nil {
		return e
	}
	var job dbus.ObjectPath
	if e = manager.CallWithContext(dialCtx, "org.freedesktop.systemd1.Manager.StartUnit", 0, unit, "replace").Store(&job); e != nil {
		return e
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case signal := <-signals:
			if signal == nil {
				return errors.New("Local system manager disconnected; preserve its update")
			}
			if signal.Sender != sender || signal.Name != "org.freedesktop.systemd1.Manager.JobRemoved" || len(signal.Body) != 4 {
				continue
			}
			p, ok := signal.Body[1].(dbus.ObjectPath)
			if !ok || p != job {
				continue
			}
			if signal.Body[2] != unit || signal.Body[3] != "done" {
				return errors.New("Local isolated maintenance failed; preserve its original operation")
			}
			if e = inspect(); e != nil {
				return e
			}
			if props["ActiveState"].Value() != "active" || props["SubState"].Value() != "exited" || service["Result"].Value() != "success" {
				return errors.New("Local isolated maintenance did not complete normally")
			}
			return nil
		}
	}
}

func validateRootMaintenanceRuntime(m Manifest, id, attempt string, p, s map[string]dbus.Variant) error {
	if attempt != "" && !applicationUpdateID(attempt) {
		return errors.New("invalid recovery invocation")
	}
	definition := rootMaintenancePath(os.Getuid())
	if attempt != "" {
		definition = rootRecoveryPath(os.Getuid())
	}
	if !applicationUpdateID(id) || p["LoadState"].Value() != "loaded" || p["FragmentPath"].Value() != definition || s["User"].Value() != strconv.Itoa(os.Getuid()) || s["Group"].Value() != strconv.Itoa(os.Getgid()) || s["NoNewPrivileges"].Value() != true || s["WorkingDirectory"].Value() != "/" {
		return errors.New("unexpected Local system maintenance identity")
	}
	if s["CapabilityBoundingSet"].Value() != uint64(0) || s["AmbientCapabilities"].Value() != uint64(0) || s["ProtectSystem"].Value() != "strict" || s["ProtectHome"].Value() != "read-only" {
		return errors.New("Local maintenance protections changed")
	}
	writable, ok := s["ReadWritePaths"].Value().([]string)
	if !ok || strings.Join(writable, "\x00") != strings.Join([]string{m.Root, m.Data, applicationUpdateDirectory(Paths{Root: m.Root, Data: m.Data})}, "\x00") {
		return errors.New("Local maintenance writable paths changed")
	}
	drops, ok := p["DropInPaths"].Value().([]string)
	if !ok || len(drops) != 0 {
		return errors.New("Local maintenance overrides refused")
	}
	c, e := decodeMaintenanceBusExec(s["ExecStart"].Value())
	if e != nil {
		return e
	}
	want := []string{maintenanceExecutable(m), "maintain-update", "--operation", id, "--prefix", m.Root, "--data-dir", m.Data}
	if attempt != "" {
		want = []string{maintenanceExecutable(m), "maintenance-recover", "--attempt", id + "-" + attempt, "--prefix", m.Root, "--data-dir", m.Data}
	}
	if c.Ignore || c.Path != maintenanceExecutable(m) || strings.Join(c.Args, "\x00") != strings.Join(want, "\x00") {
		return errors.New("Local system maintenance command changed")
	}
	return nil
}
func checkRootMaintenanceDefinition(m Manifest, recovery bool) error {
	home, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	config, e := os.UserConfigDir()
	if e != nil {
		return e
	}
	path := rootMaintenancePath(os.Getuid())
	want := rootMaintenanceTemplate(m, os.Getuid(), os.Getgid(), home, config)
	if recovery {
		path = rootRecoveryPath(os.Getuid())
		want = rootRecoveryTemplate(m, os.Getuid(), os.Getgid(), home, config)
	}
	b, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(b, want) {
		return errors.New("Local system maintenance definition changed")
	}
	return nil
}

type maintenanceHostProof struct {
	Version                                                               int
	UID, GID                                                              int
	Root, Data, TemplateSHA256, RecoverySHA256, PolicySHA256, ImageSHA256 string
}

func maintenanceHostProofPath(m Manifest) string {
	return filepath.Join(applicationUpdateDirectory(Paths{Root: m.Root, Data: m.Data}), "system-maintenance-host.json")
}
func expectedMaintenanceHostProof(m Manifest) (maintenanceHostProof, error) {
	home, e := os.UserHomeDir()
	if e != nil {
		return maintenanceHostProof{}, e
	}
	config, e := os.UserConfigDir()
	if e != nil {
		return maintenanceHostProof{}, e
	}
	u, e := user.Current()
	if e != nil {
		return maintenanceHostProof{}, e
	}
	image, e := updateFileDigest(maintenanceExecutable(m))
	if e != nil {
		return maintenanceHostProof{}, e
	}
	return maintenanceHostProof{1, os.Getuid(), os.Getgid(), m.Root, m.Data, bytesDigest(rootMaintenanceTemplate(m, os.Getuid(), os.Getgid(), home, config)), bytesDigest(rootRecoveryTemplate(m, os.Getuid(), os.Getgid(), home, config)), bytesDigest(rootMaintenancePolicy(os.Getuid(), u.Username)), image}, nil
}
func checkPreparedMaintenanceHost(m Manifest) error {
	b, e := core.ReadPrivateFile(maintenanceHostProofPath(m))
	if e != nil {
		return errors.New("Local maintenance OS permission is not prepared; use the installer owner's setup")
	}
	var p maintenanceHostProof
	expected, e := expectedMaintenanceHostProof(m)
	if e != nil {
		return e
	}
	if len(b) > 4096 || json.Unmarshal(b, &p) != nil || p != expected {
		return errors.New("Local maintenance host preparation changed")
	}
	return nil
}
