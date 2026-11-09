//go:build linux

package custodyinstall

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const maxInstalledExecutableSize int64 = 64 * 1024 * 1024

func executableInfo(i os.FileInfo) error {
	st, ok := i.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || !i.Mode().IsRegular() || i.Mode().Perm() != 0755 || i.Size() < 0 || i.Size() > maxInstalledExecutableSize {
		return errors.New("foreign or unsafe installed executable; preserving it")
	}
	return nil
}

func sameExecutable(before, after os.FileInfo) bool {
	if executableInfo(after) != nil || before.Size() != after.Size() || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		return false
	}
	a, aok := before.Sys().(*syscall.Stat_t)
	b, bok := after.Sys().(*syscall.Stat_t)
	return aok && bok && a.Dev == b.Dev && a.Ino == b.Ino && a.Uid == b.Uid && a.Gid == b.Gid && a.Ctim == b.Ctim
}

// Keep the descriptor and path bound through verification. Do not cache an
// executable hash: every caller must still detect a replaced or changed image.
func verifyRootExecutable(path, expected string) error {
	if err := trustedParents(filepath.Dir(path)); err != nil {
		return err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if err = executableInfo(before); err != nil {
		return err
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !sameExecutable(before, opened) {
		return errors.New("installed executable changed while opening")
	}
	if err = verifyExecutableContents(f, opened.Size(), expected); err != nil {
		return err
	}
	after, err := f.Stat()
	if err != nil || !sameExecutable(opened, after) {
		return errors.New("installed executable changed while reading")
	}
	current, err := os.Lstat(path)
	if err != nil || !sameExecutable(after, current) {
		return errors.New("installed executable path changed")
	}
	return trustedParents(filepath.Dir(path))
}

func verifyExecutableContents(r io.Reader, size int64, expected string) error {
	if size < 0 || size > maxInstalledExecutableSize {
		return errors.New("oversized installed executable")
	}
	h := sha256.New()
	n, err := io.CopyBuffer(h, io.LimitReader(r, size+1), make([]byte, 32*1024))
	if err != nil {
		return err
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != expected {
		return errors.New("installed executable changed")
	}
	return nil
}
