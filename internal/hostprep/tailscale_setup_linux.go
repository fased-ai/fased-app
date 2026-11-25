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
	"github.com/fased-ai/fased-app/internal/hostplatform"
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
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/privateaccess"
)

// Public APT signing key from the official Noble/Bookworm/Trixie repository:
// https://pkgs.tailscale.com/stable/ubuntu/noble.noarmor.gpg
// A replacement key requires a reviewed application change, never a setup flag.
//
//go:embed tailscale-noble-key.gpg
var tailscaleAPTKey []byte

// Public RPM signing key from the official Fedora repository, reviewed 2026-11-24:
// https://pkgs.tailscale.com/stable/fedora/repo.gpg
//
//go:embed tailscale-fedora-key.gpg
var tailscaleRPMKey []byte

const (
	tailscaleAPTKeySHA256          = "3e03dacf222698c60b8e2f990b809ca1b3e104de127767864284e6c228f1fb39"
	tailscaleKeyPath               = "/usr/share/keyrings/fased-tailscale.gpg"
	tailscaleSourcePath            = "/etc/apt/sources.list.d/fased-tailscale.list"
	standardTailscaleSourcePath    = "/etc/apt/sources.list.d/tailscale.list"
	standardTailscaleKeyPath       = "/usr/share/keyrings/tailscale-archive-keyring.gpg"
	tailscaleSource                = "deb [signed-by=/usr/share/keyrings/fased-tailscale.gpg] https://pkgs.tailscale.com/stable/ubuntu noble main\n"
	tailscaleRPMKeySHA256          = "53c6f7dfbd774839d9f37e6c5022ba952108aba9a0e556a56f292a9eb605d7cf"
	tailscaleRPMKeyPath            = "/etc/pki/rpm-gpg/RPM-GPG-KEY-fased-tailscale"
	tailscaleRPMSourcePath         = "/etc/yum.repos.d/fased-tailscale.repo"
	standardTailscaleRPMSourcePath = "/etc/yum.repos.d/tailscale.repo"
	officialFedoraTailscaleRepo    = "[tailscale-stable]\nname=Tailscale stable\nbaseurl=https://pkgs.tailscale.com/stable/fedora/$basearch\nenabled=1\ntype=rpm\nrepo_gpgcheck=1\ngpgcheck=1\ngpgkey=https://pkgs.tailscale.com/stable/fedora/repo.gpg"
)

type tailscaleRepoFile struct {
	path string
	data []byte
}

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
	platform, e := hostplatform.Current()
	if e != nil {
		return e
	}
	if !platform.Remote {
		return errors.New("Remote Tailscale setup requires a reviewed Remote OS profile")
	}
	u, e := user.Current()
	if e != nil {
		return e
	}
	if !AllowedTailscalePreparation(u.Username, []string{"status", "--json"}) {
		return errors.New("unsupported OS administrator identity")
	}
	authority, authorityErr := maintenance.ReadAuthority(os.Getuid())
	// An already enrolled, online device needs no login or preference change.
	if _, err := readPreparationFile("/usr/bin/tailscale"); err == nil {
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		probe := func(ctx context.Context, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, "/usr/bin/tailscale", args...).Output()
		}
		if _, err := (privateaccess.Manager{Run: probe}).Plan(probeCtx, "tailscale", "127.0.0.1:7331"); err == nil {
			return nil
		}
	}
	// A parent symlink/writable directory may not redirect privileged publication.
	prepDirs := []string{"/usr/share/keyrings", "/etc/apt/sources.list.d"}
	if platform.Family == "dnf" {
		prepDirs = []string{"/etc/pki/rpm-gpg", "/etc/yum.repos.d"}
	}
	for _, dir := range prepDirs {
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
		return errors.New("Tailscale setup deferred; connect Tailscale and retry Remote setup")
	}
	base, e := core.DefaultDataDir()
	if e != nil {
		return e
	}
	dir := filepath.Join(base, "host-preparation", "tailscale")
	system := &System{In: os.Stdin, Out: out, Authorization: "Authorize Tailscale's official signed package, service and this user's private access. Existing accounts/routes and SSH/firewall policy are preserved. Fased stays unprivileged."}
	if authorityErr == nil {
		if authority.Profile != "remote" {
			return errors.New("maintenance setup belongs to a different installation profile")
		}
		run := func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
			if len(input) != 0 {
				return nil, errors.New("Tailscale package preparation is owned by the maintenance helper")
			}
			if strings.Join(args, " ") == "/usr/bin/systemctl enable --now tailscaled.service" {
				return exec.CommandContext(ctx, "/usr/bin/systemctl", "is-active", "tailscaled.service").Output()
			}
			if len(args) < 2 || args[0] != "/usr/bin/tailscale" || !AllowedTailscalePreparation(u.Username, args[1:]) {
				return nil, errors.New("private setup requires the prepared Tailscale operator")
			}
			if args[1] == "login" {
				_, err := maintenance.Call(ctx, maintenance.Request{Version: 1, Action: "tailscale-login"}, out)
				return nil, err
			}
			if args[1] == "set" {
				return nil, errors.New("confined setup does not grant Tailscale operator rights")
			}
			c := exec.CommandContext(ctx, args[0], args[1:]...)
			c.Stderr = out
			return c.Output()
		}
		return prepareTailscale(ctx, dir, os.Getuid(), u.Username, out, readPreparationFile, run, platform)
	} else if !errors.Is(authorityErr, os.ErrNotExist) {
		return authorityErr
	}
	if _, err := readPreparationFile("/usr/bin/tailscale"); err != nil {
		return errors.New("use the Fased installer to prepare Tailscale; existing host settings are preserved")
	}
	run := func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
		if len(args) > 1 && args[0] == "/usr/bin/tailscale" && (args[1] == "login" || args[1] == "set") {
			return nil, errors.New("connect the existing Tailscale device through its administrator, then retry")
		}
		if len(args) > 1 && args[0] == "/usr/bin/tailscale" {
			return exec.CommandContext(ctx, args[0], args[1:]...).Output()
		}
		return system.Run(ctx, input, args...)
	}
	return prepareTailscale(ctx, dir, os.Getuid(), u.Username, out, readPreparationFile, run, platform)
}

