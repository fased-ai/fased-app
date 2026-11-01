//go:build linux

package localinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
)

const maintenanceTemplateName = "fased-app-maintenance-system@.service"
const maintenanceProperties = "--property=LoadState,FragmentPath,DropInPaths,ExecStart,ActiveState,SubState,UnitFileState,Result"

type maintenanceImage struct {
	Version int    `json:"version"`
	Root    string `json:"root"`
	Data    string `json:"data"`
	SHA256  string `json:"sha256"`
}

func maintenanceExecutable(m Manifest) string {
	return filepath.Join(applicationUpdateDirectory(Paths{Root: m.Root, Data: m.Data}), "fased-app-maintenance-system")
}
func checkMaintenanceImage(m Manifest) error {
	b, e := core.ReadPrivateFile(maintenanceExecutable(m) + ".json")
	if e != nil {
		return e
	}
	var image maintenanceImage
	if len(b) > 4096 || json.Unmarshal(b, &image) != nil || image.Version != 1 || image.Root != m.Root || image.Data != m.Data || !validExpectedDigest(image.SHA256) {
		return errors.New("owner maintenance executable identity changed")
	}
	got, e := updateFileDigest(maintenanceExecutable(m))
	if e != nil || got != image.SHA256 {
		return errors.New("owner maintenance executable changed; preserve it")
	}
	return nil
}
func installMaintenanceImage(m Manifest) error {
	if _, e := os.Lstat(maintenanceExecutable(m) + ".json"); e == nil {
		return checkMaintenanceImage(m)
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if e := copyUpdateFile(filepath.Join(m.Root, engineName), maintenanceExecutable(m), m.Files[engineName]); e != nil {
		return e
	}
	return core.WritePrivateJSON(maintenanceExecutable(m)+".json", maintenanceImage{Version: 1, Root: m.Root, Data: m.Data, SHA256: m.Files[engineName]})
}

type maintenanceRequest struct {
	Version                int    `json:"version"`
	ID                     string `json:"id"`
	Root                   string `json:"root"`
	Data                   string `json:"data"`
	TemplateSHA256         string `json:"templateSha256"`
	PreviousManifestSHA256 string `json:"previousManifestSha256"`
	Finished               string `json:"finished,omitempty"`
}

func maintenanceTemplatePath(m Manifest) string {
	return filepath.Join(applicationUpdateDirectory(Paths{Root: m.Root, Data: m.Data}), maintenanceTemplateName)
}
func maintenanceUnit(id string) string { return "fased-app-maintenance-system@" + id + ".service" }
func maintenanceTemplate(m Manifest) []byte {
	control := applicationUpdateDirectory(Paths{Root: m.Root, Data: m.Data})
	// This is a deliberate owner-maintenance role outside the read-only app
	// service. No sudo, shell, environment command, user tool or arbitrary argv.
	return []byte("[Unit]\nDescription=Fased update maintenance\nAfter=network.target\n\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=" + quote(maintenanceExecutable(m)) + " maintenance-delegate --operation %i --prefix " + quote(m.Root) + " --data-dir " + quote(m.Data) + "\nTimeoutStartSec=16min\nRestart=no\nUMask=0077\nNoNewPrivileges=yes\nAmbientCapabilities=\nRestrictSUIDSGID=yes\nLockPersonality=yes\nPrivateTmp=yes\nProtectSystem=strict\nProtectHome=read-only\nReadWritePaths=" + quote(m.Root) + " " + quote(m.Data) + " " + quote(control) + "\nWorkingDirectory=/\n\n[Install]\nWantedBy=default.target\n")
}
func maintenanceIdentity(m Manifest) error {
	control := applicationUpdateDirectory(Paths{Root: m.Root, Data: m.Data})
	for _, pair := range [][2]string{{control, m.Data}, {m.Data, control}} {
		rel, e := filepath.Rel(pair[0], pair[1])
		if e == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return errors.New("owner maintenance files must be outside application-writable state")
		}
	}
	if e := supported(); e != nil {
		return e
	}
	actual, e := Load(Paths{Root: m.Root, Data: m.Data})
	if e != nil {
		return e
	}
	if actual.Root != m.Root || actual.Data != m.Data || m.Phase != "ready" || actual.Location != m.Location || actual.Listen != m.Listen || actual.PublicOrigin != m.PublicOrigin || actual.Files[definitionName] != m.Files[definitionName] {
		return errors.New("owner maintenance requires the existing installation identity")
	}
	return nil
}
func maintenanceManager(ctx context.Context, run MaintenanceRunner, unit string) (map[string]string, error) {
	if run == nil {
		return nil, errors.New("owner OS service-manager authority is unavailable; update launch was not attempted")
	}
	b, e := run(ctx, "show", unit, maintenanceProperties)
	commandErr := e
	if len(b) > 64*1024 {
		return nil, errors.New("invalid owner service-manager response")
	}
	result := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			return nil, errors.New("invalid owner service-manager response")
		}
		if _, found := result[key]; found {
			return nil, errors.New("ambiguous owner service-manager response")
		}
		result[key] = value
	}
	if commandErr != nil && result["LoadState"] != "not-found" {
		return nil, errors.New("owner OS service manager is unavailable; sign in to the owner session and retry")
	}
	if len(result) == 0 {
		return nil, errors.New("owner OS service manager is unavailable; sign in to the owner session and retry")
	}
	return result, nil
}
func checkMaintenanceTemplate(m Manifest, p map[string]string) error {
	fragment, fragmentErr := filepath.EvalSymlinks(p["FragmentPath"])
	if p["LoadState"] != "loaded" || fragmentErr != nil || fragment != maintenanceTemplatePath(m) || p["DropInPaths"] != "" {
		return errors.New("owner maintenance template is missing or foreign; preserve it and use the installer owner's setup")
	}
	b, e := core.ReadPrivateFile(maintenanceTemplatePath(m))
	if e != nil || !bytes.Equal(b, maintenanceTemplate(m)) {
		return errors.New("owner maintenance template changed; launch refused")
	}
	if e := checkMaintenanceImage(m); e != nil {
		return e
	}
	// The manager's cached command must match the exact owned template too.
	// File-path identity alone cannot accept an old cached or overridden argv.
	execStart := p["ExecStart"]
	if strings.Count(execStart, "{") != 1 || strings.Count(execStart, "}") != 1 || !strings.Contains(execStart, "path="+maintenanceExecutable(m)+" ;") {
		return errors.New("owner maintenance loaded executable changed")
	}
	_, argv, ok := strings.Cut(execStart, "argv[]=")
	if !ok {
		return errors.New("owner maintenance argv unavailable")
	}
	argv, _, ok = strings.Cut(argv, " ;")
	if !ok {
		return errors.New("owner maintenance argv unavailable")
	}
	prefix := maintenanceExecutable(m) + " maintenance-delegate --operation "
	if !strings.HasPrefix(argv, prefix) {
		return errors.New("owner maintenance entry changed")
	}
	operation, tail, ok := strings.Cut(strings.TrimPrefix(argv, prefix), " --prefix ")
	if !ok || (operation != "%i" && !applicationUpdateID(operation)) || tail != m.Root+" --data-dir "+m.Data {
		return errors.New("owner maintenance command changed")
	}
	return nil
}

