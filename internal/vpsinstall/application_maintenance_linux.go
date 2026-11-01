//go:build linux

package vpsinstall

import (
	"context"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/hostplatform"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/localinstall"
)

const maintenanceManagerProperties = "--property=LoadState,FragmentPath,DropInPaths,ExecStart,User,Group,NoNewPrivileges,WorkingDirectory,ActiveState,SubState,UnitFileState,Result"

// Public fixed topology only; the runtime cannot choose an owner, source or
// executable. The normal installed runtime has already proved its sandbox.
func ApplicationMaintenanceManifest(owner int) (Manifest, error) {
	o := liveOps(nil)
	m, e := manifestAt(o, owner)
	if e != nil {
		return Manifest{}, e
	}
	if e = ordinary(o); e != nil {
		return Manifest{}, e
	}
	if o.uid != m.AppUID || o.gid != m.AppGID {
		return Manifest{}, errors.New("installed Remote app identity required")
	}
	if e = verifyFiles(o, m); e != nil {
		return Manifest{}, e
	}
	return m, nil
}

type applicationMaintenanceRequest struct {
	Version                                              int `json:"version"`
	ID, InstallationID, PreviousManifestSHA256, Finished string
}

// Private /etc policy directories may be unsearchable by the ordinary owner.
// Read only these exact public policy records through fixed OS tools; validate
// root metadata before reading bytes. A symlink/foreign file is never adopted.
func maintenancePolicyOps(ctx context.Context, o hostOps, m Manifest) hostOps {
	read := o.read
	policyPath := func(path string) bool {
		for _, target := range []string{maintenanceSudoPath(m.OwnerUID), maintenancePolkitPath(m.OwnerUID)} {
			if path == target || path == filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+"-"+m.ID+".pending") {
				return true
			}
		}
		return false
	}
	o.rootParents = func(path string) error {
		if !policyPath(path) {
			return parents(o, filepath.Dir(path))
		}
		// Ubuntu rules.d is root:polkitd0750. Its read-only group is legitimate;
		// root ownership/no group-or-other write, not numeric GID0, protects it.
		for p := filepath.Dir(path); ; p = filepath.Dir(p) {
			f, e := read(p)
			if e != nil {
				return e
			}
			if !f.Directory || f.Mode&os.ModeSymlink != 0 || f.UID != 0 || f.Mode.Perm()&0022 != 0 {
				return errors.New("maintenance policy ancestors must be root-owned and protected from group/other writes")
			}
			if p == "/" {
				return nil
			}
		}
	}
	o.read = func(path string) (hostFile, error) {
		f, e := read(path)
		if !errors.Is(e, os.ErrPermission) {
			return f, e
		}
		if !policyPath(path) {
			return f, e
		}
		meta, err := o.run(ctx, nil, "/usr/bin/find", filepath.Dir(path), "-maxdepth", "1", "-name", filepath.Base(path), "-printf", "%U:%G:%m:%y")
		if err != nil {
			return hostFile{}, err
		}
		if len(meta) == 0 {
			return hostFile{}, os.ErrNotExist
		}
		parts := strings.Split(string(meta), ":")
		if len(parts) != 4 || parts[0] != "0" || parts[1] != "0" || parts[3] != "f" {
			return hostFile{}, errors.New("foreign maintenance policy; preserving it")
		}
		mode, err := strconv.ParseUint(parts[2], 8, 32)
		if err != nil || mode&0022 != 0 {
			return hostFile{}, errors.New("unsafe maintenance policy")
		}
		b, err := o.run(ctx, nil, "/usr/bin/cat", "--", path)
		if err != nil || len(b) > 32<<10 {
			return hostFile{}, errors.New("maintenance policy cannot be read safely")
		}
		return hostFile{UID: 0, GID: 0, Mode: os.FileMode(mode), Regular: true, Data: b}, nil
	}
	return o
}

