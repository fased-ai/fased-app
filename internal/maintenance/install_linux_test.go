//go:build linux

package maintenance

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestMaintenancePolicyGrantsOnlyProtectedEntry(t *testing.T) {
	a := Authority{Version: 1, Owner: 1400, ImageSHA256: strings.Repeat("a", 64)}
	policy := sudoPolicy(a)
	want := "#1400 ALL=(root) NOPASSWD: NOSETENV: " + ImagePath(a.ImageSHA256) + " maintenance-request --owner 1400\n"
	lines := strings.Split(strings.TrimSpace(string(policy)), "\n")
	if len(lines) != 2 || lines[1]+"\n" != want {
		t.Fatalf("unexpected maintenance authority: %q", policy)
	}
	for _, forbidden := range []string{"*", "SETENV:", "/bin/sh", "/usr/bin/setpriv", "/usr/bin/install", "/usr/bin/systemctl", "/usr/bin/curl"} {
		if forbidden == "SETENV:" { // NOSETENV is deliberately required.
			if !strings.Contains(string(policy), "NOSETENV:") {
				t.Fatal("environment restriction missing")
			}
			continue
		}
		if strings.Contains(string(policy), forbidden) {
			t.Fatalf("unexpected root capability %q", forbidden)
		}
	}
	visudo, err := exec.LookPath("visudo")
	if err != nil {
		t.Skip("OS sudoers parser is unavailable; exact policy assertion passed")
	}
	c := exec.Command(visudo, "-c", "-f", "-")
	c.Stdin = bytes.NewReader(policy)
	if b, err := c.CombinedOutput(); err != nil {
		t.Fatalf("policy refused: %v: %s", err, b)
	}
}

func TestFreshAuthorityRefusesExistingInstallationEvidence(t *testing.T) {
	const owner = 1400
	const home = "/home/fased-test"
	var existing string
	lstat := func(path string) (os.FileInfo, error) {
		if path == existing {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}
	readDir := func(string) ([]os.DirEntry, error) { return nil, os.ErrNotExist }
	if err := requireFreshAuthority(owner, home, lstat, readDir); err != nil {
		t.Fatal("fresh installation refused", err)
	}
	for _, path := range []string{
		"/var/lib/fased-app-custody-1400",
		"/var/lib/fased-app-vps-1400",
		"/var/lib/fased-app-vps-maintenance-1400",
		home + "/.local/share/fased-app",
		home + "/.config/fased-app/core.sqlite",
		home + "/.config/fased-app-custody-installer",
		home + "/.config/fased-app-vps-installer",
		"/etc/sudoers.d/fased-maintenance-1400",
		"/etc/sudoers.d/fased-vps-1400-updates",
		"/etc/systemd/system/fased-maintenance-1400-recover.service",
	} {
		existing = path
		if err := requireFreshAuthority(owner, home, lstat, readDir); err == nil {
			t.Fatalf("existing evidence was accepted as fresh: %s", path)
		}
	}
	existing = ""
	if err := requireFreshAuthority(owner, home, lstat, func(string) ([]os.DirEntry, error) { return []os.DirEntry{nil}, nil }); err == nil {
		t.Fatal("nonempty maintenance directory was accepted as fresh")
	}
	if err := requireFreshAuthority(owner, home, func(string) (os.FileInfo, error) { return nil, errors.New("inspection denied") }, readDir); err == nil {
		t.Fatal("uninspectable state was accepted as fresh")
	}
}

func TestMaintenanceRegisterRefusesOrdinaryProcessBeforeMutation(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("ordinary-owner refusal requires the ordinary test runner")
	}
	if _, err := Register(os.Getuid(), "local"); err == nil {
		t.Fatal("ordinary process registered root authority")
	}
	if _, err := ValidateProcess(os.Getuid()); err == nil {
		t.Fatal("ordinary process entered privileged request handler")
	}
}