// ApplicationMaintenanceRunner binds the exact OS tool, unit family and linked
// source to this manifest. It never authorizes an arbitrary systemctl command.
func ApplicationMaintenanceRunner(m Manifest) MaintenanceRunner {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		if e := maintenanceIdentity(m); e != nil {
			return nil, e
		}
		validUnit := func(s string) bool {
			return s == maintenanceTemplateName || strings.HasPrefix(s, "fased-app-maintenance-system@") && strings.HasSuffix(s, ".service") && applicationUpdateID(strings.TrimSuffix(strings.TrimPrefix(s, "fased-app-maintenance-system@"), ".service"))
		}
		admitted := len(args) == 1 && args[0] == "daemon-reload" || len(args) == 3 && args[0] == "show" && validUnit(args[1]) && args[2] == maintenanceProperties || len(args) == 3 && args[0] == "link" && args[1] == "--" && args[2] == maintenanceTemplatePath(m) || len(args) == 3 && (args[0] == "enable" || args[0] == "disable" || args[0] == "reset-failed") && args[1] == "--" && validUnit(args[2]) && args[2] != maintenanceTemplateName || len(args) == 4 && args[0] == "start" && args[1] == "--no-block" && args[2] == "--" && validUnit(args[3])
		if !admitted {
			return nil, errors.New("unsupported owner maintenance OS operation")
		}
		if maintenanceSandbox(m) {
			return maintenanceBus(ctx, m, args...)
		}
		tool := "/usr/bin/systemctl"
		resolved, e := filepath.EvalSymlinks(tool)
		if e != nil {
			return nil, e
		}
		for p := resolved; ; p = filepath.Dir(p) {
			i, e := os.Lstat(p)
			if e != nil {
				return nil, e
			}
			st, ok := i.Sys().(*syscall.Stat_t)
			if !ok || st.Uid != 0 || i.Mode().Perm()&0022 != 0 || p == resolved && !i.Mode().IsRegular() || p != resolved && !i.IsDir() {
				return nil, errors.New("owner OS service-manager tool must be immutable and root-owned")
			}
			if p == "/" {
				break
			}
		}
		child, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		return exec.CommandContext(child, tool, append([]string{"--user"}, args...)...).Output()
	}
}

