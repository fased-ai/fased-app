//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

type trustedLinuxTerminal struct {
	path     string
	base     string
	execArgs []string
}

func resolveTrustedLinuxTerminal(candidates []string) (trustedLinuxTerminal, error) {
	if len(candidates) == 0 {
		candidates = []string{
			"/usr/bin/x-terminal-emulator",
			"/usr/bin/gnome-terminal",
			"/usr/bin/konsole",
			"/usr/bin/xfce4-terminal",
			"/usr/bin/xterm",
			"/usr/bin/alacritty",
			"/usr/bin/kitty",
			"/usr/bin/foot",
		}
	}
	var lastErr error
	for _, candidate := range candidates {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			lastErr = err
			continue
		}
		base := filepath.Base(resolved)
		if base == "gnome-terminal.wrapper" {
			resolved, base = "/usr/bin/gnome-terminal", "gnome-terminal"
		}
		info, err := os.Stat(resolved)
		if err != nil {
			lastErr = err
			continue
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
			lastErr = errors.New("system terminal must be a trusted root-owned OS executable")
			continue
		}
		args, ok := linuxTerminalExecArgs(base)
		if !ok {
			lastErr = errors.New("unsupported system terminal: " + base)
			continue
		}
		return trustedLinuxTerminal{path: resolved, base: base, execArgs: args}, nil
	}
	if lastErr != nil {
		return trustedLinuxTerminal{}, lastErr
	}
	return trustedLinuxTerminal{}, errors.New("system terminal unavailable")
}

func linuxTerminalExecArgs(base string) ([]string, bool) {
	switch base {
	case "xterm", "uxterm", "lxterm", "alacritty":
		return []string{"-e"}, true
	case "xfce4-terminal":
		return []string{"--disable-server", "-x"}, true
	case "gnome-terminal":
		return []string{"--wait", "--"}, true
	case "konsole":
		return []string{"--nofork", "-e"}, true
	case "kitty", "foot":
		return []string{"--"}, true
	default:
		return nil, false
	}
}