func readMaintenanceGrant(o hostOps, m Manifest) (applicationMaintenanceGrant, error) {
	var g applicationMaintenanceGrant
	if m.validate() != nil {
		return g, ErrApplicationMaintenanceAuthority
	}
	if e := checkDirectory(o, maintenanceRoot(m.OwnerUID), 0, 0, 0755); e != nil {
		return g, e
	}
	f, e := rootFile(o, maintenanceRoot(m.OwnerUID)+"/grant.json")
	if e != nil || f.Mode.Perm() != 0644 || strict(f.Data, &g) != nil || !g.matches(m) {
		return g, ErrApplicationMaintenanceAuthority
	}
	for _, p := range []string{g.OwnerHome, g.OwnerControl} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.ContainsAny(p, " \t\n\r\x00\"%") {
			return g, errors.New("fixed maintenance owner paths required")
		}
	}
	if !strings.HasSuffix(g.OwnerControl, "/fased-app-vps-installer") || g.OwnerHome == "/" || g.OwnerControl == m.Data || strings.HasPrefix(g.OwnerControl, m.Root+"/") {
		return g, errors.New("maintenance control must be outside app-owned state")
	}
	image, e := rootFile(o, maintenanceImagePath(m.OwnerUID))
	if e != nil || image.Mode.Perm() != 0755 || hash(image.Data) != g.ImageSHA256 {
		return g, errors.New("Remote maintenance image changed")
	}
	unit, e := rootFile(o, maintenanceTemplatePath(m.OwnerUID))
	if e != nil || !known(unit, maintenanceTemplate(g), 0644) {
		return g, errors.New("Remote maintenance template is missing or foreign")
	}
	return g, nil
}
func verifyMaintenancePolicies(ctx context.Context, o hostOps, m Manifest) error {
	o = maintenancePolicyOps(ctx, o, m)
	for _, p := range []struct {
		path  string
		bytes []byte
		mode  os.FileMode
	}{{maintenanceSudoPath(m.OwnerUID), maintenanceSudoers(m), 0440}, {maintenancePolkitPath(m.OwnerUID), maintenancePolkit(m), 0644}} {
		f, e := rootFile(o, p.path)
		if e != nil || !known(f, p.bytes, p.mode) {
			return errors.New("Remote maintenance policy is missing or foreign; no authority used")
		}
	}
	return nil
}
func maintenanceFields(ctx context.Context, o hostOps, m Manifest, id string) (map[string]string, error) {
	if !canonicalHex(id, 16) {
		return nil, errors.New("exact maintenance identity required")
	}
	b, e := o.command(ctx, "/usr/bin/systemctl", "show", maintenanceUnit(m.OwnerUID, id), maintenanceManagerProperties)
	if e != nil || len(b) > 32<<10 {
		return nil, ErrApplicationMaintenanceAuthority
	}
	fields := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok || k == "" {
			return nil, errors.New("invalid maintenance manager observation")
		}
		if _, exists := fields[k]; exists {
			return nil, errors.New("duplicate maintenance manager field")
		}
		fields[k] = v
	}
	g, e := readMaintenanceGrant(o, m)
	if e != nil {
		return nil, e
	}
	if fields["LoadState"] != "loaded" || fields["FragmentPath"] != maintenanceTemplatePath(m.OwnerUID) || fields["DropInPaths"] != "" || fields["User"] != strconv.Itoa(m.OwnerUID) || fields["Group"] != strconv.Itoa(m.OwnerGID) || fields["NoNewPrivileges"] != "no" || fields["WorkingDirectory"] != "/" {
		return nil, errors.New("unexpected Remote maintenance runtime identity")
	}
	execStart := fields["ExecStart"]
	_, argv, ok := strings.Cut(execStart, "argv[]=")
	argv, _, end := strings.Cut(argv, " ;")
	expected := maintenanceImagePath(g.OwnerUID) + " maintain-update --remote --unattended --operation " + id
	if !ok || !end || strings.Count(execStart, "{") != 1 || strings.Count(execStart, "}") != 1 || !strings.Contains(execStart, "path="+maintenanceImagePath(g.OwnerUID)+" ;") || argv != expected {
		return nil, errors.New("Remote maintenance cached command changed")
	}
	return fields, nil
}