// InstallApplicationMaintenance is an explicit installer/owner action outside
// the app sandbox. It links a persistent template but enables no update instance.
func InstallApplicationMaintenance(ctx context.Context, m Manifest, run MaintenanceRunner) error {
	if e := maintenanceIdentity(m); e != nil {
		return e
	}
	if e := verify(m); e != nil {
		return e
	}
	p, e := maintenanceManager(ctx, run, maintenanceUnit(strings.Repeat("0", 32)))
	if e != nil {
		return e
	}
	if e := legacyMaintenanceIdle(m); e != nil {
		return e
	}
	if p["LoadState"] != "not-found" {
		return checkMaintenanceTemplate(m, p)
	}
	dir := applicationUpdateDirectory(Paths{Root: m.Root, Data: m.Data})
	if e = core.EnsurePrivateDirectory(dir); e != nil {
		return e
	}
	lock, e := core.LockInstance(filepath.Join(dir, "maintenance.lock"))
	if e != nil {
		return errors.New("another maintenance setup/launch is active")
	}
	defer lock.Close()
	if e = installMaintenanceImage(m); e != nil {
		return e
	}
	path := maintenanceTemplatePath(m)
	want := maintenanceTemplate(m)
	if b, e := core.ReadPrivateFile(path); e == nil {
		if !bytes.Equal(b, want) {
			return errors.New("foreign owner maintenance template; preserving it")
		}
	} else if errors.Is(e, os.ErrNotExist) {
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, writeErr := f.Write(want)
		if e = errors.Join(writeErr, f.Sync(), f.Close()); e != nil {
			return e
		}
		if e = syncApplicationDirectory(dir); e != nil {
			return e
		}
	} else {
		return e
	}
	if _, e = run(ctx, "link", "--", path); e != nil {
		return errors.New("owner maintenance template could not be linked; original installation remains intact")
	}
	if _, e = run(ctx, "daemon-reload"); e != nil {
		return e
	}
	p, e = maintenanceManager(ctx, run, maintenanceUnit(strings.Repeat("0", 32)))
	if e != nil {
		return e
	}
	return checkMaintenanceTemplate(m, p)
}
func readMaintenanceRequest(m Manifest) (maintenanceRequest, error) {
	b, e := core.ReadPrivateFile(filepath.Join(applicationUpdateDirectory(Paths{Root: m.Root, Data: m.Data}), "system-maintenance.json"))
	if e != nil {
		return maintenanceRequest{}, e
	}
	var r maintenanceRequest
	if len(b) > 4096 || json.Unmarshal(b, &r) != nil || r.Version != 1 || !applicationUpdateID(r.ID) || r.Root != m.Root || r.Data != m.Data || r.TemplateSHA256 != bytesDigest(maintenanceTemplate(m)) || !validExpectedDigest(r.PreviousManifestSHA256) || r.Finished != "" && !oneOfMaintenancePhase(r.Finished) {
		return r, errors.New("owner maintenance request changed; preserve its original identity")
	}
	return r, nil
}

