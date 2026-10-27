//go:build linux

package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// The helper's output directory is owner-selected, private and not reachable by
// the app identity. No upload, overwrite or automatic recovery destination.
func ownerDirectory(path string, private ...bool) (int, error) {
	mask := uint32(0077)
	if len(private) == 1 && !private[0] {
		mask = 0022
	}
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return -1, errors.New("select an absolute owner file path")
	}
	for p := filepath.Dir(path); ; p = filepath.Dir(p) {
		var st unix.Stat_t
		if err := unix.Lstat(p, &st); err != nil {
			return -1, err
		}
		stickyRoot := st.Uid == 0 && st.Mode&unix.S_ISVTX != 0
		if st.Mode&unix.S_IFMT != unix.S_IFDIR || (int(st.Uid) != os.Geteuid() && st.Uid != 0) || (!stickyRoot && st.Mode&0022 != 0) {
			return -1, errors.New("unsafe owner output directory or ancestor")
		}
		if p == "/" {
			break
		}
	}
	fd, err := unix.Open(filepath.Dir(path), unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	var st unix.Stat_t
	err = unix.Fstat(fd, &st)
	if err != nil || int(st.Uid) != os.Geteuid() || st.Mode&mask != 0 {
		unix.Close(fd)
		return -1, errors.New("backup/export directory must be private and owner-owned")
	}
	return fd, nil
}

func preflightOwnerFile(path string) error {
	fd, err := ownerDirectory(path)
	if err != nil {
		return err
	}
	return unix.Close(fd)
}

func writeOwnerFile(path string, data []byte) error {
	dir, err := ownerDirectory(path)
	if err != nil {
		return err
	}
	defer unix.Close(dir)
	fd, err := unix.Openat(dir, filepath.Base(path), unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), filepath.Base(path))
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return unix.Fsync(dir)
}

func readOwnerFile(path string) ([]byte, error) {
	return readPrivateBoundedFile(path, 16384)
}

func readPrivateBoundedFile(path string, limit int64) ([]byte, error) {
	dir, err := ownerDirectory(path)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dir)
	fd, err := unix.Openat(dir, filepath.Base(path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), filepath.Base(path))
	defer f.Close()
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0077 != 0 || int(st.Uid) != os.Geteuid() || st.Nlink != 1 {
		return nil, errors.New("unsafe owner backup file")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("oversized backup")
	}
	return b, err
}

// Public proposals may live in an ordinary owner Downloads directory. They
// remain immutable to the app UID and are read once before digest/TTY review.
func readPublicProposal(path string) ([]byte, error) {
	dir, e := ownerDirectory(path, false)
	if e != nil {
		return nil, e
	}
	defer unix.Close(dir)
	fd, e := unix.Openat(dir, filepath.Base(path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		return nil, e
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0022 != 0 || int(st.Uid) != os.Geteuid() || st.Nlink != 1 {
		return nil, errors.New("unsafe public proposal file")
	}
	b, e := io.ReadAll(io.LimitReader(f, 16385))
	if len(b) > 16384 {
		return nil, errors.New("oversized proposal")
	}
	return b, e
}
