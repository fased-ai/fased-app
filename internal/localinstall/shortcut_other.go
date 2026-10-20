//go:build !linux

package localinstall

// macOS application bundles and the Windows installer supply their OS shortcuts.
const shortcutName = "unused.desktop"

func shortcutDefinition(Paths) []byte { return nil }
func registerShortcut(Manifest) error { return nil }
func removeShortcut(Manifest) error   { return nil }
