//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

// The real agents command and core client are exercised; only the OS identity
// assertion and transport are substituted. UID ownership is tested separately.
func TestAgentsRemoteCustodyOnchainCLI(t *testing.T) {
	for _, mode := range []string{"read", "reconcile", "foreign-instance", "foreign-directory", "identity-refused", "server-refused", "bad-session", "valid-session-swap", "valid-session-task"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "app-state")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			var session []byte
			if mode == "bad-session" {
				session = []byte("preserve malformed session")
			}
			if strings.HasPrefix(mode, "valid-session-") {
				session = []byte(`{"instanceId":"original-instance","token":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`)
			}
			if session != nil {
				if err := os.WriteFile(filepath.Join(dir, "cli-session.json"), session, 0600); err != nil {
					t.Fatal(err)
				}
			}
			const instance = "original-instance"
			reads, writes, identities, sessionCalls := 0, 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/auth/state":
					sessionCalls++
					if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("A", 43) {
						t.Error("wrong saved session")
					}
					json.NewEncoder(w).Encode(map[string]any{"authenticated": true, "csrfToken": "valid-session-csrf"})
				case "/api/v1/health":
					json.NewEncoder(w).Encode(core.Health{InstanceID: instance, Connection: &core.ConnectionInfo{Location: "remote", OwnerKind: "custody", OwnerUID: 1000}})
				case "/api/v1/agents":
					if mode == "server-refused" {
						w.WriteHeader(403)
						return
					}
					if r.Header.Get("X-Fased-Custody-Onchain") != instance || r.Header.Get("X-Fased-Custody-Accounts") != "" || r.Header.Get("Authorization") != "" {
						t.Error("incorrect authority")
					}
					if r.Method == http.MethodPost {
						writes++
						var c core.AgentCommand
						if json.NewDecoder(r.Body).Decode(&c) != nil {
							t.Error("invalid command")
							return
						}
						if mode == "valid-session-task" && c.Action == "task-create" {
							w.WriteHeader(http.StatusForbidden)
							return
						}
						if c.Action != "swap-reconcile" || c.Swap == nil || c.Swap.ID != "original-task" {
							t.Error("recovery command changed")
						}
					} else {
						reads++
					}
					json.NewEncoder(w).Encode(core.AgentView{ProfileID: "trading-analyst"})
				default:
					t.Errorf("unexpected operation %s", r.URL.Path)
					w.WriteHeader(403)
				}
			}))
			defer server.Close()
			priorClient, priorIdentity := agentsPublicClient, onchainCustodyIdentity
			defer func() { agentsPublicClient, onchainCustodyIdentity = priorClient, priorIdentity }()
			agentsPublicClient = func(_ context.Context, data string) (*core.Client, bool, error) {
				if data != dir {
					t.Fatal("wrong directory")
				}
				return &core.Client{Endpoint: core.Endpoint{URL: server.URL, InstanceID: instance, Network: "unix"}, HTTP: server.Client()}, false, nil
			}
			onchainCustodyIdentity = func(owner int, image, data string) (custodyinstall.Manifest, string, error) {
				identities++
				if owner != 1000 || image == "" || data != dir {
					t.Fatal("identity inputs lost")
				}
				m, id := custodyinstall.Manifest{Owner: owner, Root: root}, instance
				if mode == "identity-refused" {
					return m, id, errors.New("identity refused")
				}
				if mode == "foreign-instance" {
					id = "foreign"
				}
				if mode == "foreign-directory" {
					m.Root += "-foreign"
				}
				return m, id, nil
			}
			args := []string{"--data-dir", dir, "--custody-onchain", "--profile", "trading-analyst"}
			var input *os.File
			if mode == "reconcile" || strings.HasPrefix(mode, "valid-session-") {
				args = []string{"--data-dir", dir, "--custody-onchain", "--request-stdin"}
				path := filepath.Join(root, "request.json")
				body := `{"action":"swap-reconcile","profileId":"trading-analyst","revision":3,"swap":{"id":"original-task"}}`
				if mode == "valid-session-task" {
					body = `{"action":"task-create","profileId":"trading-analyst","name":"unrelated"}`
				}
				if err := os.WriteFile(path, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				var err error
				input, err = os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer input.Close()
			}
			var out bytes.Buffer
			err := agentsCommand(args, input, &out, io.Discard)
			good := mode == "read" || mode == "reconcile" || mode == "bad-session" || mode == "valid-session-swap"
			if (err == nil) != good {
				t.Fatalf("%s: %v", mode, err)
			}
			if good && (((mode == "read" || mode == "bad-session") && (reads != 2 || writes != 0)) || ((mode == "reconcile" || mode == "valid-session-swap") && (reads != 1 || writes != 1))) {
				t.Fatalf("missing admission/command: reads=%d writes=%d", reads, writes)
			}
			if !good && mode != "valid-session-task" && (writes != 0 || mode != "server-refused" && reads != 0) {
				t.Fatal("refusal reached protected operation")
			}
			if mode == "valid-session-task" && (reads != 1 || writes != 1 || identities != 1) {
				t.Fatal("unrelated command did not reach restricted authority")
			}
			if sessionCalls != 0 {
				t.Fatal("scoped helper loaded the saved session")
			}
			if session != nil {
				data, e := os.ReadFile(filepath.Join(dir, "cli-session.json"))
				if e != nil || !bytes.Equal(data, session) {
					t.Fatal("session changed", e)
				}
			} else if _, e := os.Stat(filepath.Join(dir, "cli-session.json")); !os.IsNotExist(e) {
				t.Fatal("created session", e)
			}
		})
	}
}
