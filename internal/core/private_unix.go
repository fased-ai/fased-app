//go:build !windows

package core

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func protectPath(path string, info os.FileInfo) error {
	if info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("private state permissions are too broad: %s", path)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Geteuid() {
		return errors.New("private state is owned by another user")
	}
	return nil
}

type InstanceLock struct{ file *os.File }

func LockInstance(path string) (*InstanceLock, error) {
	if err := privateFile(path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("an engine already owns this state directory")
	}
	return &InstanceLock{file: file}, nil
}
func (l *InstanceLock) Close() error {
	err := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	return errors.Join(err, l.file.Close())
}
