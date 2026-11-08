package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
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
				return readBootstrapCustodyInventory(ctx, client, custodyinstall.Manifest{Location: "local", Owner: owner}, instance, challenge, out)
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
		if err := updateInventoryCommand(args, &out, io.Discard); err == nil || !strings.Contains(err.Error(), "complete staged custody bootstrap identity") || out.Len() != 0 {
			t.Fatal("partial bootstrap mode accepted", args, err)
		}
	}
}

// The database, session issuer, HTTP authentication and inventory handler are
// real. Only the installed OS/health profile and private transport are simulated;
// this is source evidence, not installed Remote acceptance.
func TestRemoteBootstrapInventoryWithoutSavedSession(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("protected custody inventory requires Linux")
	}
	for _, scenario := range []string{"absent", "stale", "wrong-instance", "wrong-owner", "local", "unclaimed", "foreign-vps-data", "inventory-refused", "wrong-response-probe"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "app-state")
			store, err := core.OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			originalSession := ""
			if scenario != "unclaimed" {
				code, e := store.IssueClaimCode(t.Context())
				if e != nil {
					t.Fatal(e)
				}
				originalSession, err = store.Claim(t.Context(), code.Code, "disposable preserved Remote owner")
				if err != nil {
					t.Fatal(err)
				}
			}
			foreignRoot := t.TempDir()
			foreign, err := core.OpenStore(filepath.Join(foreignRoot, "app-state"))
			if err != nil {
				t.Fatal(err)
			}
			defer foreign.Close()
			code, err := foreign.IssueClaimCode(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			foreignToken, err := foreign.Claim(t.Context(), code.Code, "separate ordinary VPS owner")
			if err != nil {
				t.Fatal(err)
			}
			m := custodyinstall.Manifest{Root: root, Owner: 1000, Location: "remote", App: custodyinstall.Identity{UID: 996, GID: 989}}
			if scenario == "foreign-vps-data" {
				m.Root = foreignRoot
			}
			file := filepath.Join(dir, "cli-session.json")
			stale := []byte("expired protected session must remain unchanged")
			if scenario == "stale" {
				if err = os.WriteFile(file, stale, 0600); err != nil {
					t.Fatal(err)
				}
			}
			db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "core.sqlite")+"?mode=ro")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			digest := func() string {
				var salt, pw []byte
				var schema int
				if e := db.QueryRow("SELECT password_salt,password_hash FROM instance WHERE id=1").Scan(&salt, &pw); e != nil {
					t.Fatal(e)
				}
				if e := db.QueryRow("PRAGMA user_version").Scan(&schema); e != nil {
					t.Fatal(e)
				}
				return fmt.Sprintf("%d:%x:%x", schema, salt, pw)
			}
			before := digest()
			instance := store.Health().InstanceID
			probe := strings.Repeat("a", 32)
			handler, err := core.NewHTTPHandler(store, "http://127.0.0.1:7332", "", nil)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			issuedToken := ""
			client := &core.Client{Endpoint: core.Endpoint{URL: "http://127.0.0.1:7332", InstanceID: instance}, Token: "stale-memory-token", CSRF: "stale-csrf"}
			client.HTTP = &http.Client{Transport: bootstrapInventoryTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/api/v1/health" {
					h := store.Health()
					h.Connection = &core.ConnectionInfo{Location: "remote", OwnerUID: 1000}
					if scenario == "wrong-instance" {
						h.InstanceID = "foreign"
					}
					if scenario == "wrong-owner" {
						h.Connection.OwnerUID = 1001
					}
					if scenario == "local" {
						h.Connection.Location = "local"
					}
					raw, _ := json.Marshal(h)
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw)), Request: req}, nil
				}
				if req.Method != "GET" || (req.URL.Path != "/api/v1/auth/state" && req.URL.Path != "/api/v1/updates/inventory") {
					t.Fatal("unexpected authority/route", req.Method, req.URL.Path)
				}
				if req.URL.Path == "/api/v1/updates/inventory" {
					calls++
					issuedToken = strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
					if req.URL.Query().Get("probe") != probe || !store.Session(req.Context(), issuedToken) || foreign.Session(req.Context(), issuedToken) {
						t.Fatal("fresh session not bound to custody database")
					}
					if scenario == "inventory-refused" {
						return &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"refused"}`)), Request: req}, nil
					}
				}
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if req.URL.Path == "/api/v1/updates/inventory" && scenario == "wrong-response-probe" {
					var snap update.CoreSnapshot
					if json.Unmarshal(rec.Body.Bytes(), &snap) != nil {
						t.Fatal("expected real snapshot")
					}
					snap.ProbeID = strings.Repeat("b", 32)
					rec.Body.Reset()
					_ = json.NewEncoder(rec.Body).Encode(snap)
				}
				return rec.Result(), nil
			})}
			var out bytes.Buffer
			args := []string{"--data-dir", dir, "--probe-id", probe, "--bootstrap-owner", "1000", "--bootstrap-id", strings.Repeat("b", 32), "--bootstrap-core-sha256", strings.Repeat("c", 64)}
			err = updateInventoryCommandWithBootstrap(args, &out, io.Discard, func(ctx context.Context, data string, owner int, id, hash, challenge string, w io.Writer) error {
				if data != dir || owner != m.Owner || id != strings.Repeat("b", 32) || hash != strings.Repeat("c", 64) || challenge != probe {
					t.Fatal("typed staged dispatch changed")
				}
				return readBootstrapCustodyInventory(ctx, client, m, instance, challenge, w)
			})
			good := scenario == "absent" || scenario == "stale"
			if (err == nil) != good {
				t.Fatalf("unexpected result: %v", err)
			}
			if good {
				var snap update.CoreSnapshot
				if json.Unmarshal(out.Bytes(), &snap) != nil || snap.InstanceID != instance || snap.ProbeID != probe || calls != 1 {
					t.Fatal("complete real inventory not exercised")
				}
			}
			if !good && out.Len() != 0 {
				t.Fatal("refused inventory escaped")
			}
			if before != digest() || (originalSession != "" && !store.Session(t.Context(), originalSession)) || !foreign.Session(t.Context(), foreignToken) {
				t.Fatal("owner/schema/original sessions changed")
			}
			if client.Token != "" || client.CSRF != "" || (issuedToken != "" && bytes.Contains(out.Bytes(), []byte(issuedToken))) {
				t.Fatal("session escaped adapter")
			}
			b, e := os.ReadFile(file)
			if scenario == "stale" {
				if e != nil || !bytes.Equal(b, stale) {
					t.Fatal("old session replaced")
				}
			} else if !os.IsNotExist(e) {
				t.Fatal("saved session created")
			}
		})
	}
}
