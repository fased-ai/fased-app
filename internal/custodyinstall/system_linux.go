//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/fased-ai/fased-app/internal/update"
)

type System struct {
	In         io.Reader
	Out        io.Writer
	authorized bool
}
type hostFile struct {
	Data    []byte
	UID     int
	Mode    os.FileMode
	Regular bool
}
type readHost func(string) (hostFile, error)
type runHost func(context.Context, []byte, ...string) ([]byte, error)

func trustedParents(path string) error {
	for p := path; ; p = filepath.Dir(p) {
		i, e := os.Lstat(p)
		if e != nil {
			return e
		}
		st, ok := i.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm()&0022 != 0 {
			return errors.New("host path must have root-owned immutable directory parents")
		}
		if p == "/" {
			return nil
		}
	}
}
func readRoot(path string) (hostFile, error) {
	if e := trustedParents(filepath.Dir(path)); e != nil {
		return hostFile{}, e
	}
	i, e := os.Lstat(path)
	if e != nil {
		return hostFile{}, e
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	if !ok {
		return hostFile{}, errors.New("unknown file identity")
	}
	f := hostFile{UID: int(st.Uid), Mode: i.Mode().Perm(), Regular: i.Mode().IsRegular()}
	if !f.Regular || f.UID != 0 || f.Mode&0022 != 0 || i.Size() > 64*1024*1024 {
		return f, errors.New("foreign or unsafe root file; preserving it")
	}
	f.Data, e = os.ReadFile(path)
	return f, e
}
func known(f hostFile, b []byte, mode os.FileMode) bool {
	return f.UID == 0 && f.Regular && f.Mode == mode && bytes.Equal(f.Data, b)
}

func (s *System) Run(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	if os.Geteuid() == 0 || os.Getuid() == 0 {
		return nil, errors.New("Fased must run as an ordinary owner; only trusted OS utilities are elevated")
	}
	if len(args) == 0 {
		return nil, errors.New("missing OS operation")
	}
	switch args[0] {
	case "/usr/bin/install", "/usr/bin/mkdir", "/usr/bin/ln", "/usr/bin/mv", "/usr/bin/sync", "/usr/bin/rm", "/usr/bin/cat", "/usr/bin/systemctl", "/usr/bin/setpriv", "/usr/sbin/useradd":
	default:
		return nil, errors.New("unknown custody OS tool")
	}
	for _, p := range []string{"/usr/bin/sudo", args[0]} {
		resolved, e := filepath.EvalSymlinks(p)
		if e != nil {
			return nil, e
		}
		if e = trustedParents(filepath.Dir(resolved)); e != nil {
			return nil, e
		}
		i, e := os.Stat(resolved)
		if e != nil {
			return nil, e
		}
		st, ok := i.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || !i.Mode().IsRegular() || i.Mode().Perm()&0022 != 0 {
			return nil, errors.New("OS utilities must be root-owned and immutable")
		}
	}
	if !s.authorized {
		fmt.Fprintln(s.Out, "Authorize disposable custody installation: dedicated locked OS accounts, root-owned files and system services. Fased itself remains unprivileged. No real wallets are enabled.")
		c := exec.CommandContext(ctx, "/usr/bin/sudo", "-v")
		c.Stdin = s.In
		c.Stdout = s.Out
		c.Stderr = s.Out
		if e := c.Run(); e != nil {
			return nil, errors.New("OS authorization cancelled or unavailable; installation remains disabled")
		}
		s.authorized = true
	}
	c := exec.CommandContext(ctx, "/usr/bin/sudo", append([]string{"-n", "--"}, args...)...)
	c.Stdin = bytes.NewReader(input)
	c.Stderr = s.Out
	return c.Output()
}

// AppHelper elevates the OS credential switcher, which drops UID, groups and
// capabilities before any Fased code reads interactive owner input.
func (s *System) AppHelper(ctx context.Context, m Manifest, recoverPassword, passwordStdin bool, out io.Writer) error {
	if recoverPassword && passwordStdin {
		return errors.New("app recovery requires an owner terminal")
	}
	action := "claim-code"
	if recoverPassword {
		action = "recover"
	}
	return s.appClient(ctx, m, action, nil, out)
}
func (s *System) AccountsHelper(ctx context.Context, m Manifest, login, request, passwordStdin bool, out io.Writer) error {
	action := "accounts"
	flags := []string{}
	if login {
		action = "login"
		if passwordStdin {
			flags = append(flags, "--password-stdin")
		}
	} else if request {
		flags = append(flags, "--request-stdin")
	}
	return s.appClient(ctx, m, action, flags, out)
}
func (s *System) appClient(ctx context.Context, m Manifest, action string, flags []string, out io.Writer) error {
	if action != "claim-code" && action != "recover" && action != "accounts" && action != "login" && action != "update-inventory" && action != "update-pause" {
		return errors.New("unsupported app helper action")
	}
	if e := Verify(m); e != nil {
		return e
	}
	if m.Owner != os.Getuid() {
		return errors.New("installation owner required")
	}
	if e := loadedUnit(m, "app"); e != nil {
		return e
	}
	if _, e := s.Run(ctx, nil, "/usr/bin/setpriv", "--version"); e != nil {
		return e
	}
	args := append([]string{"-n", "--"}, appArgs(m, action)...)
	args = append(args, flags...)
	c := exec.CommandContext(ctx, "/usr/bin/sudo", args...)
	c.Stdin = s.In
	c.Stdout = out
	c.Stderr = s.Out
	return c.Run()
}

// UpdateInventory uses the installed immutable client as app UID over private
// IPC. App metadata never supplies the signer's financial history or floor.
func (s *System) UpdateInventory(ctx context.Context, m Manifest, probe string, out io.Writer) error {
	if !update.IDOK(probe) {
		return errors.New("invalid inventory challenge")
	}
	return s.appClient(ctx, m, "update-inventory", []string{"--probe-id", probe}, out)
}

// UpdatePause sends only typed public metadata over the immutable app-UID
// helper's stdin. Wallet secrets and owner credentials never cross this bridge.
func (s *System) UpdatePause(ctx context.Context, m Manifest, command update.CorePauseCommand, out io.Writer) error {
	b, e := json.Marshal(command)
	if e != nil {
		return e
	}
	copy := *s
	copy.In = bytes.NewReader(b)
	return copy.appClient(ctx, m, "update-pause", []string{"--request-stdin"}, out)
}

func appArgs(m Manifest, action string) []string {
	return []string{"/usr/bin/setpriv", "--reuid", strconv.Itoa(m.App.UID), "--regid", strconv.Itoa(m.App.GID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", m.Root + "/fased-app", action, "--data-dir", m.Root + "/app-state"}
}

// Only fixed root staging files are replaceable. Publication is an exclusive
// link in the destination filesystem; foreign targets are never overwritten.
func publish(ctx context.Context, target, id string, data []byte, mode os.FileMode, read readHost, run runHost) error {
	stage := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+"-"+id+".pending")
	current, e := read(target)
	if e == nil && !known(current, data, mode) {
		return errors.New("managed file is foreign or changed; preserving it")
	}
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	ready := e == nil
	st, se := read(stage)
	if se == nil && (st.UID != 0 || !st.Regular || st.Mode != mode || !bytes.HasPrefix(data, st.Data)) {
		return errors.New("foreign staging file; preserving it")
	}
	if se != nil && !errors.Is(se, os.ErrNotExist) {
		return se
	}
	if !ready {
		if _, e = run(ctx, data, "/usr/bin/install", "-m", strconv.FormatUint(uint64(mode), 8), "-o", "root", "-g", "root", "--", "/dev/stdin", stage); e != nil {
			return e
		}
		if st, e = read(stage); e != nil || !known(st, data, mode) {
			return errors.New("root staging copy failed verification")
		}
		if _, e = run(ctx, nil, "/usr/bin/sync", "-f", stage); e != nil {
			return e
		}
		if _, e = run(ctx, nil, "/usr/bin/ln", "--no-target-directory", "--", stage, target); e != nil {
			return e
		}
		if _, e = run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(target)); e != nil {
			return e
		}
	}
	if !ready || se == nil {
		if _, e = run(ctx, nil, "/usr/bin/rm", "--", stage); e != nil {
			return e
		}
		_, e = run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(target))
		return e
	}
	return nil
}
