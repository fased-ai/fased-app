//go:build linux

package vpsinstall

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestNetworkRequiresOpenSSHAncestry(t *testing.T) {
	for _, c := range []struct {
		name    string
		main    int
		parents map[int]int
		want    bool
	}{
		{"OpenSSH", 20, map[int]int{40: 30, 30: 20, 20: 1}, true},
		{"detached", 20, map[int]int{40: 30, 30: 1}, false},
		{"Tailscale SSH", 20, map[int]int{40: 50, 50: 1}, false},
		{"inactive", 0, map[int]int{40: 30, 30: 20}, false},
		{"cycle", 20, map[int]int{40: 30, 30: 40}, false},
		{"unreadable", 20, map[int]int{}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			calls := 0
			got := hasSSHAncestor(40, c.main, func(pid int) (int, error) {
				calls++
				if p, ok := c.parents[pid]; ok {
					return p, nil
				}
				return 0, errors.New("missing process")
			})
			if got != c.want || calls > 64 {
				t.Fatal(got, calls)
			}
		})
	}
}

func TestNetworkPrivateSession(t *testing.T) {
	status := []byte(`{"BackendState":"Running","Self":{"Online":true,"DNSName":"vps.owner.ts.net.","TailscaleIPs":["100.90.0.2","fd7a:115c:a1e0::2"]}}`)
	ipv4 := &net.IPNet{IP: net.ParseIP("100.90.0.2"), Mask: net.CIDRMask(32, 32)}
	ipv6 := &net.IPNet{IP: net.ParseIP("fd7a:115c:a1e0::2"), Mask: net.CIDRMask(128, 128)}
	for _, connection := range []string{"100.90.0.1 40000 100.90.0.2 22", "fd7a:115c:a1e0::1 40000 fd7a:115c:a1e0::2 22"} {
		got, err := checkNetworkSession(connection, 123, []net.Addr{ipv4, ipv6}, status, "https://vps.owner.ts.net")
		if err != nil || got.SessionID == "" || got.SSHPort != 22 {
			t.Fatal(got, err)
		}
	}
	first, _ := checkNetworkSession("100.90.0.1 40000 100.90.0.2 22", 123, []net.Addr{ipv4}, status, "https://vps.owner.ts.net")
	sameConnection, _ := checkNetworkSession("100.90.0.1 40000 100.90.0.2 22", 456, []net.Addr{ipv4}, status, "https://vps.owner.ts.net")
	newConnection, _ := checkNetworkSession("100.90.0.1 40001 100.90.0.2 22", 123, []net.Addr{ipv4}, status, "https://vps.owner.ts.net")
	if first.SessionID != sameConnection.SessionID || first.SessionID == newConnection.SessionID {
		t.Fatal("process identity substituted for a new TCP connection")
	}
	for name, connection := range map[string]string{
		"missing": "", "public peer": "203.0.113.2 40000 100.90.0.2 22", "public server": "100.90.0.1 40000 203.0.113.2 22",
		"wrong port": "100.90.0.1 40000 100.90.0.2 2222", "same host": "100.90.0.2 40000 100.90.0.2 22", "bad port": "100.90.0.1 0 100.90.0.2 22",
		"wrong device": "100.90.0.1 40000 100.90.0.3 22", "garbage": "100.90.0.1 40000 100.90.0.2 22 extra",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := checkNetworkSession(connection, 123, []net.Addr{ipv4}, status, "https://vps.owner.ts.net"); err == nil {
				t.Fatal("unsafe SSH session accepted")
			}
		})
	}
	for name, data := range map[string][]byte{"malformed": []byte("{"), "offline": []byte(strings.Replace(string(status), `"Online":true`, `"Online":false`, 1)), "stopped": []byte(strings.Replace(string(status), `"Running"`, `"Stopped"`, 1)), "huge": []byte(strings.Repeat("x", 1024*1024+1))} {
		t.Run(name, func(t *testing.T) {
			if _, err := checkNetworkSession("100.90.0.1 40000 100.90.0.2 22", 123, []net.Addr{ipv4}, data, "https://vps.owner.ts.net"); err == nil {
				t.Fatal("invalid Tailscale state accepted")
			}
		})
	}
	for _, c := range []struct {
		sid    int
		addrs  []net.Addr
		origin string
	}{{0, []net.Addr{ipv4}, "https://vps.owner.ts.net"}, {123, nil, "https://vps.owner.ts.net"}, {123, []net.Addr{ipv4}, "https://different.ts.net"}} {
		if _, err := checkNetworkSession("100.90.0.1 40000 100.90.0.2 22", c.sid, c.addrs, status, c.origin); err == nil {
			t.Fatal("unbound session/interface/origin accepted", c)
		}
	}
}

type networkRoundTrip func(*http.Request) (*http.Response, error)

func (r networkRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return r(req) }

func TestNetworkPrivateApplicationIdentityAndOwner(t *testing.T) {
	m := Manifest{Listen: "127.0.0.1:17445", PublicOrigin: "https://vps.owner.ts.net"}
	for _, scenario := range []string{"good", "different instance", "unclaimed", "owner failure", "huge owner", "local descriptor", "bad health"} {
		t.Run(scenario, func(t *testing.T) {
			requests := 0
			client := &http.Client{Transport: networkRoundTrip(func(req *http.Request) (*http.Response, error) {
				requests++
				if req.Method != "GET" || req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
					t.Fatal("credential or mutation in access proof")
				}
				code, body := 200, `{"claimed":true}`
				if req.URL.Path == "/api/v1/health" {
					h := core.Health{Status: "ok", APIVersion: core.APIVersion, InstanceID: strings.Repeat("a", 43), Connection: &core.ConnectionInfo{Location: "remote", Transport: "private-https"}}
					if scenario == "different instance" && req.URL.Scheme == "https" {
						h.InstanceID = strings.Repeat("b", 43)
					}
					if scenario == "local descriptor" {
						h.Connection.Location = "local"
					}
					if scenario == "bad health" {
						h.Status = "failed"
					}
					data, _ := json.Marshal(h)
					body = string(data)
				} else if req.URL.Path != "/api/v1/auth/state" {
					t.Fatal("unexpected access", req.URL)
				} else {
					switch scenario {
					case "unclaimed":
						body = `{"claimed":false}`
					case "owner failure":
						code = 503
					case "huge owner":
						body = strings.Repeat("x", 16385)
					}
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			_, err := checkPrivateApplication(context.Background(), m, client)
			if (err == nil) != (scenario == "good") {
				t.Fatal(scenario, err)
			}
			if requests > 3 {
				t.Fatal("unexpected retries")
			}
		})
	}
}
