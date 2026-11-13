//go:build linux

package maintenance

import (
	"bytes"
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
	"syscall"

	"golang.org/x/sys/unix"
)

// Register is the one-time root bootstrap entry. Its image must already be in
// the protected content-addressed location selected by the verified launcher.
// It publishes no operator access to OS utilities, shells or arbitrary argv.
func Register(owner int, profile string) (Authority, error) {
	var a Authority
	if os.Getuid() != 0 || os.Geteuid() != 0 || owner <= 0 || profile != "local" && profile != "remote" {
		return a, errors.New("fixed administrator maintenance registration required")
	}
	image, err := os.Executable()
	if err != nil {
		return a, err
	}
	hash := filepath.Base(filepath.Dir(image))
	decoded, err := hex.DecodeString(hash)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != hash || image != ImagePath(hash) {
		return a, errors.New("register only the protected verified installer image")
	}
	u, err := user.LookupId(strconv.Itoa(owner))
	if err != nil {
		return a, err
	}
	group, err := strconv.Atoi(u.Gid)
	if err != nil || group <= 0 || !filepath.IsAbs(u.HomeDir) || filepath.Clean(u.HomeDir) != u.HomeDir || u.HomeDir == "/" || u.HomeDir == "/root" {
		return a, errors.New("invalid ordinary installation owner")
	}
	a = Authority{Version: 1, Owner: owner, Group: group, Home: u.HomeDir, Profile: profile, ImageSHA256: hash}
	if err = VerifyImage(a); err != nil {
		return a, err
	}
	if previous, err := ReadAuthority(owner); err == nil {
		if previous.Owner != a.Owner || previous.Group != a.Group || previous.Home != a.Home || previous.Profile != a.Profile {
			return a, errors.New("existing maintenance owner differs; preserving it")
		}
		// The maintenance entry is stable across payload updates. An official
		// bootstrap may reuse it, but never overwrite another protected image.
		if err = VerifyImage(previous); err != nil {
			return a, err
		}
		a = previous
	} else if !errors.Is(err, os.ErrNotExist) {
		return a, err
	} else if err = requireFreshAuthority(owner, u.HomeDir, os.Lstat, os.ReadDir); err != nil {
		return a, err
	}
	for _, dir := range []string{StateRoot + "/owners", OwnerRoot(owner)} {
		if err = ensureDirectory(dir); err != nil {
			return a, err
		}
	}
	body, err := json.Marshal(a)
	if err != nil {
		return a, err
	}
	if err = publishFixed(OwnerRoot(owner)+"/authority.json", append(body, '\n'), 0644); err != nil {
		return a, err
	}
	policy := sudoPolicy(a)
	// Parse the exact policy bytes before publication. No loaded policy or
	// system setting is modified by this check.
	c := exec.Command("/usr/sbin/visudo", "-c", "-f", "-")
	c.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	c.Stdin = bytes.NewReader(policy)
	if err = c.Run(); err != nil {
		return a, errors.New("maintenance command policy did not validate")
	}
	if err = publishFixed("/etc/sudoers.d/fased-maintenance-"+strconv.Itoa(owner), policy, 0440); err != nil {
		return a, err
	}
	return a, nil
}

