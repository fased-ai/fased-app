package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
)

func desktopRuntime(binary string, install bool, in io.Reader, out io.Writer) error {
	if binary == "" {
		return nil
	}
	check := func() error {
		b, e := exec.Command("/usr/bin/ldd", binary).CombinedOutput()
		if e != nil || strings.Contains(string(b), "not found") {
			return errors.New("desktop runtime is missing: Ubuntu 24.04 needs libgtk-3-0t64 and libwebkit2gtk-4.1-0; rerun install --install-runtime to authorize the OS package manager, or use the headless package")
		}
		return nil
	}
	if e := check(); e == nil {
		return nil
	} else if !install {
		return e
	}
	b, e := os.ReadFile("/etc/os-release")
	if e != nil || !strings.Contains(string(b), "ID=ubuntu\n") || !strings.Contains(string(b), `VERSION_ID="24.04"`) {
		return errors.New("automatic desktop runtime prerequisites support Ubuntu 24.04 only; install this distribution's GTK3/WebKitGTK 4.1 runtime through its OS package manager")
	}
	// Only the OS package manager is privileged. Fased never runs as root and
	// receives no sudo password; sudo handles its own terminal ceremony.
	cmd := exec.Command("/usr/bin/sudo", "/usr/bin/apt-get", "install", "-y", "libgtk-3-0t64", "libwebkit2gtk-4.1-0", "xdg-utils")
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = out
	if e = cmd.Run(); e != nil {
		return errors.New("OS runtime installation did not complete; inspect the package-manager message and retry after repair")
	}
	return check()
}