// Explicit installer/ordinary-owner preparation. It does not set Automatic,
// start an update, enable any operation unit, or configure a public source.
func InstallApplicationMaintenance(ctx context.Context, m Manifest, s *custodyinstall.System) error {
	if s == nil || s.NonInteractive {
		return errors.New("maintenance preparation requires the ordinary administrator terminal")
	}
	o := liveOps(s)
	control, e := ownerJournal()
	if e != nil {
		return e
	}
	if e = checkMaintenanceControlParents(o, m, control); e != nil {
		return e
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	return installApplicationMaintenance(ctx, m, o, control, home, func(b []byte) error {
		// visudo parses the exact candidate, without installing or executing it.
		d, e := os.MkdirTemp(control, ".policy-check-")
		if e != nil {
			return e
		}
		defer os.RemoveAll(d)
		p := filepath.Join(d, "sudoers")
		if e = os.WriteFile(p, b, 0600); e != nil {
			return e
		}
		for _, tool := range []string{"/usr/sbin/visudo", "/usr/bin/visudo"} {
			if _, e = os.Lstat(tool); errors.Is(e, os.ErrNotExist) {
				continue
			}
			if e = trustedTool(o, tool); e != nil {
				return e
			}
			if out, e := exec.CommandContext(ctx, tool, "--check", "--file", p).CombinedOutput(); e != nil {
				return fmt.Errorf("maintenance policy syntax refused: %s", out)
			}
			return nil
		}
		return errors.New("Ubuntu sudo/visudo prerequisite is missing")
	})
}
func installApplicationMaintenance(ctx context.Context, m Manifest, o hostOps, control, home string, validate func([]byte) error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := ownedBy(o, m); e != nil {
		return e
	}
	lock, e := o.lock()
	if e != nil {
		return e
	}
	defer lock.Close()
	osInfo, e := osRelease(o)
	if e != nil {
		return e
	}
	if _, e = hostplatform.LookupRemote(string(osInfo.Data), runtime.GOARCH); e != nil {
		return e
	}

	if e := verifyFiles(o, m); e != nil {
		return e
	}
	if o.applicationContract == nil {
		return errors.New("installed maintenance capability inspection is unavailable")
	}
	contract, contractErr := o.applicationContract(ctx, m)
	var capability struct {
		Version             string `json:"version"`
		StateSchema         int    `json:"stateSchema"`
		MaintenanceProtocol int    `json:"maintenanceProtocol"`
	}
	if contractErr != nil || strict(contract, &capability) != nil || capability.MaintenanceProtocol != 1 || capability.StateSchema != core.StateSchema {
		return errors.New("upgrade this installation before preparing Remote unattended maintenance")
	}
	if e := verifyAccount(ctx, o, m); e != nil {
		return e
	}
	if e := loaded(ctx, o, m); e != nil {
		return e
	}
	if validate == nil {
		return errors.New("OS policy validation is required")
	}
	for _, p := range []string{control, home} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.ContainsAny(p, " \t\n\r\x00\"%") {
			return errors.New("fixed ordinary-owner paths required")
		}
	}
	if !strings.HasSuffix(control, "/fased-app-vps-installer") {
		return errors.New("existing owner control directory required")
	}
	// Ubuntu requires regex-aware sudo. Never silently turn ERE into globs.
	b, e := o.command(ctx, "/usr/bin/sudo", "--version")
	if e != nil {
		return e
	}
	first, _, _ := strings.Cut(string(b), "\n")
	var major, minor, patch int
	if _, e = fmt.Sscanf(first, "Sudo version %d.%d.%d", &major, &minor, &patch); e != nil || major != 1 || minor < 9 || minor == 9 && patch < 10 {
		return errors.New("Remote maintenance needs sudo 1.9.10 or newer")
	}
	if e = validate(maintenanceSudoers(m)); e != nil {
		return e
	}
	// Consent was obtained by the fixed System OS ceremony. Install only the
	// missing OS dependency; the update-time grant never permits apt-get.
	if _, e = o.command(ctx, "/usr/bin/systemctl", "is-active", "polkit.service"); e != nil {
		loaded, loadErr := o.command(ctx, "/usr/bin/systemctl", "show", "polkit.service", "--property=LoadState", "--value")
		if loadErr != nil {
			return loadErr
		}
		if strings.TrimSpace(string(loaded)) == "not-found" {
			if _, e = o.run(ctx, nil, "/usr/bin/apt-get", "install", "--yes", "--no-install-recommends", "polkitd"); e != nil {
				return e
			}
		} else if strings.TrimSpace(string(loaded)) != "loaded" {
			return errors.New("unknown polkit prerequisite state")
		}
		if _, e = o.run(ctx, nil, "/usr/bin/systemctl", "start", "polkit.service"); e != nil {
			return e
		}
	}
	g := applicationMaintenanceGrant{Version: 1, InstallationID: m.ID, OwnerUID: m.OwnerUID, OwnerGID: m.OwnerGID, AppUID: m.AppUID, AppGID: m.AppGID, Root: m.Root, Data: m.Data, Config: m.Config, Unit: m.Unit, AppUser: m.AppUser, OwnerControl: control, OwnerHome: home, ImageSHA256: m.CoreSHA256}
	grantPath := maintenanceRoot(m.OwnerUID) + "/grant.json"
	if existing, e := rootFile(o, grantPath); e == nil {
		var saved applicationMaintenanceGrant
		if strict(existing.Data, &saved) != nil || !saved.matches(m) || saved.OwnerControl != control || saved.OwnerHome != home {
			return errors.New("foreign Remote maintenance grant")
		}
		g = saved // Stable bootstrap image does not silently follow app replacements.
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	o = maintenancePolicyOps(ctx, o, m)
	// Inspect all destinations before publishing the first authority file.
	for _, p := range []struct {
		path string
		b    []byte
		mode os.FileMode
	}{{maintenanceSudoPath(m.OwnerUID), maintenanceSudoers(m), 0440}, {maintenancePolkitPath(m.OwnerUID), maintenancePolkit(m), 0644}, {maintenanceTemplatePath(m.OwnerUID), maintenanceTemplate(g), 0644}} {
		if f, e := rootFile(o, p.path); e == nil && !known(f, p.b, p.mode) {
			return errors.New("foreign maintenance policy/template; preserving it")
		} else if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}
	if e = directory(ctx, o, maintenanceRoot(m.OwnerUID), 0, 0, 0755); e != nil {
		return e
	}
	image, e := rootFile(o, maintenanceImagePath(m.OwnerUID))
	if errors.Is(e, os.ErrNotExist) {
		original, e := rootFile(o, m.Core)
		if e != nil || hash(original.Data) != g.ImageSHA256 {
			return errors.New("original maintenance image unavailable")
		}
		if e = publish(ctx, o, maintenanceImagePath(m.OwnerUID), m.ID, original.Data, 0755); e != nil {
			return e
		}
	} else if e != nil || image.Mode.Perm() != 0755 || hash(image.Data) != g.ImageSHA256 {
		return errors.New("maintenance image changed")
	}
	for _, p := range []struct {
		path string
		b    []byte
		mode os.FileMode
	}{{maintenanceTemplatePath(m.OwnerUID), maintenanceTemplate(g), 0644}, {maintenanceSudoPath(m.OwnerUID), maintenanceSudoers(m), 0440}, {maintenancePolkitPath(m.OwnerUID), maintenancePolkit(m), 0644}, {grantPath, encoded(g), 0644}} {
		if e = publish(ctx, o, p.path, m.ID, p.b, p.mode); e != nil {
			return e
		}
	}
	if _, e = o.run(ctx, nil, "/usr/bin/systemctl", "daemon-reload"); e != nil {
		return e
	}
	_, e = maintenanceFields(ctx, o, m, strings.Repeat("0", 32))
	return e
}

func ValidateApplicationMaintenanceEntry(m Manifest, image string) error {
	o := liveOps(nil)
	if e := ownedBy(o, m); e != nil {
		return e
	}
	if image != maintenanceImagePath(m.OwnerUID) {
		return errors.New("fixed Remote maintenance executable required")
	}
	g, e := readMaintenanceGrant(o, m)
	if e != nil {
		return e
	}
	return checkMaintenanceOwnerEnvironment(g)
}
func checkMaintenanceOwnerEnvironment(g applicationMaintenanceGrant) error {
	config, e := os.UserConfigDir()
	if e != nil {
		return e
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	if filepath.Join(config, "fased-app-vps-installer") != g.OwnerControl || home != g.OwnerHome {
		return errors.New("Remote maintenance must use its installed owner environment and journal")
	}
	return checkMaintenanceControlParents(liveOps(nil), Manifest{OwnerUID: g.OwnerUID}, g.OwnerControl)
}

// The app must not be able to hide an owner journal by renaming an ancestor and
// make the maintainer recreate an empty release floor at the same pathname.
func checkMaintenanceControlParents(o hostOps, m Manifest, control string) error {
	for p := control; ; p = filepath.Dir(p) {
		f, e := o.read(p)
		if e != nil || !f.Directory || f.Mode&os.ModeSymlink != 0 || (f.UID != 0 && f.UID != m.OwnerUID) || f.Mode.Perm()&0022 != 0 {
			return errors.New("maintenance journal needs protected root/owner directory ancestors")
		}
		if p == control && (f.UID != m.OwnerUID || f.Mode.Perm() != 0700) {
			return errors.New("private existing maintenance journal required")
		}
		if p == "/" {
			return nil
		}
	}
}
func ApplicationMaintenanceStatus(ctx context.Context, m Manifest, id string) (localinstall.MaintenanceStatus, error) {
	return applicationMaintenanceStatus(ctx, liveOps(nil), m, id)
}
func applicationMaintenanceStatus(ctx context.Context, o hostOps, m Manifest, id string) (localinstall.MaintenanceStatus, error) {
	result := localinstall.MaintenanceStatus{ID: id, Unit: maintenanceUnit(m.OwnerUID, id)}
	if e := ordinary(o); e != nil {
		return result, e
	}
	if m.validate() != nil || (o.uid != m.OwnerUID && o.uid != m.AppUID) {
		return result, ErrApplicationMaintenanceAuthority
	}
	fields, e := maintenanceFields(ctx, o, m, id)
	if e != nil {
		return result, e
	}
	result.Phase = "ready"
	result.Persistent = fields["UnitFileState"] == "enabled"
	if fields["ActiveState"] == "activating" || fields["ActiveState"] == "active" && fields["SubState"] != "exited" {
		result.Phase = "running"
	}
	if fields["SubState"] == "exited" {
		result.Phase = "finished"
	}
	if fields["ActiveState"] == "failed" || fields["Result"] != "" && fields["Result"] != "success" {
		result.Phase = "failed"
		result.Recoverable = true
	}
	return result, nil
}
func LaunchApplicationMaintenance(ctx context.Context, m Manifest, id string, _ *custodyinstall.System) (localinstall.MaintenanceStatus, error) {
	return launchApplicationMaintenance(ctx, liveOps(nil), m, id)
}
func launchApplicationMaintenance(ctx context.Context, o hostOps, m Manifest, id string) (localinstall.MaintenanceStatus, error) {
	if e := ctx.Err(); e != nil {
		return localinstall.MaintenanceStatus{}, e
	}
	result, e := applicationMaintenanceStatus(ctx, o, m, id)
	if e != nil {
		return result, e
	}
	if result.Phase == "running" {
		return result, nil
	}
	if result.Phase == "failed" || result.Phase == "finished" {
		return result, errors.New("Remote maintenance needs explicit recovery; automatic retry refused")
	}
	// Polkit grants AppUID only this fixed start operation; no sudo in the agent.
	if _, e = o.command(ctx, "/usr/bin/systemctl", "--no-ask-password", "start", "--no-block", "--", result.Unit); e != nil {
		return result, e
	}
	result.Phase = "running"
	return result, nil
}

func BeginApplicationMaintenance(ctx context.Context, m Manifest, id string, s *custodyinstall.System) error {
	if s == nil || !s.NonInteractive {
		return errors.New("fixed unattended owner composition required")
	}
	o := liveOps(s)
	g, e := readMaintenanceGrant(o, m)
	if e != nil {
		return e
	}
	if e = checkMaintenanceOwnerEnvironment(g); e != nil {
		return e
	}
	return beginApplicationMaintenance(ctx, o, m, id)
}
func beginApplicationMaintenance(ctx context.Context, o hostOps, m Manifest, id string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := ownedBy(o, m); e != nil {
		return e
	}
	lock, e := o.lock()
	if e != nil {
		return e
	}
	defer lock.Close()
	if e := accessIdle(o, m); e != nil {
		return e
	}
	if !canonicalHex(id, 16) {
		return errors.New("exact operation required")
	}
	if _, e := readMaintenanceGrant(o, m); e != nil {
		return e
	}
	if e := verifyMaintenancePolicies(ctx, o, m); e != nil {
		return e
	}
	var request applicationMaintenanceRequest
	b, e := o.readJournal("application-maintenance.json")
	if e == nil {
		if strict(b, &request) != nil || request.Version != 1 || request.InstallationID != m.ID || !canonicalHex(request.ID, 16) {
			return errors.New("unknown maintenance request; preserve it")
		}
		if request.ID != id && request.Finished == "" {
			return errors.New("another Remote maintenance operation is unfinished")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if request.ID != id {
		request = applicationMaintenanceRequest{Version: 1, ID: id, InstallationID: m.ID, PreviousManifestSHA256: ManifestSHA256(m)}
		if e = o.writeJournal("application-maintenance.json", request); e != nil {
			return e
		}
	}
	if request.Finished != "" {
		return nil
	}
	_, e = o.run(ctx, nil, "/usr/bin/systemctl", "enable", "--", maintenanceUnit(m.OwnerUID, id))
	return e
}
func CompleteApplicationMaintenance(ctx context.Context, m Manifest, id, phase string, s *custodyinstall.System) error {
	if s == nil || !s.NonInteractive {
		return errors.New("fixed unattended owner composition required")
	}
	o := liveOps(s)
	g, e := readMaintenanceGrant(o, m)
	if e != nil {
		return e
	}
	if e = checkMaintenanceOwnerEnvironment(g); e != nil {
		return e
	}
	return completeApplicationMaintenance(ctx, o, m, id, phase)
}
func completeApplicationMaintenance(ctx context.Context, o hostOps, m Manifest, id, phase string) error {
	if e := ownedBy(o, m); e != nil {
		return e
	}
	lock, e := o.lock()
	if e != nil {
		return e
	}
	defer lock.Close()
	if e := accessIdle(o, m); e != nil {
		return e
	}
	if !canonicalHex(id, 16) || (phase != "completed" && phase != "rolled-back" && phase != "deferred") {
		return errors.New("exact terminal outcome required")
	}
	if _, e := readMaintenanceGrant(o, m); e != nil {
		return e
	}
	if e := verifyMaintenancePolicies(ctx, o, m); e != nil {
		return e
	}
	b, e := o.readJournal("application-maintenance.json")
	if e != nil {
		return e
	}
	var request applicationMaintenanceRequest
	if strict(b, &request) != nil || request.Version != 1 || request.ID != id || request.InstallationID != m.ID || request.Finished != "" && request.Finished != phase {
		return errors.New("maintenance completion identity changed")
	}
	j, je := readApplicationJournal(o)
	if phase == "deferred" {
		if ManifestSHA256(m) != request.PreviousManifestSHA256 || je == nil && j.Status.ID == id || je != nil && !errors.Is(je, os.ErrNotExist) {
			return errors.New("started installation cannot be discarded")
		}
	} else {
		if je != nil || j.Status.ID != id || j.Status.Phase != phase {
			return errors.New("durable installation outcome required")
		}
		expected := j.Target
		if phase == "rolled-back" {
			expected = j.Previous
		}
		if m != expected || verifyFiles(o, m) != nil {
			return errors.New("terminal installed bytes changed")
		}
	}
	if _, e = o.run(ctx, nil, "/usr/bin/systemctl", "disable", "--", maintenanceUnit(m.OwnerUID, id)); e != nil {
		return e
	}
	request.Finished = phase
	return o.writeJournal("application-maintenance.json", request)
}

// Fixed public rules are removed before ordinary removal; unfinished updates
// remain recoverable. Source images/journals/data are retained for their owner.
func removeApplicationMaintenance(ctx context.Context, o hostOps, m Manifest) error {
	if _, e := rootFile(o, maintenanceRoot(m.OwnerUID)+"/grant.json"); errors.Is(e, os.ErrNotExist) {
		return nil
	} else if e != nil {
		return e
	}
	if _, e := readMaintenanceGrant(o, m); e != nil {
		return e
	}
	if b, e := o.readJournal("application-maintenance.json"); e == nil {
		var request applicationMaintenanceRequest
		if strict(b, &request) != nil || request.InstallationID != m.ID || request.Finished == "" {
			return errors.New("finish Remote maintenance before removal")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	o = maintenancePolicyOps(ctx, o, m)
	for _, p := range []struct {
		path string
		b    []byte
		mode os.FileMode
	}{{maintenancePolkitPath(m.OwnerUID), maintenancePolkit(m), 0644}, {maintenanceSudoPath(m.OwnerUID), maintenanceSudoers(m), 0440}} {
		f, e := rootFile(o, p.path)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil || !known(f, p.b, p.mode) {
			return errors.New("foreign maintenance policy; preserving it")
		}
	}
	for _, path := range []string{maintenancePolkitPath(m.OwnerUID), maintenanceSudoPath(m.OwnerUID)} {
		if _, e := rootFile(o, path); errors.Is(e, os.ErrNotExist) {
			continue
		} else if e != nil {
			return e
		}
		if _, e := o.run(ctx, nil, "/usr/bin/rm", "--", path); e != nil {
			return e
		}
	}
	return nil
}

func ApplicationAcquisitionParent(m Manifest) (string, error) {
	if e := ownedBy(liveOps(nil), m); e != nil {
		return "", e
	}
	return ownerJournal()
}
