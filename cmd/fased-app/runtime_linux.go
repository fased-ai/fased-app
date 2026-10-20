package main

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fased-ai/fased-app/internal/hostprep"
	"github.com/fased-ai/fased-app/internal/localinstall"
)

func checkDesktopRuntime(binary string) error {
	if binary == "" {
		return nil
	}
	b, e := exec.Command("/usr/bin/ldd", binary).CombinedOutput()
	if e != nil || strings.Contains(string(b), "not found") {
		return errors.New("desktop runtime is missing; rerun the verified package installer with --prepare-host on Ubuntu 24.04, or use the headless package")
	}
	return nil
}
func needsHostPreparation(binary string) bool {
	return localinstall.RequiresBubblewrap() && (localinstall.CheckHostRuntime() != nil || checkDesktopRuntime(binary) != nil)
}
func desktopRuntime(ctx context.Context, binary, bundleDir, data string, install, prepare bool, in io.Reader, out io.Writer) error {
	check := func() error {
		if e := localinstall.CheckHostRuntime(); e != nil {
			return e
		}
		return checkDesktopRuntime(binary)
	}
	if !install && !prepare {
		return check()
	}
	if !localinstall.RequiresBubblewrap() {
		return check()
	}
	policyPath := filepath.Join(bundleDir, "bwrap-userns-restrict")
	if prepare {
		if bundleDir == "" {
			return errors.New("automatic host preparation requires the verified --bundle package")
		}
		if _, e := hostprep.VerifyPolicy(policyPath); e != nil {
			return e
		}
		if e := hostprep.ValidateTarget(); e != nil {
			return e
		}
	}
	system := &hostprep.System{In: in, Out: out}
	if needsHostPreparation(binary) {
		// Privileged execution is restricted to OS tools, never project code.
		packages := []string{"install", "-y", "--no-remove", "bubblewrap", "apparmor"}
		if binary != "" {
			packages = append(packages, "libgtk-3-0t64", "libwebkit2gtk-4.1-0", "xdg-utils")
		}
		if _, e := system.Run(ctx, nil, "/usr/bin/apt-get", "update"); e != nil {
			return errors.New("OS package index refresh failed; repair and rerun the same install")
		}
		if _, e := system.Run(ctx, nil, append([]string{"/usr/bin/apt-get"}, packages...)...); e != nil {
			return errors.New("OS runtime installation failed; repair and rerun the same install")
		}
	}
	if prepare {
		if e := hostprep.Prepare(ctx, data, policyPath, system, localinstall.CheckHostRuntime); e != nil {
			return e
		}
	}
	return check()
}
