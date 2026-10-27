package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/privateaccess"
)

func installationBundleFixture(t *testing.T, desktop bool) (string, bundle.Manifest, string) {
	t.Helper()
	dir := t.TempDir()
	m := bundle.Manifest{Schema: 1, Platform: runtime.GOOS, Arch: runtime.GOARCH, Version: core.Version, UnsignedEngineering: true, Desktop: desktop, Files: map[string]bundle.File{}}
	names := []string{localinstall.EngineName(), "LICENSE", "NOTICE"}
	for name := range bundle.PlatformFiles() {
		names = append(names, name)
	}
	if desktop {
		names = append(names, localinstall.DesktopName())
	}
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("disposable "+name), 0700); err != nil {
			t.Fatal(err)
		}
		var err error
		m.Files[name], err = bundle.Inspect(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	return dir, m, saveInstallationBundle(t, dir, m)
}

func saveInstallationBundle(t *testing.T, dir string, m bundle.Manifest) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "bundle.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestInstallationBundleBindsCoreDesktopAndVersion(t *testing.T) {
	for _, desktop := range []bool{false, true} {
		dir, _, pin := installationBundleFixture(t, desktop)
		desktopPath := ""
		if desktop {
			desktopPath = filepath.Join(dir, localinstall.DesktopName())
		}
		m, canonical, err := verifiedInstallationBundle(dir, filepath.Join(dir, localinstall.EngineName()), desktopPath, pin)
		if err != nil || canonical != dir || m.Desktop != desktop {
			t.Fatal(m, canonical, err)
		}
		if _, _, err = verifiedInstallationBundle(dir, os.Args[0], desktopPath, pin); err == nil {
			t.Fatal("core outside verified package accepted")
		}
		other := filepath.Join(t.TempDir(), "other-desktop")
		if err = os.WriteFile(other, []byte("other executable"), 0700); err != nil {
			t.Fatal(err)
		}
		if _, _, err = verifiedInstallationBundle(dir, filepath.Join(dir, localinstall.EngineName()), other, pin); err == nil {
			t.Fatal("unverified desktop accepted")
		}
	}
	dir, m, _ := installationBundleFixture(t, false)
	m.Version = "different version"
	pin := saveInstallationBundle(t, dir, m)
	if _, _, err := verifiedInstallationBundle(dir, filepath.Join(dir, localinstall.EngineName()), "", pin); err == nil {
		t.Fatal("mismatched metadata version accepted")
	}
}

func TestInstallRejectsBundleBeforePrivateAccessAndMutation(t *testing.T) {
	dir, _, _ := installationBundleFixture(t, false)
	parent := t.TempDir()
	app, data := filepath.Join(parent, "app"), filepath.Join(parent, "data")
	err := hostCommand([]string{"install", "--bundle", dir, "--bundle-sha256", strings.Repeat("0", 64), "--vps", "--autostart", "--access", "tailscale", "--prefix", app, "--data-dir", data, "--start=false"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "independently obtained digest") {
		t.Fatal("bundle was not refused before host access:", err)
	}
	for _, path := range []string{app, data} {
		if _, err = os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("rejected bundle touched installation state:", path, err)
		}
	}
	if err = hostCommand([]string{"install", "--bundle-sha256", strings.Repeat("0", 64)}, io.Discard, io.Discard); err == nil {
		t.Fatal("detached bundle digest accepted")
	}
}

func TestInstallationAutostartMatchesLocalRemoteChoice(t *testing.T) {
	for _, tc := range []struct {
		name, input  string
		remote, want bool
	}{
		{"local default", "\n", false, false},
		{"remote default", "\n", true, true},
		{"local opt in", "YES\n", false, true},
		{"remote decline", "no\n", true, false},
		{"retry invalid choice", "sometimes\ny\n", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			got, err := installationAutostart(bufio.NewReader(strings.NewReader(tc.input)), &out, tc.remote)
			if err != nil || got != tc.want {
				t.Fatal(got, err, out.String())
			}
			prompt := "Start Fased at login? [y/N]"
			if tc.remote {
				prompt = "Keep Fased running after logout and reboot? [Y/n]"
			}
			if !strings.Contains(out.String(), prompt) {
				t.Fatal("wrong runtime profile prompt", out.String())
			}
		})
	}
	if _, err := installationAutostart(bufio.NewReader(strings.NewReader("")), io.Discard, true); err == nil {
		t.Fatal("EOF authorized background execution")
	}
}

func TestInstallationHandoffUsesInstalledPathsAndPrivateAccess(t *testing.T) {
	m := localinstall.Manifest{Root: "/home/owner/Fased app", Data: "/home/owner/private state", Location: "remote"}
	for _, mode := range []string{"local", "ssh", "tailscale"} {
		plan := privateaccess.Plan{Mode: mode, Origin: "https://fixture.ts.net", Instructions: []string{"AUTHORIZED-DEVICE-GUIDE"}}
		var out bytes.Buffer
		installationHandoff(&out, m, plan, "http://127.0.0.1:7441")
		text := out.String()
		for _, want := range []string{m.Root, m.Data, "claim-code", "service", "status", "AUTHORIZED-DEVICE-GUIDE", "Sign in"} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing %q: %s", mode, want, text)
			}
		}
		if strings.Contains(text, "Open: http://127.0.0.1") || strings.Contains(text, "Owner setup code: fased-app") {
			t.Fatalf("ambiguous or PATH-dependent handoff: %s", text)
		}
	}
}

func TestInstallationHandoffSSHUsesConfiguredPort(t *testing.T) {
	plan, err := privateaccess.System().Plan(t.Context(), "ssh", "127.0.0.1:7441")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	installationHandoff(&out, localinstall.Manifest{Root: "/fixture/app", Data: "/fixture/state"}, plan, plan.Target)
	for _, want := range []string{"ssh -N -o ExitOnForwardFailure=yes -L 127.0.0.1:7441:127.0.0.1:7441 USER@VPS", "on your computer", "host key", "claim-code"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, out.String())
		}
	}
}

func TestInstallationCommandPreservesLiteralArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell execution; Windows renders PowerShell commands")
	}
	args := []string{"/owner/Fased app", "/owner/it's private", "$(printf altered)", "data; printf altered", "--data-dir"}
	command := installationCommand(append([]string{"printf", "%s\\n"}, args...)...)
	output, err := exec.Command("/bin/sh", "-c", command).CombinedOutput()
	if err != nil || string(output) != strings.Join(args, "\n")+"\n" {
		t.Fatalf("arguments changed: %q, %v", output, err)
	}
}

func TestInstallationHandoffStoppedDoesNotOfferUnreadyUI(t *testing.T) {
	m := localinstall.Manifest{Root: "/fixture/app", Data: "/fixture/state", Location: "remote"}
	for _, mode := range []string{"local", "ssh", "tailscale"} {
		var out bytes.Buffer
		installationHandoff(&out, m, privateaccess.Plan{Mode: mode, Instructions: []string{"UNREADY-UI"}}, "")
		text := out.String()
		if !strings.Contains(text, "start") || strings.Contains(text, "UNREADY-UI") || strings.Contains(text, "claim-code") {
			t.Fatal(text)
		}
		if strings.Contains(text, "access") != (mode == "tailscale") {
			t.Fatalf("missing or unexpected private endpoint setup: %s", text)
		}
	}
}
