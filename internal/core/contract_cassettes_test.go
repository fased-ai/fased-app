package core

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func cassettePath(name string) string {
	return filepath.Join("testdata", "cassettes", name)
}

func TestContractCassettesPlayback(t *testing.T) {
	t.Parallel()

	t.Run("openai oidc discovery cassette", func(t *testing.T) {
		raw, err := os.ReadFile(cassettePath("openai-oidc-discovery.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !uniqueAIJSON(raw, 65536) {
			t.Fatal("openai discovery cassette contains duplicate keys or invalid JSON")
		}
		rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != signInIssuer+"/.well-known/openid-configuration" || r.Method != http.MethodGet {
				t.Fatalf("unexpected discovery request: %s %s", r.Method, r.URL.String())
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
				Body:       io.NopCloser(bytes.NewReader(raw)),
				Request:    r,
			}, nil
		})
		d, err := readSignInDiscovery(t.Context(), rt)
		if err != nil {
			t.Fatalf("readSignInDiscovery rejected cassette: %v", err)
		}
		if d.Issuer != signInIssuer || d.JWKS != "https://auth.openai.com/.well-known/jwks.json" || d.Revoke != "https://auth.openai.com/oauth/revoke" {
			t.Fatalf("unexpected discovery fields: %+v", d)
		}
	})

	t.Run("openai responses sse cassette", func(t *testing.T) {
		raw, err := os.ReadFile(cassettePath("openai-responses-sse.txt"))
		if err != nil {
			t.Fatal(err)
		}
		var deltas []string
		completed, err := readAIStream(bytes.NewReader(raw), "gpt-5.4", func(partial string) {
			deltas = append(deltas, partial)
		}, true)
		if err != nil {
			t.Fatalf("readAIStream failed on cassette: %v", err)
		}
		if len(deltas) == 0 || deltas[len(deltas)-1] != "Verified cassette replay." {
			t.Fatalf("unexpected partial deltas from cassette: %v", deltas)
		}
		var env struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Usage  struct {
				TotalTokens int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(completed, &env); err != nil {
			t.Fatal(err)
		}
		if env.ID != "resp_cassette_01" || env.Status != "completed" || env.Usage.TotalTokens != 142 {
			t.Fatalf("unexpected completed envelope: %+v", env)
		}
	})

	t.Run("google oidc discovery cassette", func(t *testing.T) {
		raw, err := os.ReadFile(cassettePath("google-oidc-discovery.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !uniqueAIJSON(raw, 32768) {
			t.Fatal("google discovery cassette invalid")
		}
		var doc struct {
			Issuer string `json:"issuer"`
			Token  string `json:"token_endpoint"`
			JWKS   string `json:"jwks_uri"`
			Revoke string `json:"revocation_endpoint"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Issuer != googleIssuer || doc.Token != googleTokenURL || doc.JWKS != googleCertsURL || doc.Revoke != googleRevokeURL {
			t.Fatalf("google discovery cassette drifted from runtime constants: %+v", doc)
		}
	})

	t.Run("solana mainnet rpc batch cassette", func(t *testing.T) {
		raw, err := os.ReadFile(cassettePath("solana-mainnet-rpc-batch.json"))
		if err != nil {
			t.Fatal(err)
		}
		var entries []json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil {
			t.Fatal(err)
		}
		if len(entries) != 5 {
			t.Fatalf("expected 5 JSON-RPC cassette frames, got %d", len(entries))
		}
		byID := map[int]json.RawMessage{}
		for _, entry := range entries {
			var header struct {
				ID int `json:"id"`
			}
			if err := json.Unmarshal(entry, &header); err != nil {
				t.Fatal(err)
			}
			byID[header.ID] = entry
		}
		rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
			var req struct {
				ID int `json:"id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				return nil, err
			}
			frame, ok := byID[req.ID]
			if !ok {
				t.Fatalf("unexpected JSON-RPC request id=%d", req.ID)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewReader(frame)),
				Request:    r,
			}, nil
		})
		const ownerAddr = "9xQeWvG816bUx9EPjHmaT23yvVM2ZWbrrpZb9PusVFin"
		snap, err := walletRPC(
			context.Background(),
			"https://api.mainnet-beta.solana.com",
			rt,
			func() error { return nil },
			false,
			ownerAddr,
			"mainnet-beta",
			nil,
		)
		if err != nil {
			t.Fatalf("walletRPC failed on mainnet cassette: %v", err)
		}
		if snap.Lamports != "2500000000" || snap.Slot != "318004200" || len(snap.Tokens) != 1 || snap.Tokens[0].Amount != "150000000" || len(snap.Activity) != 1 {
			t.Fatalf("unexpected walletRPC snapshot: %+v", snap)
		}
		if !strings.HasPrefix(snap.Activity[0].Signature, "5VER") {
			t.Fatalf("unexpected activity signature: %+v", snap.Activity[0])
		}
	})
}
