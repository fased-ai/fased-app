package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
)

type rpcTransportFunc func(*http.Request) (*http.Response, error)

func (f rpcTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func rpcInput() RPCCheckRequest {
	return RPCCheckRequest{Endpoint: FixtureRPCURL, Network: signer.FixtureNetwork}
}

func TestRPCAdmissionAndCancellation(t *testing.T) {
	result, err := CheckFixtureRPC(context.Background(), rpcInput())
	if err != nil || !result.FixtureOnly || result.LiveRPC || result.GenesisHash != fixtureGenesis || result.CheckedAt.IsZero() {
		t.Fatalf("%+v %v", result, err)
	}
	for _, endpoint := range []string{"http://rpc.fased.invalid/solana", "https://secret@rpc.fased.invalid/solana", FixtureRPCURL + "?key=secret", FixtureRPCURL + "#secret", "https://127.0.0.1/solana", "https://10.0.0.1/solana", "https://rpc.fased.invalid:443/solana", "https://example.com/solana", ""} {
		t.Run(endpoint, func(t *testing.T) {
			calls := 0
			_, err := checkRPC(context.Background(), RPCCheckRequest{Endpoint: endpoint, Network: signer.FixtureNetwork}, rpcTransportFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, fmt.Errorf("unexpected") }))
			if err == nil || calls != 0 || strings.Contains(err.Error(), "secret") {
				t.Fatal("endpoint admitted or reflected", calls, err)
			}
		})
	}
	for _, network := range []string{"mainnet-beta", "devnet", ""} {
		if _, err := CheckFixtureRPC(context.Background(), RPCCheckRequest{Endpoint: FixtureRPCURL, Network: network}); err == nil {
			t.Fatal("network admitted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CheckFixtureRPC(ctx, rpcInput()); err == nil {
		t.Fatal("cancel accepted")
	}
}

func TestRPCHTTPSProtocol(t *testing.T) {
	for _, tc := range []struct {
		name, body        string
		status, wantCalls int
		ok                bool
	}{
		{"healthy", "", 200, 2, true},
		{"wrong network", `{"jsonrpc":"2.0","id":1,"result":"other"}`, 200, 1, false},
		{"unhealthy", "unhealthy", 200, 2, false},
		{"error", `{"jsonrpc":"2.0","id":1,"error":{"message":"secret"}}`, 200, 1, false},
		{"wrong id", `{"jsonrpc":"2.0","id":2,"result":"ok"}`, 200, 1, false},
		{"wrong version", `{"jsonrpc":"1.0","id":1,"result":"ok"}`, 200, 1, false},
		{"duplicate", `{"jsonrpc":"2.0","id":1,"id":1,"result":"ok"}`, 200, 1, false},
		{"nonstring", `{"jsonrpc":"2.0","id":1,"result":{}}`, 200, 1, false},
		{"trailing", `{"jsonrpc":"2.0","id":1,"result":"ok"} {}`, 200, 1, false},
		{"batch", `[]`, 200, 1, false},
		{"oversize", strings.Repeat("x", 4097), 200, 1, false},
		{"status", "secret", 503, 1, false},
		{"redirect", "", 302, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var request struct {
					Version string `json:"jsonrpc"`
					ID      int    `json:"id"`
					Method  string `json:"method"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil || request.Version != "2.0" || request.ID != calls || request.Method != []string{"getGenesisHash", "getHealth"}[min(calls-1, 1)] || r.Method != "POST" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("unexpected request")
				}
				w.Header().Set("Location", "https://refused.invalid/")
				w.WriteHeader(tc.status)
				if tc.body != "" && tc.body != "unhealthy" {
					fmt.Fprint(w, tc.body)
					return
				}
				value := fixtureGenesis
				if calls == 2 {
					value = "ok"
					if tc.body == "unhealthy" {
						value = "behind"
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": calls, "result": value})
			}))
			defer server.Close()
			target, _ := url.Parse(server.URL)
			transport := rpcTransportFunc(func(r *http.Request) (*http.Response, error) {
				clone := r.Clone(r.Context())
				u := *r.URL
				u.Scheme = target.Scheme
				u.Host = target.Host
				clone.URL = &u
				return server.Client().Transport.RoundTrip(clone)
			})
			result, err := checkRPC(context.Background(), rpcInput(), transport)
			if (err == nil) != tc.ok || calls != tc.wantCalls {
				t.Fatalf("calls=%d result=%+v err=%v", calls, result, err)
			}
			if err != nil && (result != (RPCCheckResult{}) || strings.Contains(err.Error(), "secret")) {
				t.Fatal("failure leaked result")
			}
		})
	}
	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		_, err := checkRPC(ctx, rpcInput(), rpcTransportFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() }))
		if err == nil {
			t.Fatal("deadline ignored")
		}
	})
}

func TestRPCHTTPAuthorizationAndNoWrite(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(rpcInput())
	for _, tc := range []struct {
		name, method, auth, csrf, body string
		status                         int
	}{
		{"success", "POST", token, csrfToken(token), string(body), 200},
		{"unauthorized", "POST", "", "", string(body), 401},
		{"csrf", "POST", token, "", string(body), 403},
		{"method", "GET", token, "", "", 405},
		{"secret", "POST", token, csrfToken(token), `{"endpoint":"https://rpc.fased.invalid/solana","network":"disposable-fixture:v1","key":"secret"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://127.0.0.1:7331/api/v1/accounts/rpc-check", bytes.NewBufferString(tc.body))
			r.Header.Set("Origin", "http://127.0.0.1:7331")
			r.Header.Set("Content-Type", "application/json")
			if tc.auth != "" {
				r.Header.Set("Authorization", "Bearer "+tc.auth)
			}
			r.Header.Set("X-CSRF-Token", tc.csrf)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
}
