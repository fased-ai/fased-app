//go:build linux

package custodyinstall

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type approvedExecutableVerifier func(string, string, int64) error

// ApprovedExecutablePath is a root-managed, content-addressed source for a
// scoped installer. The ordinary owner cannot replace the source or its parent.
func ApprovedExecutablePath(owner int, data []byte) string {
	hash := sha256.Sum256(data)
	return fmt.Sprintf("/var/lib/fased-app-approved-%d/%x", owner, hash)
}

func approvedExecutable(read readHost, owner int, data []byte, verify approvedExecutableVerifier) (string, error) {
	path := ApprovedExecutablePath(owner, data)
	if verify != nil {
		if err := verify(path, filepath.Base(path), int64(len(data))); errors.Is(err, os.ErrNotExist) {
			return "", nil
		} else if err != nil {
			return "", err
		}
		return path, nil
	}
	f, err := read(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil // Ordinary administrator setup retains its existing path.
	}
	if err != nil {
		return "", err
	}
	if f.UID != 0 || !f.Regular || f.Mode != 0444 || !bytes.Equal(f.Data, data) {
		return "", errors.New("approved executable source changed; preserving it")
	}
	return path, nil
}

// The production path streams the root-managed image through a held,
// no-follow descriptor; it never holds a second whole executable in memory.
func verifyApprovedExecutable(path, expected string, size int64) error {
	if err := trustedParents(filepath.Dir(path)); err != nil {
		return err
	}
	before, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("approved executable is missing; preserving scoped setup")
		}
		return err
	}
	valid := func(info os.FileInfo) bool {
		st, ok := info.Sys().(*syscall.Stat_t)
		return ok && st.Uid == 0 && st.Gid == 0 && info.Mode().IsRegular() && info.Mode() == 0444 && info.Size() == size && size >= 0 && size <= maxInstalledExecutableSize
	}
	if !valid(before) {
		return errors.New("foreign approved executable source; preserving it")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !valid(opened) || !os.SameFile(before, opened) {
		return errors.New("approved executable changed while opening")
	}
	if err = verifyExecutableContents(f, size, expected); err != nil {
		return err
	}
	after, err := f.Stat()
	current, pathErr := os.Lstat(path)
	if err != nil || pathErr != nil || !valid(after) || !valid(current) || !os.SameFile(opened, after) || !os.SameFile(after, current) || !before.ModTime().Equal(after.ModTime()) || !after.ModTime().Equal(current.ModTime()) {
		return errors.New("approved executable changed during verification")
	}
	a, aok := before.Sys().(*syscall.Stat_t)
	b, bok := after.Sys().(*syscall.Stat_t)
	c, cok := current.Sys().(*syscall.Stat_t)
	if !aok || !bok || !cok || a.Ctim != b.Ctim || b.Ctim != c.Ctim {
		return errors.New("approved executable metadata changed")
	}
	return trustedParents(filepath.Dir(path))
}
