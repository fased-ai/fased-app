//go:build linux

package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// CheckLegacyEnrollmentFence fails closed if data is unreadable, symlinked,
// root-owned, or contains a legacy enrollment.json fence. It returns
// os.ErrNotExist when no legacy fence blocks ordinary access.
func CheckLegacyEnrollmentFence(data string, ownerUID int) error {
	if ownerUID <= 0 || !filepath.IsAbs(data) || filepath.Clean(data) != data {
		return errors.New("invalid enrollment fence path")
	}
	real, err := filepath.EvalSymlinks(data)
	if err != nil {
		return err
	}
	if real != data {
		return errors.New("symlinked enrollment fence path refused")
	}
	info, err := os.Lstat(data)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() {
		return errors.New("invalid enrollment fence directory")
	}
	if st.Uid == 0 {
		return errors.New("legacy root-owned enrollment fence or foreign directory; preserve state")
	}
	if _, statErr := os.Lstat(filepath.Join(data, "enrollment.json")); !errors.Is(statErr, os.ErrNotExist) {
		return errors.New("legacy enrollment fence present; preserve state")
	}
	return os.ErrNotExist
}
