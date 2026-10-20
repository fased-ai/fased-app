//go:build !linux

package main

import (
	"context"
	"io"
)

// Native macOS uses OS frameworks. The Windows desktop build explicitly selects
// Wails' pinned embedded WebView2 bootstrapper with an owner confirmation.
func desktopRuntime(context.Context, string, string, string, bool, bool, io.Reader, io.Writer) error {
	return nil
}
func needsHostPreparation(string) bool { return false }
