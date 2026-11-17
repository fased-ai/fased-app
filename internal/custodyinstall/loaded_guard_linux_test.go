//go:build linux

package custodyinstall

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/tailnet"
)

func TestPackageRuntimeRejectsLegacyUnitAndUnverifiedOverrides(t *testing.T) {
	m := Manifest{Layout: PackageLayout, Owner: 1000, Location: "local", ID: strings.Repeat("a", 32), Root: "/var/lib/example", App: Identity{UID: 1201, GID: 1201}}
	fields := map[string]string{
		"FragmentPath": "/etc/systemd/system/" + instanceUnit(m, "app"),
		"User":         strconv.Itoa(m.App.UID), "Group": strconv.Itoa(m.App.GID), "NoNewPrivileges": "yes",
		"ProtectSystem": "strict", "ExecStart": "{ path=" + m.Root + "/fased-app ; argv[]=... ; }", "NeedDaemonReload": "no", "LimitCORE": "0",
		"Requires": packageRecoveryUnit(m), "After": packageRecoveryUnit(m),
	}
	if err := validateLoadedUnitFields(m, "app", m.Root+"/fased-app", fields, nil, nil); err != nil {
		t.Fatal("exact instance unit rejected", err)
	}
	for _, property := range []string{"Requires", "After"} {
		original := fields[property]
		fields[property] = "different-recovery.service"
		if err := validateLoadedUnitFields(m, "app", m.Root+"/fased-app", fields, nil, nil); err == nil {
			t.Fatalf("loaded service without recovery %s was accepted", property)
		}
		fields[property] = original
	}
	fields["FragmentPath"] = "/etc/systemd/system/" + Unit(m.Owner, "app")
	if err := validateLoadedUnitFields(m, "app", m.Root+"/fased-app", fields, nil, nil); err == nil {
		t.Fatal("legacy owner-wide unit accepted")
	}
	fields["FragmentPath"] = "/etc/systemd/system/" + instanceUnit(m, "app")
	fields["DropInPaths"] = "/etc/systemd/system/foreign.conf"
	if err := validateLoadedUnitFields(m, "app", m.Root+"/fased-app", fields, nil, nil); err == nil {
		t.Fatal("unverified override accepted")
	}
	const vendor = "/usr/lib/systemd/system/service.d/10-timeout-abort.conf"
	fields["DropInPaths"] = vendor
	readVendor := func(path string) (hostFile, error) {
		if path != vendor {
			return hostFile{}, os.ErrNotExist
		}
		return hostFile{UID: 0, Regular: true, Mode: 0644, Data: []byte("# Fedora default\n[Service]\nTimeoutStopFailureMode=abort\n")}, nil
	}
	if err := validateLoadedUnitFields(m, "app", m.Root+"/fased-app", fields, readVendor, nil); err != nil {
		t.Fatal("safe Fedora systemd default refused", err)
	}
	fields["LimitCORE"] = "infinity"
	if err := validateLoadedUnitFields(m, "app", m.Root+"/fased-app", fields, readVendor, nil); err == nil {
		t.Fatal("service with unrestricted core dumps accepted")
	}
	fields["LimitCORE"] = "0"
	badVendor := func(string) (hostFile, error) {
		return hostFile{UID: 0, Regular: true, Mode: 0644, Data: []byte("[Service]\nTimeoutStopFailureMode=abort\nExecStartPre=/tmp/foreign\n")}, nil
	}
	if err := validateLoadedUnitFields(m, "app", m.Root+"/fased-app", fields, badVendor, nil); err == nil {
		t.Fatal("vendor override with an extra command accepted")
	}
}

