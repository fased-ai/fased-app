//go:build unix

package custody

import (
	"errors"
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// readPrivateRegularFile safely opens a regular, non-symlink, single-link, 0600-max
// private file without a stat-then-open TOCTOU race.
func readPrivateRegularFile(cleanAbsPath string, maxBytes int64) ([]byte, error) {
	f, err := os.OpenFile(cleanAbsPath, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("keypair file must be a regular non-symlink file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("keypair file permissions are too broad; run chmod 600 first")
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
		return nil, errors.New("keypair file must not have multiple hard links")
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return nil, errors.New("file exceeds maximum allowed size")
	}
	return io.ReadAll(io.LimitReader(f, maxBytes))
}
