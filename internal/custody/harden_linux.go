//go:build linux

package custody

import "golang.org/x/sys/unix"

// HardenProcessMemory disables core dumps and ptrace attachment by non-root same-UID processes
// on Linux (PR_SET_DUMPABLE=0 + RLIMIT_CORE=0) to protect in-memory signer keys.
func HardenProcessMemory() error {
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return err
	}
	return unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{Cur: 0, Max: 0})
}
