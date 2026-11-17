//go:build linux

package hostprep

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

// A root-only proof that this helper installed Tailscale where no prior
// Tailscale state or host configuration existed. It is not an auth key.
type freshTailscaleGrant struct {
	Version              int    `json:"version"`
	Owner                int    `json:"owner"`
	HelperSHA256         string `json:"helperSha256"`
	ClientSHA256         string `json:"clientSha256"`
	DaemonSHA256         string `json:"daemonSha256"`
	DaemonIdentitySHA256 string `json:"daemonIdentitySha256"`
	DaemonPID            int    `json:"daemonPid"`
	PrefsSHA256          string `json:"prefsSha256"`
	DefaultSHA256        string `json:"defaultSha256"`
	ClientVersion        string `json:"clientVersion"`
}

type freshTailscaleIntent struct {
	Version      int    `json:"version"`
	Owner        int    `json:"owner"`
	HelperSHA256 string `json:"helperSha256"`
}

func tailscaleIntentPath(owner int) string {
	return filepath.Join(maintenance.OwnerRoot(owner), "tailscale", "fresh-install-intent.json")
}

func recordFreshTailscaleIntent(a maintenance.Authority) error {
	body, err := json.Marshal(freshTailscaleIntent{Version: 1, Owner: a.Owner, HelperSHA256: a.ImageSHA256})
	if err != nil {
		return err
	}
	return maintenance.PublishProtected(tailscaleIntentPath(a.Owner), append(body, '\n'), 0600)
}

func readFreshTailscaleIntent(a maintenance.Authority) error {
	f, err := update.OpenProtected(tailscaleIntentPath(a.Owner), 8192, 0600)
	if err != nil {
		return errors.New("fresh Tailscale install intent unavailable; preserve existing state")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 8193))
	var intent freshTailscaleIntent
	if err != nil || update.StrictJSON(b, &intent) != nil || intent.Version != 1 || intent.Owner != a.Owner || intent.HelperSHA256 != a.ImageSHA256 {
		return errors.New("fresh Tailscale install intent differs; preserve it")
	}
	return nil
}

// If setup stopped after the package was installed but before the fresh grant
// was recorded, do not silently authorize a replacement operation.
func checkInterruptedFreshTailscale(ctx context.Context, a maintenance.Authority, run func(context.Context, ...string) ([]byte, error)) error {
	if _, err := os.Lstat(tailscaleIntentPath(a.Owner)); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := readFreshTailscaleIntent(a); err != nil {
		return err
	}
	_, grantErr := os.Lstat(tailscaleGrantPath(a.Owner))
	_, usedErr := os.Lstat(tailscaleGrantPath(a.Owner) + ".used")
	if grantErr == nil && usedErr == nil || grantErr != nil && !errors.Is(grantErr, os.ErrNotExist) || usedErr != nil && !errors.Is(usedErr, os.ErrNotExist) {
		return errors.New("ambiguous Tailscale setup authorization; preserve it")
	}
	if grantErr == nil || usedErr == nil {
		_, err := readFreshTailscaleGrant(a, usedErr == nil)
		return err
	}
	b, err := run(ctx, "/usr/bin/tailscale", "status", "--json")
	if err != nil && len(b) == 0 {
		return err
	}
	state, _, err := parseTailscaleStatus(b)
	if err == nil && state == "Running" {
		return nil
	}
	return errors.New("fresh Tailscale setup stopped before authorization; preserve it and connect Tailscale as its administrator before retrying")
}

func tailscaleGrantPath(owner int) string {
	return filepath.Join(maintenance.OwnerRoot(owner), "tailscale", "fresh-enrollment.json")
}

func noPriorTailscaleState(stat func(string) (os.FileInfo, error)) error {
	for _, path := range []string{
		"/var/lib/tailscale", "/etc/tailscale", "/etc/default/tailscaled",
		"/run/tailscale/tailscaled.sock", "/var/run/tailscale/tailscaled.sock",
		"/usr/sbin/tailscaled", "/usr/bin/tailscaled", "/usr/local/sbin/tailscaled", "/usr/local/bin/tailscaled",
		"/etc/systemd/system/tailscaled.service", "/etc/systemd/system/tailscaled.service.d",
		"/usr/lib/systemd/system/tailscaled.service", "/lib/systemd/system/tailscaled.service",
	} {
		if _, err := stat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("existing Tailscale state or host configuration; preserve it and connect it as its administrator")
		}
	}
	return nil
}

