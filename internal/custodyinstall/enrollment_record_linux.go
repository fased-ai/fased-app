//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
)

// EnrollmentRecord publishes only small public bindings inside a root-owned
// source fence reached through the current controller's pinned parent FD.
func (s *System) EnrollmentRecord(ctx context.Context, target, id string, data []byte) error {
	pattern := `^/proc/` + strconv.Itoa(os.Getpid()) + `/fd/[0-9]+/[^/]+/(enrollment|ready)\.json$`
	if !regexp.MustCompile(pattern).MatchString(target) || filepath.Clean(target) != target || !validHex(id, 16) || len(data) == 0 || len(data) > 16384 {
		return errors.New("fixed pinned enrollment record required")
	}
	read := func(path string) (hostFile, error) {
		var out hostFile
		i, err := os.Lstat(filepath.Dir(path))
		if err != nil {
			return out, err
		}
		st, ok := i.Sys().(*syscall.Stat_t)
		if !ok || !i.IsDir() || i.Mode().Perm() != 0755 || st.Uid != 0 {
			return out, errors.New("root-owned source fence required")
		}
		i, err = os.Lstat(path)
		if err != nil {
			return out, err
		}
		st, ok = i.Sys().(*syscall.Stat_t)
		if !ok || !i.Mode().IsRegular() || st.Uid != 0 || i.Mode().Perm() != 0644 || i.Size() > 16384 {
			return out, errors.New("foreign enrollment record; preserving it")
		}
		out = hostFile{UID: 0, Mode: 0644, Regular: true}
		out.Data, err = os.ReadFile(path)
		return out, err
	}
	return publish(ctx, target, id, data, 0644, read, s.Run)
}
