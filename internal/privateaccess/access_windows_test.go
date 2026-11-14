//go:build windows

package privateaccess

import "testing"

// A local Windows uninstall has no managed Tailscale route to remove.
func TestRemoveWithoutReceiptOnWindows(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 2; i++ {
		if err := (Manager{}).Remove(t.Context(), root); err != nil {
			t.Fatal(err)
		}
	}
}