// LaunchApplicationMaintenance requests one fixed installed entry. It does not
// download, authenticate or perform an update itself. The entry independently
// admits the durable operation and channel before calling the update adapter.
func LaunchApplicationMaintenance(ctx context.Context, m Manifest, id string, run MaintenanceRunner) (MaintenanceStatus, error) {
	if e := ctx.Err(); e != nil {
		return MaintenanceStatus{}, e
	}
	if !applicationUpdateID(id) {
		return MaintenanceStatus{}, errors.New("one opaque maintenance operation required")
	}
	if e := maintenanceIdentity(m); e != nil {
		return MaintenanceStatus{}, e
	}
	if e := verify(m); e != nil {
		return MaintenanceStatus{}, e
	}
	p, e := maintenanceManager(ctx, run, maintenanceUnit(id))
	if e != nil {
		return MaintenanceStatus{}, e
	}
	if e = checkMaintenanceTemplate(m, p); e != nil {
		return MaintenanceStatus{}, e
	}
	dir := applicationUpdateDirectory(Paths{Root: m.Root, Data: m.Data})
	lock, e := core.LockInstance(filepath.Join(dir, "maintenance.lock"))
	if e != nil {
		return MaintenanceStatus{}, errors.New("another owner maintenance launch is active")
	}
	defer lock.Close()
	prior, e := readMaintenanceRequest(m)
	if e == nil && prior.ID != id && prior.Finished != "deferred" {
		state, se := ApplicationUpdateState(Paths{Root: m.Root, Data: m.Data})
		if se != nil || state.ID != prior.ID || (state.Phase != "completed" && state.Phase != "rolled-back") {
			return MaintenanceStatus{}, errors.New("another owner maintenance operation needs its exact completion or recovery")
		}
	}
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return MaintenanceStatus{}, e
	}
	// RemainAfterExit and the same system-manager unit identity prevent repeated
	// starts from replaying a completed invocation. Installation success still
	// requires the separate durable controller receipt.
	if prior.ID == id && prior.Finished != "" {
		return ApplicationMaintenanceStatus(ctx, m, id, run)
	}
	if prior.ID == id && (p["ActiveState"] == "active" || p["ActiveState"] == "activating") {
		return ApplicationMaintenanceStatus(ctx, m, id, run)
	}
	request := maintenanceRequest{Version: 1, ID: id, Root: m.Root, Data: m.Data, TemplateSHA256: bytesDigest(maintenanceTemplate(m)), PreviousManifestSHA256: ManifestSHA256(m)}
	if e = core.WritePrivateJSON(filepath.Join(dir, "system-maintenance.json"), request); e != nil {
		return MaintenanceStatus{}, e
	}
	if _, e = run(ctx, "enable", "--", maintenanceUnit(id)); e != nil {
		return MaintenanceStatus{}, errors.New("persistent owner maintenance authority unavailable; launch refused")
	}
	if p["ActiveState"] == "failed" {
		if _, e = run(ctx, "reset-failed", "--", maintenanceUnit(id)); e != nil {
			return MaintenanceStatus{}, e
		}
	}
	if _, e = run(ctx, "start", "--no-block", "--", maintenanceUnit(id)); e != nil {
		return MaintenanceStatus{}, errors.New("owner maintenance launch failed; retry only this same operation")
	}
	return ApplicationMaintenanceStatus(ctx, m, id, run)
}
func ApplicationMaintenanceStatus(ctx context.Context, m Manifest, id string, run MaintenanceRunner) (MaintenanceStatus, error) {
	if !applicationUpdateID(id) {
		return MaintenanceStatus{}, errors.New("one opaque maintenance operation required")
	}
	if e := maintenanceIdentity(m); e != nil {
		return MaintenanceStatus{}, e
	}
	p, e := maintenanceManager(ctx, run, maintenanceUnit(id))
	if e != nil {
		return MaintenanceStatus{}, e
	}
	if e = checkMaintenanceTemplate(m, p); e != nil {
		return MaintenanceStatus{}, e
	}
	r, e := readMaintenanceRequest(m)
	if e != nil {
		return MaintenanceStatus{}, e
	}
	if r.ID != id {
		return MaintenanceStatus{}, errors.New("maintenance status belongs to another operation")
	}
	phase := "ready"
	switch p["ActiveState"] {
	case "active":
		if p["SubState"] == "exited" {
			phase = "finished"
		} else {
			phase = "running"
		}
	case "activating", "deactivating":
		phase = "running"
	case "failed":
		phase = "failed"
	}
	if r.Finished != "" {
		phase = "finished"
	}
	result := MaintenanceStatus{ID: id, Unit: maintenanceUnit(id), Phase: phase, Persistent: p["UnitFileState"] == "enabled", Recoverable: r.Finished == ""}
	if s, e := ApplicationUpdateState(Paths{Root: m.Root, Data: m.Data}); e == nil && s.ID == id {
		result.UpdatePhase = s.Phase
	} else if e != nil && !errors.Is(e, os.ErrNotExist) {
		return result, e
	}
	return result, nil
}

