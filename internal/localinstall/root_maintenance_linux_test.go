//go:build linux

package localinstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/godbus/dbus/v5"
)

func rootRuntimeFixture(m Manifest, id, attempt string) (map[string]dbus.Variant, map[string]dbus.Variant) {
	definition := rootMaintenancePath(os.Getuid())
	args := []string{maintenanceExecutable(m), "maintain-update", "--operation", id, "--prefix", m.Root, "--data-dir", m.Data}
	if attempt != "" {
		definition = rootRecoveryPath(os.Getuid())
		args[1] = "maintenance-recover"
		args[2] = "--attempt"
		args[3] = id + "-" + attempt
	}
	row := []interface{}{maintenanceExecutable(m), args, false, uint64(0), uint64(0), uint64(0), uint64(0), uint32(0), int32(0), int32(0)}
	return map[string]dbus.Variant{"LoadState": dbus.MakeVariant("loaded"), "FragmentPath": dbus.MakeVariant(definition), "DropInPaths": dbus.MakeVariant([]string{})}, map[string]dbus.Variant{"User": dbus.MakeVariant(strconv.Itoa(os.Getuid())), "Group": dbus.MakeVariant(strconv.Itoa(os.Getgid())), "NoNewPrivileges": dbus.MakeVariant(true), "WorkingDirectory": dbus.MakeVariant("/"), "CapabilityBoundingSet": dbus.MakeVariant(uint64(0)), "AmbientCapabilities": dbus.MakeVariant(uint64(0)), "ProtectSystem": dbus.MakeVariant("strict"), "ProtectHome": dbus.MakeVariant("read-only"), "ReadWritePaths": dbus.MakeVariant([]string{m.Root, m.Data, applicationUpdateDirectory(Paths{Root: m.Root, Data: m.Data})}), "ExecStart": dbus.MakeVariant([][]interface{}{row})}
}

func TestRootMaintenancePreservesKnownFinishedOlderHelpers(t *testing.T) {
	a, _ := maintenanceFixture(t)
	old := bytes.ReplaceAll(maintenanceTemplate(a.old), []byte("fased-app-maintenance-system"), []byte("fased-app-maintenance"))
	old = bytes.ReplaceAll(old, []byte("maintenance-delegate --operation"), []byte("maintain-update --operation"))
	old = bytes.ReplaceAll(old, []byte("TimeoutStartSec=16min"), []byte("TimeoutStartSec=15min"))
	path := filepath.Join(applicationUpdateDirectory(a.paths), "maintenance.json")
	for _, body := range [][]byte{old, bytes.Replace(old, []byte("NoNewPrivileges=yes\n"), []byte("NoNewPrivileges=yes\nCapabilityBoundingSet=\n"), 1)} {
		r := maintenanceRequest{Version: 1, ID: strings.Repeat("a", 32), Root: a.old.Root, Data: a.old.Data, TemplateSHA256: bytesDigest(body), PreviousManifestSHA256: strings.Repeat("b", 64), Finished: "completed"}
		if e := core.WritePrivateJSON(path, r); e != nil {
			t.Fatal(e)
		}
		before, _ := os.ReadFile(path)
		if e := legacyMaintenanceIdle(a.old); e != nil {
			t.Fatal(e)
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatal("old receipt changed")
		}
		r.Finished = ""
		if e := core.WritePrivateJSON(path, r); e != nil {
			t.Fatal(e)
		}
		if e := legacyMaintenanceIdle(a.old); e == nil {
			t.Fatal("unfinished older operation admitted")
		}
	}
}

func TestRootMaintenanceRequiresBoundInstallerPreparation(t *testing.T) {
	a, f := maintenanceFixture(t)
	if e := InstallApplicationMaintenance(t.Context(), a.old, f.run); e != nil {
		t.Fatal(e)
	}
	if e := checkPreparedMaintenanceHost(a.old); e == nil {
		t.Fatal("missing host preparation accepted")
	}
	p, e := expectedMaintenanceHostProof(a.old)
	if e != nil {
		t.Fatal(e)
	}
	path := maintenanceHostProofPath(a.old)
	if e = core.WritePrivateJSON(path, p); e != nil {
		t.Fatal(e)
	}
	if e = checkPreparedMaintenanceHost(a.old); e != nil {
		t.Fatal(e)
	}
	p.Data += "-other"
	if e = core.WritePrivateJSON(path, p); e != nil {
		t.Fatal(e)
	}
	if e = checkPreparedMaintenanceHost(a.old); e == nil {
		t.Fatal("changed preparation identity accepted")
	}
}

