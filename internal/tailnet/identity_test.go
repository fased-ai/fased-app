package tailnet

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestIdentityFailsClosed(t *testing.T) {
	good := `{"Node":{"StableID":"node-first","User":7,"Addresses":["100.101.102.103/32"],"KeyExpiry":"2030-01-01T00:00:00Z"},"UserProfile":{"ID":7,"LoginName":"owner@example.test"}}`
	for _, tc := range []struct {
		name, peer, body string
		status           int
		want             bool
	}{
		{"owner", "100.101.102.103", good, 200, true},
		{"wrong address", "100.101.102.104", good, 200, false},
		{"public", "8.8.8.8", good, 200, false},
		{"tagged", "100.101.102.103", strings.Replace(good, `"User":7`, `"User":7,"Tags":["tag:server"]`, 1), 200, false},
		{"wrong owner", "100.101.102.103", strings.Replace(good, `"ID":7`, `"ID":8`, 1), 200, false},
		{"expired", "100.101.102.103", strings.Replace(good, "2030-01-01", "2020-01-01", 1), 200, false},
		{"revoked", "100.101.102.103", good, 404, false},
		{"oversize", "100.101.102.103", strings.Repeat(" ", 65537), 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "local-tailscaled.sock" || r.URL.Path != "/localapi/v0/whois" {
					t.Fatal("wrong authority")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			id, err := resolve(context.Background(), client, tc.peer, time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
			if (err == nil) != tc.want {
				t.Fatalf("identity=%+v error=%v", id, err)
			}
		})
	}
}
