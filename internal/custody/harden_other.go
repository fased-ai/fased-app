//go:build !linux

package custody

// HardenProcessMemory is a no-op on non-Linux platforms.
func HardenProcessMemory() error {
	return nil
}
