//go:build linux

package privateaccess

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func openPreflightDirectory(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 || int(st.Uid) != os.Geteuid() {
		return nil, errors.New("private setup directory changed; preserve it")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		f.Close()
		return nil, errors.New("private setup directory changed; preserve it")
	}
	return f, nil
}

func removePreflightDirectory(dir *os.File, path string) error {
	var socket unix.Stat_t
	err := unix.Fstatat(int(dir.Fd()), "tailscale.sock", &socket, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		if socket.Mode&unix.S_IFMT != unix.S_IFSOCK {
			return errors.New("private setup socket changed; preserve it")
		}
		if err = unix.Unlinkat(int(dir.Fd()), "tailscale.sock", 0); err != nil {
			return err
		}
	} else if !errors.Is(err, unix.ENOENT) {
		return err
	}
	opened, err := dir.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !os.SameFile(opened, current) {
		return errors.New("private setup directory changed; preserve it")
	}
	return os.Remove(path)
}
