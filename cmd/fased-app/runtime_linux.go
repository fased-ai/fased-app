package main

import (
	"errors"
	"io"
	"os/exec"
	"strings"

	"github.com/fased-ai/fased-app/internal/localinstall"
)

func desktopRuntime(binary string, install bool, in io.Reader, out io.Writer) error {
	check := func() error {
		if e := localinstall.CheckHostRuntime(); e != nil {
			return e
		}
		if binary == "" {
			return nil
		}
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
	if !localinstall.RequiresBubblewrap() {
		return errors.New("automatic desktop runtime prerequisites support Ubuntu 24.04 only; install this distribution's GTK3/WebKitGTK 4.1 runtime through its OS package manager")
	}
	// Only the OS package manager is privileged. Fased never runs as root and
	// receives no sudo password; sudo handles its own terminal ceremony.
	packages := []string{"install", "-y", "bubblewrap"}
	if binary != "" {
		packages = append(packages, "libgtk-3-0t64", "libwebkit2gtk-4.1-0", "xdg-utils")
	}
	cmd := exec.Command("/usr/bin/sudo", append([]string{"/usr/bin/apt-get"}, packages...)...)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = out
	if e := cmd.Run(); e != nil {
		return errors.New("OS runtime installation did not complete; inspect the package-manager message and retry after repair")
	}
	return check()
}