// A daemon may have been started outside systemd with a custom state path.
// Preserve it before the package manager is allowed to start another daemon.
func noRunningTailscale() error {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return errors.New("Tailscale process inventory unavailable; preserve existing access")
	}
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		path := filepath.Join("/proc", entry.Name())
		comm, err := os.ReadFile(filepath.Join(path, "comm"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return errors.New("Tailscale process inventory unavailable; preserve existing access")
		}
		if strings.TrimSpace(string(comm)) == "tailscaled" {
			return errors.New("existing Tailscale daemon; preserve it")
		}
		// Go build metadata identifies an ordinary renamed tailscaled binary;
		// inspect only metadata, never its arguments, keys or state.
		info, err := buildinfo.ReadFile(filepath.Join(path, "exe"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if errors.Is(err, os.ErrPermission) {
			return errors.New("Tailscale process inventory unavailable; preserve existing access")
		}
		if err == nil && info.Path == "tailscale.com/cmd/tailscaled" {
			return errors.New("existing Tailscale daemon; preserve it")
		}
	}
	return nil
}

func tailscaleMachineLock() (*core.InstanceLock, error) {
	return core.LockInstance(filepath.Join(maintenance.StateRoot, "tailscale-setup.lock"))
}

func tailscaleExecutableHash(path string) (string, error) {
	if err := trustedHostTool(path); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	f, err := update.OpenProtected(resolved, 128<<20, 0755)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func supportedTailscaleVersion(b []byte) (string, error) {
	var v struct {
		Short string `json:"short"`
	}
	if len(b) > 64<<10 || json.Unmarshal(b, &v) != nil {
		return "", errors.New("Tailscale version unavailable; preserve existing settings")
	}
	if !supportedTailscaleRelease(v.Short) {
		return "", errors.New("Tailscale version is not yet verified for automatic enrollment")
	}
	return v.Short, nil
}

func supportedTailscaleRelease(version string) bool {
	switch version {
	case "1.104.5", "1.104.6", "1.104.7":
		return true
	default:
		return false
	}
}

func tailscaleAptCandidate(b []byte) (string, error) {
	if len(b) == 0 || len(b) > 64<<10 {
		return "", errors.New("Tailscale package candidate unavailable")
	}
	var candidate string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Candidate:") {
			if candidate != "" {
				return "", errors.New("ambiguous Tailscale package candidate")
			}
			candidate = strings.TrimSpace(strings.TrimPrefix(line, "Candidate:"))
		}
	}
	if !supportedTailscaleRelease(candidate) {
		return "", errors.New("Tailscale package version is not yet verified for automatic enrollment")
	}
	return candidate, nil
}

