//go:build !unix

package custody

import (
	"errors"
	"io"
	"os"
)

func readPrivateRegularFile(cleanAbsPath string, maxBytes int64) ([]byte, error) {
	info, err := os.Lstat(cleanAbsPath)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("keypair file must be a regular non-symlink file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("keypair file permissions are too broad; run chmod 600 first")
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return nil, errors.New("file exceeds maximum allowed size")
	}
	f, err := os.Open(cleanAbsPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxBytes))
}
