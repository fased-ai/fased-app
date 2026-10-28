package main

import (
	"bufio"
	"bytes"
	"github.com/fased-ai/fased-app/internal/privateaccess"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestManagedRemoteRequiresBundleBeforeHostAccess(t *testing.T) {
	err := hostCommand([]string{"install", "--managed-remote", "--autostart", "--prepare-host", "--access", "ssh"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "verified headless bundle") {
		t.Fatalf("managed Remote reached the wrong admission path: %v", err)
	}
}

func TestManagedRemoteOwnerDirectoryHandoff(t *testing.T) {
	uid, err := managedRemoteOwner(vpsinstall.Root(1000))
	if err != nil || uid != 1000 {
		t.Fatal(uid, err)
	}
	for _, root := range []string{"", "/var/lib/fased-app-vps-0", "/var/lib/fased-app-vps--1", "/var/lib/fased-app-vps-01", "/other/fased-app-vps-1000", "/var/lib/fased-app-vps-1000/extra", "/var/lib/fased-app-vps-4294967295"} {
		if _, err = managedRemoteOwner(root); err == nil {
			t.Fatal("unbound managed owner accepted", root)
		}
	}
}

func TestManagedRemoteNetworkConsentAndHandoff(t *testing.T) {
	for _, input := range []string{"", "n\n", "yes\nn\n", "yes\n"} {
		if _, err := networkConsent(strings.NewReader(input), io.Discard); err == nil {
			t.Fatal("missing/cancelled network approval accepted")
		}
	}
	approval, err := networkConsent(strings.NewReader("yes\ny\n"), io.Discard)
	if err != nil || !approval.PrivateUI || !approval.ConsoleRecovery {
		t.Fatal(approval, err)
	}
	var out bytes.Buffer
	networkHandoff(&out, vpsinstall.Manifest{Core: "/immutable/fased-app"}, vpsinstall.NetworkStatus{Phase: "pending", AttemptID: "attempt", Deadline: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)})
	if !strings.Contains(out.String(), "second SSH") || !strings.Contains(out.String(), "'vps' 'confirm' 'attempt'") || !strings.Contains(out.String(), "2026-10-28T12:00:00Z") {
		t.Fatal("missing second-session/recovery instructions", out.String())
	}
	for _, args := range [][]string{{"confirm"}, {"confirm", "one", "two"}, {"network", "extra"}, {"harden", "--yes"}} {
		if vpsCommand(args, nil, io.Discard, io.Discard) == nil {
			t.Fatal("invalid network command accepted", args)
		}
	}
}

func TestManagedRemoteAdmissionAndOwnerBoundary(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux Remote admission")
	}
	for _, c := range []struct {
		bundle                    string
		desktop, autostart, paths bool
	}{
		{"", false, true, false}, {"/bundle", true, true, false}, {"/bundle", false, false, false}, {"/bundle", false, true, true},
	} {
		if validateManagedRemote(c.bundle, c.desktop, c.autostart, c.paths) == nil {
			t.Fatal("unsafe managed input accepted", c)
		}
	}
	if err := validateManagedRemote("/bundle", false, true, false); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][2]int{{0, 0}, {1000, 0}, {1000, 1001}, {-1, -1}} {
		if validateRuntimeUID(ids[0], ids[1]) == nil {
			t.Fatal("privileged or switched identity accepted", ids)
		}
	}
	if validateRuntimeUID(1000, 1000) != nil {
		t.Fatal("ordinary owner rejected")
	}
	for _, args := range [][]string{nil, {"run"}, {"status", "--data-dir", "/foreign"}, {"claim-code", "foreign"}} {
		if vpsCommand(args, nil, io.Discard, io.Discard) == nil {
			t.Fatal("unsafe vps command accepted", args)
		}
	}
}

func TestManagedRemoteWizardAndHandoff(t *testing.T) {
	for _, c := range []struct {
		input string
		want  bool
	}{{"\n", true}, {"bad\nyes\n", true}, {"no\n", false}} {
		got, err := installationManagedRemote(bufio.NewReader(strings.NewReader(c.input)), io.Discard)
		if err != nil || got != c.want {
			t.Fatal(c, got, err)
		}
	}
	if _, err := installationManagedRemote(bufio.NewReader(strings.NewReader("")), io.Discard); err == nil {
		t.Fatal("cancelled wizard accepted")
	}
	m := vpsinstall.Manifest{Core: "/managed/Fased app/fased-app", Data: "/managed/private data"}
	for _, started := range []bool{false, true} {
		var out bytes.Buffer
		remoteHandoff(&out, m, privateaccess.Plan{Mode: "ssh", Instructions: []string{"PRIVATE-GUIDE"}}, started)
		text := out.String()
		if !strings.Contains(text, "vps") || !strings.Contains(text, "status") || strings.Contains(text, "custody") || strings.Contains(text, "wallet") {
			t.Fatal(text)
		}
		if started != strings.Contains(text, "claim-code") || started != strings.Contains(text, "PRIVATE-GUIDE") {
			t.Fatal("unready UI was offered or ready handoff absent", text)
		}
	}
	if remotePublicOrigin(privateaccess.Plan{Mode: "ssh", Origin: "http://127.0.0.1:7331"}) != "" {
		t.Fatal("SSH exposed as public origin")
	}
	if remotePublicOrigin(privateaccess.Plan{Mode: "tailscale", Origin: "https://fixture.ts.net"}) != "https://fixture.ts.net" {
		t.Fatal("private HTTPS origin lost")
	}
}
