// Package hostprep prepares a fixed Ubuntu host prerequisite using trusted OS
// tools. Fased itself never runs as root, interprets a privileged script, or
// changes global AppArmor settings. Policy data stays separately licensed.
package hostprep

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
)

const PolicySHA256 = "11d39094f044f0cda0febb3ad517b830301da6b2ce929664af09ee9e4dd264f9"
const policyTarget = "/etc/apparmor.d/fased-app-bwrap-v1"
const policyDir = "/etc/apparmor.d"

type hostFile struct {
	Data          []byte
	Root, Regular bool
	Mode          os.FileMode
}
type receipt struct {
	Schema int    `json:"schema"`
	Stage  string `json:"stage"`
	Phase  string `json:"phase"`
}
type readHost func(string) (hostFile, error)
type runHost func(context.Context, []byte, ...string) ([]byte, error)

func validReceipt(r receipt) error {
	prefix := fmt.Sprintf(".fased-app-bwrap-%d-", os.Getuid())
	token := strings.TrimSuffix(strings.TrimPrefix(r.Stage, prefix), ".pending")
	if r.Schema != 1 || len(token) != 32 || r.Stage != prefix+token+".pending" {
		return errors.New("invalid host preparation identity")
	}
	if _, e := hex.DecodeString(token); e != nil {
		return e
	}
	if r.Phase != "staging" && r.Phase != "published" && r.Phase != "ready" {
		return errors.New("unknown host preparation phase")
	}
	return nil
}