func freshTailscalePrefs(b, profiles []byte) (string, error) {
	var p struct{ OperatorUser string }
	var saved []json.RawMessage
	if len(b) == 0 || len(b) > 64<<10 || json.Unmarshal(b, &p) != nil || p.OperatorUser != "" ||
		len(profiles) > 64<<10 || json.Unmarshal(profiles, &saved) != nil || saved == nil || len(saved) != 0 {
		return "", errors.New("existing Tailscale preferences or profiles require administrator attention")
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func tailscaleDefaultHash() (string, error) {
	f, err := readPreparationFile("/etc/default/tailscaled")
	if err != nil || !f.root || !f.regular || len(f.data) == 0 {
		return "", errors.New("Tailscale service configuration is unavailable; preserve it")
	}
	h := sha256.Sum256(f.data)
	return hex.EncodeToString(h[:]), nil
}

// Bind the grant to the running, unmodified system daemon and its fixed
// state/socket paths. Unknown flags or daemon policy are not adopted.
func freshTailscaleDaemon(ctx context.Context, run func(context.Context, ...string) ([]byte, error)) (int, string, error) {
	b, err := run(ctx, "/usr/bin/systemctl", "show", "--property=MainPID", "--value", "tailscaled.service")
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || parseErr != nil || pid <= 1 {
		return 0, "", errors.New("Tailscale daemon identity unavailable")
	}
	proc := fmt.Sprintf("/proc/%d", pid)
	actual, err := os.Stat(filepath.Join(proc, "exe"))
	if err != nil {
		return 0, "", err
	}
	expected, err := os.Stat("/usr/sbin/tailscaled")
	if err != nil || !os.SameFile(actual, expected) {
		return 0, "", errors.New("Tailscale daemon executable differs")
	}
	cmd, err := os.ReadFile(filepath.Join(proc, "cmdline"))
	if err != nil || len(cmd) > 16<<10 {
		return 0, "", errors.New("Tailscale daemon arguments unavailable")
	}
	args := strings.Split(strings.TrimSuffix(string(cmd), "\x00"), "\x00")
	if len(args) != 4 || args[0] != "/usr/sbin/tailscaled" || args[1] != "--state=/var/lib/tailscale/tailscaled.state" || args[2] != "--socket=/run/tailscale/tailscaled.sock" || !strings.HasPrefix(args[3], "--port=") {
		return 0, "", errors.New("Tailscale has custom daemon settings; preserve them")
	}
	port, err := strconv.Atoi(strings.TrimPrefix(args[3], "--port="))
	if err != nil || port < 0 || port > 65535 {
		return 0, "", errors.New("invalid Tailscale daemon port")
	}
	env, err := os.ReadFile(filepath.Join(proc, "environ"))
	if err != nil || len(env) > 64<<10 {
		return 0, "", errors.New("Tailscale daemon environment unavailable")
	}
	for _, entry := range strings.Split(string(env), "\x00") {
		if strings.HasPrefix(entry, "TS_") || strings.HasPrefix(entry, "TAILSCALE_") {
			return 0, "", errors.New("Tailscale has custom daemon policy; preserve it")
		}
	}
	stat, err := os.ReadFile(filepath.Join(proc, "stat"))
	if err != nil || len(stat) > 4096 {
		return 0, "", errors.New("Tailscale daemon start identity unavailable")
	}
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return 0, "", errors.New("invalid Tailscale daemon identity")
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) <= 19 {
		return 0, "", errors.New("invalid Tailscale daemon identity")
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || len(boot) > 128 {
		return 0, "", errors.New("boot identity unavailable")
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%d/%s", strings.TrimSpace(string(boot)), pid, fields[19])))
	return pid, hex.EncodeToString(sum[:]), nil
}

func recordFreshTailscale(ctx context.Context, a maintenance.Authority, run func(context.Context, ...string) ([]byte, error)) error {
	if err := readFreshTailscaleIntent(a); err != nil {
		return err
	}
	versionJSON, err := run(ctx, "/usr/bin/tailscale", "version", "--json")
	if err != nil {
		return err
	}
	version, err := supportedTailscaleVersion(versionJSON)
	if err != nil {
		return err
	}
	prefs, err := run(ctx, "/usr/bin/tailscale", "debug", "prefs")
	if err != nil {
		return err
	}
	profiles, err := run(ctx, "/usr/bin/tailscale", "switch", "--list", "--json")
	if err != nil {
		return err
	}
	digest, err := freshTailscalePrefs(prefs, profiles)
	if err != nil {
		return err
	}
	client, err := tailscaleExecutableHash("/usr/bin/tailscale")
	if err != nil {
		return err
	}
	daemon, err := tailscaleExecutableHash("/usr/sbin/tailscaled")
	if err != nil {
		return err
	}
	defaultHash, err := tailscaleDefaultHash()
	if err != nil {
		return err
	}
	if err = noTailscaleOverrides(os.Lstat); err != nil {
		return err
	}
	pid, identity, err := freshTailscaleDaemon(ctx, run)
	if err != nil {
		return err
	}
	grant := freshTailscaleGrant{Version: 1, Owner: a.Owner, HelperSHA256: a.ImageSHA256, ClientSHA256: client, DaemonSHA256: daemon, DaemonPID: pid, DaemonIdentitySHA256: identity, PrefsSHA256: digest, DefaultSHA256: defaultHash, ClientVersion: version}
	body, err := json.Marshal(grant)
	if err != nil {
		return err
	}
	return maintenance.PublishProtected(tailscaleGrantPath(a.Owner), append(body, '\n'), 0600)
}

func readFreshTailscaleGrant(a maintenance.Authority, used bool) (freshTailscaleGrant, error) {
	var g freshTailscaleGrant
	path := tailscaleGrantPath(a.Owner)
	if used {
		path += ".used"
	}
	f, err := update.OpenProtected(path, 8192, 0600)
	if err != nil {
		return g, errors.New("automatic Tailscale enrollment requires a fresh protected setup")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || update.StrictJSON(b, &g) != nil || g.Version != 1 || g.Owner != a.Owner || g.HelperSHA256 != a.ImageSHA256 || g.DaemonPID <= 1 ||
		len(g.ClientSHA256) != 64 || len(g.DaemonSHA256) != 64 || len(g.DaemonIdentitySHA256) != 64 || len(g.PrefsSHA256) != 64 || len(g.DefaultSHA256) != 64 {
		return g, errors.New("fresh Tailscale authorization differs; preserve it")
	}
	return g, nil
}

func tailscaleRequestPath(owner int) string {
	return filepath.Join(maintenance.OwnerRoot(owner), "tailscale", "fresh-request.sha256")
}

func recordTailscaleRequest(owner int, url string) error {
	if !tailscaleLoginURL.MatchString(url) {
		return errors.New("invalid Tailscale sign-in request")
	}
	h := sha256.Sum256([]byte(url))
	return maintenance.PublishProtected(tailscaleRequestPath(owner), []byte(hex.EncodeToString(h[:])+"\n"), 0600)
}

func sameTailscaleRequest(a maintenance.Authority, url string) error {
	if !tailscaleLoginURL.MatchString(url) {
		return errors.New("invalid Tailscale sign-in request")
	}
	if _, err := readFreshTailscaleGrant(a, true); err != nil {
		return err
	}
	f, err := update.OpenProtected(tailscaleRequestPath(a.Owner), 128, 0600)
	if err != nil {
		return errors.New("original Tailscale sign-in request unavailable; preserve it")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 129))
	h := sha256.Sum256([]byte(url))
	if err != nil || string(b) != hex.EncodeToString(h[:])+"\n" {
		return errors.New("Tailscale sign-in request changed; preserve it")
	}
	return nil
}

// Consume before contacting the daemon. An interrupted request is never
// automatically replaced; the original auth URL can still be observed.
func consumeFreshTailscaleGrant(owner int) error {
	path := tailscaleGrantPath(owner)
	if err := unix.Renameat2(unix.AT_FDCWD, path, unix.AT_FDCWD, path+".used", unix.RENAME_NOREPLACE); err != nil {
		return errors.New("fresh Tailscale authorization changed; preserve it")
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return fmt.Errorf("preserve Tailscale enrollment after directory sync: %w", err)
	}
	return nil
}

func noTailscaleOverrides(stat func(string) (os.FileInfo, error)) error {
	for _, p := range []string{"/etc/tailscale", "/etc/systemd/system/tailscaled.service", "/etc/systemd/system/tailscaled.service.d"} {
		if _, err := stat(p); !errors.Is(err, os.ErrNotExist) {
			return errors.New("Tailscale has a host policy or override; preserve it")
		}
	}
	return nil
}

func parseTailscaleStatus(b []byte) (string, string, error) {
	var s struct {
		BackendState string
		AuthURL      string
	}
	if len(b) > 1<<20 || json.Unmarshal(b, &s) != nil || strings.TrimSpace(s.BackendState) == "" {
		return "", "", errors.New("Tailscale status unavailable; preserve existing access")
	}
	return s.BackendState, s.AuthURL, nil
}
