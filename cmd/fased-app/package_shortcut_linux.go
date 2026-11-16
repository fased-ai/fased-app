//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const packageWalletDesktop = "org.fased.package-wallet.desktop"
const packageWalletScheme = "x-scheme-handler/fased-wallet"

var packageWalletDesktopEntry = []byte("[Desktop Entry]\nType=Application\nName=Fased Wallets\nExec=/usr/lib/fased-app/fased-app open %u\nTerminal=false\nNoDisplay=true\nMimeType=" + packageWalletScheme + ";\n")

// An existing entry is never replaced. This runs only as the ordinary Local
// owner when opening the browser, not from the privileged installer or core.
func ensurePackageWalletDesktopEntry(dataHome string) error {
	if !filepath.IsAbs(dataHome) || filepath.Clean(dataHome) != dataHome {
		return errors.New("an absolute desktop data directory is required")
	}
	dir := filepath.Join(dataHome, "applications")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, packageWalletDesktop)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if errors.Is(err, os.ErrExist) {
		info, e := os.Lstat(path)
		if e != nil || !info.Mode().IsRegular() {
			return errors.New("existing Wallets browser entry is preserved")
		}
		b, e := os.ReadFile(path)
		if e != nil || !bytes.Equal(b, packageWalletDesktopEntry) {
			return errors.New("existing Wallets browser entry is preserved")
		}
		return nil
	}
	if err != nil {
		return err
	}
	created, statErr := f.Stat()
	_, writeErr := f.Write(packageWalletDesktopEntry)
	closeErr := f.Close()
	if result := errors.Join(statErr, writeErr, closeErr); result != nil {
		if current, e := os.Lstat(path); e == nil && statErr == nil && os.SameFile(created, current) {
			_ = os.Remove(path)
		}
		return result
	}
	return nil
}

func registerPackageWalletShortcut(ctx context.Context) error {
	if os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
		return errors.New("ordinary Local owner required")
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	query := func() (string, error) {
		out, err := exec.CommandContext(ctx, "xdg-mime", "query", "default", packageWalletScheme).Output()
		return strings.TrimSpace(string(out)), err
	}
	current, err := query()
	if err != nil {
		return err
	}
	if current != "" && current != packageWalletDesktop {
		return errors.New("another application handles the Wallets link; its setting was preserved")
	}
	if err = ensurePackageWalletDesktopEntry(dataHome); err != nil {
		return err
	}
	if _, err = exec.LookPath("update-desktop-database"); err == nil {
		if err = exec.CommandContext(ctx, "update-desktop-database", filepath.Join(dataHome, "applications")).Run(); err != nil {
			return err
		}
	}
	if current == "" {
		if err = exec.CommandContext(ctx, "xdg-mime", "default", packageWalletDesktop, packageWalletScheme).Run(); err != nil {
			return err
		}
	}
	if current, err = query(); err != nil || current != packageWalletDesktop {
		return errors.New("Wallets browser entry could not be verified")
	}
	return nil
}