func policyDigest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func VerifyPolicy(path string) ([]byte, error) {
	i, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !i.Mode().IsRegular() || i.Size() > 16*1024 {
		return nil, errors.New("sandbox policy must be a regular bundled file")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	if policyDigest(b) != PolicySHA256 {
		return nil, errors.New("sandbox policy differs from the compiled reviewed digest; refusing OS preparation")
	}
	return b, nil
}
func readSystemFile(path string) (hostFile, error) {
	i, e := os.Lstat(path)
	if e != nil {
		return hostFile{}, e
	}
	f := hostFile{Regular: i.Mode().IsRegular(), Mode: i.Mode().Perm()}
	owner, ok := i.Sys().(*syscall.Stat_t)
	f.Root = ok && owner.Uid == 0
	if !f.Regular || i.Size() > 16*1024 {
		return f, nil
	}
	f.Data, e = os.ReadFile(path)
	return f, e
}
func known(f hostFile, policy []byte) bool {
	return f.Root && f.Regular && f.Mode&0022 == 0 && bytes.Equal(f.Data, policy)
}
func ValidateTarget() error {
	f, e := readSystemFile(policyTarget)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	if !f.Root || !f.Regular || f.Mode&0022 != 0 || policyDigest(f.Data) != PolicySHA256 {
		return errors.New("sandbox policy destination is foreign or changed; preserving it")
	}
	return nil
}

// System invokes only fixed, absolute OS tools. sudo obtains its password from
// the terminal; it never receives -S, a password, or project code to execute.
type System struct {
	In         io.Reader
	Out        io.Writer
	authorized bool
}

func (s *System) Run(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	if os.Geteuid() == 0 {
		return nil, errors.New("run Fased as an ordinary OS user")
	}
	if len(args) == 0 {
		return nil, errors.New("missing OS operation")
	}
	switch args[0] {
	case "/usr/bin/apt-get", "/usr/bin/cat", "/usr/bin/install", "/usr/bin/ln", "/usr/bin/rm", "/usr/bin/sync", "/usr/sbin/apparmor_parser":
	default:
		return nil, errors.New("unknown OS preparation tool")
	}
	for _, p := range []string{"/usr/bin/sudo", args[0]} {
		i, e := os.Stat(p)
		if e != nil {
			return nil, e
		}
		owner, ok := i.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != 0 || !i.Mode().IsRegular() || i.Mode().Perm()&0022 != 0 {
			return nil, errors.New("OS preparation requires root-owned trusted tools")
		}
	}
	if !s.authorized {
		fmt.Fprintln(s.Out, "Authorize Ubuntu prerequisites: OS packages and the reviewed restrictive Bubblewrap profile. Existing policies are preserved; global AppArmor restrictions stay enabled. Fased remains unprivileged.")
		c := exec.CommandContext(ctx, "/usr/bin/sudo", "-v")
		c.Stdin = s.In
		c.Stdout = s.Out
		c.Stderr = s.Out
		if e := c.Run(); e != nil {
			return nil, errors.New("OS authorization cancelled or unavailable; no unprotected fallback")
		}
		s.authorized = true
	}
	c := exec.CommandContext(ctx, "/usr/bin/sudo", append([]string{"-n", "--"}, args...)...)
	c.Stdin = bytes.NewReader(input)
	c.Stderr = s.Out
	if args[0] == "/usr/bin/apt-get" {
		c.Stdout = s.Out
		return nil, c.Run()
	}
	return c.Output()
}
func Prepare(ctx context.Context, data, policyPath string, system *System, probe func() error) error {
	policy, e := VerifyPolicy(policyPath)
	if e != nil {
		return e
	}
	if e = localinstall.CheckHostTools(); e != nil {
		return e
	}
	i, e := os.Lstat(policyDir)
	if e != nil {
		return e
	}
	owner, ok := i.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != 0 || !i.IsDir() || i.Mode().Perm()&0022 != 0 {
		return errors.New("AppArmor directory must be an existing root-owned directory")
	}
	return preparePolicy(ctx, data, policy, readSystemFile, system.Run, probe)
}
func preparePolicy(ctx context.Context, data string, policy []byte, read readHost, run runHost, probe func() error) error {
	if policyDigest(policy) != PolicySHA256 {
		return errors.New("unreviewed sandbox policy")
	}
	f, e := read(policyTarget)
	exists := e == nil
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if exists && !known(f, policy) {
		return errors.New("sandbox destination changed or foreign; preserving it")
	}
	if probe() == nil {
		// Loading may have completed just before the installer was interrupted.
		// Finish only our private receipt; never rewrite ready host policy.
		p := filepath.Join(data, "host-preparation.json")
		if b, e := core.ReadPrivateFile(p); e == nil {
			var r receipt
			if len(b) > 4096 || json.Unmarshal(b, &r) != nil || validReceipt(r) != nil {
				return errors.New("unknown host preparation receipt; preserving it")
			}
			if r.Phase != "ready" {
				stage := filepath.Join(policyDir, r.Stage)
				if st, e := read(stage); e == nil {
					if !st.Root || !st.Regular || st.Mode&0022 != 0 || !bytes.HasPrefix(policy, st.Data) {
						return errors.New("changed staging file; preserving it")
					}
					if _, e = run(ctx, nil, "/usr/bin/rm", "--", stage); e != nil {
						return e
					}
				} else if !errors.Is(e, os.ErrNotExist) {
					return e
				}
				r.Phase = "ready"
				return core.WritePrivateJSON(p, r)
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		return nil
	}
	if e = core.EnsurePrivateDirectory(data); e != nil {
		return e
	}
	lock, e := core.LockInstance(filepath.Join(data, "host-preparation.lock"))
	if e != nil {
		return e
	}
	defer lock.Close()
	loaded, e := run(ctx, nil, "/usr/bin/cat", "/sys/kernel/security/apparmor/profiles")
	if e != nil {
		return fmt.Errorf("cannot inspect loaded sandbox identities: %w", e)
	}
	for _, line := range strings.Split(string(loaded), "\n") {
		if strings.HasPrefix(line, "bwrap ") || strings.HasPrefix(line, "bwrap//") || strings.HasPrefix(line, "unpriv_bwrap ") {
			if exists && probe() == nil {
				return nil
			}
			return errors.New("an existing Bubblewrap profile blocks preparation; preserving it, never replacing loaded policy")
		}
	}
	path := filepath.Join(data, "host-preparation.json")
	var r receipt
	b, e := core.ReadPrivateFile(path)
	if e == nil {
		if len(b) > 4096 || json.Unmarshal(b, &r) != nil || validReceipt(r) != nil {
			return errors.New("unknown host preparation receipt; preserving it")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	prefix := fmt.Sprintf(".fased-app-bwrap-%d-", os.Getuid())
	if r.Stage == "" {
		var nonce [16]byte
		if _, e = rand.Read(nonce[:]); e != nil {
			return e
		}
		r = receipt{1, prefix + hex.EncodeToString(nonce[:]) + ".pending", "staging"}
	}
	if e = validReceipt(r); e != nil {
		return e
	}
	if e = core.WritePrivateJSON(path, r); e != nil {
		return e
	}
	stage := filepath.Join(policyDir, r.Stage)
	if !exists {
		st, e := read(stage)
		if e == nil {
			if !st.Root || !st.Regular || st.Mode&0022 != 0 || !bytes.HasPrefix(policy, st.Data) {
				return errors.New("foreign staging file; preserving it")
			}
			if !bytes.Equal(st.Data, policy) {
				if _, e = run(ctx, nil, "/usr/bin/rm", "--", stage); e != nil {
					return e
				}
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if _, e = read(stage); errors.Is(e, os.ErrNotExist) {
			if _, e = run(ctx, policy, "/usr/bin/install", "-m", "0644", "--", "/dev/stdin", stage); e != nil {
				return fmt.Errorf("sandbox staging interrupted; rerun the same install: %w", e)
			}
		} else if e != nil {
			return e
		}
		st, e = read(stage)
		if e != nil || !known(st, policy) {
			return errors.New("sandbox staging verification failed")
		}
		if _, e = run(ctx, nil, "/usr/bin/sync", "--", stage, policyDir); e != nil {
			return e
		}
		// Hard-link publication is atomic and refuses every existing destination;
		// another preparer can win only with identical verified policy bytes.
		if _, e = run(ctx, nil, "/usr/bin/ln", "--no-target-directory", "--", stage, policyTarget); e != nil {
			f, re := read(policyTarget)
			if re != nil || !known(f, policy) {
				return fmt.Errorf("sandbox publication refused; preserving destination: %w", e)
			}
		}
	}
	f, e = read(policyTarget)
	if e != nil || !known(f, policy) {
		return errors.New("sandbox publication verification failed")
	}
	if _, e = run(ctx, nil, "/usr/bin/sync", "--", policyTarget, policyDir); e != nil {
		return e
	}
	if st, e := read(stage); e == nil {
		if !known(st, policy) {
			return errors.New("changed sandbox staging file; preserving it")
		}
		if _, e = run(ctx, nil, "/usr/bin/rm", "--", stage); e != nil {
			return e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	r.Phase = "published"
	if e = core.WritePrivateJSON(path, r); e != nil {
		return e
	}
	if _, e = run(ctx, nil, "/usr/sbin/apparmor_parser", "-a", policyTarget); e != nil {
		if probe() != nil {
			return fmt.Errorf("sandbox loading interrupted or conflicting; rerun the same install: %w", e)
		}
	}
	if e = probe(); e != nil {
		return fmt.Errorf("sandbox enforcement verification failed: %w", e)
	}
	r.Phase = "ready"
	return core.WritePrivateJSON(path, r)
}
