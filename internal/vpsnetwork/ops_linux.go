//go:build linux

package vpsnetwork

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
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

const sshConfigDir = "/etc/ssh/sshd_config.d"

type hostFile struct {
	Data               []byte
	Size               int64
	UID, GID           int
	Mode               os.FileMode
	Directory, Regular bool
	Link               string
}

type hostOps struct {
	uid, euid, gid int
	read           func(string) (hostFile, error)
	list           func(string) ([]string, error)
	run            func(context.Context, []byte, ...string) ([]byte, error)
	command        func(context.Context, ...string) ([]byte, error)
	readJournal    func(string) ([]byte, error)
	writeJournal   func(string, any) error
	lock           func() (io.Closer, error)
}

func file(path string) (hostFile, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return hostFile{}, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return hostFile{}, errors.New("unknown host file ownership")
	}
	f := hostFile{
		Size:      info.Size(),
		UID:       int(st.Uid),
		GID:       int(st.Gid),
		Mode:      info.Mode(),
		Directory: info.IsDir(),
		Regular:   info.Mode().IsRegular(),
	}
	if f.Regular {
		if info.Size() > 64<<20 {
			return f, errors.New("oversized host input")
		}
		f.Data, err = os.ReadFile(path)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		f.Link, err = os.Readlink(path)
	}
	return f, err
}

func ownerJournal() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	d = filepath.Join(d, "fased-app-vps-installer")
	return d, core.EnsurePrivateDirectory(d)
}

func liveOps(s *custodyinstall.System) hostOps {
	o := hostOps{
		uid:  os.Getuid(),
		euid: os.Geteuid(),
		gid:  os.Getgid(),
		read: file,
		list: func(p string) ([]string, error) {
			es, err := os.ReadDir(p)
			if err != nil {
				return nil, err
			}
			names := make([]string, 0, len(es))
			for _, v := range es {
				names = append(names, v.Name())
			}
			return names, nil
		},
		command: func(ctx context.Context, args ...string) ([]byte, error) {
			if len(args) == 0 || (args[0] != "/usr/bin/getent" && args[0] != "/usr/bin/id" && args[0] != "/usr/bin/systemctl") {
				return nil, errors.New("unknown Remote read-only OS tool")
			}
			if err := trustedTool(fileOps(), args[0]); err != nil {
				return nil, err
			}
			return exec.CommandContext(ctx, args[0], args[1:]...).Output()
		},
		readJournal: func(name string) ([]byte, error) {
			d, err := ownerJournal()
			if err != nil {
				return nil, err
			}
			return core.ReadPrivateFile(filepath.Join(d, name))
		},
		writeJournal: func(name string, v any) error {
			d, err := ownerJournal()
			if err != nil {
				return err
			}
			return core.WritePrivateJSON(filepath.Join(d, name), v)
		},
		lock: func() (io.Closer, error) {
			d, err := ownerJournal()
			if err != nil {
				return nil, err
			}
			return core.LockInstance(filepath.Join(d, "install.lock"))
		},
	}
	if s != nil {
		if s.In == nil {
			s.In = os.Stdin
		}
		if s.Out == nil {
			s.Out = io.Discard
		}
		o.run = s.Run
	}
	return o
}

func fileOps() hostOps {
	return hostOps{read: file}
}

func strict(b []byte, v any) error {
	if len(b) > 16<<10 {
		return errors.New("oversized Remote record")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing Remote record")
	}
	return nil
}

func ordinary(o hostOps) error {
	if o.uid <= 0 || o.euid != o.uid {
		return errors.New("ordinary installation owner required")
	}
	return nil
}

func ownedBy(o hostOps, m Manifest) error {
	if err := ordinary(o); err != nil {
		return err
	}
	if m.OwnerUID != o.uid {
		return errors.New("Remote installation belongs to another OS owner")
	}
	return m.validate()
}

func parents(o hostOps, path string) error {
	for p := path; ; p = filepath.Dir(p) {
		f, err := o.read(p)
		if err != nil {
			return err
		}
		if !f.Directory || f.UID != 0 || f.GID != 0 || f.Mode.Perm()&0022 != 0 || f.Mode&os.ModeSymlink != 0 {
			return errors.New("root-owned immutable directory parents required")
		}
		if p == "/" {
			return nil
		}
	}
}

