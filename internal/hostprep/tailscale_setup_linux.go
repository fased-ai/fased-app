//go:build linux

package hostprep

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/privateaccess"
)

// Public APT signing key from the official Noble repository, reviewed 2026-10-30:
// https://pkgs.tailscale.com/stable/ubuntu/noble.noarmor.gpg
// A replacement key requires a reviewed application change, never a setup flag.
//
//go:embed tailscale-noble-key.gpg
var tailscaleAPTKey []byte

const tailscaleAPTKeySHA256 = "3e03dacf222698c60b8e2f990b809ca1b3e104de127767864284e6c228f1fb39"
const tailscaleKeyPath = "/usr/share/keyrings/fased-tailscale.gpg"
const tailscaleSourcePath = "/etc/apt/sources.list.d/fased-tailscale.list"
const standardTailscaleSourcePath = "/etc/apt/sources.list.d/tailscale.list"
const standardTailscaleKeyPath = "/usr/share/keyrings/tailscale-archive-keyring.gpg"
const tailscaleSource = "deb [signed-by=/usr/share/keyrings/fased-tailscale.gpg] https://pkgs.tailscale.com/stable/ubuntu noble main\n"

type preparationFile struct {
	data          []byte
	root, regular bool
	mode          os.FileMode
}
type preparationRead func(string) (preparationFile, error)
type preparationRun func(context.Context, []byte, ...string) ([]byte, error)
type tailscalePreparation struct {
	Version int `json:"version"`
	Owner   int `json:"owner"`
}

func readPreparationFile(path string) (preparationFile, error) {
	i, e := os.Lstat(path)
	if e != nil {
		return preparationFile{}, e
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	f := preparationFile{root: ok && st.Uid == 0, regular: i.Mode().IsRegular(), mode: i.Mode().Perm()}
	if !f.root || !f.regular || f.mode&0022 != 0 {
		return f, errors.New("foreign or unsafe prerequisite file; preserving it")
	}
	if i.Size() > 64*1024 {
		return f, nil
	} // Executable presence needs metadata, not its bytes.
	f.data, e = os.ReadFile(path)
	return f, e
}
func knownPreparationFile(f preparationFile, b []byte) bool {
	return f.root && f.regular && f.mode&0022 == 0 && bytes.Equal(f.data, b)
}

// PrepareTailscale is called only from the owner's interactive installation
// wizard, never a web request or the background service. Only OS utilities are
// elevated. Route creation remains in Manager's conditional-write transaction.
func PrepareTailscale(ctx context.Context, in *bufio.Reader, out io.Writer) error {
	if in == nil || os.Getuid() <= 0 || os.Getuid() != os.Geteuid() {
		return errors.New("Tailscale preparation needs an ordinary administrator terminal")
	}
	release, e := os.ReadFile("/etc/os-release")
	if e != nil {
		return e
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(release), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			values[k] = strings.Trim(v, "\"'")
		}
	}
	if values["ID"] != "ubuntu" || values["VERSION_ID"] != "24.04" {
		return errors.New("guided Tailscale preparation supports Ubuntu 24.04; install/enroll Tailscale for this OS, or choose SSH")
	}
	u, e := user.Current()
	if e != nil {
		return e
	}
	if !AllowedTailscalePreparation(u.Username, []string{"status", "--json"}) {
		return errors.New("unsupported OS administrator identity")
	}
	// An already enrolled, online device with this operator needs no package,
	// sudo ceremony, login, or preference mutation.
	if _, err := readPreparationFile("/usr/bin/tailscale"); err == nil {
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		probe := func(ctx context.Context, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, "/usr/bin/tailscale", args...).Output()
		}
		if _, err := (privateaccess.Manager{Run: probe}).Plan(probeCtx, "tailscale", "127.0.0.1:7331"); err == nil {
			if b, err := probe(probeCtx, "debug", "prefs"); err == nil && len(b) <= 64*1024 {
				var p struct{ OperatorUser string }
				if json.Unmarshal(b, &p) == nil && p.OperatorUser == u.Username {
					return nil
				}
			}
		}
	}
	// A parent symlink/writable directory may not redirect privileged publication.
	for _, dir := range []string{"/usr/share/keyrings", "/etc/apt/sources.list.d"} {
		for path := dir; ; path = filepath.Dir(path) {
			i, err := os.Lstat(path)
			if err != nil {
				return err
			}
			st, ok := i.Sys().(*syscall.Stat_t)
			if !ok || st.Uid != 0 || !i.IsDir() || i.Mode().Perm()&0022 != 0 {
				return errors.New("Tailscale prerequisite directories must be immutable root-owned directories")
			}
			if path == "/" {
				break
			}
		}
	}
	fmt.Fprint(out, "Set up Tailscale packages, service, browser sign-in and access for this OS user? [y/N]: ")
	line, e := in.ReadString('\n')
	if e != nil {
		return e
	}
	if v := strings.ToLower(strings.TrimSpace(line)); v != "y" && v != "yes" {
		return errors.New("Tailscale setup deferred; retry or explicitly choose SSH")
	}
	base, e := core.DefaultDataDir()
	if e != nil {
		return e
	}
	dir := filepath.Join(base, "host-preparation", "tailscale")
	system := &System{In: os.Stdin, Out: out, Authorization: "Authorize Tailscale's official signed Ubuntu package, service and this user's private access. Existing accounts/routes and SSH/firewall policy are preserved. Fased stays unprivileged."}
	return prepareTailscale(ctx, dir, os.Getuid(), u.Username, out, readPreparationFile, system.Run)
}

