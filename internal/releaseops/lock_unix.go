//go:build linux || darwin

package releaseops

import (
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

const noFollow = unix.O_NOFOLLOW

func owned(s os.FileInfo) bool {
	st, ok := s.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Geteuid())
}
func lock(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func unlock(f *os.File)     { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }
