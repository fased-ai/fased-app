package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	fasedapp "github.com/fased-ai/fased-app"
	"github.com/fased-ai/fased-app/internal/core"
)

func TestServerInitAllTransportsAndValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		args      []string
		wantUnit  string
		wantComp  string
		wantCmd   string
		compCheck string
	}{
		{
			name:     "tailscale",
			args:     []string{"server", "init", "--transport", "tailscale", "--domain", "box.owner.ts.net", "--listen", "127.0.0.1:7331", "--json"},
			wantUnit: "--remote-transport tailscale --listen 127.0.0.1:7331",
			wantCmd:  "sudo tailscale serve --bg --https=443 unix:",
		},
		{
			name:      "cloudflare",
			args:      []string{"server", "init", "--transport", "cloudflare", "--domain", "fased.example.com", "--listen", "127.0.0.1:7345", "--json"},
			wantUnit:  "--remote-transport cloudflare --listen 127.0.0.1:7345",
			wantComp:  "cloudflared.yml",
			wantCmd:   "cloudflared tunnel --config cloudflared.yml run fased",
			compCheck: "hostname: fased.example.com",
		},
		{
			name:      "domain",
			args:      []string{"server", "init", "--transport", "domain", "--domain", "app.example.org", "--listen", "127.0.0.1:7331", "--json"},
			wantUnit:  "--remote-transport domain --listen 127.0.0.1:7331",
			wantComp:  "Caddyfile",
			wantCmd:   "caddy",
			compCheck: "reverse_proxy 127.0.0.1:7331",
		},
		{
			name:     "ssh",
			args:     []string{"server", "init", "--transport", "ssh", "--listen", "127.0.0.1:7331", "--json"},
			wantUnit: "--remote-transport ssh --listen 127.0.0.1:7331",
			wantCmd:  "ssh -N -L 7331:127.0.0.1:7331",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outDir := filepath.Join(t.TempDir(), "out")
			dataDir := filepath.Join(t.TempDir(), "data")
			args := append(append([]string{}, tc.args...), "--data-dir", dataDir, "--output-dir", outDir, "--binary", "/usr/local/bin/fased-app")
			var out bytes.Buffer
			if err := run(args, nil, &out, io.Discard); err != nil {
				t.Fatalf("server init %s failed: %v", tc.name, err)
			}
			var plan serverInitPlan
			if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(plan.ServiceUnit, tc.wantUnit) || !strings.Contains(plan.ServiceUnit, "NoNewPrivileges=yes") {
				t.Fatalf("unexpected service unit:\n%s", plan.ServiceUnit)
			}
			for _, directive := range []string{
				"ProtectProc=invisible",
				"LimitCORE=0",
				"SystemCallArchitectures=native",
				"SystemCallFilter=@system-service",
			} {
				if !strings.Contains(plan.ServiceUnit, directive) {
					t.Fatalf("server init unit for %s missing %s:\n%s", tc.name, directive, plan.ServiceUnit)
				}
			}
			joined := strings.Join(plan.Commands, "\n")
			if !strings.Contains(joined, tc.wantCmd) || !strings.Contains(joined, "/usr/local/bin/fased-app pair --data-dir") {
				t.Fatalf("missing expected command in:\n%s", joined)
			}
			unitBytes, err := os.ReadFile(filepath.Join(outDir, "fased-server.service"))
			if err != nil || string(unitBytes) != plan.ServiceUnit {
				t.Fatalf("unit file not written cleanly: %v", err)
			}
			if tc.wantComp != "" {
				compBytes, err := os.ReadFile(filepath.Join(outDir, tc.wantComp))
				if err != nil || !strings.Contains(string(compBytes), tc.compCheck) {
					t.Fatalf("companion file mismatch: %v %s", err, string(compBytes))
				}
			}
		})
	}

	for _, bad := range [][]string{
		{"server", "init", "--transport", "invalid"},
		{"server", "init", "--transport", "tailscale"},
		{"server", "init", "--transport", "cloudflare"},
		{"server", "init", "--transport", "domain", "--domain", "127.0.0.1"},
		{"server", "init", "--transport", "ssh", "--domain", "fased.example.com"},
		{"server", "init", "--transport", "ssh", "--listen", "0.0.0.0:7331"},
	} {
		if err := run(bad, nil, io.Discard, io.Discard); err == nil {
			t.Fatalf("invalid server init args accepted: %v", bad)
		}
	}
}

func TestServerInstallStatusAndUninstallLifecycle(t *testing.T) {
	prevCtl := serverSystemctl
	prevTimeout := serverReadyTimeout
	t.Cleanup(func() {
		serverSystemctl = prevCtl
		serverReadyTimeout = prevTimeout
	})

	var calls [][]string
	serverSystemctl = func(_ context.Context, args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}
	serverReadyTimeout = 10 * time.Millisecond

	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "data")
	systemdDir := filepath.Join(tmp, "systemd-user")

	var installOut bytes.Buffer
	if err := run([]string{
		"server", "install",
		"--transport", "cloudflare",
		"--domain", "fased.example.com",
		"--listen", "127.0.0.1:7345",
		"--data-dir", dataDir,
		"--systemd-dir", systemdDir,
		"--binary", "/usr/local/bin/fased-app",
		"--json",
	}, nil, &installOut, io.Discard); err != nil {
		t.Fatalf("server install failed: %v", err)
	}
	var installed struct {
		Installed     bool   `json:"installed"`
		Started       bool   `json:"started"`
		Transport     string `json:"transport"`
		UnitPath      string `json:"unitPath"`
		CompanionPath string `json:"companionPath"`
	}
	if err := json.Unmarshal(installOut.Bytes(), &installed); err != nil {
		t.Fatal(err)
	}
	if !installed.Installed || !installed.Started || installed.Transport != "cloudflare" {
		t.Fatalf("unexpected install result: %+v", installed)
	}
	if len(calls) != 2 || calls[0][0] != "daemon-reload" || strings.Join(calls[1], " ") != "enable --now fased-server.service" {
		t.Fatalf("unexpected systemctl calls: %+v", calls)
	}
	unitBytes, err := os.ReadFile(installed.UnitPath)
	if err != nil || !strings.Contains(string(unitBytes), "WantedBy=default.target") || !strings.Contains(string(unitBytes), "--remote-transport cloudflare") {
		t.Fatalf("unexpected user unit: %v\n%s", err, string(unitBytes))
	}
	compBytes, err := os.ReadFile(installed.CompanionPath)
	if err != nil || !strings.Contains(string(compBytes), "hostname: fased.example.com") {
		t.Fatalf("unexpected companion file: %v\n%s", err, string(compBytes))
	}

	// Stopped server status reads server-config.json cleanly.
	var statusOut bytes.Buffer
	if err := run([]string{"server", "status", "--data-dir", dataDir, "--json"}, nil, &statusOut, io.Discard); err != nil {
		t.Fatalf("stopped server status failed: %v", err)
	}
	if !strings.Contains(statusOut.String(), `"running":false`) || !strings.Contains(statusOut.String(), `"unitInstalled":true`) {
		t.Fatalf("unexpected stopped server status: %s", statusOut.String())
	}

	// Create sentinel state file; uninstall without --purge must preserve it.
	sentinel := filepath.Join(dataDir, "core.sqlite")
	if err := os.WriteFile(sentinel, []byte("keep-me"), 0600); err != nil {
		t.Fatal(err)
	}
	calls = nil
	if err := run([]string{"server", "uninstall", "--data-dir", dataDir, "--systemd-dir", systemdDir}, nil, io.Discard, io.Discard); err != nil {
		t.Fatalf("server uninstall failed: %v", err)
	}
	if _, err := os.Stat(installed.UnitPath); !os.IsNotExist(err) {
		t.Fatalf("unit not removed: %v", err)
	}
	if _, err := os.Stat(installed.CompanionPath); !os.IsNotExist(err) {
		t.Fatalf("companion not removed: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("state sentinel was not preserved: %v", err)
	}

	// Reinstall and uninstall --purge must remove dataDir completely.
	if err := run([]string{
		"server", "install",
		"--transport", "ssh",
		"--listen", "127.0.0.1:7346",
		"--data-dir", dataDir,
		"--systemd-dir", systemdDir,
		"--binary", "/usr/local/bin/fased-app",
		"--start=false",
	}, nil, io.Discard, io.Discard); err != nil {
		t.Fatalf("ssh server install failed: %v", err)
	}
	if err := run([]string{"server", "uninstall", "--data-dir", dataDir, "--systemd-dir", systemdDir, "--purge"}, nil, io.Discard, io.Discard); err != nil {
		t.Fatalf("server uninstall --purge failed: %v", err)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("dataDir not purged: %v", err)
	}
}

func TestServerInstallLiveDaemonAutoPairs(t *testing.T) {
	prevCtl := serverSystemctl
	t.Cleanup(func() { serverSystemctl = prevCtl })
	serverSystemctl = func(_ context.Context, _ ...string) error { return nil }

	dataDir := shortFixtureDir(t, "srv-pair-")
	systemdDir := filepath.Join(t.TempDir(), "systemd-user")

	srv, err := core.Start(t.Context(), dataDir, "127.0.0.1:0", "", fasedapp.UI())
	if err != nil {
		t.Fatal(err)
	}
	if err = srv.ConfigureConnectionWithTransport("remote", "ssh"); err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	defer srv.Close(context.Background())

	var out bytes.Buffer
	if err := run([]string{
		"server", "install",
		"--transport", "ssh",
		"--listen", "127.0.0.1:7331",
		"--data-dir", dataDir,
		"--systemd-dir", systemdDir,
		"--binary", "/usr/local/bin/fased-app",
		"--json",
	}, nil, &out, io.Discard); err != nil {
		t.Fatalf("live server install failed: %v", err)
	}
	var res struct {
		PairURL    string `json:"pairUrl"`
		PairCode   string `json:"pairCode"`
		SSHCommand string `json:"sshCommand"`
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.PairCode) != 43 || !strings.Contains(res.PairURL, "#pair="+res.PairCode) || !strings.Contains(res.SSHCommand, "ssh -N -L") {
		t.Fatalf("expected live auto-pair output, got %+v", res)
	}
}

func decodeUTF16LEBOMTest(b []byte) (string, bool) {
	if len(b) < 2 || b[0] != 0xFF || b[1] != 0xFE || (len(b)-2)%2 != 0 {
		return "", false
	}
	words := make([]uint16, (len(b)-2)/2)
	for i := range words {
		words[i] = binary.LittleEndian.Uint16(b[2+i*2:])
	}
	return string(utf16.Decode(words)), true
}

func TestServerInitCrossPlatformLaunchdAndWindows(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	// Include XML special chars & spaces to verify escaping in both plist and Windows Task XML.
	dataDir := filepath.Join(tmp, "data & dir")
	binPath := filepath.Join(tmp, "Fased App", "fased-app")

	// 1. launchd plist generation
	launchOutDir := filepath.Join(tmp, "out-launchd")
	var launchBuf bytes.Buffer
	if err := run([]string{
		"server", "init",
		"--service-manager", "launchd",
		"--transport", "domain",
		"--domain", "mac.example.com",
		"--listen", "127.0.0.1:7341",
		"--data-dir", dataDir,
		"--binary", binPath,
		"--output-dir", launchOutDir,
		"--json",
	}, nil, &launchBuf, io.Discard); err != nil {
		t.Fatalf("server init launchd failed: %v", err)
	}
	var launchPlan serverInitPlan
	if err := json.Unmarshal(launchBuf.Bytes(), &launchPlan); err != nil {
		t.Fatal(err)
	}
	if launchPlan.ServiceManager != "launchd" || launchPlan.ServiceFile != "org.fased.server.plist" {
		t.Fatalf("unexpected launchd plan metadata: %+v", launchPlan)
	}
	plistBytes, err := os.ReadFile(filepath.Join(launchOutDir, "org.fased.server.plist"))
	if err != nil {
		t.Fatal(err)
	}
	plistText := string(plistBytes)
	for _, want := range []string{
		"<key>Label</key>",
		"<string>org.fased.server</string>",
		"<string>" + escapeXMLText(dataDir) + "</string>",
		"<string>--public-origin</string>",
		"<string>https://mac.example.com</string>",
		"<key>RunAtLoad</key>",
		"<key>Umask</key>",
		"<integer>63</integer>",
	} {
		if !strings.Contains(plistText, want) {
			t.Fatalf("launchd plist missing %q:\n%s", want, plistText)
		}
	}
	if !strings.Contains(strings.Join(launchPlan.Commands, "\n"), "caddy run --config Caddyfile") {
		t.Fatalf("expected non-systemd caddy command, got %+v", launchPlan.Commands)
	}

	// 2. windows Task Scheduler XML generation
	winOutDir := filepath.Join(tmp, "out-windows")
	var winBuf bytes.Buffer
	if err := run([]string{
		"server", "init",
		"--service-manager", "windows",
		"--transport", "cloudflare",
		"--domain", "win.example.com",
		"--listen", "127.0.0.1:7342",
		"--data-dir", dataDir,
		"--binary", binPath + ".exe",
		"--output-dir", winOutDir,
		"--json",
	}, nil, &winBuf, io.Discard); err != nil {
		t.Fatalf("server init windows failed: %v", err)
	}
	var winPlan serverInitPlan
	if err := json.Unmarshal(winBuf.Bytes(), &winPlan); err != nil {
		t.Fatal(err)
	}
	if winPlan.ServiceManager != "windows" || winPlan.ServiceFile != "fased-server.task.xml" {
		t.Fatalf("unexpected windows plan metadata: %+v", winPlan)
	}
	taskBytes, err := os.ReadFile(filepath.Join(winOutDir, "fased-server.task.xml"))
	if err != nil {
		t.Fatal(err)
	}
	taskText := string(taskBytes)
	for _, want := range []string{
		"<LogonTrigger>",
		"<LogonType>InteractiveToken</LogonType>",
		"<RunLevel>LeastPrivilege</RunLevel>",
		"<Command>" + escapeXMLText(binPath+".exe") + "</Command>",
		escapeXMLText(`"` + dataDir + `"`),
		"--remote-transport cloudflare",
		"https://win.example.com",
	} {
		if !strings.Contains(taskText, want) {
			t.Fatalf("windows task XML missing %q:\n%s", want, taskText)
		}
	}

	// Reject unsupported service manager.
	if err := run([]string{"server", "init", "--service-manager", "upstart"}, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("expected unsupported service manager to fail")
	}
}

func TestServerInstallAndUninstallLaunchdLifecycle(t *testing.T) {
	prevLaunch := serverLaunchctl
	prevTimeout := serverReadyTimeout
	t.Cleanup(func() {
		serverLaunchctl = prevLaunch
		serverReadyTimeout = prevTimeout
	})

	var calls [][]string
	serverLaunchctl = func(_ context.Context, args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}
	serverReadyTimeout = 10 * time.Millisecond

	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "data")
	launchAgentsDir := filepath.Join(tmp, "LaunchAgents")

	var installOut bytes.Buffer
	if err := run([]string{
		"server", "install",
		"--service-manager", "launchd",
		"--service-dir", launchAgentsDir,
		"--transport", "ssh",
		"--listen", "127.0.0.1:7347",
		"--data-dir", dataDir,
		"--binary", "/tmp/fake-fased-app",
		"--json",
	}, nil, &installOut, io.Discard); err != nil {
		t.Fatalf("launchd server install failed: %v", err)
	}

	var installed struct {
		Installed      bool   `json:"installed"`
		Started        bool   `json:"started"`
		ServiceManager string `json:"serviceManager"`
		UnitPath       string `json:"unitPath"`
	}
	if err := json.Unmarshal(installOut.Bytes(), &installed); err != nil {
		t.Fatal(err)
	}
	if !installed.Installed || !installed.Started || installed.ServiceManager != "launchd" || !strings.HasSuffix(installed.UnitPath, "org.fased.server.plist") {
		t.Fatalf("unexpected launchd install result: %+v", installed)
	}
	if len(calls) != 2 || calls[0][0] != "bootout" || calls[1][0] != "bootstrap" || calls[1][2] != installed.UnitPath {
		t.Fatalf("unexpected launchctl calls: %+v", calls)
	}
	plistBytes, err := os.ReadFile(installed.UnitPath)
	if err != nil || !strings.Contains(string(plistBytes), "org.fased.server") || !strings.Contains(string(plistBytes), "--listen") {
		t.Fatalf("unexpected plist on disk: %v\n%s", err, string(plistBytes))
	}

	// Stopped status works via persisted server-config.json.
	var statusOut bytes.Buffer
	if err := run([]string{"server", "status", "--data-dir", dataDir, "--json"}, nil, &statusOut, io.Discard); err != nil {
		t.Fatalf("launchd server status failed: %v", err)
	}
	if !strings.Contains(statusOut.String(), `"unitInstalled":true`) {
		t.Fatalf("expected unitInstalled true, got: %s", statusOut.String())
	}

	sentinel := filepath.Join(dataDir, "core.sqlite")
	if err := os.WriteFile(sentinel, []byte("keep-mac"), 0600); err != nil {
		t.Fatal(err)
	}
	calls = nil
	if err := run([]string{"server", "uninstall", "--data-dir", dataDir}, nil, io.Discard, io.Discard); err != nil {
		t.Fatalf("launchd uninstall failed: %v", err)
	}
	if len(calls) != 1 || calls[0][0] != "bootout" {
		t.Fatalf("expected bootout on uninstall, got: %+v", calls)
	}
	if _, err := os.Stat(installed.UnitPath); !os.IsNotExist(err) {
		t.Fatalf("plist was not removed: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("data sentinel was not preserved: %v", err)
	}

	// Purge removes everything.
	if err := run([]string{
		"server", "install",
		"--service-manager", "launchd",
		"--service-dir", launchAgentsDir,
		"--transport", "ssh",
		"--listen", "127.0.0.1:7347",
		"--data-dir", dataDir,
		"--binary", "/tmp/fake-fased-app",
		"--start=false",
	}, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"server", "uninstall", "--data-dir", dataDir, "--purge"}, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("dataDir was not purged: %v", err)
	}
}

func TestServerInstallAndUninstallWindowsLifecycle(t *testing.T) {
	prevSch := serverSchtasks
	prevTimeout := serverReadyTimeout
	t.Cleanup(func() {
		serverSchtasks = prevSch
		serverReadyTimeout = prevTimeout
	})

	var calls [][]string
	serverSchtasks = func(_ context.Context, args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}
	serverReadyTimeout = 10 * time.Millisecond

	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "data")
	winDir := filepath.Join(tmp, "win-service")

	var installOut bytes.Buffer
	if err := run([]string{
		"server", "install",
		"--service-manager", "windows",
		"--service-dir", winDir,
		"--transport", "cloudflare",
		"--domain", "win.example.com",
		"--listen", "127.0.0.1:7348",
		"--data-dir", dataDir,
		"--binary", "/tmp/fased-app.exe",
		"--json",
	}, nil, &installOut, io.Discard); err != nil {
		t.Fatalf("windows server install failed: %v", err)
	}

	var installed struct {
		Installed      bool   `json:"installed"`
		Started        bool   `json:"started"`
		ServiceManager string `json:"serviceManager"`
		UnitPath       string `json:"unitPath"`
		CompanionPath  string `json:"companionPath"`
	}
	if err := json.Unmarshal(installOut.Bytes(), &installed); err != nil {
		t.Fatal(err)
	}
	if !installed.Installed || !installed.Started || installed.ServiceManager != "windows" || !strings.HasSuffix(installed.UnitPath, "fased-server.task.xml") {
		t.Fatalf("unexpected windows install result: %+v", installed)
	}
	if len(calls) != 2 || calls[0][0] != "/Create" || calls[1][0] != "/Run" {
		t.Fatalf("unexpected schtasks calls: %+v", calls)
	}
	taskBytes, err := os.ReadFile(installed.UnitPath)
	if err != nil {
		t.Fatal(err)
	}
	taskText, ok := decodeUTF16LEBOMTest(taskBytes)
	if !ok {
		t.Fatalf("expected UTF-16LE BOM in Task Scheduler XML, got prefix %x", taskBytes[:min(len(taskBytes), 8)])
	}
	if !strings.Contains(taskText, `encoding="UTF-16"`) || !strings.Contains(taskText, "--remote-transport cloudflare") {
		t.Fatalf("unexpected Task XML content:\n%s", taskText)
	}

	sentinel := filepath.Join(dataDir, "core.sqlite")
	if err := os.WriteFile(sentinel, []byte("keep-win"), 0600); err != nil {
		t.Fatal(err)
	}
	calls = nil
	if err := run([]string{"server", "uninstall", "--data-dir", dataDir, "--purge"}, nil, io.Discard, io.Discard); err != nil {
		t.Fatalf("windows uninstall --purge failed: %v", err)
	}
	if len(calls) != 2 || calls[0][0] != "/End" || calls[1][0] != "/Delete" {
		t.Fatalf("unexpected schtasks uninstall calls: %+v", calls)
	}
	if _, err := os.Stat(installed.UnitPath); !os.IsNotExist(err) {
		t.Fatalf("task XML was not removed: %v", err)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("dataDir was not purged: %v", err)
	}
}

func TestServerHardeningSystemdQuotingPurgeAndSetupLaunch(t *testing.T) {
	// 1. quoteSystemdArgs & formatServerUnit hardening directives
	unit := formatServerUnit("cloudflare", quoteSystemdArgs([]string{"/tmp/Fased App/fased-app", "serve", "--data-dir", "/tmp/data 100%$"}), "/tmp/data 100%$", true, "multi-user.target")
	for _, want := range []string{
		`ExecStart="/tmp/Fased App/fased-app" serve --data-dir "/tmp/data 100%%$$"`,
		"UMask=0077",
		"PrivateDevices=yes",
		"ProtectKernelTunables=yes",
		"RestrictSUIDSGID=yes",
		"CapabilityBoundingSet=",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("hardened unit missing %q:\n%s", want, unit)
		}
	}

	// 2. server uninstall --purge removes managed binary and ~/.local/bin/fased symlink
	home := t.TempDir()
	t.Setenv("HOME", home)
	managedDir := filepath.Join(home, ".local", "share", "fased-app")
	if err := os.MkdirAll(managedDir, 0700); err != nil {
		t.Fatal(err)
	}
	managedBin := filepath.Join(managedDir, "fased-app")
	if err := os.WriteFile(managedBin, []byte("bin"), 0700); err != nil {
		t.Fatal(err)
	}
	binLinkDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binLinkDir, 0700); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(binLinkDir, "fased")
	if err := os.Symlink(managedBin, symlink); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(home, "server-data")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfgBytes, _ := json.Marshal(serverConfig{Version: 1, ServiceManager: "systemd-user", Transport: "ssh", Listen: "127.0.0.1:7349", DataDir: dataDir, BinaryPath: managedBin})
	if err := os.WriteFile(filepath.Join(dataDir, serverConfigFile), cfgBytes, 0600); err != nil {
		t.Fatal(err)
	}
	prevCtl := serverSystemctl
	t.Cleanup(func() { serverSystemctl = prevCtl })
	serverSystemctl = func(context.Context, ...string) error { return nil }
	if err := run([]string{"server", "uninstall", "--service-manager", "systemd", "--data-dir", dataDir, "--purge"}, nil, io.Discard, io.Discard); err != nil {
		t.Fatalf("purge failed: %v", err)
	}
	if _, err := os.Lstat(symlink); !os.IsNotExist(err) {
		t.Fatalf("expected symlink removed on --purge, got %v", err)
	}
	if _, err := os.Stat(managedBin); !os.IsNotExist(err) {
		t.Fatalf("expected managed binary removed on --purge, got %v", err)
	}

	// 3. needsPrivateLaunchFile recognizes #pair= tokens alongside #local= and #setup=
	for _, target := range []string{"http://127.0.0.1:7331/#local=tok", "http://127.0.0.1:7331/#setup=tok", "https://box.ts.net/#pair=tok"} {
		if !needsPrivateLaunchFile(target) {
			t.Fatalf("expected needsPrivateLaunchFile(%q) == true", target)
		}
	}
	if needsPrivateLaunchFile("https://box.ts.net/#wallets/overview") {
		t.Fatal("expected non-secret fragment false")
	}
}
