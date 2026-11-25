//go:build linux

package signer

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func (s *storage) readPredictionKeyFile(name string) ([]byte, error) {
	if s == nil {
		return nil, ErrUnavailable
	}
	return s.readName(name)
}

func (s *storage) writePredictionKeyFile(name string, data []byte) error {
	if s == nil || s.readOnly || s.failed {
		return ErrUnavailable
	}
	if name == "" || len(data) == 0 || len(data) > maxDocument {
		return errors.New("invalid sealed prediction key payload")
	}
	tmp := name + ".pending"
	_ = unix.Unlinkat(s.dir, tmp, 0)
	fd, err := unix.Openat(s.dir, tmp, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), tmp)
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = unix.Unlinkat(s.dir, tmp, 0)
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	}
	if err := unix.Renameat(s.dir, tmp, s.dir, name); err != nil {
		_ = unix.Unlinkat(s.dir, tmp, 0)
		return err
	}
	return unix.Fsync(s.dir)
}
