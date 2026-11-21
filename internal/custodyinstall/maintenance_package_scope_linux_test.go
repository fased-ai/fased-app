//go:build linux

package custodyinstall

import (
	"context"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/tailnet"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestPackageUpdateCannotWriteAnotherInstancesBootstrap(t *testing.T) {
	t.Parallel()
	_, m := packageGateInitialRecord(t)
	m.Owner = os.Getuid()
	m.Root, _ = PackageRoot(m.Owner, m.Location, m.ID)
	m.App = Identity{Name: packageAccountName(m.Owner, m.Location, m.ID, "app"), UID: m.Owner + 1, GID: m.Owner + 1}
	m.Signer = Identity{Name: packageAccountName(m.Owner, m.Location, m.ID, "signer"), UID: m.Owner + 2, GID: m.Owner + 2}
	a := maintenance.Authority{Owner: m.Owner, Profile: m.Location, ImageSHA256: strings.Repeat("b", 64)}
	files, err := packageMaintenanceFiles(a, m)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := update.PackageMaintenanceRoot(m.Owner, m.Location, m.ID)
	for name, contents := range files {
		if !strings.HasSuffix(name, ".service") {
			continue
		}
		for _, line := range strings.Split(string(contents), "\n") {
			if !strings.HasPrefix(line, "ReadWritePaths=") {
				continue
			}
			for _, path := range strings.Fields(strings.TrimPrefix(line, "ReadWritePaths=")) {
				if path != m.Root && path != state {
					t.Errorf("%s can mutate a shared path during an instance update: %s", name, path)
				}
			}
		}
	}
}

func TestRemotePackageUnitsRequireTheirProtectedSocket(t *testing.T) {
	t.Parallel()
	_, m := packageGateInitialRecord(t)
	m.Owner, m.Location, m.PublicOrigin = os.Getuid(), "remote", "https://node.example.ts.net"
	if m.Owner <= 0 {
		t.Skip("ordinary owner required")
	}
	m.Root, _ = PackageRoot(m.Owner, m.Location, m.ID)
	m.App = Identity{Name: packageAccountName(m.Owner, m.Location, m.ID, "app"), UID: m.Owner + 1, GID: m.Owner + 1}
	m.Signer = Identity{Name: packageAccountName(m.Owner, m.Location, m.ID, "signer"), UID: m.Owner + 2, GID: m.Owner + 2}
	a := maintenance.Authority{Owner: m.Owner, Profile: m.Location, ImageSHA256: strings.Repeat("b", 64)}
	name, socket, err := tailnet.ProtectedSocketUnit("package-"+m.ID, m.Owner)
	if err != nil {
		t.Fatal(err)
	}
	files, err := packageMaintenanceFiles(a, m)
	if err != nil || string(files["/etc/systemd/system/"+name]) != string(socket) {
		t.Fatal("Remote package did not provision its protected socket unit", err)
	}
	app := string(definitions(m)["fased-app.service"])
	for _, line := range []string{"Requires=" + name, "After=" + name, "Sockets=" + name} {
		if !strings.Contains(app, line+"\n") {
			t.Fatal("Remote application is not bound to its private socket", line)
		}
	}
	if strings.Contains(string(definitions(m)["fased-signer.service"]), name) {
		t.Fatal("the signer acquired private network ingress")
	}
	fields := map[string]string{
		"FragmentPath": "/etc/systemd/system/" + instanceUnit(m, "app"),
		"User":         strconv.Itoa(m.App.UID), "Group": strconv.Itoa(m.App.GID), "NoNewPrivileges": "yes",
		"ProtectSystem": "strict", "ExecStart": "{ path=" + m.Root + "/fased-app ; argv[]=... ; }", "NeedDaemonReload": "no", "LimitCORE": "0",
		"Requires": packageRecoveryUnit(m) + " " + name, "After": packageRecoveryUnit(m) + " " + name,
	}
	read := func(path string) (hostFile, error) {
		if path != "/etc/systemd/system/"+name {
			return hostFile{}, os.ErrNotExist
		}
		return hostFile{UID: 0, Regular: true, Mode: 0644, Data: socket}, nil
	}
	if err := validateLoadedUnitFields(m, "app", m.Root+"/fased-app", fields, read, nil); err != nil {
		t.Fatal("exact protected Remote socket was refused", err)
	}
	for _, key := range []string{"Requires", "After"} {
		old := fields[key]
		fields[key] = packageRecoveryUnit(m)
		if err := validateLoadedUnitFields(m, "app", m.Root+"/fased-app", fields, read, nil); err == nil {
			t.Fatal("Remote service accepted without its socket dependency", key)
		}
		fields[key] = old
	}
	if err := validateLoadedUnitFields(m, "app", m.Root+"/fased-app", fields, func(string) (hostFile, error) { return hostFile{}, os.ErrNotExist }, nil); err == nil {
		t.Fatal("missing protected socket unit was accepted")
	}
}

