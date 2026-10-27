//go:build linux || darwin

package update

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockFixtureTrust(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX) }
func unlockFixtureTrust(f *os.File)     { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }
