//go:build linux

package vpsnetwork

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestRequiresOpenSSHAncestry(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
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
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			got := hasSSHAncestor(40, tc.main, func(pid int) (int, error) {
				calls++
				if p, ok := tc.parents[pid]; ok {
					return p, nil
				}
				return 0, errors.New("missing process")
			})
			if got != tc.want || calls > 64 {
				t.Fatal(got, calls)
			}
		})
	}
}

func TestPrivateSession(t *testing.T) {
	t.Parallel()
	status := []byte(`{"BackendState":"Running","Self":{"Online":true,"DNSName":"vps.owner.ts.net.","TailscaleIPs":["100.90.0.2","fd7a:115c:a1e0::2"]}}`)
	ipv4 := &net.IPNet{IP: net.ParseIP("100.90.0.2"), Mask: net.CIDRMask(32, 32)}
	ipv6 := &net.IPNet{IP: net.ParseIP("fd7a:115c:a1e0::2"), Mask: net.CIDRMask(128, 128)}
	for _, connection := range []string{"100.90.0.1 40000 100.90.0.2 22", "fd7a:115c:a1e0::1 40000 fd7a:115c:a1e0::2 22"} {
		got, err := checkNetworkSession(connection, 123, []net.Addr{ipv4, ipv6}, status, "https://vps.owner.ts.net")
		if err != nil || got.SessionID == "" || got.SSHPort != 22 {
			t.Fatal(got, err)
		}
	}
	if _, err := checkNetworkSession("100.90.0.1 40000 100.90.0.2 22", 123, []net.Addr{ipv4}, status, "https://vps.owner.ts.net:8443"); err != nil {
		t.Fatal("isolated HTTPS port changed SSH peer identity", err)
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
	for name, data := range map[string][]byte{
		"malformed": []byte("{"),
		"offline":   []byte(strings.Replace(string(status), `"Online":true`, `"Online":false`, 1)),
		"stopped":   []byte(strings.Replace(string(status), `"Running"`, `"Stopped"`, 1)),
		"huge":      []byte(strings.Repeat("x", 1024*1024+1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := checkNetworkSession("100.90.0.1 40000 100.90.0.2 22", 123, []net.Addr{ipv4}, data, "https://vps.owner.ts.net"); err == nil {
				t.Fatal("invalid Tailscale state accepted")
			}
		})
	}
	for _, tc := range []struct {
		sid    int
		addrs  []net.Addr
		origin string
	}{
		{0, []net.Addr{ipv4}, "https://vps.owner.ts.net"},
		{123, nil, "https://vps.owner.ts.net"},
		{123, []net.Addr{ipv4}, "https://different.ts.net"},
	} {
		if _, err := checkNetworkSession("100.90.0.1 40000 100.90.0.2 22", tc.sid, tc.addrs, status, tc.origin); err == nil {
			t.Fatal("unbound session/interface/origin accepted", tc)
		}
	}
}

type networkRoundTrip func(*http.Request) (*http.Response, error)

func (r networkRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return r(req) }

func TestLocalReadinessNeverSubstitutesForOwnerBinding(t *testing.T) {
	t.Parallel()
	m := Manifest{OwnerUID: 1000, Listen: "127.0.0.1:17445", PublicOrigin: "https://vps.owner.ts.net:8443"}
	for _, scenario := range []string{"ready", "unclaimed", "wrong owner", "wrong profile"} {
		t.Run(scenario, func(t *testing.T) {
			client := &http.Client{Transport: networkRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != "http://"+m.Listen+"/api/v1/health" {
					t.Fatal("unexpected readiness request", r.URL)
				}
				h := core.Health{
					Status:     "ok",
					APIVersion: core.APIVersion,
					InstanceID: strings.Repeat("a", 43),
					Connection: &core.ConnectionInfo{RemoteReady: true, Location: "remote", Transport: "loopback", OwnerUID: m.OwnerUID},
				}
				switch scenario {
				case "unclaimed":
					h.Connection.RemoteReady = false
				case "wrong owner":
					h.Connection.OwnerUID++
				case "wrong profile":
					h.Connection.Location = "local"
				}
				data, _ := json.Marshal(h)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(data))}, nil
			})}
			_, err := checkPackagePrivateLocalApplication(t.Context(), m, strings.Repeat("a", 43), client)
			if (err == nil) != (scenario == "ready") {
				t.Fatal(scenario, err)
			}
		})
	}
}
