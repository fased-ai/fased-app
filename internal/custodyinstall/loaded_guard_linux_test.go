//go:build linux

package custodyinstall

import (
	"errors"
	"os"
	"strconv"
	"testing"

	"github.com/fased-ai/fased-app/internal/tailnet"
)

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
