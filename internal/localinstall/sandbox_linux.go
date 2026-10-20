package localinstall

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// CheckSandbox runs inside the installed engine's own namespace, before opening
// its database or listener. Unit directives alone are not enforcement evidence.
func CheckSandbox(root, data string) error {
	if root == "" || data == "" {
		return errors.New("installed sandbox paths are missing")
	}
	b, e := os.ReadFile("/proc/self/status")
	if e != nil || !strings.Contains(string(b), "NoNewPrivs:\t1\n") {
		return errors.New("installed core requires enforced NoNewPrivileges")
	}
	// This write-open performs no write. Only EROFS proves the expected mount;
	// ordinary file permissions are not accepted as isolation.
	f, err := os.OpenFile(filepath.Join(root, definitionName), os.O_RDWR, 0)
	if err == nil {
		f.Close()
		var st unix.Statfs_t
		_ = unix.Statfs(root, &st)
		return fmt.Errorf("installed application is writable; namespace isolation was not enforced (mount flags=%#x)", st.Flags)
	}
	if !errors.Is(err, unix.EROFS) {
		return fmt.Errorf("cannot verify read-only application mount: %w", err)
	}
	probe, err := os.CreateTemp(data, ".fased-sandbox-check-")
	if err != nil {
		return fmt.Errorf("installed state is not writable: %w", err)
	}
	name := probe.Name()
	if err = probe.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err = os.Remove(name); err != nil {
		return err
	}
	return nil
}
