package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type remoteRoundTrip func(*http.Request) (*http.Response, error)

func (f remoteRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRemoteClientUsesPinnedHTTPSAndNoPassword(t *testing.T) {
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
