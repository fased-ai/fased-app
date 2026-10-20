//go:build !linux

package main

import "io"

// Native macOS uses OS frameworks. The Windows desktop build explicitly selects
// Wails' pinned embedded WebView2 bootstrapper with an owner confirmation.
func desktopRuntime(string, bool, io.Reader, io.Writer) error { return nil }
