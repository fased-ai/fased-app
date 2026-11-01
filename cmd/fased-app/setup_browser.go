package main

import (
	"io"
	"os"
	"os/exec"
	"runtime"
)

// Use the exact already-issued setup link. Reissuing a proof while opening a
// browser would invalidate the terminal fallback. Headless setup prints it only.
func openSetupBrowser(url string, out io.Writer) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return
		}
		c = exec.Command("xdg-open", url)
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url)
	default:
		return
	}
	c.Stdout, c.Stderr = io.Discard, io.Discard
	if c.Start() == nil {
		_ = c.Process.Release()
	}
}
