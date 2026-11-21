package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
			wantCmd:  "tailscale serve --bg --https=443 http://127.0.0.1:7331",
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
