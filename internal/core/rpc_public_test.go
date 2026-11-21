package core

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPublicRPCAdmissionClosed(t *testing.T) {
	for _, raw := range []string{"https://example.com", "https://api.devnet.solana.com/"} {
		if _, err := publicRPCURL(raw); err != nil {
			t.Fatal(err)
		}
		if _, err := CheckFixtureRPC(context.Background(), RPCCheckRequest{Endpoint: raw, Network: "devnet"}); err == nil || !strings.Contains(err.Error(), "only the disposable RPC") {
			t.Fatal("ordinary dispatch admitted or refusal contract changed")
		}
	}
	for _, raw := range []string{"http://example.com", "https://user:secret@example.com", "https://example.com?key=secret", "https://example.com?", "https://example.com/#secret", "https://example.com/secret", "https://example.com/%2f", "https://example.com:443", "https://example.com:", "https://127.0.0.1", "https://[::1]", "https://host.local", "https://host.invalid", "https://EXAMPLE.com", "https://example.com.", "https://-bad.com", "https://example.com\\secret", "https://example.com#"} {
		if _, err := publicRPCURL(raw); err == nil {
			t.Errorf("admitted %q", raw)
		}
	}
	rt := newPublicRPCTransport()
	if rt.enabled != rpcActivationEnabled {
		t.Fatal("unexpected transport activation state", rt.enabled)
	}
	wantCurves := []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384}
	if rt.transport.TLSClientConfig == nil || rt.transport.TLSClientConfig.MinVersion != tls.VersionTLS12 || !slices.Equal(rt.transport.TLSClientConfig.CurvePreferences, wantCurves) {
		t.Fatalf("unexpected TLS config: %+v", rt.transport.TLSClientConfig)
	}
	rt.enabled = false
	rt.transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("closed transport dialed")
		return nil, nil
	}
	req, _ := http.NewRequest(http.MethodPost, "https://example.com", nil)
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatal("closed transport admitted")
	}
}

func TestPublicRPCPinnedDNS(t *testing.T) {
	for _, bad := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "192.0.2.1", "::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "64:ff9b::a00:1", "2001:db8::1", "2002:7f00:1::"} {
		calls := 0
		_, err := dialPublicRPC(context.Background(), "tcp", "example.com:443", func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr(bad)}, nil
		}, func(context.Context, string, string) (net.Conn, error) { calls++; return nil, errors.New("unexpected") })
		if err == nil || calls != 0 {
			t.Fatal("mixed answer admitted", bad, err)
		}
	}
	lookups, dials := 0, 0
	_, err := dialPublicRPC(context.Background(), "tcp", "example.com:443", func(_ context.Context, network, host string) ([]netip.Addr, error) {
		lookups++
		if host != "example.com" || network != "ip" {
			t.Fatal(host)
		}
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}, func(_ context.Context, network, address string) (net.Conn, error) {
		dials++
		if address != "8.8.8.8:443" || network != "tcp" {
			t.Fatal(address)
		}
		return nil, errors.New("do not dial")
	})
	if err == nil || lookups != 1 || dials != 1 {
		t.Fatal("pinning failed", err, lookups, dials)
	}
	for _, address := range []string{"example.com:80", "127.0.0.1:443", "host.local:443"} {
		_, err := dialPublicRPC(context.Background(), "tcp", address, func(context.Context, string, string) ([]netip.Addr, error) {
			t.Fatal("invalid destination resolved")
			return nil, nil
		}, nil)
		if err == nil {
			t.Fatal("destination admitted")
		}
	}
}

func TestPublicRPCControlledTLS(t *testing.T) {
	for _, tc := range []struct {
		name, network, body string
		status, want        int
		ok                  bool
	}{
		{"devnet", "devnet", "", 200, 2, true}, {"mainnet", "mainnet-beta", "", 200, 2, true},
		{"mismatch", "devnet", `{"jsonrpc":"2.0","id":1,"result":"wrong"}`, 200, 1, false},
		{"redirect", "devnet", "", 302, 1, false}, {"error", "devnet", "secret", 403, 1, false},
		{"oversized", "devnet", strings.Repeat("secret", 700), 200, 1, false},
		{"unknown network", "testnet", "", 200, 0, false},
		{"untrusted TLS", "devnet", "", 200, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count := int(calls.Add(1))
				var req struct {
					Method  string
					ID      int
					JSONRPC string
				}
				if json.NewDecoder(r.Body).Decode(&req) != nil || count > 2 || req.Method != []string{"getGenesisHash", "getHealth"}[min(count-1, 1)] || req.ID != count || req.JSONRPC != "2.0" || r.Host != "example.com" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("unexpected wire")
				}
				w.Header().Set("Location", "https://private.local/secret")
				w.WriteHeader(tc.status)
				if tc.body != "" {
					_, _ = w.Write([]byte(tc.body))
					return
				}
				result := solanaGenesis(tc.network)
				if count == 2 {
					result = "ok"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": count, "result": result})
			}))
			defer server.Close()
			rt := newPublicRPCTransport()
			rt.enabled = true
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			if tc.name != "untrusted TLS" {
				rt.transport.TLSClientConfig.RootCAs = roots
			}
			rt.transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return dialPublicRPC(ctx, network, address, func(context.Context, string, string) ([]netip.Addr, error) {
					return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
				}, func(ctx context.Context, network, pinned string) (net.Conn, error) {
					if pinned != "8.8.8.8:443" {
						t.Fatal("unpinned dial", pinned)
					}
					return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
				})
			}
			in := RPCCheckRequest{Endpoint: "https://example.com", Network: tc.network}
			result, err := checkPublicRPC(context.Background(), in, rt)
			if (err == nil) != tc.ok || int(calls.Load()) != tc.want {
				t.Fatalf("result=%+v err=%v calls=%d", result, err, calls.Load())
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("reflected input")
			}
			if tc.ok && (result.FixtureOnly || !result.LiveRPC || result.GenesisHash != solanaGenesis(tc.network)) {
				t.Fatal("wrong result", result)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := checkPublicRPC(ctx, in, rt); err == nil {
				t.Fatal("cancel accepted")
			}
		})
	}
}

func TestPublicRPCMultiIPFallback(t *testing.T) {
	t.Run("ipv6 fallback to ipv4 on single lookup", func(t *testing.T) {
		lookups := 0
		var dialed []string
		c1, c2 := net.Pipe()
		defer c1.Close()
		defer c2.Close()
		conn, err := dialPublicRPC(context.Background(), "tcp", "example.com:443",
			func(context.Context, string, string) ([]netip.Addr, error) {
				lookups++
				return []netip.Addr{
					netip.MustParseAddr("2606:4700:4700::1111"),
					netip.MustParseAddr("1.1.1.1"),
				}, nil
			},
			func(attemptCtx context.Context, network, addr string) (net.Conn, error) {
				if _, ok := attemptCtx.Deadline(); !ok {
					t.Fatal("multi-IP fallback attempt missing bounded timeout")
				}
				dialed = append(dialed, addr)
				if addr == "[2606:4700:4700::1111]:443" {
					return nil, errors.New("ipv6 unreachable")
				}
				return c1, nil
			})
		if err != nil || conn != c1 {
			t.Fatalf("expected fallback connection, got conn=%v err=%v", conn, err)
		}
		if lookups != 1 || len(dialed) != 2 || dialed[0] != "[2606:4700:4700::1111]:443" || dialed[1] != "1.1.1.1:443" {
			t.Fatalf("unexpected lookups=%d dialed=%v", lookups, dialed)
		}
	})

	t.Run("deduplicate and interleave ipv6 and ipv4", func(t *testing.T) {
		var dialed []string
		_, err := dialPublicRPC(context.Background(), "tcp", "example.com:443",
			func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{
					netip.MustParseAddr("2606:4700:4700::1111"),
					netip.MustParseAddr("2606:4700:4700::1111"),
					netip.MustParseAddr("2606:4700:4700::1001"),
					netip.MustParseAddr("2606:4700:4700::64"),
					netip.MustParseAddr("1.1.1.1"),
					netip.MustParseAddr("8.8.8.8"),
				}, nil
			},
			func(_ context.Context, _ string, addr string) (net.Conn, error) {
				dialed = append(dialed, addr)
				return nil, errors.New("refused")
			})
		if err == nil {
			t.Fatal("expected failure when all targets fail")
		}
		want := []string{
			"[2606:4700:4700::1111]:443",
			"1.1.1.1:443",
			"[2606:4700:4700::1001]:443",
			"8.8.8.8:443",
			"[2606:4700:4700::64]:443",
		}
		if strings.Join(dialed, ",") != strings.Join(want, ",") {
			t.Fatalf("got dialed=%v, want %v", dialed, want)
		}
	})

	t.Run("target cap", func(t *testing.T) {
		dials := 0
		_, err := dialPublicRPC(context.Background(), "tcp", "example.com:443",
			func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{
					netip.MustParseAddr("1.1.1.1"),
					netip.MustParseAddr("1.0.0.1"),
					netip.MustParseAddr("8.8.8.8"),
					netip.MustParseAddr("8.8.4.4"),
					netip.MustParseAddr("9.9.9.9"),
					netip.MustParseAddr("149.112.112.112"),
					netip.MustParseAddr("208.67.222.222"),
					netip.MustParseAddr("208.67.220.220"),
					netip.MustParseAddr("4.2.2.1"),
					netip.MustParseAddr("4.2.2.2"),
				}, nil
			},
			func(context.Context, string, string) (net.Conn, error) {
				dials++
				return nil, errors.New("refused")
			})
		if err == nil || dials != 6 {
			t.Fatalf("expected error and 6 dials, got err=%v dials=%d", err, dials)
		}
	})

	t.Run("mixed public and private rejected with zero dials", func(t *testing.T) {
		dials := 0
		_, err := dialPublicRPC(context.Background(), "tcp", "example.com:443",
			func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{
					netip.MustParseAddr("2606:4700:4700::1111"),
					netip.MustParseAddr("1.1.1.1"),
					netip.MustParseAddr("10.0.0.1"),
				}, nil
			},
			func(context.Context, string, string) (net.Conn, error) {
				dials++
				return nil, errors.New("unexpected dial")
			})
		if err == nil || dials != 0 {
			t.Fatalf("expected rejection with zero dials, got err=%v dials=%d", err, dials)
		}
	})

	t.Run("canceled parent context stops fallback after attempt 1", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		dials := 0
		_, err := dialPublicRPC(ctx, "tcp", "example.com:443",
			func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{
					netip.MustParseAddr("2606:4700:4700::1111"),
					netip.MustParseAddr("1.1.1.1"),
					netip.MustParseAddr("8.8.8.8"),
				}, nil
			},
			func(context.Context, string, string) (net.Conn, error) {
				dials++
				cancel()
				return nil, errors.New("canceled")
			})
		if err == nil || dials != 1 {
			t.Fatalf("expected early stop after 1 dial, got err=%v dials=%d", err, dials)
		}
	})
}
