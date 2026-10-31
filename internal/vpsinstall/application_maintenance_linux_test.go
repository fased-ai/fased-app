//go:build linux

package vpsinstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

type maintenanceHost struct {
	host     *memoryHost
	ops      hostOps
	manifest Manifest
	enabled  map[string]bool
	running  map[string]bool
	starts   int
	override bool
}

func maintenanceHostFor(t *testing.T) *maintenanceHost {
	t.Helper()
	h, o, m := installFixture(t)
	for _, p := range []string{"/etc/sudoers.d", "/etc/polkit-1", "/etc/polkit-1/rules.d"} {
		h.files[p] = hostFile{UID: 0, GID: 0, Directory: true, Mode: os.ModeDir | 0755}
	}
	f := &maintenanceHost{host: h, ops: o, manifest: m, enabled: map[string]bool{}, running: map[string]bool{}}
	f.ops.applicationContract = func(context.Context, Manifest) ([]byte, error) {
		return []byte(`{"version":"test","stateSchema":35,"maintenanceProtocol":1}`), nil
	}
	command := o.command
	f.ops.command = func(ctx context.Context, a ...string) ([]byte, error) {
		if a[0] == "/usr/bin/sudo" && strings.Join(a[1:], " ") == "--version" {
			return []byte("Sudo version 1.9.15p5\n"), nil
		}
		if a[0] == "/usr/bin/systemctl" {
			if len(a) == 3 && a[1] == "is-active" && a[2] == "polkit.service" {
				return []byte("active\n"), nil
			}
			if len(a) > 3 && a[1] == "show" && strings.HasPrefix(a[2], "fased-vps-maintenance-") {
				var g applicationMaintenanceGrant
				if b, e := h.read(maintenanceRoot(m.OwnerUID) + "/grant.json"); e != nil {
					return nil, e
				} else if strict(b.Data, &g) != nil {
					return nil, errors.New("bad grant")
				}
				unit := a[2]
				id := strings.TrimSuffix(strings.Split(unit, "@")[1], ".service")
				active, sub, enabled, drop := "inactive", "dead", "disabled", ""
				if f.running[unit] {
					active, sub = "activating", "start"
				}
				if f.enabled[unit] {
					enabled = "enabled"
				}
				if f.override {
					drop = "/etc/systemd/system/foreign.conf"
				}
				return []byte(fmt.Sprintf("LoadState=loaded\nFragmentPath=%s\nDropInPaths=%s\nExecStart={ path=%s ; argv[]=%s maintain-update --remote --unattended --operation %s ; ignore_errors=no ; }\nUser=%d\nGroup=%d\nNoNewPrivileges=no\nWorkingDirectory=/\nActiveState=%s\nSubState=%s\nUnitFileState=%s\nResult=success\n", maintenanceTemplatePath(m.OwnerUID), drop, maintenanceImagePath(m.OwnerUID), maintenanceImagePath(m.OwnerUID), id, m.OwnerUID, m.OwnerGID, active, sub, enabled)), nil
			}
			if len(a) == 6 && a[1] == "--no-ask-password" && a[2] == "start" && a[3] == "--no-block" && a[4] == "--" {
				f.starts++
				f.running[a[5]] = true
				return nil, nil
			}
		}
		return command(ctx, a...)
	}
	run := o.run
	f.ops.run = func(ctx context.Context, in []byte, a ...string) ([]byte, error) {
		if a[0] == "/usr/bin/systemctl" && len(a) == 4 && (a[1] == "enable" || a[1] == "disable") && strings.HasPrefix(a[3], "fased-vps-maintenance-") {
			h.calls = append(h.calls, append([]string(nil), a...))
			f.enabled[a[3]] = a[1] == "enable"
			return nil, nil
		}
		return run(ctx, in, a...)
	}
	return f
}
func (f *maintenanceHost) prepare(t *testing.T) {
	t.Helper()
	if e := installApplicationMaintenance(t.Context(), f.manifest, f.ops, "/home/owner/.config/fased-app-vps-installer", "/home/owner", func(b []byte) error {
		if !bytes.Contains(b, []byte("#1000 ALL=(root) NOPASSWD:")) {
			return errors.New("bad owner")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestRemoteMaintenancePreparationLaunchAndDeferredCompletion(t *testing.T) {
	f := maintenanceHostFor(t)
	f.prepare(t)
	if f.starts != 0 || len(f.enabled) != 0 {
		t.Fatal("preparation activated an operation")
	}
	before := len(f.host.calls)
	f.prepare(t)
	for _, c := range f.host.calls[before:] {
		if c[0] == "/usr/bin/install" {
			t.Fatal("unchanged preparation recopied files")
		}
	}
	id := strings.Repeat("a", 32)
	app := f.ops
	app.uid, app.euid, app.gid = f.manifest.AppUID, f.manifest.AppUID, f.manifest.AppGID
	s, e := launchApplicationMaintenance(t.Context(), app, f.manifest, id)
	if e != nil || s.Phase != "running" || f.starts != 1 {
		t.Fatal(s, e)
	}
	if _, e = launchApplicationMaintenance(t.Context(), app, f.manifest, id); e != nil || f.starts != 1 {
		t.Fatal("duplicate launch", e)
	}
	if e = beginApplicationMaintenance(t.Context(), f.ops, f.manifest, id); e != nil {
		t.Fatal(e)
	}
	if !f.enabled[maintenanceUnit(f.manifest.OwnerUID, id)] {
		t.Fatal("reboot continuation was not retained")
	}
	if e = beginApplicationMaintenance(t.Context(), f.ops, f.manifest, strings.Repeat("b", 32)); e == nil {
		t.Fatal("another operation overlapped")
	}
	if e = completeApplicationMaintenance(t.Context(), f.ops, f.manifest, id, "completed"); e == nil {
		t.Fatal("unproven completion accepted")
	}
	if e = completeApplicationMaintenance(t.Context(), f.ops, f.manifest, id, "deferred"); e != nil {
		t.Fatal(e)
	}
	if f.enabled[maintenanceUnit(f.manifest.OwnerUID, id)] {
		t.Fatal("completed actor still enabled")
	}
	if e = completeApplicationMaintenance(t.Context(), f.ops, f.manifest, id, "deferred"); e != nil {
		t.Fatal("exact cleanup retry", e)
	}
}
func TestRemoteMaintenanceRefusesForeignAuthorityAndUnrelatedCaller(t *testing.T) {
	for _, kind := range []string{"policy", "image", "template", "override", "caller", "id"} {
		t.Run(kind, func(t *testing.T) {
			f := maintenanceHostFor(t)
			f.prepare(t)
			o := f.ops
			id := strings.Repeat("a", 32)
			switch kind {
			case "policy":
				v := f.host.files[maintenanceSudoPath(1000)]
				v.Data = []byte("foreign policy")
				f.host.files[maintenanceSudoPath(1000)] = v
			case "image":
				v := f.host.files[maintenanceImagePath(1000)]
				v.Data = []byte("changed")
				f.host.files[maintenanceImagePath(1000)] = v
			case "template":
				v := f.host.files[maintenanceTemplatePath(1000)]
				v.Mode = 0666
				f.host.files[maintenanceTemplatePath(1000)] = v
			case "override":
				f.override = true
			case "caller":
				o.uid, o.euid = 1234, 1234
			case "id":
				id = "../foreign"
			}
			if kind == "policy" {
				if e := beginApplicationMaintenance(t.Context(), o, f.manifest, id); e == nil {
					t.Fatal("foreign policy used")
				}
				return
			}
			if _, e := launchApplicationMaintenance(t.Context(), o, f.manifest, id); e == nil {
				t.Fatal("foreign authority launched")
			}
			if f.starts != 0 {
				t.Fatal("mutation before identity validation")
			}
		})
	}
}
func TestRemoteMaintenanceRemovalRevokesOnlyOwnedPolicies(t *testing.T) {
	f := maintenanceHostFor(t)
	f.prepare(t)
	id := strings.Repeat("a", 32)
	if e := beginApplicationMaintenance(t.Context(), f.ops, f.manifest, id); e != nil {
		t.Fatal(e)
	}
	if e := removeApplicationMaintenance(t.Context(), f.ops, f.manifest); e == nil {
		t.Fatal("pending actor removed")
	}
	if e := completeApplicationMaintenance(t.Context(), f.ops, f.manifest, id, "deferred"); e != nil {
		t.Fatal(e)
	}
	if e := removeApplicationMaintenance(t.Context(), f.ops, f.manifest); e != nil {
		t.Fatal(e)
	}
	if _, e := f.host.read(maintenanceSudoPath(1000)); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("sudo grant retained")
	}
	if _, e := f.host.read(maintenancePolkitPath(1000)); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("launch grant retained")
	}
	if _, e := f.host.read(f.manifest.Core); e != nil {
		t.Fatal("application removed by authority cleanup")
	}
	if e := removeApplicationMaintenance(t.Context(), f.ops, f.manifest); e != nil {
		t.Fatal("interrupted/exact cleanup retry", e)
	}
}
func TestRemoteMaintenanceOSRuleBoundariesAndSudoSyntax(t *testing.T) {
	f := maintenanceHostFor(t)
	m := f.manifest
	id := strings.Repeat("a", 32)
	dir := m.Root + "-update-" + id
	allowed := func(args []string) bool {
		for _, r := range maintenanceRules(m) {
			if r.tool == args[0] && regexp.MustCompile(r.args).MatchString(strings.Join(args[1:], " ")) {
				return true
			}
		}
		return false
	}
	good := [][]string{
		{"/usr/bin/systemctl", "enable", "--", m.Unit},
		{"/usr/bin/install", "-d", "-m", "755", "-o", "0", "-g", "0", "--", dir},
		{"/usr/bin/install", "-m", "755", "-o", "root", "-g", "root", "--", "/dev/stdin", dir + "/.next-core-" + id + ".pending"},
		{"/usr/bin/mv", "--force", "--no-target-directory", "--", dir + "/replacement-fased-app", m.Core},
		applicationOwnerHelperArgs(m, m.Core), applicationOwnerHelperArgs(m, dir+"/next-core"),
	}
	for _, args := range good {
		if !allowed(args) {
			t.Fatal("required fixed operation not granted", args)
		}
	}
	bad := [][]string{
		{"/usr/bin/systemctl", "enable", "--", "ssh.service"},
		{"/usr/bin/install", "-m", "755", "-o", "root", "-g", "root", "--", "/etc/shadow", dir + "/.next-core-" + id + ".pending"},
		{"/usr/bin/mv", "--force", "--no-target-directory", "--", dir + "/replacement-fased-app", "/etc/systemd/system/evil.service"},
		{"/usr/bin/systemctl", "start", "--", "ssh.service"},
		{"/usr/bin/systemctl", "daemon-reload"},
		{"/usr/bin/apt-get", "install", "--yes", "polkitd"},
		{"/usr/bin/cat", "--", "/etc/shadow"},
		{"/usr/bin/install", "-d", "-m", "755", "-o", "0", "-g", "0", "--", dir + "/../../evil"},
		{"/usr/bin/setpriv", "--reuid", "0", "--", "/bin/sh"},
	}
	for _, args := range bad {
		if allowed(args) {
			t.Fatal("unrelated root authority granted", args)
		}
	}
	policy := maintenanceSudoers(m)
	path := filepath.Join(t.TempDir(), "sudoers")
	if e := os.WriteFile(path, policy, 0600); e != nil {
		t.Fatal(e)
	}
	tool, e := exec.LookPath("visudo")
	if e != nil {
		t.Skip("syntax parser unavailable; generated boundary cases checked")
	}
	if out, e := exec.CommandContext(t.Context(), tool, "-c", "-f", path).CombinedOutput(); e != nil {
		t.Fatalf("OS policy parser: %v: %s", e, out)
	}
	// A numeric UID line must be a rule, not a comment. The JSON converter is an
	// independent optional parser check; it reads only the fixture, not host policy.
	if tool, e := exec.LookPath("cvtsudoers"); e == nil {
		out, e := exec.CommandContext(t.Context(), tool, "-f", "json", path).CombinedOutput()
		if e != nil || !bytes.Contains(out, []byte(strconv.Itoa(m.OwnerUID))) || !bytes.Contains(out, []byte("/usr/bin/install")) {
			t.Fatalf("numeric owner rule was not parsed: %v: %s", e, out)
		}
	}
}
func TestRemoteMaintenancePolkitLaunchOnly(t *testing.T) {
	f := maintenanceHostFor(t)
	node, e := exec.LookPath("node")
	if e != nil {
		t.Skip("node parser unavailable; no OS policy activation")
	}
	script := `const vm=require('node:vm');let rule;const scope={polkit:{Result:{YES:'yes'},addRule:r=>rule=r}};vm.runInNewContext(process.argv[1],scope);const user=process.argv[2],id='a'.repeat(32);for(const [u,verb,unit,action,yes] of [[user,'start','fased-vps-maintenance-1000@'+id+'.service','org.freedesktop.systemd1.manage-units',true],[user,'stop','fased-vps-maintenance-1000@'+id+'.service','org.freedesktop.systemd1.manage-units',false],[user,'start','ssh.service','org.freedesktop.systemd1.manage-units',false],[user,'start','fased-vps-maintenance-1000@../bad.service','org.freedesktop.systemd1.manage-units',false],['other','start','fased-vps-maintenance-1000@'+id+'.service','org.freedesktop.systemd1.manage-units',false],[user,'start','fased-vps-maintenance-1000@'+id+'.service','org.freedesktop.systemd1.manage-unit-files',false]]){const result=rule({id:action,lookup:k=>k==='unit'?unit:verb},{user:u});if((result==='yes')!==yes)process.exit(1)}`
	if out, e := exec.CommandContext(t.Context(), node, "-e", script, string(maintenancePolkit(f.manifest)), f.manifest.AppUser).CombinedOutput(); e != nil {
		t.Fatalf("launch-only policy: %v %s", e, out)
	}
}

func TestRemoteMaintenancePolicyReadDoesNotTreatUnavailableAuthorityAsMissing(t *testing.T) {
	f := maintenanceHostFor(t)
	f.prepare(t)
	read := f.ops.read
	run := f.ops.run
	path := maintenanceSudoPath(f.manifest.OwnerUID)
	f.ops.read = func(p string) (hostFile, error) {
		if p == path {
			return hostFile{}, os.ErrPermission
		}
		return read(p)
	}
	unavailable := false
	f.ops.run = func(ctx context.Context, in []byte, args ...string) ([]byte, error) {
		if args[0] == "/usr/bin/find" {
			if unavailable {
				return nil, errors.New("OS authority unavailable")
			}
			return []byte("0:0:440:f"), nil
		}
		if args[0] == "/usr/bin/cat" && args[len(args)-1] == path {
			return maintenanceSudoers(f.manifest), nil
		}
		return run(ctx, in, args...)
	}
	o := maintenancePolicyOps(t.Context(), f.ops, f.manifest)
	if got, e := rootFile(o, path); e != nil || !known(got, maintenanceSudoers(f.manifest), 0440) {
		t.Fatal(got, e)
	}
	unavailable = true
	if _, e := rootFile(o, path); e == nil || errors.Is(e, os.ErrNotExist) {
		t.Fatal("unknown authority was treated as a missing policy", e)
	}
}
func TestRemoteMaintenanceCancelledPreparationDoesNotMutate(t *testing.T) {
	f := maintenanceHostFor(t)
	before := len(f.host.calls)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if e := installApplicationMaintenance(ctx, f.manifest, f.ops, "/home/owner/.config/fased-app-vps-installer", "/home/owner", func([]byte) error { return nil }); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if len(f.host.calls) != before {
		t.Fatal("cancelled preparation touched host")
	}
}

func TestRemoteMaintenancePreparationInterruptionPreservesDataAndResumes(t *testing.T) {
	baseline := maintenanceHostFor(t)
	before := len(baseline.host.calls)
	baseline.prepare(t)
	steps := len(baseline.host.calls) - before
	for step := 1; step <= steps; step++ {
		t.Run(strconv.Itoa(step), func(t *testing.T) {
			f := maintenanceHostFor(t)
			sentinel := f.manifest.Data + "/owner-original"
			original := hostFile{UID: f.manifest.AppUID, GID: f.manifest.AppGID, Regular: true, Mode: 0600, Data: []byte("saved records; preserve exactly")}
			f.host.files[sentinel] = original
			f.host.fail = len(f.host.calls) + step
			e := installApplicationMaintenance(t.Context(), f.manifest, f.ops, "/home/owner/.config/fased-app-vps-installer", "/home/owner", func([]byte) error { return nil })
			if e == nil {
				t.Fatal("injected preparation failure ignored")
			}
			f.host.fail = 0
			if e = installApplicationMaintenance(t.Context(), f.manifest, f.ops, "/home/owner/.config/fased-app-vps-installer", "/home/owner", func([]byte) error { return nil }); e != nil {
				t.Fatal("exact retry failed", e)
			}
			after := f.host.files[sentinel]
			if after.UID != original.UID || after.GID != original.GID || after.Mode != original.Mode || !bytes.Equal(after.Data, original.Data) {
				t.Fatal("owner state changed")
			}
			if f.starts != 0 || len(f.enabled) != 0 {
				t.Fatal("preparation/retry activated updates")
			}
		})
	}
}

func TestRemoteMaintenanceNeverCopiesUnsupportedOlderHelper(t *testing.T) {
	f := maintenanceHostFor(t)
	before := len(f.host.calls)
	f.ops.applicationContract = func(context.Context, Manifest) ([]byte, error) { return []byte("0.0.0-core.1\n"), nil }
	if e := installApplicationMaintenance(t.Context(), f.manifest, f.ops, "/home/owner/.config/fased-app-vps-installer", "/home/owner", func([]byte) error { return nil }); e == nil {
		t.Fatal("old helper copied into a persistent grant")
	}
	if len(f.host.calls) != before {
		t.Fatal("OS authorization/mutation before unsupported-helper refusal")
	}
	if _, e := f.host.read(maintenanceImagePath(1000)); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("unsupported maintenance image persisted")
	}
}

func TestRemoteMaintenanceOwnerJournalCannotBeReplacedThroughParent(t *testing.T) {
	f := maintenanceHostFor(t)
	control := "/home/owner/.config/fased-app-vps-installer"
	for _, p := range []string{"/home", "/home/owner", "/home/owner/.config", control} {
		uid, mode := f.manifest.OwnerUID, os.FileMode(0755)
		if p == "/home" {
			uid = 0
		}
		if p == control {
			mode = 0700
		}
		f.host.files[p] = hostFile{UID: uid, GID: uid, Directory: true, Mode: os.ModeDir | mode}
	}
	if e := checkMaintenanceControlParents(f.ops, f.manifest, control); e != nil {
		t.Fatal(e)
	}
	parent := f.host.files["/home/owner/.config"]
	for _, kind := range []string{"app-owner", "shared-write", "symlink"} {
		v := parent
		switch kind {
		case "app-owner":
			v.UID = f.manifest.AppUID
		case "shared-write":
			v.Mode = os.ModeDir | 0777
		case "symlink":
			v.Directory = false
			v.Mode = os.ModeSymlink | 0777
		}
		f.host.files["/home/owner/.config"] = v
		if e := checkMaintenanceControlParents(f.ops, f.manifest, control); e == nil {
			t.Fatal("unsafe release-floor ancestry accepted", kind)
		}
	}
}

func TestRemoteMaintenanceUbuntuProtectedPolkitDirectoryGroup(t *testing.T) {
	f := maintenanceHostFor(t)
	dir := f.host.files["/etc/polkit-1/rules.d"]
	dir.GID = 990
	dir.Mode = os.ModeDir | 0750
	f.host.files["/etc/polkit-1/rules.d"] = dir
	f.prepare(t)
	if e := verifyMaintenancePolicies(t.Context(), f.ops, f.manifest); e != nil {
		t.Fatal(e)
	}
	if _, e := rootFile(f.ops, maintenancePolkitPath(1000)); e == nil {
		t.Fatal("ordinary root-file contract was weakened")
	}
	dir.Mode = os.ModeDir | 0770
	f.host.files["/etc/polkit-1/rules.d"] = dir
	if e := verifyMaintenancePolicies(t.Context(), f.ops, f.manifest); e == nil {
		t.Fatal("group-writable policy parent accepted")
	}
}
