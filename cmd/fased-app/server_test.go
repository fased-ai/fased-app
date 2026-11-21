package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