func TestPackageMaintenanceRecordUsesOnlyItsInstanceJournal(t *testing.T) {
	t.Parallel()
	r, m := packageGateInitialRecord(t)
	root, err := update.PackageMaintenanceRoot(m.Owner, m.Location, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if path, err := protectedRecordPathFor(r); err != nil || path != root+"/custody.json" || path == protectedRecordPath(m.Owner) {
		t.Fatal("package record did not stay in its own journal", path, err)
	}
	legacy := initialMaintenanceFixture()
	if path, err := protectedRecordPathFor(legacy); err != nil || path != protectedRecordPath(legacy.Owner) {
		t.Fatal("legacy journal path changed", path, err)
	}
	foreign := m
	foreign.Location = "remote"
	r.Target = mustJSON(foreign)
	if _, err := protectedRecordPathFor(r); err == nil {
		t.Fatal("foreign target could select the original instance journal")
	}
}

func TestPackageMaintenanceChildrenUseTheSameInstance(t *testing.T) {
	t.Parallel()
	_, m := packageGateInitialRecord(t)
	owner := strconv.Itoa(m.Owner)
	for _, command := range []string{"custody-maintenance", "custody-maintenance-inventory", "custody-maintenance-status", "custody-maintenance-verify"} {
		args := []string{command, "--owner", owner}
		got := packageMaintenanceChildArgs(m, args)
		want := []string{command, "--owner", owner, "--location", m.Location, "--instance", m.ID}
		if !reflect.DeepEqual(got, want) || len(args) != 3 {
			t.Fatalf("%s lost its instance or changed its caller's arguments: %v", command, got)
		}
	}
	plain := []string{"native-release-binding"}
	if got := packageMaintenanceChildArgs(m, plain); !reflect.DeepEqual(got, plain) {
		t.Fatalf("non-maintenance command changed: %v", got)
	}
	legacy := m
	legacy.Layout = ""
	args := []string{"custody-maintenance", "--owner", owner}
	if got := packageMaintenanceChildArgs(legacy, args); !reflect.DeepEqual(got, args) {
		t.Fatalf("legacy maintenance entry changed: %v", got)
	}
	if got := maintenancePeerArgs("maintenance-peer", m.Owner, m); !reflect.DeepEqual(got, []string{"maintenance-peer", "--owner", owner, "--location", m.Location, "--instance", m.ID}) {
		t.Fatalf("owner peer lost its instance: %v", got)
	}
}

func TestPackageMaintenanceUnitsAreInstanceScoped(t *testing.T) {
	t.Parallel()
	_, m := packageGateInitialRecord(t)
	m.Owner = os.Getuid()
	if m.Owner <= 0 {
		t.Skip("ordinary owner required")
	}
	m.Root, _ = PackageRoot(m.Owner, m.Location, m.ID)
	m.App = Identity{Name: packageAccountName(m.Owner, m.Location, m.ID, "app"), UID: m.Owner + 1, GID: m.Owner + 1}
	m.Signer = Identity{Name: packageAccountName(m.Owner, m.Location, m.ID, "signer"), UID: m.Owner + 2, GID: m.Owner + 2}
	a := maintenance.Authority{Owner: m.Owner, Profile: m.Location, ImageSHA256: strings.Repeat("b", 64)}
	files, err := packageMaintenanceFiles(a, m)
	if err != nil {
		t.Fatal(err)
	}
	recovery := string(files["/etc/systemd/system/"+packageRecoveryUnit(m)])
	worker := string(files["/etc/systemd/system/"+packageWorkerTemplate(m)])
	identity := "--owner " + strconv.Itoa(m.Owner) + " --location " + m.Location + " --instance " + m.ID
	if !strings.Contains(recovery, "maintenance-package-recover "+identity+"\n") || !strings.Contains(recovery, "Before="+instanceUnit(m, "app")+" "+instanceUnit(m, "signer")) {
		t.Fatal("boot recovery does not select this instance")
	}
	if !strings.Contains(worker, "maintenance-package-worker "+identity+" --operation %i\n") || !strings.Contains(worker, "ReadWritePaths="+m.Root+" ") || strings.Contains(worker, Root(m.Owner)) {
		t.Fatal("worker does not remain in the selected instance")
	}
	for _, unit := range []string{worker, recovery} {
		state, _ := update.PackageMaintenanceRoot(m.Owner, m.Location, m.ID)
		if !strings.Contains(unit, "ReadWritePaths="+m.Root+" "+state+"\n") {
			t.Fatal("update and recovery must write only their instance and original journal")
		}
	}
	if !strings.Contains(worker, "LimitCORE=0\n") || !strings.Contains(recovery, "LimitCORE=0\n") {
		t.Fatal("protected maintenance services must limit crash dumps")
	}
	for _, role := range []string{"app", "signer"} {
		unit := string(definitions(m)["fased-"+role+".service"])
		if !strings.Contains(unit, "Requires="+packageRecoveryUnit(m)+"\nAfter="+packageRecoveryUnit(m)+"\n") || strings.Contains(unit, "Wants="+packageRecoveryUnit(m)+"\n") {
			t.Fatal("service does not require successful recovery before startup", role)
		}
		if !strings.Contains(unit, "LimitCORE=0\n") {
			t.Fatal("instance service must limit crash dumps", role)
		}
	}
	if !strings.Contains(worker, "Wants="+instanceUnit(m, "signer")) || !strings.Contains(string(definitions(m)["fased-signer.service"]), "ConditionPathExists="+m.Root+"/wallet-configured.json\n") {
		t.Fatal("the stable worker must skip an unconfigured signer")
	}
	a.Profile = "remote"
	if _, err = packageMaintenanceFiles(a, m); err == nil {
		t.Fatal("foreign profile produced privileged service files")
	}
}

func TestPackageMaintenancePeersRejectForeignAuthority(t *testing.T) {
	t.Parallel()
	_, m := packageGateInitialRecord(t)
	a := maintenance.Authority{Owner: m.Owner, Profile: m.Location}
	for name, change := range map[string]func(*maintenance.Authority, *Manifest){
		"other owner":   func(a *maintenance.Authority, _ *Manifest) { a.Owner++ },
		"other profile": func(a *maintenance.Authority, _ *Manifest) { a.Profile = "remote" },
		"other layout":  func(_ *maintenance.Authority, m *Manifest) { m.Layout = "unknown" },
	} {
		t.Run(name, func(t *testing.T) {
			badA, badM := a, m
			change(&badA, &badM)
			if _, err := protectedOwnerPeerImage(context.Background(), badA, badM, "", nil, signer.Request{}); err == nil || !strings.Contains(err.Error(), "another installation") {
				t.Fatal("foreign owner authority reached a process", err)
			}
			if err := protectedAppPeerImage(context.Background(), badA, badM, "", nil, maintenance.AppMaintenanceRequest{}, nil); err == nil || !strings.Contains(err.Error(), "another installation") {
				t.Fatal("foreign app authority reached a process", err)
			}
		})
	}
}
