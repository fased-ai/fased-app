package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/tailnet"
)

type remoteRoundTrip func(*http.Request) (*http.Response, error)

func (f remoteRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRemoteUpdateStatusGatewayErrorScope(t *testing.T) {
	t.Parallel()
	const statusRoute = "/api/v1/updates/application"
	for _, tc := range []struct {
		name, method, route string
		status              int
		remote, want        bool
	}{
		{"remote status", http.MethodGet, statusRoute, http.StatusBadGateway, true, true},
		{"remote apply", http.MethodPost, statusRoute, http.StatusBadGateway, true, false},
		{"remote health", http.MethodGet, "/api/v1/health", http.StatusBadGateway, true, false},
		{"local status", http.MethodGet, statusRoute, http.StatusBadGateway, false, false},
		{"authorization failure", http.MethodGet, statusRoute, http.StatusForbidden, true, false},
		{"other gateway response", http.MethodGet, statusRoute, http.StatusServiceUnavailable, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := Client{
				Endpoint: Endpoint{URL: "https://node.owner.ts.net"},
				remote:   tc.remote,
				HTTP: &http.Client{Transport: remoteRoundTrip(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(http.StatusText(tc.status)))}, nil
				})},
			}
			err := client.Do(t.Context(), tc.method, tc.route, nil, nil)
			if err == nil || errors.Is(err, ErrRemoteUpdateStatusUnavailable) != tc.want || errors.Is(err, ErrCoreUnavailable) {
				t.Fatalf("unexpected classification: %v", err)
			}
		})
	}
}

func TestRemoteClientUsesPinnedHTTPSAndNoPassword(t *testing.T) {
	t.Parallel()
	identity := strings.Repeat("a", 43)
	for _, scenario := range []string{"good", "changed instance", "revoked"} {
		t.Run(scenario, func(t *testing.T) {
			requests := 0
			auths := 0
			transport := remoteRoundTrip(func(r *http.Request) (*http.Response, error) {
				requests++
				if r.URL.Scheme != "https" || r.URL.Host != "node.owner.ts.net" || r.Header.Get("X-Fased-Instance") != identity {
					t.Fatal("unpinned request", r.URL)
				}
				status := 200
				var body any
				switch r.URL.Path {
				case "/api/v1/health":
					id := identity
					if scenario == "changed instance" {
						id = strings.Repeat("b", 43)
					}
					body = Health{Status: "ok", APIVersion: APIVersion, InstanceID: id, Connection: &ConnectionInfo{Location: "remote", Transport: "private-https"}}
				case "/api/v1/auth/remote":
					auths++
					if r.Method != "POST" || r.Header.Get("Origin") != "https://node.owner.ts.net" {
						t.Fatal("bad authentication transport")
					}
					data, _ := io.ReadAll(r.Body)
					if strings.Contains(string(data), "password") {
						t.Fatal("password requested")
					}
					body = map[string]string{"sessionToken": strings.Repeat("c", 43), "csrfToken": "controlled-csrf"}
					if scenario == "revoked" {
						status = 403
						body = map[string]string{"error": "Device is not approved"}
					}
				default:
					t.Fatal("unrelated request", r.URL)
				}
				data, _ := json.Marshal(body)
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(data))), Header: http.Header{"Content-Type": []string{"application/json"}}}, nil
			})
			client, err := newRemoteClient(context.Background(), "https://node.owner.ts.net", identity, transport)
			if (err == nil) != (scenario == "good") {
				t.Fatal(scenario, err)
			}
			if scenario == "changed instance" && auths != 0 {
				t.Fatal("authenticated another instance")
			}
			if requests > 2 {
				t.Fatal("unexpected fallback or retry")
			}
			if client != nil {
				client.Close()
			}
		})
	}
	for _, origin := range []string{"http://node.owner.ts.net", "https://public.example", "https://node.owner.ts.net?", "https://user:secret@node.owner.ts.net", "https://node.owner.ts.net/path"} {
		if c, err := NewRemoteClient(context.Background(), origin, identity); err == nil {
			c.Close()
			t.Fatal("unsafe origin accepted", origin)
		}
	}
}

func TestRemoteClientCookieModeAndConfirmReconnect(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	owner := tailnet.Identity{UserID: 9, NodeID: "cli-node", Login: "cli@example.test"}
	claim, err := s.IssueRemoteClaim(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.remoteSession(t.Context(), owner, claim.Code, ""); err != nil {
		t.Fatal(err)
	}
	// Simulate explicit browser logout on this node before CLI reconnect.
	s.markRemoteSignedOut(owner.NodeID)

	const origin = "https://cli.owner.ts.net"
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", origin, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.remoteIngress, h.instanceLocation = true, "remote"

	transport := remoteRoundTrip(func(r *http.Request) (*http.Response, error) {
		ctx := context.WithValue(r.Context(), remoteIdentityKey{}, owner)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r.WithContext(ctx))
		return rec.Result(), nil
	})
	client, err := newRemoteClient(t.Context(), origin, s.Health().InstanceID, transport)
	if err != nil {
		t.Fatalf("cookie-mode remote client failed: %v", err)
	}
	defer client.Close()
	if client.Token != "" || client.cookie == "" {
		t.Fatalf("expected cookie session without bearer token, got token=%q cookieLen=%d", client.Token, len(client.cookie))
	}
	var setup SetupRecord
	if err = client.Do(t.Context(), http.MethodGet, "/api/v1/setup", nil, &setup); err != nil {
		t.Fatalf("authenticated GET over cookie session failed: %v", err)
	}
}