func TestPreparedMaintenanceStartNeverRepeatsOSAuthorization(t *testing.T) {
	a, f := maintenanceFixture(t)
	if e := InstallApplicationMaintenance(t.Context(), a.old, f.run); e != nil {
		t.Fatal(e)
	}
	checks, preparations := 0, 0
	refused := errors.New("prepared host changed")
	check := func(context.Context, Manifest) error { checks++; return nil }
	prepare := func(context.Context, Manifest) error { preparations++; return nil }
	if e := prepareMaintenanceOwner(t.Context(), a.old, check, prepare); e != nil || checks != 0 || preparations != 1 {
		t.Fatal(checks, preparations, e)
	}
	proof, e := expectedMaintenanceHostProof(a.old)
	if e != nil {
		t.Fatal(e)
	}
	if e = core.WritePrivateJSON(maintenanceHostProofPath(a.old), proof); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(maintenanceHostProofPath(a.old))
	checks, preparations = 0, 0
	if e = prepareMaintenanceOwner(t.Context(), a.old, check, prepare); e != nil || checks != 1 || preparations != 0 {
		t.Fatal(checks, preparations, e)
	}
	check = func(context.Context, Manifest) error { checks++; return refused }
	if e = prepareMaintenanceOwner(t.Context(), a.old, check, prepare); e != refused || preparations != 0 {
		t.Fatal("failed readiness retried privileged preparation", e)
	}
	after, _ := os.ReadFile(maintenanceHostProofPath(a.old))
	if !bytes.Equal(before, after) {
		t.Fatal("start changed preparation proof")
	}
}
func TestRootMaintenanceNeverRunsProjectAsRootOrBroadensFilesystem(t *testing.T) {
	m := Manifest{Root: "/home/owner/.local/share/fased-app", Data: "/home/owner/.config/fased-app"}
	for _, body := range [][]byte{rootMaintenanceTemplate(m, 1000, 1001, "/home/owner", "/home/owner/.config"), rootRecoveryTemplate(m, 1000, 1001, "/home/owner", "/home/owner/.config")} {
		for _, want := range []string{"User=1000\nGroup=1001\n", "NoNewPrivileges=yes\n", "CapabilityBoundingSet=\nAmbientCapabilities=\n", "PrivateTmp=yes\nProtectSystem=strict\nProtectHome=read-only\n", "ReadWritePaths=\"" + m.Root + "\" \"" + m.Data + "\" ", "Environment=XDG_RUNTIME_DIR=/run/user/1000", "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus"} {
			if !bytes.Contains(body, []byte(want)) {
				t.Fatal("missing protection", want)
			}
		}
		for _, bad := range []string{"User=0\n", "sudo", "/bin/sh", "ReadWritePaths=/", "NoNewPrivileges=no"} {
			if bytes.Contains(body, []byte(bad)) {
				t.Fatal("unexpected authority", bad)
			}
		}
	}
	policy := string(rootMaintenancePolicy(1000, "owner"))
	if !strings.Contains(policy, "verb') === 'start'") || strings.Contains(policy, "manage-unit-files") || strings.Contains(policy, "restart") || strings.Contains(policy, "recovery-") {
		t.Fatal(policy)
	}
}
func TestRootMaintenanceRejectsChangedManagerIdentityProtectionOrArguments(t *testing.T) {
	m := Manifest{Root: "/home/owner/app", Data: "/home/owner/data"}
	id := strings.Repeat("a", 32)
	for _, attempt := range []string{"", strings.Repeat("b", 32)} {
		p, s := rootRuntimeFixture(m, id, attempt)
		if e := validateRootMaintenanceRuntime(m, id, attempt, p, s); e != nil {
			t.Fatal(e)
		}
		for _, mutation := range []struct {
			key   string
			value interface{}
			unit  bool
		}{{"User", "0", false}, {"Group", "0", false}, {"NoNewPrivileges", false, false}, {"CapabilityBoundingSet", uint64(1), false}, {"AmbientCapabilities", uint64(1), false}, {"ProtectHome", "no", false}, {"ReadWritePaths", []string{"/home"}, false}, {"DropInPaths", []string{"override"}, true}, {"FragmentPath", "/tmp/foreign.service", true}} {
			a, b := rootRuntimeFixture(m, id, attempt)
			if mutation.unit {
				a[mutation.key] = dbus.MakeVariant(mutation.value)
			} else {
				b[mutation.key] = dbus.MakeVariant(mutation.value)
			}
			if e := validateRootMaintenanceRuntime(m, id, attempt, a, b); e == nil {
				t.Fatal("accepted", mutation.key)
			}
		}
		s["ExecStart"] = dbus.MakeVariant([][]interface{}{{maintenanceExecutable(m), []string{maintenanceExecutable(m), "maintain-update", "--operation", strings.Repeat("c", 32)}, false, uint64(0), uint64(0), uint64(0), uint64(0), uint32(0), int32(0), int32(0)}})
		if e := validateRootMaintenanceRuntime(m, id, attempt, p, s); e == nil {
			t.Fatal("different operation accepted")
		}
	}
}
func TestRootMaintenancePublicationRefusesForeignAndRetriesExactStaging(t *testing.T) {
	path := rootMaintenancePath(os.Getuid())
	body := []byte("controlled fixture")
	files := map[string][]byte{}
	foreign := false
	calls := []string{}
	run := func(_ context.Context, input []byte, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		switch args[0] {
		case "/usr/bin/find":
			p := filepath.Join(args[1], args[5])
			if _, ok := files[p]; ok {
				if foreign {
					return []byte("1001:0:644:f"), nil
				}
				return []byte("0:0:644:f"), nil
			}
		case "/usr/bin/cat":
			return files[args[2]], nil
		case "/usr/bin/install":
			files[args[len(args)-1]] = append([]byte{}, input...)
		case "/usr/bin/ln":
			if _, ok := files[args[4]]; ok {
				return nil, fmt.Errorf("exists")
			}
			files[args[4]] = files[args[3]]
		case "/usr/bin/rm":
			delete(files, args[2])
		}
		return nil, nil
	}
	if e := publishRootMaintenanceFile(t.Context(), path, body, run); e != nil {
		t.Fatal(e)
	}
	before := len(calls)
	if e := publishRootMaintenanceFile(t.Context(), path, body, run); e != nil {
		t.Fatal(e)
	}
	for _, c := range calls[before:] {
		if strings.Contains(c, "/usr/bin/install") {
			t.Fatal("unchanged policy rewritten")
		}
	}
	foreign = true
	if e := publishRootMaintenanceFile(t.Context(), path, body, run); e == nil {
		t.Fatal("foreign policy accepted")
	}
	if !bytes.Equal(files[path], body) {
		t.Fatal("foreign policy changed")
	}
}
