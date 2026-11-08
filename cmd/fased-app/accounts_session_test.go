//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

// Actual saved-session loader and account-client request; OS validation is
// injected here and separately checked against the immutable installer model.
func TestRemoteCustodyAccountsSessionAdmission(t *testing.T) {
	for _, kind := range []string{"absent", "expired", "bad-file", "foreign-session", "local", "foreign-vps", "wrong-instance", "wrong-directory", "identity-refused", "server-refused"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "app-state")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			instance := "original-instance"
			calls, accountCalls := 0, 0
			file := filepath.Join(dir, "cli-session.json")
			var original []byte
			if kind == "bad-file" {
				original = []byte("preserve damaged session")
			}
			if kind == "foreign-session" {
				original = []byte(`{"instanceId":"foreign","token":"invalid"}`)
			}
			if kind == "expired" {
				original = []byte(`{"instanceId":"original-instance","token":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`)
			}
			if original != nil {
				if err := os.WriteFile(file, original, 0600); err != nil {
					t.Fatal(err)
				}
			}
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/auth/state":
					json.NewEncoder(w).Encode(map[string]any{"authenticated": false})
				case "/api/v1/health":
					location, ownerKind := "remote", "custody"
					if kind == "local" {
						location = "local"
					}
					if kind == "foreign-vps" {
						ownerKind = ""
					}
					json.NewEncoder(w).Encode(core.Health{InstanceID: instance, Connection: &core.ConnectionInfo{Location: location, OwnerUID: 1000, OwnerKind: ownerKind}})
				case "/api/v1/accounts":
					accountCalls++
					if kind == "server-refused" {
						http.Error(w, `{"error":"refused"}`, 403)
						return
					}
					if r.Header.Get("X-Fased-Custody-Accounts") != instance || r.Header.Get("Authorization") != "" {
						t.Error("wrong authority")
					}
					w.Header().Set("Content-Type", "application/json")
					w.Write([]byte(`{"record":{"version":1,"revision":0,"accounts":[]}}`))
				default:
					t.Errorf("unexpected auth operation %s", r.URL.Path)
					http.Error(w, "refused", 403)
				}
			})
			server := httptest.NewServer(handler)
			defer server.Close()
			client := &core.Client{Endpoint: core.Endpoint{URL: server.URL, InstanceID: instance, Network: "unix"}, HTTP: server.Client()}
			identity := func(owner int, image, data string) (custodyinstall.Manifest, string, error) {
				calls++
				if owner != 1000 || image == "" || data != dir {
					t.Fatal("missing exact identity inputs")
				}
				m := custodyinstall.Manifest{Owner: owner, Root: root}
				id := instance
				if kind == "wrong-instance" {
					id = "foreign"
				}
				if kind == "wrong-directory" {
					m.Root += "-foreign"
				}
				if kind == "identity-refused" {
					return m, id, errors.New("immutable identity refused")
				}
				return m, id, nil
			}
			err := loadAccountsSessionWithIdentity(context.Background(), client, dir, identity)
			ok := kind == "absent" || kind == "expired"
			if (err == nil) != ok {
				t.Fatalf("admission %s: %v", kind, err)
			}
			if kind == "bad-file" || kind == "foreign-session" || kind == "local" || kind == "foreign-vps" {
				if calls != 0 {
					t.Fatal("broadened an unrelated auth failure")
				}
			}
			if ok && accountCalls != 1 {
				t.Fatal("did not reach ordinary accounts adapter")
			}
			got, e := os.ReadFile(file)
			if original == nil {
				if !os.IsNotExist(e) {
					t.Fatal("created a session file")
				}
			} else if string(got) != string(original) {
				t.Fatal("rewrote original authentication")
			}
		})
	}
}
