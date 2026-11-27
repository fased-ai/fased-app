//go:build darwin

package custody

import "golang.org/x/sys/unix"

// HardenProcessMemory disables core dumps and debugger attachment on macOS.
func HardenProcessMemory() error {
	var zero unix.Rlimit
	_ = unix.Setrlimit(unix.RLIMIT_CORE, &zero)
	// PT_DENY_ATTACH (31) prevents ptrace debugger attachment on macOS.
	_, _, _ = unix.Syscall6(unix.SYS_PTRACE, 31, 0, 0, 0, 0, 0)
	return nil
}
