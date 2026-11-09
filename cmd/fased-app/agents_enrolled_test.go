package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/core"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The command, auth client and HTTP requests are real. Only the already-tested
// OS-owned enrollment transport factory is substituted; this is not UID proof.
func TestAgentsEnrolledOwnerTransportNoSavedSession(t *testing.T) {
	for _, mode := range []string{"read", "reconcile", "auth-refused", "identity-refused"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			original := []byte("preserve the original invalid session")
			session := filepath.Join(dir, "cli-session.json")
			if e := os.WriteFile(session, original, 0600); e != nil {
				t.Fatal(e)
			}
			auth, reads, writes := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/auth/native-local":
					auth++
					if mode == "auth-refused" {
						w.WriteHeader(403)
						return
					}
					json.NewEncoder(w).Encode(map[string]string{"sessionToken": "new-transient-owner-token", "csrfToken": "owner-csrf"})
				case "/api/v1/agents":
					if r.Header.Get("Authorization") != "Bearer new-transient-owner-token" {
						t.Error("lost authenticated owner")
					}
					if r.Method == http.MethodPost {
						writes++
						if r.Header.Get("X-CSRF-Token") != "owner-csrf" {
							t.Error("lost CSRF")
						}
						var c core.AgentCommand
						if json.NewDecoder(r.Body).Decode(&c) != nil || c.Action != "swap-reconcile" || c.ProfileID != "trading-analyst" || c.Swap.ID != "original-task" {
							t.Error("changed original recovery")
						}
					} else {
						reads++
					}
					json.NewEncoder(w).Encode(core.AgentView{ProfileID: "trading-analyst"})
				default:
					t.Error("unexpected auth or helper route", r.URL.Path)
					w.WriteHeader(403)
				}
			}))
			defer server.Close()
			prior := agentsPublicClient
			defer func() { agentsPublicClient = prior }()
			factoryCalls := 0
			agentsPublicClient = func(ctx context.Context, data string) (*core.Client, bool, error) {
				factoryCalls++
				if data != dir {
					t.Fatal("changed owner directory")
				}
				if mode == "identity-refused" {
					return nil, false, errors.New("foreign enrollment")
				}
				return &core.Client{Endpoint: core.Endpoint{URL: server.URL, InstanceID: "instance"}, HTTP: server.Client()}, true, nil
			}
			args := []string{"--data-dir", dir, "--profile", "trading-analyst"}
			var input *os.File
			if mode == "reconcile" {
				args = []string{"--data-dir", dir, "--request-stdin"}
				p := filepath.Join(dir, "public-recovery.json")
				if e := os.WriteFile(p, []byte(`{"action":"swap-reconcile","profileId":"trading-analyst","revision":3,"swap":{"id":"original-task"}}`), 0600); e != nil {
					t.Fatal(e)
				}
				var e error
				input, e = os.Open(p)
				if e != nil {
					t.Fatal(e)
				}
				defer input.Close()
			}
			var out bytes.Buffer
			e := agentsCommand(args, input, &out, io.Discard)
			good := mode == "read" || mode == "reconcile"
			if (e == nil) != good {
				t.Fatal(mode, e)
			}
			if factoryCalls != 1 {
				t.Fatal("ordinary enrollment transport bypassed")
			}
			if good && (auth != 1 || reads+writes != 1) {
				t.Fatal("missing actual authenticated API path")
			}
			if !good && reads+writes != 0 {
				t.Fatal("refusal reached protected records")
			}
			got, e := os.ReadFile(session)
			if e != nil || !bytes.Equal(got, original) {
				t.Fatal("saved credentials changed", e)
			}
		})
	}
}
