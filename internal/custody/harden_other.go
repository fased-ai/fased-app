//go:build !linux && !darwin

package custody

// HardenProcessMemory is a no-op on non-Linux/non-Darwin platforms.
func HardenProcessMemory() error {
	return nil
}
