package connection

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type ownerStateTransport func(*http.Request) (*http.Response, error)

func (f ownerStateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClaimedOwnerURLPreservesSignInBoundary(t *testing.T) {
	const id = "fixture-instance-1234"
	for _, scenario := range []string{"claimed", "unclaimed", "wrong instance", "missing identity", "missing claimed", "invalid claimed", "oversized", "redirect", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			stateReads := 0
			client, close := instanceClient()
			defer close()
			client.Transport = ownerStateTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != "GET" || req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
					t.Fatal("owner reopen attempted authority")
				}
				body, status := "", 200
				switch req.URL.Path {
				case "/api/v1/health":
					healthID := id
					if scenario == "wrong instance" {
						healthID = "another-instance-1234"
					}
					body = `{"status":"ok","apiVersion":"1","instanceId":"` + healthID + `","connection":{"location":"remote","transport":"private-https"}}`
				case "/api/v1/auth/state":
					stateReads++
					body = `{"claimed":true,"authenticated":false}`
					switch scenario {
					case "unclaimed":
						body = `{"claimed":false}`
					case "missing claimed":
						body = `{}`
					case "invalid claimed":
						body = `{"claimed":"true"}`
					case "oversized":
						body = strings.Repeat(" ", 4097) + body
					case "redirect":
						status = 302
					case "unavailable":
						status = 503
					}
				default:
					t.Fatal("unexpected owner operation", req.URL.Path)
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}, "Location": []string{"https://elsewhere.owner.ts.net"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			expected := id
			if scenario == "missing identity" {
				expected = ""
			}
			address, claimed, err := claimedOwnerURL(context.Background(), "https://node.owner.ts.net", expected, client)
			if scenario == "claimed" {
				if err != nil || !claimed || address != "https://node.owner.ts.net/?instance="+id || strings.Contains(address, "#") {
					t.Fatal(address, claimed, err)
				}
			} else if scenario == "unclaimed" {
				if err != nil || claimed || address != "" {
					t.Fatal("unclaimed owner received a handoff", address, claimed, err)
				}
			} else if err == nil || claimed || address != "" {
				t.Fatal("invalid owner state accepted", address, claimed, err)
			}
			if (scenario == "wrong instance" || scenario == "missing identity") && stateReads != 0 {
				t.Fatal("read owner state without matching identity")
			}
		})
	}
}
