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
	"github.com/fased-ai/fased-app/internal/update"
)

func TestPackageMaintenanceRecordUsesOnlyItsInstanceJournal(t *testing.T) {
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
	if strings.Contains(worker, "Wants="+instanceUnit(m, "signer")) {
		t.Fatal("an unconfigured signer would be started during an update")
	}
	a.Profile = "remote"
	if _, err = packageMaintenanceFiles(a, m); err == nil {
		t.Fatal("foreign profile produced privileged service files")
	}
}

func TestPackageMaintenancePeersRejectForeignAuthority(t *testing.T) {
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
