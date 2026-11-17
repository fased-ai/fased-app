//go:build linux

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"golang.org/x/sys/unix"
)

func TestBrowserInstallDoesNotRequireWalletWindowRuntime(t *testing.T) {
	missingWindow := filepath.Join(t.TempDir(), "missing-wallet-window")
	if err := checkDesktopRuntime(missingWindow); err == nil {
		t.Fatal("missing native window was not detected")
	}
	// Browser setup must still enforce the operating system's core sandbox.
	want := localinstall.CheckHostRuntime()
	got := desktopRuntime(context.Background(), missingWindow, "", t.TempDir(), false, false, nil, io.Discard)
	if (got == nil) != (want == nil) || got != nil && got.Error() != want.Error() {
		t.Fatalf("browser installation depended on the wallet window: got %v, host prerequisite %v", got, want)
	}
}

// This invokes the actual install command with a sealed, matching core and an
// inert native file. It never starts a service or changes the real home.
func TestBrowserInstallEntryDoesNotRequireWalletWindowRuntime(t *testing.T) {
	if os.Getenv("FASED_TEST_BROWSER_INSTALL") == "1" {
		dir, pin := os.Getenv("FASED_TEST_BUNDLE"), os.Getenv("FASED_TEST_PIN")
		root, data := os.Getenv("FASED_TEST_ROOT"), os.Getenv("FASED_TEST_DATA")
		if err := hostCommand([]string{"install", "--bundle", dir, "--bundle-sha256", pin, "--prefix", root, "--data-dir", data, "--start=false"}, io.Discard, io.Discard); err != nil {
			t.Fatal("browser installation failed before Wallets was opened:", err)
		}
		m, err := localinstall.Load(localinstall.Paths{Root: root, Data: data})
		if err != nil || m.Files[localinstall.DesktopName()] == "" {
			t.Fatal("the verified optional Wallets file was not preserved", err)
		}
		return
	}
	if os.Getuid() == 0 || localinstall.CheckHostRuntime() != nil {
		t.Skip("an ordinary user with the core sandbox prerequisite is required")
	}
	dir, manifest, _ := installationBundleFixture(t, true)
	source, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	engine, err := os.OpenFile(filepath.Join(dir, localinstall.EngineName()), os.O_WRONLY|os.O_TRUNC, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(engine, source)
	if closeErr := engine.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	manifest.Files[localinstall.EngineName()], err = bundle.Inspect(filepath.Join(dir, localinstall.EngineName()))
	if err != nil {
		t.Fatal(err)
	}
	pin := saveInstallationBundle(t, dir, manifest)
	fd, err := unix.MemfdCreate("fased-verified-executable", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING|unix.MFD_EXEC)
	if err == unix.EINVAL {
		fd, err = unix.MemfdCreate("fased-verified-executable", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	}
	if err != nil {
		t.Fatal(err)
	}
	image := os.NewFile(uintptr(fd), "browser-install")
	defer image.Close()
	if _, err = source.Seek(0, io.SeekStart); err == nil {
		_, err = io.Copy(image, source)
	}
	if err == nil {
		err = image.Chmod(0500)
	}
	if err == nil {
		_, err = unix.FcntlInt(image.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL)
	}
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	command := exec.CommandContext(t.Context(), "/proc/self/fd/3", "-test.run=^TestBrowserInstallEntryDoesNotRequireWalletWindowRuntime$")
	command.ExtraFiles = []*os.File{image}
	command.Env = append(os.Environ(), "HOME="+home, "FASED_TEST_BROWSER_INSTALL=1", "FASED_TEST_BUNDLE="+dir, "FASED_TEST_PIN="+pin,
		"FASED_TEST_ROOT="+filepath.Join(home, "app"), "FASED_TEST_DATA="+filepath.Join(home, "data"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("actual installer entry failed: %v %s", err, strings.TrimSpace(string(output)))
	}
}