// A missing authority is not a fresh installation if any custody or
// maintenance evidence is still present. An empty interrupted bootstrap
// directory may be safely reused; all other state is preserved and refused.
func requireFreshAuthority(owner int, home string, lstat func(string) (os.FileInfo, error), readDir func(string) ([]os.DirEntry, error)) error {
	if owner <= 0 || !filepath.IsAbs(home) || filepath.Clean(home) != home || home == "/" || home == "/root" || lstat == nil || readDir == nil {
		return errors.New("ordinary installation owner required")
	}
	info, err := lstat(OwnerRoot(owner))
	if err == nil {
		if info == nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
			return errors.New("maintenance directory is foreign; preserve it")
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || st.Gid != 0 {
			return errors.New("maintenance directory is foreign; preserve it")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("maintenance state cannot be inspected; preserve it")
	}
	entries, err := readDir(OwnerRoot(owner))
	if err == nil && len(entries) != 0 {
		return errors.New("maintenance authority missing with existing state; preserve it")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("maintenance state cannot be inspected; preserve it")
	}
	u := strconv.Itoa(owner)
	for _, path := range []string{
		"/var/lib/fased-app-custody-" + u,
		"/var/lib/fased-app-vps-" + u,
		"/var/lib/fased-app-vps-maintenance-" + u,
		filepath.Join(home, ".local/share/fased-app"),
		filepath.Join(home, ".config/fased-app/core.sqlite"),
		filepath.Join(home, ".config/fased-app-custody-installer"),
		filepath.Join(home, ".config/fased-app-vps-installer"),
		filepath.Join(home, ".config/systemd/user/fased-app.service"),
		"/etc/sudoers.d/fased-maintenance-" + u,
		"/etc/sudoers.d/fased-vps-" + u + "-updates",
		"/etc/systemd/system/fased-vps-" + u + ".service",
		"/etc/systemd/system/fased-vps-maintenance-" + u + "@.service",
		"/etc/polkit-1/rules.d/60-fased-vps-" + u + "-updates.rules",
		"/etc/systemd/system/fased-custody-" + u + "-app.service",
		"/etc/systemd/system/fased-custody-" + u + "-app.service.d",
		"/etc/systemd/system/fased-custody-" + u + "-signer.service",
		"/etc/systemd/system/fased-custody-" + u + "-signer.service.d",
		"/etc/systemd/system/fased-maintenance-" + u + "-recover.service",
		"/etc/systemd/system/fased-maintenance-" + u + "@.service",
		"/etc/systemd/system/multi-user.target.wants/fased-maintenance-" + u + "-recover.service",
	} {
		_, err = lstat(path)
		if err == nil || !errors.Is(err, os.ErrNotExist) {
			return errors.New("maintenance authority missing with existing installation; preserve it")
		}
	}
	return nil
}

// PublishProtected is an in-process installer primitive, not an IPC operation.
// Callers are the reviewed helper's semantic handlers and choose fixed paths.
func PublishProtected(path string, data []byte, mode os.FileMode) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		return errors.New("protected maintenance publication required")
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(data) > 128<<20 {
		return errors.New("invalid protected publication")
	}
	return publishFixed(path, data, mode)
}

func EnsureProtectedDirectory(path string) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("protected maintenance directory required")
	}
	return ensureDirectory(path)
}

func sudoPolicy(a Authority) []byte {
	return []byte(fmt.Sprintf("# Fased fixed maintenance entry; no general OS-tool authority.\n#%d ALL=(root) NOPASSWD: NOSETENV: %s maintenance-request --owner %d\n", a.Owner, ImagePath(a.ImageSHA256), a.Owner))
}

func ensureDirectory(path string) error {
	created := false
	if err := os.Mkdir(path, 0755); err == nil {
		created = true
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	i, err := os.Lstat(path)
	if err != nil {
		return err
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || st.Gid != 0 || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm()&0022 != 0 {
		return errors.New("foreign maintenance directory; preserving it")
	}
	if !created && i.Mode().Perm() != 0755 {
		return errors.New("maintenance directory mode differs; preserving it")
	}
	// The bootstrap umask can remove read/search bits from our newly created
	// directory. Fixed root-owned metadata is public, so publish the fixed mode.
	if created {
		if err := os.Chmod(path, 0755); err != nil {
			return err
		}
	}
	if err := syncDirectory(path); err != nil {
		return err
	}
	// Resuming after mkdir is indistinguishable from creation. Persist both
	// the directory and its parent before publishing authority inside it.
	return syncDirectory(filepath.Dir(path))
}

func publishFixed(path string, data []byte, mode os.FileMode) error {
	if old, err := openProtected(path, int64(len(data)), mode); err == nil {
		defer old.Close()
		b, err := io.ReadAll(io.LimitReader(old, int64(len(data))+1))
		if err == nil && bytes.Equal(b, data) {
			// A previous process may have died after rename but before fsync.
			return syncDirectory(filepath.Dir(path))
		}
		return errors.New("existing maintenance input differs; preserving it")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".fased-maintenance-")
	if err != nil {
		return err
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	if _, err = f.Write(data); err == nil {
		err = f.Chmod(mode)
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err = unix.Renameat2(unix.AT_FDCWD, temporary, unix.AT_FDCWD, path, unix.RENAME_NOREPLACE); err != nil {
		return errors.New("maintenance publication conflicted; preserving it")
	}
	return syncDirectory(dir)
}

func syncDirectory(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
