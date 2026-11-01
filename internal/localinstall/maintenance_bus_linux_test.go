//go:build linux

package localinstall

import (
	"bufio"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type maintenanceTestManager struct{ starts chan string }

func (m *maintenanceTestManager) LoadUnit(unit string) (dbus.ObjectPath, *dbus.Error) {
	return "/org/freedesktop/systemd1/unit/fixture", nil
}
func (m *maintenanceTestManager) StartUnit(unit, mode string) (dbus.ObjectPath, *dbus.Error) {
	m.starts <- unit
	return "/org/freedesktop/systemd1/job/1", nil
}

type maintenanceTestProperties struct {
	m  Manifest
	id string
}

func (p *maintenanceTestProperties) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if iface == "org.freedesktop.systemd1.Service" {
		return map[string]dbus.Variant{"Result": dbus.MakeVariant("success"), "ExecStart": dbus.MakeVariant([]maintenanceExec{{Path: maintenanceExecutable(p.m), Args: []string{maintenanceExecutable(p.m), "maintenance-delegate", "--operation", p.id, "--prefix", p.m.Root, "--data-dir", p.m.Data}}})}, nil
	}
	return map[string]dbus.Variant{"LoadState": dbus.MakeVariant("loaded"), "FragmentPath": dbus.MakeVariant(maintenanceTemplatePath(p.m)), "DropInPaths": dbus.MakeVariant([]string{}), "ActiveState": dbus.MakeVariant("inactive"), "SubState": dbus.MakeVariant("dead"), "UnitFileState": dbus.MakeVariant("disabled")}, nil
}
func TestSandboxMaintenanceBusReadsExactCommandAndStartsOnlyOwnUnit(t *testing.T) {
	if _, e := exec.LookPath("dbus-daemon"); e != nil {
		t.Skip("OS test broker unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	daemon := exec.CommandContext(ctx, "dbus-daemon", "--session", "--nofork", "--print-address=1", "--address=unix:path="+filepath.Join(t.TempDir(), "bus"))
	pipe, e := daemon.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = daemon.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { cancel(); _ = daemon.Wait() })
	line, e := bufio.NewReader(pipe).ReadString('\n')
	if e != nil {
		t.Fatal(e)
	}
	server, e := dbus.Connect(strings.TrimSpace(line))
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	manager := &maintenanceTestManager{starts: make(chan string, 2)}
	if e = server.Export(manager, "/org/freedesktop/systemd1", "org.freedesktop.systemd1.Manager"); e != nil {
		t.Fatal(e)
	}
	a, f := maintenanceFixture(t)
	if e = InstallApplicationMaintenance(ctx, a.old, f.run); e != nil {
		t.Fatal(e)
	}
	id := strings.Repeat("0", 32)
	if e = server.Export(&maintenanceTestProperties{a.old, id}, "/org/freedesktop/systemd1/unit/fixture", "org.freedesktop.DBus.Properties"); e != nil {
		t.Fatal(e)
	}
	if _, e = server.RequestName("org.freedesktop.systemd1", dbus.NameFlagDoNotQueue); e != nil {
		t.Fatal(e)
	}
	client, e := dbus.Connect(strings.TrimSpace(line))
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	unit := maintenanceUnit(id)
	b, e := maintenanceBusCall(ctx, client, a.old, unit, "show")
	if e != nil {
		t.Fatal(e)
	}
	p, e := maintenanceManager(ctx, func(context.Context, ...string) ([]byte, error) { return b, nil }, unit)
	if e != nil {
		t.Fatal(e)
	}
	if e = checkMaintenanceTemplate(a.old, p); e != nil {
		t.Fatal(e, string(b))
	}
	if _, e = maintenanceBusCall(ctx, client, a.old, "ssh.service", "start"); e == nil {
		t.Fatal("foreign service accepted")
	}
	if _, e = maintenanceBusCall(ctx, client, a.old, unit, "start"); e != nil {
		t.Fatal(e)
	}
	select {
	case got := <-manager.starts:
		if got != unit {
			t.Fatal(got)
		}
	case <-ctx.Done():
		t.Fatal("start was not delivered")
	}
}

func TestMaintenanceBusPreservesArgumentBoundariesAndExactOperation(t *testing.T) {
	m := Manifest{Root: "/home/owner with space/app", Data: "/home/owner with space/data"}
	id := strings.Repeat("a", 32)
	unit := maintenanceUnit(id)
	args := []string{maintenanceExecutable(m), "maintenance-delegate", "--operation", id, "--prefix", m.Root, "--data-dir", m.Data}
	c := maintenanceExec{Path: maintenanceExecutable(m), Args: args}
	if e := validateMaintenanceBusCommand(m, unit, c); e != nil {
		t.Fatal(e)
	}
	c.Args = strings.Split(strings.Join(args, " "), " ")
	if e := validateMaintenanceBusCommand(m, unit, c); e == nil {
		t.Fatal("regrouped arguments accepted")
	}
	c.Args = append([]string{}, args...)
	c.Args[3] = strings.Repeat("b", 32)
	if e := validateMaintenanceBusCommand(m, unit, c); e == nil {
		t.Fatal("different operation accepted")
	}
}

func TestMaintenanceBusMissingAndMalformedCommandRefused(t *testing.T) {
	row := []interface{}{"/fixed", []string{"/fixed"}, false, uint64(0), uint64(0), uint64(0), uint64(0), uint32(0), int32(0), int32(0)}
	if _, e := decodeMaintenanceBusExec([][]interface{}{row}); e != nil {
		t.Fatal(e)
	}
	for _, value := range []interface{}{nil, []interface{}{}, [][]interface{}{}, [][]interface{}{row[:9]}, [][]interface{}{row, row}} {
		if _, e := decodeMaintenanceBusExec(value); e == nil {
			t.Fatal("malformed command accepted")
		}
	}
	for _, tc := range []struct {
		index int
		value interface{}
	}{{0, dbus.ObjectPath("/fixed")}, {1, []interface{}{"/fixed"}}, {2, "false"}, {3, uint32(0)}, {7, uint64(0)}, {8, uint32(0)}} {
		bad := append([]interface{}{}, row...)
		bad[tc.index] = tc.value
		if _, e := decodeMaintenanceBusExec([][]interface{}{bad}); e == nil {
			t.Fatal("wire conversion accepted", tc.index)
		}
	}
}