func oneOfMaintenancePhase(p string) bool {
	return p == "completed" || p == "rolled-back" || p == "deferred"
}

// CompleteApplicationMaintenance disables only this owned exact instance. It
// never stops its running actor. Process success is not an installation proof.
func CompleteApplicationMaintenance(ctx context.Context, m Manifest, id, phase string, run MaintenanceRunner) error {
	if !applicationUpdateID(id) || !oneOfMaintenancePhase(phase) {
		return errors.New("one exact authoritative maintenance outcome required")
	}
	if e := maintenanceIdentity(m); e != nil {
		return e
	}
	dir := applicationUpdateDirectory(Paths{Root: m.Root, Data: m.Data})
	lock, e := core.LockInstance(filepath.Join(dir, "maintenance.lock"))
	if e != nil {
		return e
	}
	defer lock.Close()
	request, e := readMaintenanceRequest(m)
	if e != nil {
		return e
	}
	if request.ID != id {
		return errors.New("cannot clean up another maintenance operation")
	}
	state, se := ApplicationUpdateState(Paths{Root: m.Root, Data: m.Data})
	actual, e := Load(Paths{Root: m.Root, Data: m.Data})
	if e != nil {
		return e
	}
	if e = verify(actual); e != nil {
		return e
	}
	if phase == "deferred" {
		if ManifestSHA256(actual) != request.PreviousManifestSHA256 || se == nil && state.ID == id || se != nil && !errors.Is(se, os.ErrNotExist) {
			return errors.New("dispatched or changed installation cannot be discarded as deferred")
		}
	} else {
		if se != nil || state.ID != id || state.Phase != phase {
			return errors.New("durable exact installation outcome required before cleanup")
		}
		expected := state.TargetManifestSHA256
		if phase == "rolled-back" {
			expected = state.PreviousManifestSHA256
		}
		if ManifestSHA256(actual) != expected {
			return errors.New("completed installation identity changed")
		}
	}
	if request.Finished != "" && request.Finished != phase {
		return errors.New("maintenance completion outcome changed")
	}
	p, e := maintenanceManager(ctx, run, maintenanceUnit(id))
	if e != nil {
		return e
	}
	if e = checkMaintenanceTemplate(m, p); e != nil {
		return e
	}
	if _, e = run(ctx, "disable", "--", maintenanceUnit(id)); e != nil {
		return errors.New("could not disable the owned maintenance instance; preserve its identity and retry cleanup")
	}
	request.Finished = phase
	return core.WritePrivateJSON(filepath.Join(dir, "system-maintenance.json"), request)
}

// ApplicationMaintenanceEntry is the fixed, verified maintenance image; callers
// may not select another executable. It remains available if target code fails.
func ApplicationMaintenanceEntry(m Manifest) (string, error) {
	if e := maintenanceIdentity(m); e != nil {
		return "", e
	}
	if e := checkMaintenanceImage(m); e != nil {
		return "", e
	}
	return maintenanceExecutable(m), nil
}
func readMaintenanceRequestForRemoval(m Manifest) (string, error) {
	r, e := readMaintenanceRequest(m)
	return r.Finished, e
}

