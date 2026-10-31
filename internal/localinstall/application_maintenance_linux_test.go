//go:build linux

package localinstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

type maintenanceManagerFixture struct {
	m             Manifest
	linked        bool
	enabled       map[string]bool
	active        map[string]bool
	exited        map[string]bool
	calls         [][]string
	drop, command string
	denied        bool
}

func maintenanceFixture(t *testing.T) (*applicationFixture, *maintenanceManagerFixture) {
	t.Helper()
	a := applicationFixtureFor(t)
	return a, &maintenanceManagerFixture{m: a.old, enabled: map[string]bool{}, active: map[string]bool{}, exited: map[string]bool{}}
}
func (f *maintenanceManagerFixture) run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{}, args...))
	if f.denied {
		return nil, errors.New("owner manager unavailable")
	}
	switch args[0] {
	case "show":
		if !f.linked {
			return []byte("LoadState=not-found\nFragmentPath=\nDropInPaths=\nExecStart=\nActiveState=inactive\nSubState=dead\nUnitFileState=not-found\nResult=success\n"), nil
		}
		unit := args[1]
		id := strings.TrimSuffix(strings.TrimPrefix(unit, "fased-app-maintenance@"), ".service")
		if id == "" {
			id = "%i"
		}
		active, sub := "inactive", "dead"
		if f.active[unit] {
			active, sub = "active", "running"
			if f.exited[unit] {
				sub = "exited"
			}
		}
		enabled := "linked"
		if f.enabled[unit] {
			enabled = "enabled"
		}
		command := maintenanceExecutable(f.m) + " maintain-update --operation " + id + " --prefix " + f.m.Root + " --data-dir " + f.m.Data
		if f.command != "" {
			command = f.command
		}
		return []byte(fmt.Sprintf("LoadState=loaded\nFragmentPath=%s\nDropInPaths=%s\nExecStart={ path=%s ; argv[]=%s ; ignore_errors=no ; }\nActiveState=%s\nSubState=%s\nUnitFileState=%s\nResult=success\n", maintenanceTemplatePath(f.m), f.drop, maintenanceExecutable(f.m), command, active, sub, enabled)), nil
	case "link":
		f.linked = true
	case "daemon-reload":
	case "enable":
		f.enabled[args[2]] = true
	case "disable":
		f.enabled[args[2]] = false
	case "start":
		f.active[args[3]] = true
	case "reset-failed":
	default:
		return nil, errors.New("unexpected manager command")
	}
	return nil, nil
}
func (f *maintenanceManagerFixture) count(action string) int {
	n := 0
	for _, a := range f.calls {
		if a[0] == action {
			n++
		}
	}
	return n
}
func TestApplicationMaintenancePersistentFixedOwnerEntry(t *testing.T) {
	a, f := maintenanceFixture(t)
	if e := InstallApplicationMaintenance(t.Context(), a.old, f.run); e != nil {
		t.Fatal(e)
	}
	if f.count("start") != 0 || f.count("enable") != 0 {
		t.Fatal("installation activated updater")
	}
	body, e := os.ReadFile(maintenanceTemplatePath(a.old))
	if e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"maintain-update --operation %i", "RemainAfterExit=yes", "WantedBy=default.target", "NoNewPrivileges=yes", "Restart=no"} {
		if !strings.Contains(string(body), want) {
			t.Fatal(want, string(body))
		}
	}
	if strings.Contains(string(body), "--rollback") || strings.Contains(string(body), "sudo") || strings.Contains(string(body), "/bin/sh") {
		t.Fatal("template gained arbitrary/root recovery authority")
	}
	state, e := LaunchApplicationMaintenance(t.Context(), a.old, a.candidate.ID, f.run)
	if e != nil || !state.Persistent || state.Phase != "running" {
		t.Fatal(state, e)
	}
	for _, call := range f.calls {
		if call[0] == "start" && (len(call) != 4 || call[1] != "--no-block" || call[2] != "--" || call[3] != maintenanceUnit(a.candidate.ID)) {
			t.Fatal("actor launch blocks core or escaped exact unit", call)
		}
	}
	if _, e = LaunchApplicationMaintenance(t.Context(), a.old, a.candidate.ID, f.run); e != nil || f.count("start") != 1 {
		t.Fatal("same running identity duplicated launch", e, f.calls)
	}
	// App service stop has no effect on the independent OS-manager actor. A new
	// user-manager boot retains the enabled unit and same durable request.
	f.active[maintenanceUnit(a.candidate.ID)] = false
	state, e = ApplicationMaintenanceStatus(t.Context(), a.old, a.candidate.ID, f.run)
	if e != nil || !state.Persistent || !state.Recoverable {
		t.Fatal(state, e)
	}
	if _, e = LaunchApplicationMaintenance(t.Context(), a.old, a.candidate.ID, f.run); e != nil || f.count("start") != 2 {
		t.Fatal("same authorized reboot recovery failed", e)
	}
	if _, e = LaunchApplicationMaintenance(t.Context(), a.old, strings.Repeat("b", 32), f.run); e == nil {
		t.Fatal("different operation bypassed existing actor")
	}
}
func TestApplicationMaintenanceNoOSAuthorityOrArbitraryCommands(t *testing.T) {
	a, f := maintenanceFixture(t)
	if e := InstallApplicationMaintenance(t.Context(), a.old, nil); e == nil {
		t.Fatal("missing authority accepted")
	}
	if _, e := os.Lstat(applicationUpdateDirectory(a.paths)); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("refused setup changed controller files", e)
	}
	f.denied = true
	if _, e := LaunchApplicationMaintenance(t.Context(), a.old, a.candidate.ID, f.run); e == nil {
		t.Fatal("absent owner manager accepted")
	}
	run := ApplicationMaintenanceRunner(a.old)
	for _, args := range [][]string{{"start", "--no-block", "--", "other.service"}, {"link", "--", "/tmp/other.service"}, {"start", "--no-block", "--", maintenanceUnit("../evil")}, {"stop", "--", maintenanceUnit(a.candidate.ID)}, {"enable", "--", maintenanceTemplateName, "arbitrary.service"}, {"disable", "--", maintenanceTemplateName}} {
		if _, e := run(t.Context(), args...); e == nil {
			t.Fatal("arbitrary owner command accepted", args)
		}
	}
}
func TestApplicationMaintenanceRefusesForeignTemplateImageAndCache(t *testing.T) {
	for _, kind := range []string{"template", "image", "dropin", "cached command"} {
		t.Run(kind, func(t *testing.T) {
			a, f := maintenanceFixture(t)
			if e := InstallApplicationMaintenance(t.Context(), a.old, f.run); e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "template":
				if e := os.WriteFile(maintenanceTemplatePath(a.old), []byte("foreign service"), 0600); e != nil {
					t.Fatal(e)
				}
			case "image":
				if e := os.WriteFile(maintenanceExecutable(a.old), []byte("changed maintenance executable"), 0700); e != nil {
					t.Fatal(e)
				}
			case "dropin":
				f.drop = "/home/owner/override.conf"
			case "cached command":
				f.command = maintenanceExecutable(a.old) + " maintain-update --operation " + a.candidate.ID + " --prefix " + a.old.Root + " --data-dir " + a.old.Data + " --unsafe"
			}
			if _, e := LaunchApplicationMaintenance(t.Context(), a.old, a.candidate.ID, f.run); e == nil {
				t.Fatal("foreign actor configuration accepted", kind)
			}
			if f.count("start") > 0 || f.count("enable") > 0 {
				t.Fatal("foreign state reached launch")
			}
		})
	}
}
func TestApplicationMaintenanceCleanupRequiresActualExactOutcome(t *testing.T) {
	a, f := maintenanceFixture(t)
	if e := InstallApplicationMaintenance(t.Context(), a.old, f.run); e != nil {
		t.Fatal(e)
	}
	if _, e := LaunchApplicationMaintenance(t.Context(), a.old, a.candidate.ID, f.run); e != nil {
		t.Fatal(e)
	}
	if e := CompleteApplicationMaintenance(t.Context(), a.old, a.candidate.ID, "completed", f.run); e == nil {
		t.Fatal("process launch mistaken for installed success")
	}
	if _, e := a.apply(t, nil, false); e != nil {
		t.Fatal(e)
	}
	if e := CompleteApplicationMaintenance(t.Context(), a.old, strings.Repeat("b", 32), "completed", f.run); e == nil {
		t.Fatal("other actor cleanup accepted")
	}
	if e := CompleteApplicationMaintenance(t.Context(), a.old, a.candidate.ID, "completed", f.run); e != nil {
		t.Fatal(e)
	}
	if !f.active[maintenanceUnit(a.candidate.ID)] || f.enabled[maintenanceUnit(a.candidate.ID)] || f.count("disable") != 1 {
		t.Fatal("cleanup stopped actor or retained reboot launch", f.calls)
	}
	actual, e := Load(a.paths)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = LaunchApplicationMaintenance(t.Context(), actual, a.candidate.ID, f.run); e != nil || f.count("start") != 1 {
		t.Fatal("cleaned actor was re-executed", e)
	}
	entry, e := ApplicationMaintenanceEntry(actual)
	if e != nil || entry != maintenanceExecutable(a.old) {
		t.Fatal(entry, e)
	}
	if got, e := updateFileDigest(entry); e != nil || got != a.old.Files[engineName] {
		t.Fatal("stable maintenance image was replaced with target", got, e)
	}
}
func TestApplicationMaintenanceDeferredCleanupNeverDropsDispatchedWork(t *testing.T) {
	a, f := maintenanceFixture(t)
	if e := InstallApplicationMaintenance(t.Context(), a.old, f.run); e != nil {
		t.Fatal(e)
	}
	if _, e := LaunchApplicationMaintenance(t.Context(), a.old, a.candidate.ID, f.run); e != nil {
		t.Fatal(e)
	}
	if e := CompleteApplicationMaintenance(t.Context(), a.old, a.candidate.ID, "deferred", f.run); e != nil {
		t.Fatal(e)
	}
	if _, e := LaunchApplicationMaintenance(t.Context(), a.old, strings.Repeat("b", 32), f.run); e != nil {
		t.Fatal("new authorized actor after deferral refused", e)
	}
	b, g := maintenanceFixture(t)
	if e := InstallApplicationMaintenance(t.Context(), b.old, g.run); e != nil {
		t.Fatal(e)
	}
	if _, e := LaunchApplicationMaintenance(t.Context(), b.old, b.candidate.ID, g.run); e != nil {
		t.Fatal(e)
	}
	_, _ = b.apply(t, func(p string) error {
		if p == "before-stop" {
			return errors.New("reboot")
		}
		return nil
	}, false)
	if e := CompleteApplicationMaintenance(t.Context(), b.old, b.candidate.ID, "deferred", g.run); e == nil {
		t.Fatal("pending replacement was discarded as deferred")
	}
	// No code path ever issues stop/kill on a maintenance actor.
	for _, c := range append(f.calls, g.calls...) {
		if c[0] == "stop" || c[0] == "kill" {
			t.Fatal(c)
		}
	}
}

