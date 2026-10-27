package localinstall

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// CheckSandbox runs inside the installed engine's own namespace, before opening
// its database or listener. Unit directives alone are not enforcement evidence.
func CheckSandbox(root, data string) error {
	return checkSandbox(root, data, definitionName)
}

// The managed topology's code belongs to root. Permission checks on root's
// files can return EACCES before mount read-only checks. Use a non-policy marker
// owned/writable by the service UID so only EROFS establishes this predicate.
func CheckManagedSandbox(root, data string) error {
	path := filepath.Join(root, "app-readonly-probe")
	i, e := os.Lstat(path)
	if e != nil {
		return e
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	if !ok || !i.Mode().IsRegular() || int(st.Uid) != os.Geteuid() || i.Mode().Perm() != 0600 {
		return errors.New("managed read-only marker must be private and writable by the app UID")
	}
	return checkSandbox(root, data, "app-readonly-probe")
}

func checkSandbox(root, data, probeName string) error {
	if root == "" || data == "" {
		return errors.New("installed sandbox paths are missing")
	}
	b, e := os.ReadFile("/proc/self/status")
	if e != nil || !strings.Contains(string(b), "NoNewPrivs:\t1\n") {
		return errors.New("installed core requires enforced NoNewPrivileges")
	}
	// This write-open performs no write. Only EROFS proves the expected mount;
	// ordinary file permissions are not accepted as isolation.
	f, err := os.OpenFile(filepath.Join(root, probeName), os.O_RDWR, 0)
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