// ApplicationMaintenanceReady reads immutable installation/actor identity only.
// The sandboxed core may inspect it but cannot write the owner control directory.
func ApplicationMaintenanceReady(ctx context.Context, m Manifest, id string, run MaintenanceRunner) error {
	if !applicationUpdateID(id) {
		return errors.New("one opaque maintenance operation required")
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := maintenanceIdentity(m); e != nil {
		return e
	}
	if e := verify(m); e != nil {
		return e
	}
	p, e := maintenanceManager(ctx, run, maintenanceUnit(id))
	if e != nil {
		return e
	}
	return checkMaintenanceTemplate(m, p)
}

// StartApplicationMaintenance is the only Local launch path exposed to the core:
// start one validated existing user unit, without writing or enabling anything.
func StartApplicationMaintenance(ctx context.Context, m Manifest, id string, run MaintenanceRunner) error {
	if e := ApplicationMaintenanceReady(ctx, m, id, run); e != nil {
		return e
	}
	p, e := maintenanceManager(ctx, run, maintenanceUnit(id))
	if e != nil {
		return e
	}
	if p["Result"] != "success" {
		return errors.New("maintenance failed; use explicit owner recovery")
	}
	switch p["ActiveState"] {
	case "activating", "deactivating":
		return nil
	case "active":
		if p["SubState"] == "running" {
			return nil
		}
		return errors.New("maintenance exited without a receipt; use explicit owner recovery")
	case "inactive":
		if p["SubState"] != "dead" {
			return errors.New("unknown maintenance state; preserve recovery")
		}
	default:
		return errors.New("maintenance is failed or unknown; use explicit owner recovery")
	}
	_, e = run(ctx, "start", "--no-block", "--", maintenanceUnit(id))
	return e
}

// PrepareApplicationMaintenanceOperation runs in the ordinary owner actor after
// admitted-operation IPC inspection. Reuse durable prior-ID/replay/enable checks;
// the actor is already running, so never recursively start or wait on itself.
func PrepareApplicationMaintenanceOperation(ctx context.Context, m Manifest, id string, run MaintenanceRunner) error {
	_, e := LaunchApplicationMaintenance(ctx, m, id, func(ctx context.Context, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "start" {
			return nil, nil
		}
		return run(ctx, args...)
	})
	if e != nil {
		return e
	}
	request, e := readMaintenanceRequest(m)
	if e != nil {
		return e
	}
	if request.ID != id {
		return errors.New("owner operation changed")
	}
	if request.Finished != "" {
		return nil
	}
	// Retry after request publication must still establish reboot persistence.
	status, e := ApplicationMaintenanceStatus(ctx, m, id, run)
	if e != nil {
		return e
	}
	if status.Persistent {
		return nil
	}
	_, e = run(ctx, "enable", "--", maintenanceUnit(id))
	return e
}
func ApplicationAcquisitionParent(m Manifest) (string, error) {
	if e := maintenanceIdentity(m); e != nil {
		return "", e
	}
	parent := applicationUpdateDirectory(Paths{Root: m.Root, Data: m.Data})
	return parent, nil
}

func IsApplicationMaintenanceImage(m Manifest, image string) bool {
	return filepath.Clean(strings.TrimSuffix(image, " (deleted)")) == maintenanceExecutable(m)
}
func ValidateApplicationMaintenanceProcess() error {
	b, e := os.ReadFile("/proc/self/status")
	if e != nil {
		return e
	}
	return validateMaintenanceProcess(os.Getuid(), os.Geteuid(), string(b))
}
func validateMaintenanceProcess(uid, euid int, status string) error {
	if uid <= 0 || uid != euid {
		return errors.New("ordinary maintenance identity required")
	}
	for _, line := range []string{"NoNewPrivs:\t1\n", "CapEff:\t0000000000000000\n", "CapPrm:\t0000000000000000\n", "CapInh:\t0000000000000000\n", "CapAmb:\t0000000000000000\n"} {
		if !strings.Contains(status, line) {
			return errors.New("maintenance requires NoNewPrivileges and zero effective/permitted/inheritable/ambient capabilities")
		}
	}
	return nil
}