func TestLoadedRemoteUnitRequiresExactProtectedIngressAndGuard(t *testing.T) {
	m := fixtureManifest()
	m.Location, m.PublicOrigin = "remote", "https://owner.example.ts.net"
	guard := "/etc/systemd/system/" + Unit(m.Owner, "app") + ".d/20-fased-update-guard.conf"
	guardBody := []byte("[Unit]\nAssertPathExists=!" + m.Root + "/update-start-blocked.json\n")
	ingress, ingressBody, err := tailnet.ProtectedSocketDropIn("custody", m.Owner)
	if err != nil {
		t.Fatal(err)
	}
	unit, unitBody, err := tailnet.ProtectedSocketUnit("custody", m.Owner)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]hostFile{
		guard:                         {Data: guardBody, UID: 0, Mode: 0644, Regular: true},
		ingress:                       {Data: ingressBody, UID: 0, Mode: 0644, Regular: true},
		"/etc/systemd/system/" + unit: {Data: unitBody, UID: 0, Mode: 0644, Regular: true},
	}
	read := func(p string) (hostFile, error) {
		f, ok := files[p]
		if !ok {
			return hostFile{}, os.ErrNotExist
		}
		return f, nil
	}
	fields := map[string]string{
		"FragmentPath": "/etc/systemd/system/" + Unit(m.Owner, "app"), "DropInPaths": guard + " " + ingress,
		"User": strconv.Itoa(m.App.UID), "Group": strconv.Itoa(m.App.GID), "NoNewPrivileges": "yes",
		"ProtectSystem": "strict", "ExecStart": "{ path=" + m.Root + "/fased-app ; argv[]=... ; }", "NeedDaemonReload": "no",
	}
	scope := func(int) (bool, error) { return true, nil }
	if err := validateLoadedUnitFields(m, "app", m.Root+"/fased-app", fields, read, scope); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(){
		"unscoped":            func() { fields["DropInPaths"] = ingress + " /etc/systemd/system/foreign.conf" },
		"duplicate":           func() { fields["DropInPaths"] = guard + " " + ingress + " " + ingress },
		"changed socket unit": func() { delete(files, "/etc/systemd/system/"+unit) },
		"stale systemd":       func() { fields["NeedDaemonReload"] = "yes" },
	} {
		t.Run(name, func(t *testing.T) {
			fields["DropInPaths"], fields["NeedDaemonReload"] = guard+" "+ingress, "no"
			files["/etc/systemd/system/"+unit] = hostFile{Data: unitBody, UID: 0, Mode: 0644, Regular: true}
			mutate()
			if err := validateLoadedUnitFields(m, "app", m.Root+"/fased-app", fields, read, scope); err == nil {
				t.Fatal("untrusted ingress accepted")
			}
		})
	}
}

func TestLoadedUnitAdmitsOnlyExactRootManagedRestartGuard(t *testing.T) {
	m := fixtureManifest()
	path := "/etc/systemd/system/" + Unit(m.Owner, "app") + ".d/20-fased-update-guard.conf"
	body := []byte("[Unit]\nAssertPathExists=!" + m.Root + "/update-start-blocked.json\n")
	fields := map[string]string{
		"FragmentPath": "/etc/systemd/system/" + Unit(m.Owner, "app"), "DropInPaths": path,
		"User": strconv.Itoa(m.App.UID), "Group": strconv.Itoa(m.App.GID), "NoNewPrivileges": "yes",
		"ProtectSystem": "strict", "ExecStart": "{ path=" + m.Root + "/fased-app ; argv[]=... ; }", "NeedDaemonReload": "no",
	}
	read := func(p string) (hostFile, error) {
		if p != path {
			return hostFile{}, os.ErrNotExist
		}
		return hostFile{Data: body, UID: 0, Mode: 0644, Regular: true}, nil
	}
	scope := func(int) (bool, error) { return true, nil }
	if err := validateLoadedUnitFields(m, "app", m.Root+"/fased-app", fields, read, scope); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(){
		func() { fields["DropInPaths"] = path + " /etc/systemd/system/foreign.conf" },
		func() { fields["DropInPaths"] = "/etc/systemd/system/foreign.conf" },
		func() { fields["NeedDaemonReload"] = "yes" },
	} {
		change()
		if err := validateLoadedUnitFields(m, "app", m.Root+"/fased-app", fields, read, scope); err == nil {
			t.Fatal("foreign or stale unit accepted")
		}
		fields["DropInPaths"], fields["NeedDaemonReload"] = path, "no"
	}
	for _, bad := range []hostFile{{Data: body, UID: m.Owner, Mode: 0644, Regular: true}, {Data: []byte("[Service]\nUser=root\n"), UID: 0, Mode: 0644, Regular: true}, {Data: body, UID: 0, Mode: 0664, Regular: true}} {
		if err := validateLoadedUnitFields(m, "app", m.Root+"/fased-app", fields, func(string) (hostFile, error) { return bad, nil }, scope); err == nil {
			t.Fatal("unsafe guard accepted")
		}
	}
	if err := validateLoadedUnitFields(m, "app", m.Root+"/fased-app", fields, read, func(int) (bool, error) { return false, nil }); err == nil {
		t.Fatal("unscoped override accepted")
	}
	if err := validateLoadedUnitFields(m, "app", m.Root+"/fased-app", fields, read, func(int) (bool, error) { return false, errors.New("unavailable") }); err == nil {
		t.Fatal("unknown scope accepted")
	}
	fields["DropInPaths"] = ""
	if err := validateLoadedUnitFields(m, "app", m.Root+"/fased-app", fields, read, scope); err != nil {
		t.Fatal("original unit rejected", err)
	}
}
