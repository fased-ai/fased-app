package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
)

type bootstrapInventoryTransport func(*http.Request) (*http.Response, error)

func (f bootstrapInventoryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Exercise the real client authentication/inventory adapter. Only the protected
// OS/HTTP peer is simulated here; installed-package acceptance remains separate.
func TestLocalBootstrapInventoryWithoutSavedSession(t *testing.T) {
	for _, scenario := range []string{"absent", "stale", "wrong-instance", "wrong-owner", "remote", "unclaimed", "auth-refused", "wrong-probe", "wrong-response-instance"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "cli-session.json")
			if scenario == "stale" {
				if err := os.WriteFile(file, []byte("expired-session-must-be-preserved"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			instance, probe, token := "existing-instance", strings.Repeat("a", 32), "disposable-private-native-session"
			authCalls, inventoryCalls := 0, 0
			client := &core.Client{Endpoint: core.Endpoint{URL: "http://127.0.0.1:7332", InstanceID: instance}, Token: "stale-in-memory-session"}
			client.HTTP = &http.Client{Transport: bootstrapInventoryTransport(func(r *http.Request) (*http.Response, error) {
				code := 200
				var body any
				switch r.URL.Path {
				case "/api/v1/health":
					h := core.Health{InstanceID: instance, Connection: &core.ConnectionInfo{Location: "local", OwnerUID: 1400}}
					if scenario == "wrong-instance" {
						h.InstanceID = "foreign"
					}
					if scenario == "wrong-owner" {
						h.Connection.OwnerUID++
					}
					if scenario == "remote" {
						h.Connection.Location = "remote"
					}
					body = h
				case "/api/v1/auth/state":
					body = map[string]bool{"claimed": scenario != "unclaimed"}
				case "/api/v1/auth/native-local":
					authCalls++
					var command map[string]string
					if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&command) != nil || command["action"] != "session" {
						t.Fatal("wrong native authentication")
					}
					body = map[string]string{"sessionToken": token, "csrfToken": "disposable-csrf"}
					if scenario == "auth-refused" {
						code = 403
					}
				case "/api/v1/updates/inventory":
					inventoryCalls++
					if r.Method != "GET" || r.URL.Query().Get("probe") != probe || r.Header.Get("Authorization") != "Bearer "+token {
						t.Fatal("inventory lacks fresh native authorization/challenge")
					}
					s := update.CoreSnapshot{Version: 1, InstanceID: instance, ProbeID: probe, BootID: strings.Repeat("b", 32), State: update.State{Name: "core-db"}}
					if scenario == "wrong-probe" {
						s.ProbeID = strings.Repeat("c", 32)
					}
					if scenario == "wrong-response-instance" {
						s.InstanceID = "foreign"
					}
					body = s
				default:
					t.Fatal("unexpected route", r.URL.Path)
				}
				b, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				return &http.Response{StatusCode: code, Status: http.StatusText(code), Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(b)), Request: r}, nil
			})}
			var output bytes.Buffer
			args := []string{"--data-dir", dir, "--probe-id", probe, "--bootstrap-owner", "1400", "--bootstrap-id", strings.Repeat("d", 32), "--bootstrap-core-sha256", strings.Repeat("e", 64)}
			err := updateInventoryCommandWithBootstrap(args, &output, io.Discard, func(ctx context.Context, data string, owner int, id, hash, challenge string, out io.Writer) error {
				if data != dir || owner != 1400 || id != strings.Repeat("d", 32) || hash != strings.Repeat("e", 64) || challenge != probe {
					t.Fatal("typed command dispatch changed identity")
				}
				return readBootstrapLocalInventory(ctx, client, instance, owner, challenge, out)
			})
			good := scenario == "absent" || scenario == "stale"
			if (err == nil) != good {
				t.Fatalf("result: %v", err)
			}
			if good && (authCalls != 1 || inventoryCalls != 1 || output.Len() == 0) {
				t.Fatal("complete native inventory path not exercised")
			}
			if !good && output.Len() != 0 {
				t.Fatal("foreign/refused inventory escaped")
			}
			if scenario == "wrong-instance" || scenario == "wrong-owner" || scenario == "remote" || scenario == "unclaimed" {
				if authCalls != 0 || inventoryCalls != 0 {
					t.Fatal("foreign/unclaimed target received authority")
				}
			}
			if client.Token != "" || client.CSRF != "" || bytes.Contains(output.Bytes(), []byte(token)) {
				t.Fatal("native session escaped inventory adapter")
			}
			b, e := os.ReadFile(file)
			if scenario == "stale" {
				if e != nil || string(b) != "expired-session-must-be-preserved" {
					t.Fatal("saved session modified")
				}
			} else if !os.IsNotExist(e) {
				t.Fatal("saved session created")
			}
		})
	}
}

func TestBootstrapInventoryFlagsRequireCompleteIdentity(t *testing.T) {
	for _, args := range [][]string{
		{"--bootstrap-owner", "1400"}, {"--bootstrap-id", strings.Repeat("a", 32)}, {"--bootstrap-core-sha256", strings.Repeat("b", 64)},
	} {
		args = append(args, "--probe-id", strings.Repeat("c", 32))
		var out bytes.Buffer
		if err := updateInventoryCommand(args, &out, io.Discard); err == nil || !strings.Contains(err.Error(), "complete staged Local bootstrap identity") || out.Len() != 0 {
			t.Fatal("partial bootstrap mode accepted", args, err)
		}
	}
}
