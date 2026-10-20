package localinstall

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"strings"
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
	var app, state unix.Statfs_t
	if unix.Statfs(root, &app) != nil || unix.Statfs(data, &state) != nil || app.Flags&unix.ST_RDONLY == 0 || state.Flags&unix.ST_RDONLY != 0 {
		return errors.New("installed core requires read-only application and writable state mounts; check systemd user namespace support, not a weaker unit")
	}
	return nil
}
