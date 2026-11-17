//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestWalletShortcutUsesVerifiedInstanceAndPreservesForeignEntries(t *testing.T) {
	owner := os.Getuid()
	if owner <= 0 {
		t.Skip("ordinary owner required")
	}
	root := t.TempDir()
	image := "/var/lib/fased-app-package/" + strconv.Itoa(owner) + "/local/" + strings.Repeat("a", 32) + "/fased-app"
	entry, err := packageWalletEntry(owner, image)
	if err != nil || !bytes.Contains(entry, []byte("Exec="+image+" open %u")) {
		t.Fatal("the instance entry was not bound", err)
	}
	if _, err = packageWalletEntry(owner, "/tmp/untrusted"); err == nil {
		t.Fatal("an unprotected launcher was accepted")
	}
	if err = ensurePackageWalletEntry(root, owner, entry); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "applications", packageWalletDesktop)
	if err = os.WriteFile(path, []byte("foreign"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = ensurePackageWalletEntry(root, owner, entry); err == nil {
		t.Fatal("a foreign entry was replaced")
	}
	if got, _ := os.ReadFile(path); string(got) != "foreign" {
		t.Fatal("the original entry changed")
	}
}

func TestWalletShortcutMigratesOnlyKnownFasedEntries(t *testing.T) {
	owner := os.Getuid()
	if owner <= 0 {
		t.Skip("ordinary owner required")
	}
	root := t.TempDir()
	if err := ensurePackageWalletEntry(root, owner, packageWalletDesktopEntry); err != nil {
		t.Fatal(err)
	}
	base := "/var/lib/fased-app-package/" + strconv.Itoa(owner) + "/local/"
	for _, id := range []string{strings.Repeat("a", 32), strings.Repeat("b", 32)} {
		entry, err := packageWalletEntry(owner, base+id+"/fased-app")
		if err != nil {
			t.Fatal(err)
		}
		if err = ensurePackageWalletEntry(root, owner, entry); err != nil {
			t.Fatal("known entry was not migrated", err)
		}
		if got, err := os.ReadFile(filepath.Join(root, "applications", packageWalletDesktop)); err != nil || !bytes.Equal(got, entry) {
			t.Fatal("the selected instance was not recorded", err)
		}
	}
}

func TestPackageBrowserRegistersWalletAfterOwnerIdentity(t *testing.T) {
	var diagnostics bytes.Buffer
	ready, registered := false, false
	ownerLink := func(context.Context) (string, error) {
		ready = true // The owner channel establishes the protected commitment.
		return "http://127.0.0.1/", nil
	}
	register := func(context.Context) error {
		registered = true
		if !ready {
			return errors.New("owner commitment is not yet present")
		}
		return nil
	}
	address, err := preparePackageBrowser(context.Background(), &diagnostics, ownerLink, register)
	if err != nil || address == "" || !registered || diagnostics.Len() != 0 {
		t.Fatal("Wallets was not registered after the owner identity", err, diagnostics.String())
	}
	registered = false
	_, err = preparePackageBrowser(context.Background(), &diagnostics, func(context.Context) (string, error) {
		return "", errors.New("owner unavailable")
	}, register)
	if err == nil || registered {
		t.Fatal("Wallets registration ran after owner connection failed")
	}
}

func TestPackageWalletShortcutDoesNotReplaceForeignHandler(t *testing.T) {
	if os.Getuid() <= 0 || os.Getuid() != os.Geteuid() {
		t.Skip("ordinary owner required")
	}
	dir := t.TempDir()
	tool := filepath.Join(dir, "xdg-mime")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf 'other-wallet.desktop\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	if err := registerPackageWalletShortcut(context.Background(), "/tmp/untrusted"); err == nil || !strings.Contains(err.Error(), "another application") {
		t.Fatal("a foreign protocol handler was accepted")
	}
	if _, err := os.Lstat(filepath.Join(dir, "data", "applications", packageWalletDesktop)); !os.IsNotExist(err) {
		t.Fatal("the foreign handler caused an entry to be written", err)
	}
}

func TestPackageWalletDesktopEntryPreservesExistingFiles(t *testing.T) {
	root := t.TempDir()
	if err := ensurePackageWalletEntry(root, os.Getuid(), packageWalletDesktopEntry); err != nil {
		t.Fatal(err)
	}
	if err := ensurePackageWalletEntry(root, os.Getuid(), packageWalletDesktopEntry); err != nil {
		t.Fatal("matching entry was not reusable", err)
	}
	path := filepath.Join(root, "applications", packageWalletDesktop)
	if err := os.WriteFile(path, []byte("foreign"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ensurePackageWalletEntry(root, os.Getuid(), packageWalletDesktopEntry); err == nil {
		t.Fatal("foreign entry was accepted")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "foreign" {
		t.Fatal("foreign entry changed", err)
	}
}
