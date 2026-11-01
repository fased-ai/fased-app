//go:build linux

package localinstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		id := strings.TrimSuffix(strings.TrimPrefix(unit, "fased-app-maintenance-system@"), ".service")
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
		command := maintenanceExecutable(f.m) + " maintenance-delegate --operation " + id + " --prefix " + f.m.Root + " --data-dir " + f.m.Data
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
	for _, want := range []string{"maintenance-delegate --operation %i", "RemainAfterExit=yes", "WantedBy=default.target", "NoNewPrivileges=yes", "Restart=no"} {
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
				f.command = maintenanceExecutable(a.old) + " maintenance-delegate --operation " + a.candidate.ID + " --prefix " + a.old.Root + " --data-dir " + a.old.Data + " --unsafe"
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
	}{{"ready", true, false, "ready"}, {"manager unavailable", true, true, "needs-setup"}, {"service deferred", false, false, "deferred"}} {
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

func TestApplicationMaintenanceCoreStartCannotWriteOwnerControl(t *testing.T) {
	a, f := maintenanceFixture(t)
	if e := InstallApplicationMaintenance(t.Context(), a.old, f.run); e != nil {
		t.Fatal(e)
	}
	dir := applicationUpdateDirectory(Paths{Root: a.old.Root, Data: a.old.Data})
	if e := os.Chmod(dir, 0500); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(dir, 0700)
	lockPath := filepath.Join(dir, "maintenance.lock")
	if e := os.Chmod(lockPath, 0400); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(lockPath, 0600)
	before, e := os.Stat(lockPath)
	if e != nil {
		t.Fatal(e)
	}
	if e := StartApplicationMaintenance(t.Context(), a.old, a.candidate.ID, f.run); e != nil {
		t.Fatal(e)
	}
	if f.count("start") != 1 || f.count("enable") != 0 {
		t.Fatal(f.calls)
	}
	if _, e := os.Stat(filepath.Join(dir, "system-maintenance.json")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("core wrote request", e)
	}
	after, e := os.Stat(lockPath)
	if e != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("core changed owner lock", e)
	}
	if e := os.Chmod(lockPath, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := PrepareApplicationMaintenanceOperation(t.Context(), a.old, a.candidate.ID, f.run); e != nil {
		t.Fatal(e)
	}
	if f.count("start") != 1 || f.count("enable") != 1 {
		t.Fatal("actor recursed or did not persist", f.calls)
	}
	if r, e := readMaintenanceRequest(a.old); e != nil || r.ID != a.candidate.ID {
		t.Fatal(r, e)
	}
}
func TestApplicationMaintenanceCoreRefusesForeignActor(t *testing.T) {
	a, f := maintenanceFixture(t)
	if e := InstallApplicationMaintenance(t.Context(), a.old, f.run); e != nil {
		t.Fatal(e)
	}
	f.drop = "/foreign.conf"
	if e := StartApplicationMaintenance(t.Context(), a.old, a.candidate.ID, f.run); e == nil || f.count("start") != 0 {
		t.Fatal(e, f.calls)
	}
}

func TestApplicationMaintenanceActorRetryEnsuresPersistence(t *testing.T) {
	a, f := maintenanceFixture(t)
	if e := InstallApplicationMaintenance(t.Context(), a.old, f.run); e != nil {
		t.Fatal(e)
	}
	if e := PrepareApplicationMaintenanceOperation(t.Context(), a.old, a.candidate.ID, f.run); e != nil {
		t.Fatal(e)
	}
	unit := maintenanceUnit(a.candidate.ID)
	// Model interruption after durable request but before enable; actual actor starts
	// again without a new operation. It must restore exactly its own persistence.
	f.active[unit] = true
	f.enabled[unit] = false
	before := f.count("enable")
	if e := PrepareApplicationMaintenanceOperation(t.Context(), a.old, a.candidate.ID, f.run); e != nil {
		t.Fatal(e)
	}
	if !f.enabled[unit] || f.count("enable") != before+1 {
		t.Fatal("retry did not persist", f.calls)
	}
}

func TestMaintenanceOwnerMapRefusesAmbiguousRootViews(t *testing.T) {
	for _, tc := range []struct {
		uid  int
		text string
		ok   bool
	}{{1100, "1100 0 1\n", true}, {1100, "1100 1100 1", false}, {1100, "1100 0 2", false}, {1100, "0 0 1\n1100 0 1", false}, {0, "0 0 1", false}, {1100, "1101 0 1", false}} {
		if got := maintenanceOwnerMap(tc.uid, tc.text); got != tc.ok {
			t.Fatal(tc, got)
		}
	}
}

func TestApplicationMaintenanceAcceptsOnlyExactLinkedTemplate(t *testing.T) {
	a, f := maintenanceFixture(t)
	if e := InstallApplicationMaintenance(t.Context(), a.old, f.run); e != nil {
		t.Fatal(e)
	}
	p, e := maintenanceManager(t.Context(), f.run, maintenanceUnit(strings.Repeat("0", 32)))
	if e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(t.TempDir(), "fased-app-maintenance-system@.service")
	if e = os.Symlink(maintenanceTemplatePath(a.old), link); e != nil {
		t.Fatal(e)
	}
	p["FragmentPath"] = link
	if e = checkMaintenanceTemplate(a.old, p); e != nil {
		t.Fatal("valid linked template refused", e)
	}
	if e = os.Remove(link); e != nil {
		t.Fatal(e)
	}
	foreign := filepath.Join(t.TempDir(), "foreign.service")
	if e = os.WriteFile(foreign, maintenanceTemplate(a.old), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(foreign, link); e != nil {
		t.Fatal(e)
	}
	if e = checkMaintenanceTemplate(a.old, p); e == nil {
		t.Fatal("foreign template alias accepted")
	}
}

func TestCoreMaintenanceDoesNotRestartFailedOrFinishedActor(t *testing.T) {
	for _, tc := range []struct {
		active, sub, result string
		ok                  bool
	}{{"inactive", "dead", "success", true}, {"active", "running", "success", true}, {"activating", "start", "success", true}, {"active", "exited", "success", false}, {"failed", "failed", "exit-code", false}, {"unknown", "dead", "success", false}, {"inactive", "dead", "resources", false}} {
		a, f := maintenanceFixture(t)
		if e := InstallApplicationMaintenance(t.Context(), a.old, f.run); e != nil {
			t.Fatal(e)
		}
		runner := func(ctx context.Context, args ...string) ([]byte, error) {
			b, e := f.run(ctx, args...)
			if args[0] == "show" {
				text := string(b)
				text = strings.ReplaceAll(text, "ActiveState=inactive", "ActiveState="+tc.active)
				text = strings.ReplaceAll(text, "SubState=dead", "SubState="+tc.sub)
				text = strings.ReplaceAll(text, "Result=success", "Result="+tc.result)
				b = []byte(text)
			}
			return b, e
		}
		e := StartApplicationMaintenance(t.Context(), a.old, a.candidate.ID, runner)
		if (e == nil) != tc.ok {
			t.Fatal(tc, e)
		}
		want := 0
		if tc.active == "inactive" && tc.ok {
			want = 1
		}
		if f.count("start") != want {
			t.Fatal("actor restarted", tc, f.calls)
		}
	}
}
func TestLocalMaintenancePrivilegeAssertionAndActorPath(t *testing.T) {
	good := "NoNewPrivs:\t1\nCapEff:\t0000000000000000\nCapPrm:\t0000000000000000\nCapInh:\t0000000000000000\nCapAmb:\t0000000000000000\nCapBnd:\t00000000a80425fb\n"
	if e := validateMaintenanceProcess(1000, 1000, good); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{strings.Replace(good, "NoNewPrivs:\t1", "NoNewPrivs:\t0", 1), strings.Replace(good, "CapPrm:\t0000000000000000", "CapPrm:\t0000000000000001", 1), strings.Replace(good, "CapAmb:\t0000000000000000", "CapAmb:\t0000000000000001", 1)} {
		if e := validateMaintenanceProcess(1000, 1000, bad); e == nil {
			t.Fatal("privileged actor accepted")
		}
	}
	if e := validateMaintenanceProcess(0, 0, good); e == nil {
		t.Fatal("root accepted")
	}
	m := Manifest{Root: "/fixed/app"}
	if !IsApplicationMaintenanceImage(m, maintenanceExecutable(m)) || !IsApplicationMaintenanceImage(m, maintenanceExecutable(m)+" (deleted)") {
		t.Fatal("invalid receipt could downgrade actor")
	}
}
