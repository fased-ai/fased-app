//go:build linux

package maintenance

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
)

func ProtectedDigest(path string, mode os.FileMode, max int64) (string, int64, error) {
	f, err := openProtected(path, max, mode)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.CopyBuffer(h, io.LimitReader(f, max+1), make([]byte, 32<<10))
	if err != nil || n > max {
		return "", n, errors.New("bounded protected content required")
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// ReplaceProtected atomically replaces only a previously classified root file.
// This in-process primitive is never exposed as a caller-selected path request.
func ReplaceProtected(path string, data []byte, mode os.FileMode, previous string) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		return errors.New("protected replacement required")
	}
	h := sha256.Sum256(data)
	wanted := hex.EncodeToString(h[:])
	actual, _, err := ProtectedDigest(path, mode, 128<<20)
	if errors.Is(err, os.ErrNotExist) && previous == "" {
		return PublishProtected(path, data, mode)
	}
	if err != nil {
		return err
	}
	if actual == wanted {
		return syncDirectory(filepath.Dir(path))
	}
	if actual != previous {
		return errors.New("protected replacement target changed; preserving it")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".fased-maintenance-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
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
	current, _, err := ProtectedDigest(path, mode, 128<<20)
	if err != nil || current != previous {
		return errors.New("protected target changed before replacement")
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func publishCopiedExclusive(temporary, target string) error {
	if err := unix.Renameat2(unix.AT_FDCWD, temporary, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE); err != nil {
		return errors.New("protected copy publication conflicted; preserving it")
	}
	return syncDirectory(filepath.Dir(target))
}

// RemoveProtected retires only a fixed helper-owned fence after the durable
// completed journal exists. It is not an IPC-selected deletion operation.
func RemoveProtected(path, hash string, mode os.FileMode) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		return errors.New("protected cleanup required")
	}
	actual, _, err := ProtectedDigest(path, mode, 8192)
	if errors.Is(err, os.ErrNotExist) {
		return syncDirectory(filepath.Dir(path))
	}
	if err != nil {
		return err
	}
	if actual != hash {
		return errors.New("foreign maintenance fence; preserve it")
	}
	if err = os.Remove(path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func CopyProtected(source, target string, mode os.FileMode, hash string, size int64, previous string) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 || size <= 0 || size > 128<<20 {
		return errors.New("bounded protected copy required")
	}
	in, err := openProtected(source, size, mode)
	if err != nil {
		return err
	}
	defer in.Close()
	actual, _, err := ProtectedDigest(target, mode, 128<<20)
	if err == nil && actual == hash {
		return syncDirectory(filepath.Dir(target))
	}
	if err == nil && actual != previous {
		return errors.New("copy target is unclassified; preserving it")
	}
	if err != nil && (!errors.Is(err, os.ErrNotExist) || previous != "") {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".fased-maintenance-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(f, h), io.LimitReader(in, size+1), make([]byte, 32<<10))
	if err != nil || n != size || hex.EncodeToString(h.Sum(nil)) != hash {
		f.Close()
		return errors.New("verified copy source changed")
	}
	if err = f.Chmod(mode); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if previous == "" {
		// The directory is root-owned, but another authorized setup may race.
		return publishCopiedExclusive(f.Name(), target)
	}
	actual, _, err = ProtectedDigest(target, mode, 128<<20)
	if err != nil || actual != previous {
		return errors.New("protected target changed before copy publication")
	}
	if err = os.Rename(f.Name(), target); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(target))
}