func TestApplicationMaintenanceNeverInstallsIntoCoreWritableState(t *testing.T) {
	p, engine := installFixture(t)
	p.Data = applicationUpdateDirectory(p)
	m, e := Install(t.Context(), p, Options{Engine: engine})
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	run := func(context.Context, ...string) ([]byte, error) { calls++; return nil, nil }
	if e = InstallApplicationMaintenance(t.Context(), m, run); e == nil || calls != 0 {
		t.Fatal("maintenance role installed into app-writable state", e, calls)
	}
}

func TestInstallationPreparesMaintenanceWithoutActivatingOrUndoingApp(t *testing.T) {
	for _, tc := range []struct {
		name          string
		ready, denied bool
		want          string
	}{{"ready", true, false, "ready"}, {"manager unavailable", true, true, "manual-only"}, {"service deferred", false, false, "deferred"}} {
		t.Run(tc.name, func(t *testing.T) {
			a, f := maintenanceFixture(t)
			f.denied = tc.denied
			before := ManifestSHA256(a.old)
			var out bytes.Buffer
			got := prepareInstallationMaintenance(t.Context(), a.old, tc.ready, &out, func(ctx context.Context, m Manifest) error { return InstallApplicationMaintenance(ctx, m, f.run) })
			if got != tc.want {
				t.Fatal(got, out.String())
			}
			actual, e := Load(a.paths)
			if e != nil || ManifestSHA256(actual) != before {
				t.Fatal("optional setup undid installed app", e)
			}
			if f.count("start") > 0 || f.count("enable") > 0 {
				t.Fatal("installation enabled updates")
			}
			if !tc.ready && len(f.calls) > 0 {
				t.Fatal("deferred install contacted OS manager")
			}
		})
	}
}
func TestApplicationRemovalRefusesPendingOwnerActor(t *testing.T) {
	a, f := maintenanceFixture(t)
	if e := InstallApplicationMaintenance(t.Context(), a.old, f.run); e != nil {
		t.Fatal(e)
	}
	if _, e := LaunchApplicationMaintenance(t.Context(), a.old, a.candidate.ID, f.run); e != nil {
		t.Fatal(e)
	}
	if e := Uninstall(t.Context(), a.paths); e == nil {
		t.Fatal("removal ignored pending owner actor")
	}
	if e := verify(a.old); e != nil {
		t.Fatal("pending removal changed app", e)
	}
	if e := CompleteApplicationMaintenance(t.Context(), a.old, a.candidate.ID, "deferred", f.run); e != nil {
		t.Fatal(e)
	}
	if e := checkMaintenanceRemoval(a.old); e != nil {
		t.Fatal("completed cleanup blocked removal", e)
	}
}

func TestApplicationRemovalRefusesStandalonePendingUpdate(t *testing.T) {
	a := applicationFixtureFor(t)
	if _, e := a.apply(t, func(p string) error {
		if p == "before-stop" {
			return errors.New("interrupted owner terminal")
		}
		return nil
	}, false); e == nil {
		t.Fatal("fault missed")
	}
	if e := Uninstall(t.Context(), a.paths); e == nil {
		t.Fatal("removal ignored pending standalone update")
	}
	if e := verify(a.old); e != nil {
		t.Fatal("pending removal changed app", e)
	}
}
