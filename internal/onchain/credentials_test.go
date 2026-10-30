package onchain_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/onchaintest"
)

type authenticatedHTTPS struct {
	target *url.URL
	next   http.RoundTripper
}

func (*authenticatedHTTPS) ControlledFixture() bool { return true }
func (t *authenticatedHTTPS) RoundTrip(r *http.Request) (*http.Response, error) {
	request := r.Clone(r.Context())
	u := *r.URL
	request.URL = &u
	u.Scheme = t.target.Scheme
	u.Host = t.target.Host
	return t.next.RoundTrip(request)
}
func authenticatedFixture(t *testing.T, origin string, chain *onchaintest.Chain) (onchain.ControlledTransport, onchain.CredentialAccess, func()) {
	t.Helper()
	var mu sync.Mutex
	var borrowed [][]byte
	var purposes []string
	https := onchaintest.NewHTTPS(t, origin)
	// Keep the caller-provided chain so receipt mutations exercise this journey.
	https.Chain = chain
	access := onchain.CredentialAccess{RPCOrigin: origin, Network: "mainnet-beta", Valid: func() error { return nil }, Borrow: func(_ context.Context, purpose string) ([]byte, error) {
		var key []byte
		switch purpose {
		case "rpc":
			key = []byte(https.RPCKey)
		case "jupiter":
			key = []byte(https.JupiterKey)
		default:
			return nil, errors.New("purpose refused")
		}
		mu.Lock()
		borrowed = append(borrowed, key)
		purposes = append(purposes, purpose)
		mu.Unlock()
		return key, nil
	}}
	cleared := func() {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		for _, key := range borrowed {
			for _, b := range key {
				if b != 0 {
					t.Fatal("borrowed credential retained")
				}
			}
		}
		if len(purposes) == 0 {
			t.Fatal("no scoped borrowing")
		}
	}
	return https.Transport, access, cleared
}
func TestAuthenticatedHTTPSJourney(t *testing.T) {
	for _, origin := range []string{"https://mainnet.helius-rpc.com", "https://solana-mainnet.g.alchemy.com"} {
		t.Run(origin, func(t *testing.T) {
			chain, _, q, key := fixture(t)
			transport, access, cleared := authenticatedFixture(t, origin, chain)
			client, err := onchain.NewControlledAuthenticatedClient(transport, access)
			if err != nil {
				t.Fatal(err)
			}
			verifier, err := onchain.NewControlledAuthenticatedVerifier(transport, access)
			if err != nil {
				t.Fatal(err)
			}
			q, err = client.Prepare(t.Context(), q)
			if err != nil {
				t.Fatal(err)
			}
			message, err := onchain.Message(q)
			if err != nil {
				t.Fatal(err)
			}
			packet, err := onchain.Signed(q, ed25519.Sign(key, message))
			if err != nil {
				t.Fatal(err)
			}
			attempted, err := client.Send(t.Context(), q, packet)
			if err != nil || !attempted {
				t.Fatal(attempted, err)
			}
			observation, err := verifier.Reconcile(t.Context(), q, packet)
			if err != nil || observation.State != "confirmed" || observation.Input != q.Amount || observation.Output != q.Output || chain.Submissions != 1 {
				t.Fatal(observation, err)
			}
			if reflect.TypeOf(verifier).NumMethod() != 1 {
				t.Fatal("verifier exposes a write surface")
			}
			wrong := q
			wrong.Network = "devnet"
			if _, err = verifier.Reconcile(t.Context(), wrong, packet); err == nil {
				t.Fatal("cross-network receipt accepted")
			}
			changed := append([]byte(nil), packet...)
			changed[len(changed)-1] ^= 1
			if _, err = verifier.Reconcile(t.Context(), q, changed); err == nil {
				t.Fatal("altered original accepted")
			}
			chain.SettlementMode = "missing-fee"
			if _, err = verifier.Reconcile(t.Context(), q, packet); err == nil {
				t.Fatal("altered receipt accepted")
			}
			cleared()
		})
	}
}

func TestAuthenticatedHTTPSRejectsRedirectAndSecretEcho(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusUnauthorized, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "https://must-not-connect.invalid/?secret=echo")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":"disposable-rpc-secret-00000000","duplicate":1,"duplicate":2}`)
			}))
			defer server.Close()
			target, _ := url.Parse(server.URL)
			access := onchain.CredentialAccess{RPCOrigin: "https://mainnet.helius-rpc.com", Network: "mainnet-beta", Valid: func() error { return nil }, Borrow: func(context.Context, string) ([]byte, error) { return []byte("disposable-rpc-secret-00000000"), nil }}
			client, err := onchain.NewControlledAuthenticatedClient(&authenticatedHTTPS{target: target, next: server.Client().Transport}, access)
			if err != nil {
				t.Fatal(err)
			}
			_, _, q, _ := fixture(t)
			if _, err = client.Prepare(t.Context(), q); err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "echo") {
				t.Fatal("redirect, error or ambiguous body exposed")
			}
			if calls != 1 {
				t.Fatal("redirect followed or implicit retry")
			}
		})
	}
}