func prepareTailscale(ctx context.Context, dir string, uid int, username string, out io.Writer, read preparationRead, run preparationRun) error {
	if uid <= 0 || !AllowedTailscalePreparation(username, []string{"status", "--json"}) {
		return errors.New("ordinary administrator required")
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	h := sha256.Sum256(tailscaleAPTKey)
	if hex.EncodeToString(h[:]) != tailscaleAPTKeySHA256 {
		return errors.New("bundled Tailscale signing key differs from the reviewed digest")
	}
	if _, e := read("/usr/bin/tailscale"); errors.Is(e, os.ErrNotExist) {
		if e = prepareTailscalePackage(ctx, dir, uid, read, run); e != nil {
			return e
		}
	} else if e != nil {
		return e
	}
	if _, e := run(ctx, nil, "/usr/bin/systemctl", "enable", "--now", "tailscaled.service"); e != nil {
		return errors.New("Tailscale service preparation failed; preserve SSH, repair and retry")
	}
	prefs, e := run(ctx, nil, "/usr/bin/tailscale", "debug", "prefs")
	if e != nil || len(prefs) > 64*1024 {
		return errors.New("Tailscale preferences unavailable; no account or route settings were changed")
	}
	var p struct{ OperatorUser string }
	if json.Unmarshal(prefs, &p) != nil {
		return errors.New("invalid Tailscale preferences; preserve existing settings")
	}
	if p.OperatorUser != "" && p.OperatorUser != username {
		return errors.New("Tailscale already has a different operator; ask its administrator or choose SSH")
	}
	status, e := run(ctx, nil, "/usr/bin/tailscale", "status", "--json")
	if e != nil || len(status) > 1024*1024 {
		return errors.New("Tailscale status unavailable; preserve SSH and retry")
	}
	var state struct{ BackendState string }
	if json.Unmarshal(status, &state) != nil {
		return errors.New("invalid Tailscale status")
	}
	switch state.BackendState {
	case "Running": // Do not log in again or replace an existing account.
	case "NeedsLogin":
		fmt.Fprintln(out, "Open the Tailscale sign-in URL printed below in your computer's browser. No auth key is needed. Keep this SSH connection open.")
		if _, e = run(ctx, nil, "/usr/bin/tailscale", "login", "--timeout=120s"); e != nil {
			return errors.New("Tailscale sign-in was not completed; retry or choose SSH; existing settings are preserved")
		}
	default:
		return errors.New("Tailscale needs owner attention (for example device approval or a deliberately stopped connection); preserve its settings, then retry or choose SSH")
	}
	if p.OperatorUser == "" {
		if _, e = run(ctx, nil, "/usr/bin/tailscale", "set", "--operator="+username); e != nil {
			return errors.New("Tailscale operator permission was not granted; ask its administrator or choose SSH")
		}
	}
	fmt.Fprintln(out, "On the computer you will use, install Tailscale from https://tailscale.com/download and sign in to the same tailnet. Enable tailnet HTTPS and allow only your intended devices. Fased will verify private HTTPS; keep SSH/console recovery.")
	return nil
}

func prepareTailscalePackage(ctx context.Context, dir string, uid int, read preparationRead, run preparationRun) error {
	if e := core.EnsurePrivateDirectory(dir); e != nil {
		return e
	}
	lock, e := core.LockInstance(filepath.Join(dir, "prepare.lock"))
	if e != nil {
		return e
	}
	defer lock.Close()
	receipt := filepath.Join(dir, "preparation.json")
	if b, e := core.ReadPrivateFile(receipt); e == nil {
		var r tailscalePreparation
		if len(b) > 1024 || json.Unmarshal(b, &r) != nil || r.Version != 1 || r.Owner != uid {
			return errors.New("unknown Tailscale preparation record; preserve it")
		}
	} else if errors.Is(e, os.ErrNotExist) {
		if e = core.WritePrivateJSON(receipt, tailscalePreparation{Version: 1, Owner: uid}); e != nil {
			return e
		}
	} else {
		return e
	}
	files := []struct {
		path string
		data []byte
	}{{tailscaleKeyPath, tailscaleAPTKey}, {tailscaleSourcePath, []byte(tailscaleSource)}}
	sourcePath := tailscaleSourcePath
	// Reuse an exact already-configured official repository instead of adding a
	// conflicting Signed-By definition. Never rewrite an administrator's files.
	if existing, err := read(standardTailscaleSourcePath); err == nil {
		want := strings.ReplaceAll(tailscaleSource, tailscaleKeyPath, standardTailscaleKeyPath)
		var lines []string
		for _, line := range strings.Split(string(existing.data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				lines = append(lines, line)
			}
		}
		key, keyErr := read(standardTailscaleKeyPath)
		if !existing.root || !existing.regular || existing.mode&0022 != 0 || strings.Join(lines, "\n") != strings.TrimSpace(want) || keyErr != nil || !knownPreparationFile(key, tailscaleAPTKey) {
			return errors.New("existing Tailscale repository differs; ask its administrator or choose SSH")
		}
		files = nil
		sourcePath = standardTailscaleSourcePath
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Refuse both foreign destinations before publishing either file.
	for _, f := range files {
		if v, e := read(f.path); e == nil {
			if !knownPreparationFile(v, f.data) {
				return errors.New("existing Tailscale repository files differ; preserving them")
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}
	for _, f := range files {
		stage := filepath.Join(filepath.Dir(f.path), ".fased-tailscale-"+strconv.Itoa(uid)+"-"+filepath.Base(f.path)+".pending")
		if v, e := read(stage); e == nil {
			if !v.root || !v.regular || v.mode&0022 != 0 || !bytes.HasPrefix(f.data, v.data) {
				return errors.New("Tailscale preparation staging changed; preserve it")
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if _, e := read(f.path); errors.Is(e, os.ErrNotExist) {
			if _, e = run(ctx, f.data, "/usr/bin/install", "-m", "0644", "/dev/stdin", stage); e != nil {
				return e
			}
			if _, e = run(ctx, nil, "/usr/bin/ln", "--", stage, f.path); e != nil {
				v, re := read(f.path)
				if re != nil || !knownPreparationFile(v, f.data) {
					return errors.New("Tailscale repository publication conflicted; preserve it")
				}
			}
		} else if e != nil {
			return e
		}
		v, e := read(f.path)
		if e != nil || !knownPreparationFile(v, f.data) {
			return errors.New("Tailscale repository verification failed")
		}
		if v, e := read(stage); e == nil {
			if !knownPreparationFile(v, f.data) {
				return errors.New("Tailscale staging changed; preserve it")
			}
			if _, e = run(ctx, nil, "/usr/bin/rm", "--", stage); e != nil {
				return e
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}
	if _, e = run(ctx, nil, "/usr/bin/apt-get", "-o", "Dir::Etc::sourcelist="+sourcePath, "-o", "Dir::Etc::sourceparts=-", "-o", "Acquire::AllowInsecureRepositories=false", "-o", "Acquire::AllowDowngradeToInsecureRepositories=false", "-o", "APT::Get::AllowUnauthenticated=false", "update"); e != nil {
		return errors.New("signed Tailscale package index refresh failed; retry preserves preparation")
	}
	if _, e = run(ctx, nil, "/usr/bin/apt-get", "-o", "APT::Get::AllowUnauthenticated=false", "install", "-y", "--no-remove", "--no-install-recommends", "tailscale"); e != nil {
		return errors.New("Tailscale package installation failed; repair and retry, no source or account reset")
	}
	return nil
}
