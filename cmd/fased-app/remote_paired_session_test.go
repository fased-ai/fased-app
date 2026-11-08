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

// Exercise the shared installed update loader with actual session storage and
// authenticated HTTP inventory. Immutable OS identity validation is injected.
func TestRemotePairedCustodyWithoutSession(t *testing.T) {
	for _, kind := range []string{"absent", "expired", "malformed", "foreign-identity", "wrong-instance", "wrong-owner", "wrong-directory", "unclaimed"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "app-state")
			store, err := core.OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if kind != "unclaimed" {
				code, e := store.IssueClaimCode(t.Context())
				if e != nil {
					t.Fatal(e)
				}
				if _, e = store.Claim(t.Context(), code.Code, "disposable Remote owner password"); e != nil {
					t.Fatal(e)
				}
			}
			instance := store.Health().InstanceID
			sessionFile := filepath.Join(dir, "cli-session.json")
			var original []byte
			if kind == "malformed" {
				original = []byte("preserve malformed session")
			}
			if kind == "expired" {
				original, _ = json.Marshal(core.SavedSession{InstanceID: instance, Token: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"})
			}
			if original != nil {
				if err := os.WriteFile(sessionFile, original, 0600); err != nil {
					t.Fatal(err)
				}
			}
			handler, err := core.NewHTTPHandler(store, "http://127.0.0.1:7332", "", nil)
			if err != nil {
				t.Fatal(err)
			}
			transport := bootstrapInventoryTransport(func(req *http.Request) (*http.Response, error) {
				rec := httptest.NewRecorder()
				if req.URL.Path == "/api/v1/health" {
					health := store.Health()
					health.Connection = &core.ConnectionInfo{Location: "remote", OwnerUID: 1400, OwnerKind: "custody"}
					json.NewEncoder(rec).Encode(health)
				} else {
					handler.ServeHTTP(rec, req)
				}
				return rec.Result(), nil
			})
			fresh := func() *core.Client {
				return &core.Client{Endpoint: core.Endpoint{URL: "http://127.0.0.1:7332", InstanceID: instance}, HTTP: &http.Client{Transport: transport}}
			}
			calls := 0
			validate := func(owner int, image, data string) (custodyinstall.Manifest, string, error) {
				calls++
				if owner != 1400 || image == "" || data != dir {
					t.Fatal("identity input changed")
				}
				if kind == "foreign-identity" {
					return custodyinstall.Manifest{}, "", errors.New("foreign identity refused")
				}
				m := custodyinstall.Manifest{Root: root, Owner: owner, Location: "remote"}
				m.App.UID = os.Getuid()
				id := instance
				switch kind {
				case "wrong-instance":
					id = "foreign"
				case "wrong-owner":
					m.Owner++
				case "wrong-directory":
					m.Root += "-foreign"
				}
				return m, id, nil
			}
			local := func(int, string, string) (custodyinstall.Manifest, string, error) {
				t.Fatal("Remote entered Local admission")
				return custodyinstall.Manifest{}, "", nil
			}
			client := fresh()
			err = loadUpdateSessionWithIdentities(context.Background(), client, dir, local, validate)
			good := kind == "absent" || kind == "expired"
			if (err == nil) != good {
				t.Fatalf("admission=%v", err)
			}
			if good {
				var inventory map[string]any
				if err := client.Do(t.Context(), http.MethodGet, "/api/v1/updates/inventory?probe=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil, &inventory); err != nil {
					t.Fatal(err)
				}
				// An independent invocation reuses its legitimately issued app-private session.
				if err := loadUpdateSessionWithIdentities(t.Context(), fresh(), dir, local, validate); err != nil {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Fatal("valid session repeated OS admission")
				}
			} else if original != nil {
				after, e := os.ReadFile(sessionFile)
				if e != nil || string(after) != string(original) {
					t.Fatal("refusal changed original session")
				}
			}
		})
	}
}
