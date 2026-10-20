//go:build linux || darwin

package update

import (
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

func acquisitionParentOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func openReadOnly(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func openInertArtifact(root *os.Root, name string) (*os.File, error) {
	// Keep containment and refuse final symlinks/FIFO blocking if an entry is
	// replaced after Lstat. The caller still verifies the opened file identity.
	return root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
}
