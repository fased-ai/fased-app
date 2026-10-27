package core

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRPCAuthenticatedHTTPS(t *testing.T) {
	for _, mode := range []string{"ok", "unauthorized", "redirect", "oversize", "genesis", "health", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			c := rpcSecretSetup(t, s)
			key := c.Key
			v, e := s.RPCCredential(t.Context(), c)
			if e != nil {
				t.Fatal(e)
			}
			c.Ref = v.Ref
			c.Key = ""
			c.Action = "check"
			before, _ := os.ReadFile(s.rpcVaultPath(c.AccountID))
			records, _ := s.accountsRecord(t.Context())
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/solana" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer "+key || r.Header.Get("Cookie") != "" {
					t.Error("wrong credential scope")
					w.WriteHeader(401)
					return
				}
				var req struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
				}
				json.NewDecoder(r.Body).Decode(&req)
				switch mode {
				case "unauthorized":
					w.WriteHeader(401)
					fmt.Fprint(w, key)
					return
				case "redirect":
					w.Header().Set("Location", "https://example.invalid/"+key)
					w.WriteHeader(307)
					return
				case "oversize":
					fmt.Fprint(w, strings.Repeat(key, 300))
					return
				case "malformed":
					fmt.Fprintf(w, `{"error":"%s"}`, key)
					return
				}
				result := fixtureGenesis
				if req.Method == "getHealth" {
					result = "ok"
				}
				if mode == "genesis" && req.ID == 1 {
					result = "wrong-network"
				}
				if mode == "health" && req.ID == 2 {
					result = "unhealthy"
				}
				json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
			}))
			defer server.Close()
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			rt, e := newRPCHTTPSFixture(server.URL, roots)
			if e != nil {
				t.Fatal(e)
			}
			defer rt.transport.CloseIdleConnections()
			var borrowed []byte
			result, e := s.rpcCredential(t.Context(), c, func(ctx context.Context, in RPCCheckRequest, secret []byte, valid func() error) (RPCCheckResult, error) {
				borrowed = secret
				return checkAuthenticatedRPC(ctx, in, secret, valid, rt)
			})
			if (mode == "ok") != (e == nil) {
				t.Fatal(mode, e)
			}
			if mode == "ok" && (result.Check == nil || !result.Check.FixtureOnly || result.Check.LiveRPC || !result.Locked || result.Check.Account.ID != c.AccountID) {
				t.Fatal(result)
			}
			output, _ := json.Marshal(result)
			if bytes.Contains(output, []byte(key)) || (e != nil && strings.Contains(e.Error(), key)) {
				t.Fatal("credential reflected")
			}
			for _, b := range borrowed {
				if b != 0 {
					t.Fatal("plaintext retained")
				}
			}
			after, _ := os.ReadFile(s.rpcVaultPath(c.AccountID))
			updated, _ := s.accountsRecord(t.Context())
			if !bytes.Equal(before, after) || updated.Revision != records.Revision {
				t.Fatal("check changed state")
			}
			expected := int32(1)
			if mode == "ok" || mode == "health" {
				expected = 2
			}
			if calls.Load() != expected {
				t.Fatal("retry/redirect", calls.Load())
			}
		})
	}
}

func TestRPCAuthenticatedAdmissionCancellationAndBinding(t *testing.T) {
	key := []byte("fased-rpc-test-controlled-check-123456")
	roots := x509.NewCertPool()
	for _, u := range []string{"http://127.0.0.1:1", "https://example.com:443", "https://127.0.0.1:1/path", "https://user@127.0.0.1:1", "https://127.0.0.1:1?key=secret"} {
		if _, e := newRPCHTTPSFixture(u, roots); e == nil {
			t.Fatal("unsafe fixture origin")
		}
	}
	var calls atomic.Int32
	stop := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	defer func() { close(stop); server.Close() }()
	roots.AddCert(server.Certificate())
	rt, e := newRPCHTTPSFixture(server.URL, roots)
	if e != nil {
		t.Fatal(e)
	}
	defer rt.transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	if _, e = checkAuthenticatedRPC(ctx, rpcInput(), key, func() error { return nil }, rt); e == nil {
		t.Fatal("timeout accepted")
	}
	if calls.Load() > 1 {
		t.Fatal("retry")
	}
	// Certificate verification stays active, even for a loopback fixture.
	wrong, _ := newRPCHTTPSFixture(server.URL, x509.NewCertPool())
	defer wrong.transport.CloseIdleConnections()
	if _, e = checkAuthenticatedRPC(t.Context(), rpcInput(), key, func() error { return nil }, wrong); e == nil {
		t.Fatal("untrusted TLS")
	}
	invoked := 0
	_, e = checkAuthenticatedRPC(t.Context(), rpcInput(), key, func() error { return errors.New("revoked") }, rpcTransportFunc(func(*http.Request) (*http.Response, error) { invoked++; return nil, errors.New("must not dispatch") }))
	if e == nil || invoked != 0 {
		t.Fatal("invalid binding dispatched")
	}
}

func TestRPCAuthenticatedCredentialChangeStopsSecondRequest(t *testing.T) {
	s := fixtureStore(t)
	c := rpcSecretSetup(t, s)
	v, e := s.RPCCredential(t.Context(), c)
	if e != nil {
		t.Fatal(e)
	}
	c.Key = ""
	c.Ref = v.Ref
	c.Action = "check"
	original, _ := os.ReadFile(s.rpcVaultPath(c.AccountID))
	defer os.WriteFile(s.rpcVaultPath(c.AccountID), original, 0600)
	calls := 0
	_, e = s.rpcCredential(t.Context(), c, func(ctx context.Context, in RPCCheckRequest, key []byte, valid func() error) (RPCCheckResult, error) {
		return checkAuthenticatedRPC(ctx, in, key, valid, rpcTransportFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			result, e := (authenticatedRPCFixture{}).RoundTrip(r)
			os.WriteFile(s.rpcVaultPath(c.AccountID), []byte("changed outside core"), 0600)
			return result, e
		}))
	})
	if e == nil || calls != 1 {
		t.Fatal("changed vault dispatched again", calls, e)
	}
}