func trustedTool(o hostOps, p string) error {
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return err
	}
	if err = parents(o, filepath.Dir(resolved)); err != nil {
		return err
	}
	f, err := o.read(resolved)
	if err != nil {
		return err
	}
	if !f.Regular || f.UID != 0 || f.Mode.Perm()&0022 != 0 || f.Mode.Perm()&0111 == 0 {
		return errors.New("trusted root-owned OS tool required")
	}
	return nil
}

func rootFile(o hostOps, p string) (hostFile, error) {
	if err := parents(o, filepath.Dir(p)); err != nil {
		return hostFile{}, err
	}
	f, err := o.read(p)
	if err != nil {
		return f, err
	}
	if !f.Regular || f.UID != 0 || f.GID != 0 || f.Mode.Perm()&0022 != 0 ||
		f.Mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return f, errors.New("foreign root file; preserve it")
	}
	return f, nil
}

// readSSHDirEntries inspects /etc/ssh/sshd_config.d via the argument-pinned root find helper
// so Fedora's 0700 /etc/ssh/sshd_config.d directory works cleanly without relaxing permissions.
func readSSHDirEntries(ctx context.Context, o hostOps) (map[string]hostFile, error) {
	if err := parents(o, sshConfigDir); err != nil {
		return nil, err
	}
	if o.run == nil {
		return nil, os.ErrPermission
	}
	out, err := o.run(ctx, nil, "/usr/bin/find", sshConfigDir, "-mindepth", "1", "-maxdepth", "1", "-printf", "%U:%G:%m:%y:%s:%f\\0")
	if err != nil {
		return nil, err
	}
	entries := map[string]hostFile{}
	for _, raw := range bytes.Split(out, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		parts := strings.SplitN(string(raw), ":", 6)
		if len(parts) != 6 {
			return nil, errors.New("invalid SSH directory entry metadata")
		}
		uid, uErr := strconv.Atoi(parts[0])
		gid, gErr := strconv.Atoi(parts[1])
		modeVal, mErr := strconv.ParseUint(parts[2], 8, 32)
		size, sErr := strconv.ParseInt(parts[4], 10, 64)
		name := parts[5]
		if uErr != nil || gErr != nil || mErr != nil || sErr != nil ||
			name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00\r\n") {
			return nil, errors.New("invalid SSH directory entry metadata")
		}
		mode := os.FileMode(modeVal & 0o777)
		if modeVal&0o4000 != 0 {
			mode |= os.ModeSetuid
		}
		if modeVal&0o2000 != 0 {
			mode |= os.ModeSetgid
		}
		if modeVal&0o1000 != 0 {
			mode |= os.ModeSticky
		}
		var dir, reg bool
		switch parts[3] {
		case "f":
			reg = true
		case "d":
			dir = true
			mode |= os.ModeDir
		case "l":
			mode |= os.ModeSymlink
		}
		entries[name] = hostFile{
			Size:      size,
			UID:       uid,
			GID:       gid,
			Mode:      mode,
			Directory: dir,
			Regular:   reg,
		}
	}
	return entries, nil
}

