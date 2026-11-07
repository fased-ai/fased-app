//go:build !windows

package ownerclient

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/fased-ai/fased-app/internal/core"
)

func protectRoot(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || os.Geteuid() <= 0 {
		return errors.New("private OS-owner storage required")
	}
	for p := path; ; p = filepath.Dir(p) {
		i, err := os.Lstat(p)
		if err == nil {
			st, ok := i.Sys().(*syscall.Stat_t)
			stickyRoot := ok && st.Uid == 0 && i.Mode()&os.ModeSticky != 0
			if !ok || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || (int(st.Uid) != os.Geteuid() && st.Uid != 0) || (i.Mode().Perm()&0022 != 0 && !stickyRoot) {
				return errors.New("unsafe owner storage ancestor")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if p == "/" {
			break
		}
	}
	return core.EnsurePrivateDirectory(path)
}
