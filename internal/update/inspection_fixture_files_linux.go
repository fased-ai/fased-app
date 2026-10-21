//go:build linux

package update

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

// uid is an explicit disposable-fixture publisher, never installed authority.
// No exported entrypoint, configuration or environment override selects it.
func inspectionFileIdentity(st os.FileInfo, uid int, dir bool, mode os.FileMode) bool {
	s, ok := st.Sys().(*syscall.Stat_t)
	return ok && int(s.Uid) == uid && st.IsDir() == dir && (dir || st.Mode().IsRegular() && s.Nlink == 1) && st.Mode()&os.ModeSymlink == 0 && st.Mode().Perm() == mode && st.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0
}
func inspectionOpen(r *os.Root, name string) (*os.File, error) {
	before, e := r.Lstat(name)
	if e != nil || !before.Mode().IsRegular() {
		return nil, errors.New("regular fixture file required")
	}
	f, e := r.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	after, e := f.Stat()
	if e != nil || !os.SameFile(before, after) {
		f.Close()
		return nil, errors.New("fixture file changed")
	}
	return f, nil
}

func inspectionPublish(r *os.Root, from, to string) error {
	d, e := r.Open(".")
	if e != nil {
		return e
	}
	defer d.Close()
	return unix.Renameat2(int(d.Fd()), from, int(d.Fd()), to, unix.RENAME_NOREPLACE)
}