func prepareTailscale(ctx context.Context, dir string, uid int, username string, out io.Writer, read preparationRead, run preparationRun, platforms ...hostplatform.Platform) error {
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
	rh := sha256.Sum256(tailscaleRPMKey)
	if hex.EncodeToString(rh[:]) != tailscaleRPMKeySHA256 {
		return errors.New("bundled Tailscale RPM signing key differs from the reviewed digest")
	}
	installed := false
	if _, e := read("/usr/bin/tailscale"); errors.Is(e, os.ErrNotExist) {
		if e = prepareTailscalePackage(ctx, dir, uid, read, run, platforms...); e != nil {
			return e
		}
		installed = true
	} else if e != nil {
		return e
	}
	if installed {
		if _, e := run(ctx, nil, "/usr/bin/systemctl", "enable", "--now", "tailscaled.service"); e != nil {
			return errors.New("Tailscale service preparation failed; preserve SSH, repair and retry")
		}
	}
	prefs, e := run(ctx, nil, "/usr/bin/tailscale", "debug", "prefs")
	if e != nil || len(prefs) > 64*1024 {
		return errors.New("Tailscale preferences unavailable; no account or route settings were changed")
	}
	var p struct{ OperatorUser string }
	if json.Unmarshal(prefs, &p) != nil {
		return errors.New("invalid Tailscale preferences; preserve existing settings")
	}
	status, e := run(ctx, nil, "/usr/bin/tailscale", "status", "--json")
	if e != nil || len(status) > 1024*1024 {
		return errors.New("Tailscale status unavailable; preserve SSH and retry")
	}
	var state struct{ BackendState string }
	if json.Unmarshal(status, &state) != nil {
		return errors.New("invalid Tailscale status")
	}
	if state.BackendState != "Running" && p.OperatorUser != "" && p.OperatorUser != username {
		return errors.New("Tailscale already has a different operator; ask its administrator before retrying Remote setup")
	}
	switch state.BackendState {
	case "Running": // Do not log in again or replace an existing account.
	case "NeedsLogin":
		fmt.Fprintln(out, "Open the Tailscale sign-in URL printed below in your computer's browser. No auth key is needed. Keep this SSH connection open.")
		if _, e = run(ctx, nil, "/usr/bin/tailscale", "login", "--timeout=120s"); e != nil {
			return errors.New("Tailscale sign-in was not completed; retry Tailscale sign-in; existing settings are preserved")
		}
	default:
		return errors.New("Tailscale needs owner attention (for example device approval or a deliberately stopped connection); preserve its settings, then retry Remote setup")
	}
	fmt.Fprintln(out, "On the computer you will use, install Tailscale from https://tailscale.com/download and sign in to the same tailnet. Enable tailnet HTTPS and allow only your intended devices. Fased will verify private HTTPS; keep SSH/console recovery.")
	return nil
}

func recordTailscalePreparation(dir string, uid int) (io.Closer, error) {
	if e := core.EnsurePrivateDirectory(dir); e != nil {
		return nil, e
	}
	lock, e := core.LockInstance(filepath.Join(dir, "prepare.lock"))
	if e != nil {
		return nil, e
	}
	receipt := filepath.Join(dir, "preparation.json")
	if b, e := core.ReadPrivateFile(receipt); e == nil {
		var r tailscalePreparation
		if len(b) > 1024 || json.Unmarshal(b, &r) != nil || r.Version != 1 || r.Owner != uid {
			_ = lock.Close()
			return nil, errors.New("unknown Tailscale preparation record; preserve it")
		}
	} else if errors.Is(e, os.ErrNotExist) {
		if e = core.WritePrivateJSON(receipt, tailscalePreparation{Version: 1, Owner: uid}); e != nil {
			_ = lock.Close()
			return nil, e
		}
	} else {
		_ = lock.Close()
		return nil, e
	}
	return lock, nil
}

func nonEmptyRepoLines(data []byte) string {
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func publishTailscaleRepoFiles(ctx context.Context, uid int, read preparationRead, run preparationRun, files []tailscaleRepoFile) error {
	// Refuse foreign destinations before publishing any file.
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
	return nil
}

func prepareTailscalePackage(ctx context.Context, dir string, uid int, read preparationRead, run preparationRun, platforms ...hostplatform.Platform) error {
	if len(platforms) > 1 {
		return errors.New("one exact OS package profile required")
	}
	if len(platforms) == 1 && platforms[0].Family == "dnf" {
		return prepareTailscaleDnfPackage(ctx, dir, uid, read, run, platforms[0])
	}
	source := tailscaleSource
	if len(platforms) == 1 {
		source = platforms[0].TailscaleSource(tailscaleKeyPath)
	}
	lock, e := recordTailscalePreparation(dir, uid)
	if e != nil {
		return e
	}
	defer lock.Close()
	files := []tailscaleRepoFile{{tailscaleKeyPath, tailscaleAPTKey}, {tailscaleSourcePath, []byte(source)}}
	// Reuse an exact already-configured official repository instead of adding a
	// conflicting Signed-By definition. Never rewrite an administrator's files.
	if existing, err := read(standardTailscaleSourcePath); err == nil {
		want := strings.ReplaceAll(source, tailscaleKeyPath, standardTailscaleKeyPath)
		key, keyErr := read(standardTailscaleKeyPath)
		if !existing.root || !existing.regular || existing.mode&0022 != 0 || nonEmptyRepoLines(existing.data) != strings.TrimSpace(want) || keyErr != nil || !knownPreparationFile(key, tailscaleAPTKey) {
			return errors.New("existing Tailscale repository differs; ask its administrator before retrying Remote setup")
		}
		files = nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if e = publishTailscaleRepoFiles(ctx, uid, read, run, files); e != nil {
		return e
	}
	// Refresh the administrator's configured OS indexes as well as the signed
	// Tailscale source. Minimal images may not cache distro dependencies, and a
	// source-restricted update can discard those lists. Keep the configured trust
	// roots; refresh metadata only, never upgrade unrelated installed packages.
	if _, e = run(ctx, nil, "/usr/bin/apt-get", "-o", "Acquire::AllowInsecureRepositories=false", "-o", "Acquire::AllowDowngradeToInsecureRepositories=false", "-o", "APT::Get::AllowUnauthenticated=false", "-o", "APT::Update::Error-Mode=any", "update"); e != nil {
		return errors.New("signed OS and Tailscale package index refresh failed; retry preserves preparation")
	}
	policy, e := run(ctx, nil, "/usr/bin/apt-cache", "policy", "tailscale")
	if e != nil {
		return errors.New("Tailscale package candidate unavailable; preserve preparation")
	}
	version, e := tailscaleAptCandidate(policy)
	if e != nil {
		return e
	}
	if _, e = run(ctx, nil, "/usr/bin/apt-get", "-o", "APT::Get::AllowUnauthenticated=false", "install", "-y", "--no-remove", "--no-install-recommends", "tailscale="+version); e != nil {
		return errors.New("Tailscale package installation failed; repair and retry, no source or account reset")
	}
	return nil
}

func prepareTailscaleDnfPackage(ctx context.Context, dir string, uid int, read preparationRead, run preparationRun, platform hostplatform.Platform) error {
	source := platform.TailscaleSource(tailscaleRPMKeyPath)
	lock, e := recordTailscalePreparation(dir, uid)
	if e != nil {
		return e
	}
	defer lock.Close()
	repoID := "fased-tailscale-stable"
	files := []tailscaleRepoFile{{tailscaleRPMKeyPath, tailscaleRPMKey}, {tailscaleRPMSourcePath, []byte(source)}}
	if existing, err := read(standardTailscaleRPMSourcePath); err == nil {
		if !existing.root || !existing.regular || existing.mode&0022 != 0 || nonEmptyRepoLines(existing.data) != officialFedoraTailscaleRepo {
			return errors.New("existing Tailscale RPM repository differs; ask its administrator before retrying Remote setup")
		}
		repoID = "tailscale-stable"
		files = nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if e = publishTailscaleRepoFiles(ctx, uid, read, run, files); e != nil {
		return e
	}
	if _, e = run(ctx, nil, "/usr/bin/dnf", "makecache", "--refresh", "--repo="+repoID); e != nil {
		return errors.New("signed Tailscale RPM metadata refresh failed; retry preserves preparation")
	}
	policy, e := run(ctx, nil, "/usr/bin/dnf", "repoquery", "--available", "--latest-limit=1", "--qf=%{version}", "--repo="+repoID, "tailscale")
	if e != nil {
		return errors.New("Tailscale RPM candidate unavailable; preserve preparation")
	}
	version, e := tailscaleDnfCandidate(policy)
	if e != nil {
		return e
	}
	if _, e = run(ctx, nil, "/usr/bin/dnf", "install", "--assumeyes", "--setopt=install_weak_deps=False", "--setopt=gpgcheck=1", "--setopt=repo_gpgcheck=1", "tailscale-"+version); e != nil {
		return errors.New("Tailscale RPM installation failed; repair and retry, no source or account reset")
	}
	return nil
}
