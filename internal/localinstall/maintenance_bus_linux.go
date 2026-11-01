//go:build linux

package localinstall

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
)

// libsystemd requires a visible peer PID and returns ENODATA inside our private
// PID namespace. The authenticated per-user bus itself works there. Keep the
// namespace and use only the two already-permitted core actions, never a general
// bus proxy, unit-file operation, source selection or privileged helper.
func maintenanceSandbox(m Manifest) bool {
	if os.Getuid() <= 0 || os.Getuid() != os.Geteuid() {
		return false
	}
	b, e := os.ReadFile("/proc/self/uid_map")
	if e != nil || !maintenanceOwnerMap(os.Getuid(), string(b)) {
		return false
	}
	b, e = os.ReadFile("/proc/self/status")
	if e != nil || !strings.Contains(string(b), "CapEff:\t0000000000000000\n") {
		return false
	}
	return CheckSandbox(m.Root, m.Data) == nil
}
func maintenanceOwnerMap(uid int, text string) bool {
	return uid > 0 && strings.Join(strings.Fields(text), " ") == strconv.Itoa(uid)+" 0 1"
}
func maintenanceBus(ctx context.Context, m Manifest, args ...string) ([]byte, error) {
	if len(args) != 3 || args[0] != "show" || args[2] != maintenanceProperties {
		if len(args) != 4 || args[0] != "start" || args[1] != "--no-block" || args[2] != "--" {
			return nil, errors.New("sandbox maintenance permits inspect/start only")
		}
	}
	unit := args[1]
	if args[0] == "start" {
		unit = args[3]
	}
	if !strings.HasPrefix(unit, "fased-app-maintenance-system@") || !strings.HasSuffix(unit, ".service") || !applicationUpdateID(strings.TrimSuffix(strings.TrimPrefix(unit, "fased-app-maintenance-system@"), ".service")) {
		return nil, errors.New("exact maintenance instance required")
	}
	uid := os.Getuid()
	runtime := filepath.Join("/run/user", strconv.Itoa(uid))
	socket := filepath.Join(runtime, "bus")
	for _, p := range []string{runtime, socket} {
		i, e := os.Lstat(p)
		if e != nil {
			return nil, e
		}
		s, ok := i.Sys().(*syscall.Stat_t)
		if !ok || int(s.Uid) != uid || i.Mode()&os.ModeSymlink != 0 || p == runtime && (!i.IsDir() || i.Mode().Perm() != 0700) || p == socket && i.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("fixed owner session bus required")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	raw, e := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if e != nil {
		return nil, e
	}
	deadline, _ := ctx.Deadline()
	if e = raw.SetDeadline(deadline); e != nil {
		_ = raw.Close()
		return nil, e
	}
	conn, e := dbus.NewConn(raw, dbus.WithContext(ctx))
	if e != nil {
		_ = raw.Close()
		return nil, e
	}
	defer conn.Close()
	if e = conn.Auth([]dbus.Auth{dbus.AuthExternal(strconv.Itoa(uid))}); e != nil {
		return nil, e
	}
	if e = conn.Hello(); e != nil {
		return nil, e
	}
	return maintenanceBusCall(ctx, conn, m, unit, args[0])
}

type maintenanceExec struct {
	Path                                     string
	Args                                     []string
	Ignore                                   bool
	StartReal, StartMono, ExitReal, ExitMono uint64
	PID                                      uint32
	Code, Status                             int32
}

func maintenanceBusCall(ctx context.Context, conn *dbus.Conn, m Manifest, unit, action string) ([]byte, error) {
	if !strings.HasPrefix(unit, "fased-app-maintenance-system@") || !strings.HasSuffix(unit, ".service") || !applicationUpdateID(strings.TrimSuffix(strings.TrimPrefix(unit, "fased-app-maintenance-system@"), ".service")) {
		return nil, errors.New("exact maintenance instance required")
	}
	manager := conn.Object("org.freedesktop.systemd1", "/org/freedesktop/systemd1")
	if action == "start" {
		return nil, manager.CallWithContext(ctx, "org.freedesktop.systemd1.Manager.StartUnit", 0, unit, "replace").Err
	}
	if action != "show" {
		return nil, errors.New("unsupported maintenance bus operation")
	}
	var path dbus.ObjectPath
	if e := manager.CallWithContext(ctx, "org.freedesktop.systemd1.Manager.LoadUnit", 0, unit).Store(&path); e != nil {
		return nil, e
	}
	object := conn.Object("org.freedesktop.systemd1", path)
	var props, service map[string]dbus.Variant
	if e := object.CallWithContext(ctx, "org.freedesktop.DBus.Properties.GetAll", 0, "org.freedesktop.systemd1.Unit").Store(&props); e != nil {
		return nil, e
	}
	if e := object.CallWithContext(ctx, "org.freedesktop.DBus.Properties.GetAll", 0, "org.freedesktop.systemd1.Service").Store(&service); e != nil {
		return nil, e
	}
	var out strings.Builder
	for _, key := range []string{"LoadState", "FragmentPath", "ActiveState", "SubState", "UnitFileState"} {
		value, ok := props[key].Value().(string)
		if !ok || strings.ContainsAny(value, "\r\n\x00") {
			return nil, errors.New("invalid manager property")
		}
		fmt.Fprintf(&out, "%s=%s\n", key, value)
	}
	drops, ok := props["DropInPaths"].Value().([]string)
	if !ok {
		return nil, errors.New("invalid maintenance overrides")
	}

	if len(drops) != 0 {
		return nil, errors.New("maintenance overrides refused")
	}
	fmt.Fprintln(&out, "DropInPaths=")
	result, ok := service["Result"].Value().(string)
	if !ok || strings.ContainsAny(result, "\r\n\x00") {
		return nil, errors.New("invalid maintenance result")
	}
	fmt.Fprintf(&out, "Result=%s\n", result)
	c, e := decodeMaintenanceBusExec(service["ExecStart"].Value())
	if e != nil {
		return nil, e
	}

	if e := validateMaintenanceBusCommand(m, unit, c); e != nil {
		return nil, e
	}
	if c.Ignore || strings.ContainsAny(c.Path+strings.Join(c.Args, ""), "\r\n\x00{}") {
		return nil, errors.New("invalid manager command")
	}
	fmt.Fprintf(&out, "ExecStart={ path=%s ; argv[]=%s ; }\n", c.Path, strings.Join(c.Args, " "))
	return []byte(out.String()), nil
}

func validateMaintenanceBusCommand(m Manifest, unit string, c maintenanceExec) error {
	id := strings.TrimSuffix(strings.TrimPrefix(unit, "fased-app-maintenance-system@"), ".service")
	want := []string{maintenanceExecutable(m), "maintenance-delegate", "--operation", id, "--prefix", m.Root, "--data-dir", m.Data}
	if !applicationUpdateID(id) || c.Ignore || c.Path != maintenanceExecutable(m) || !slices.Equal(c.Args, want) {
		return errors.New("maintenance executable or exact argument boundaries changed")
	}
	return nil
}

// GetAll represents a wire struct as []any. Check every field's original Go
// wire type before projection; dbus.Store would permit numeric conversions.
func decodeMaintenanceBusExec(value interface{}) (maintenanceExec, error) {
	fail := errors.New("one complete typed maintenance command required")
	rows, ok := value.([][]interface{})
	if !ok || len(rows) != 1 || len(rows[0]) != 10 {
		return maintenanceExec{}, fail
	}
	row := rows[0]
	path, a := row[0].(string)
	args, b := row[1].([]string)
	ignore, c := row[2].(bool)
	if !a || !b || !c {
		return maintenanceExec{}, fail
	}
	for i := 3; i <= 6; i++ {
		if _, ok := row[i].(uint64); !ok {
			return maintenanceExec{}, fail
		}
	}
	if _, ok := row[7].(uint32); !ok {
		return maintenanceExec{}, fail
	}
	for i := 8; i <= 9; i++ {
		if _, ok := row[i].(int32); !ok {
			return maintenanceExec{}, fail
		}
	}
	return maintenanceExec{Path: path, Args: args, Ignore: ignore}, nil
}