func listSSHDir(ctx context.Context, o hostOps) ([]string, error) {
	names, err := o.list(sshConfigDir)
	if err == nil {
		sort.Strings(names)
		return names, nil
	}
	if !errors.Is(err, os.ErrPermission) {
		return nil, err
	}
	entries, fErr := readSSHDirEntries(ctx, o)
	if fErr != nil {
		return nil, fErr
	}
	names = make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func readRootFileCtx(ctx context.Context, o hostOps, path string) (hostFile, error) {
	f, err := rootFile(o, path)
	if err == nil {
		return f, nil
	}
	if !errors.Is(err, os.ErrPermission) || filepath.Dir(path) != sshConfigDir {
		return f, err
	}
	entries, fErr := readSSHDirEntries(ctx, o)
	if fErr != nil {
		return hostFile{}, fErr
	}
	entry, ok := entries[filepath.Base(path)]
	if !ok {
		return hostFile{}, os.ErrNotExist
	}
	if !entry.Regular || entry.UID != 0 || entry.GID != 0 || entry.Mode.Perm()&0022 != 0 ||
		entry.Mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || entry.Size < 0 || entry.Size > 64<<10 {
		return entry, errors.New("foreign root file; preserve it")
	}
	if o.run == nil {
		return entry, os.ErrPermission
	}
	data, rErr := o.run(ctx, nil, "/usr/bin/cat", "--", path)
	if rErr != nil {
		return entry, rErr
	}
	if len(data) > 64<<10 || int64(len(data)) != entry.Size {
		return entry, errors.New("bounded SSH configuration required")
	}
	entry.Data = data
	return entry, nil
}

func osRelease(o hostOps) (hostFile, error) {
	f, err := o.read("/etc/os-release")
	if err != nil {
		return f, err
	}
	if f.Mode&os.ModeSymlink == 0 {
		return rootFile(o, "/etc/os-release")
	}
	if err = parents(o, "/etc"); err != nil {
		return f, err
	}
	if f.UID != 0 || f.GID != 0 || (f.Link != "/usr/lib/os-release" && f.Link != "../usr/lib/os-release") {
		return f, errors.New("untrusted OS-release alias; preserving it")
	}
	return rootFile(o, "/usr/lib/os-release")
}

func known(f hostFile, b []byte, mode os.FileMode) bool {
	return f.Regular && f.UID == 0 && f.GID == 0 && f.Mode.Perm() == mode && bytes.Equal(f.Data, b)
}

func checkDirectory(o hostOps, path string, uid, gid int, mode os.FileMode) error {
	f, err := o.read(path)
	if err != nil {
		return err
	}
	if !f.Directory || f.UID != uid || f.GID != gid || f.Mode.Perm() != mode || f.Mode&os.ModeSymlink != 0 {
		return errors.New("foreign managed directory; ownership is never repaired")
	}
	return nil
}

func directory(ctx context.Context, o hostOps, path string, uid, gid int, mode os.FileMode) error {
	if _, err := o.read(path); err == nil {
		return checkDirectory(o, path, uid, gid, mode)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := o.run(ctx, nil, "/usr/bin/install", "-d", "-m", strconv.FormatUint(uint64(mode), 8), "-o", strconv.Itoa(uid), "-g", strconv.Itoa(gid), "--", path); err != nil {
		return err
	}
	return checkDirectory(o, path, uid, gid, mode)
}

func publish(ctx context.Context, o hostOps, target, id string, b []byte, mode os.FileMode) error {
	stage := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+"-"+id+".pending")
	f, err := readRootFileCtx(ctx, o, target)
	ready := err == nil
	if ready && !known(f, b, mode) {
		return errors.New("foreign managed file; preserving it")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	st, se := readRootFileCtx(ctx, o, stage)
	if se == nil && (!st.Regular || st.Mode.Perm() != mode || !bytes.HasPrefix(b, st.Data)) {
		return errors.New("foreign staging file; preserving it")
	}
	if se != nil && !errors.Is(se, os.ErrNotExist) {
		return se
	}
	if !ready {
		args := []string{"/usr/bin/install", "-m", strconv.FormatUint(uint64(mode), 8), "-o", "root", "-g", "root", "--", "/dev/stdin", stage}
		if _, err = o.run(ctx, b, args...); err != nil {
			return err
		}
		st, err = readRootFileCtx(ctx, o, stage)
		if err != nil || !known(st, b, mode) {
			return errors.New("root staging verification failed")
		}
		if _, err = o.run(ctx, nil, "/usr/bin/sync", "-f", stage); err != nil {
			return err
		}
		if _, err = o.run(ctx, nil, "/usr/bin/ln", "--no-target-directory", "--", stage, target); err != nil {
			return err
		}
		if _, err = o.run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(target)); err != nil {
			return err
		}
	}
	if !ready || se == nil {
		if _, err = o.run(ctx, nil, "/usr/bin/rm", "--", stage); err != nil {
			return err
		}
		if _, err = o.run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(target)); err != nil {
			return err
		}
		f, err = readRootFileCtx(ctx, o, target)
		if err != nil || !known(f, b, mode) {
			return fmt.Errorf("published file verification failed: %w", err)
		}
	}
	return nil
}

func fields(b []byte) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			m[k] = v
		}
	}
	return m
}

func checkSymlinkTarget(o hostOps, linkPath, expectedTarget string) (bool, error) {
	f, err := o.read(linkPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	target := f.Link
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(linkPath), target)
	}
	if f.UID != 0 || f.Mode&os.ModeSymlink == 0 || filepath.Clean(target) != expectedTarget {
		return true, errors.New("foreign service enablement link; preserving it")
	}
	return true, nil
}

func enableLink(o hostOps, unitName, unitPath string) error {
	_, err := checkSymlinkTarget(o, "/etc/systemd/system/multi-user.target.wants/"+unitName, unitPath)
	return err
}

func filepathParent(p string) string {
	i := strings.LastIndexByte(p, '/')
	if i <= 0 {
		return "/"
	}
	return p[:i]
}
