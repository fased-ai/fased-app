//go:build !linux

package desktop

import "os"

// CheckLegacyEnrollmentFence is a Linux-only guard.
func CheckLegacyEnrollmentFence(string, int) error {
	return os.ErrNotExist
}
