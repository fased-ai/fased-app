//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

const packageWalletDesktop = "org.fased.package-wallet.desktop"
const packageWalletScheme = "x-scheme-handler/fased-wallet"

var packageWalletDesktopEntry = walletDesktopBytes("/usr/lib/fased-app/fased-app")

func walletDesktopBytes(image string) []byte {
	return []byte("[Desktop Entry]\nType=Application\nName=Fased Wallets\nExec=" + image + " open %u\nTerminal=false\nNoDisplay=true\nMimeType=" + packageWalletScheme + ";\n")
}

func packageWalletEntry(owner int, image string) ([]byte, error) {
	prefix := "/var/lib/fased-app-package/" + strconv.Itoa(owner) + "/local/"
	parts := strings.Split(strings.TrimPrefix(image, prefix), "/")
	if owner <= 0 || !strings.HasPrefix(image, prefix) || len(parts) != 2 || parts[1] != "fased-app" {
		return nil, errors.New("the original Local package executable is required")
	}
	root, err := update.PackageInstanceRoot(owner, "local", parts[0])
	if err != nil || image != filepath.Join(root, "fased-app") {
		return nil, errors.New("the original Local package executable is required")
	}
	return walletDesktopBytes(image), nil
}

func knownPackageWalletEntry(owner int, b []byte) bool {
	if bytes.Equal(b, packageWalletDesktopEntry) {
		return true
	}
	lines := strings.Split(string(b), "\n")
	if len(lines) != 8 || !strings.HasPrefix(lines[3], "Exec=") || !strings.HasSuffix(lines[3], " open %u") {
		return false
	}
	image := strings.TrimSuffix(strings.TrimPrefix(lines[3], "Exec="), " open %u")
	want, err := packageWalletEntry(owner, image)
	return err == nil && bytes.Equal(b, want)
}

// Only an exact known Fased entry may be atomically replaced. Unknown entries
// and handlers remain untouched; this runs as the ordinary owner.
func ensurePackageWalletEntry(dataHome string, owner int, entry []byte) error {
	if !filepath.IsAbs(dataHome) || filepath.Clean(dataHome) != dataHome || !knownPackageWalletEntry(owner, entry) {
		return errors.New("an absolute desktop data directory is required")
	}
	dir := filepath.Join(dataHome, "applications")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil || !ownerControlledEntry(dirInfo, owner, true) {
		return errors.New("the Wallets browser entry directory is not owner controlled")
	}
	path := filepath.Join(dir, packageWalletDesktop)
	info, err := os.Lstat(path)
	var previous []byte
	if err == nil {
		if !ownerControlledEntry(info, owner, false) {
			return errors.New("existing Wallets browser entry is preserved")
		}
		previous, err = os.ReadFile(path)
		if err != nil || !knownPackageWalletEntry(owner, previous) {
			return errors.New("existing Wallets browser entry is preserved")
		}
		if bytes.Equal(previous, entry) {
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".fased-wallet-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err = tmp.Chmod(0644); err == nil {
		var n int
		n, err = tmp.Write(entry)
		if err == nil && n != len(entry) {
			err = io.ErrShortWrite
		}
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if info == nil {
		if err = os.Link(tmp.Name(), path); err != nil {
			return err
		}
	} else {
		current, e := os.Lstat(path)
		if e != nil || !os.SameFile(info, current) {
			return errors.New("existing Wallets browser entry changed")
		}
		currentBytes, e := os.ReadFile(path)
		if e != nil || !bytes.Equal(currentBytes, previous) {
			return errors.New("existing Wallets browser entry changed")
		}
		if err = os.Rename(tmp.Name(), path); err != nil {
			return err
		}
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func ownerControlledEntry(info os.FileInfo, owner int, directory bool) bool {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == owner
}

func registerPackageWalletShortcut(ctx context.Context, image string) error {
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
	entry, err := packageWalletEntry(os.Getuid(), image)
	if err != nil || signer.VerifyExecutablePath(image) != nil {
		return errors.New("the protected Local wallet executable is required")
	}
	if err = ensurePackageWalletEntry(dataHome, os.Getuid(), entry); err != nil {
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
