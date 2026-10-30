package onchain

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type scopedTransportFunc func(*http.Request) (*http.Response, error)

func (f scopedTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func (scopedTransportFunc) ControlledFixture() bool                             { return true }
func scopedAccess() CredentialAccess {
	return CredentialAccess{RPCOrigin: "https://mainnet.helius-rpc.com", Network: "mainnet-beta", Borrow: func(context.Context, string) ([]byte, error) { return []byte(strings.Repeat("s", 32)), nil }, Valid: func() error { return nil }}
}
func scopedRequest(t *testing.T, origin, method string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodPost, origin, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"`+method+`","params":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func scopedResponse(r *http.Request) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":null}`)), Request: r}
}
func TestCredentialVerifierTransportFence(t *testing.T) {
	access := scopedAccess()
	calls, borrows := 0, 0
	access.Borrow = func(_ context.Context, purpose string) ([]byte, error) {
		borrows++
		if purpose != "rpc" {
			t.Fatal("verifier borrowed Jupiter credential")
		}
		return []byte(strings.Repeat("s", 32)), nil
	}
	rt := scopedTransportFunc(func(r *http.Request) (*http.Response, error) { calls++; return scopedResponse(r), nil })
	verifier, err := NewControlledAuthenticatedVerifier(rt, access)
	if err != nil {
		t.Fatal(err)
	}
	transport := verifier.client.http.Transport
	for _, method := range []string{"sendTransaction", "simulateTransaction", "getGenesisHash", "getBlockHeight", "getAccountInfo", "requestAirdrop", "sendRawTransaction", "getTransaction ", "getSignatureStatuses\",\"method\":\"sendTransaction"} {
		if response, err := transport.RoundTrip(scopedRequest(t, access.RPCOrigin, method)); err == nil {
			response.Body.Close()
			t.Fatalf("method admitted: %q", method)
		}
	}
	for _, origin := range []string{"https://solana-mainnet.g.alchemy.com", "https://mainnet.helius-rpc.com/", "https://mainnet.helius-rpc.com?api-key=secret", "https://mainnet.helius-rpc.com:443", "http://mainnet.helius-rpc.com", "https://mainnet.helius-rpc.com.evil.invalid"} {
		if response, err := transport.RoundTrip(scopedRequest(t, origin, "getTransaction")); err == nil {
			response.Body.Close()
			t.Fatal("changed origin admitted")
		}
	}
	for _, header := range []string{"Authorization", "authorization", "Proxy-Authorization", "Cookie", "x-api-key"} {
		request := scopedRequest(t, access.RPCOrigin, "getTransaction")
		request.Header[header] = []string{"secret"}
		if response, err := transport.RoundTrip(request); err == nil {
			response.Body.Close()
			t.Fatal("preauthenticated request admitted")
		}
	}
	request, err := http.NewRequest(http.MethodGet, buildURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response, err := transport.RoundTrip(request); err == nil {
		response.Body.Close()
		t.Fatal("verifier admitted Jupiter")
	}
	if calls != 0 || borrows != 0 {
		t.Fatal("denied request reached credentials or network")
	}
	for _, method := range []string{"getSignatureStatuses", "getTransaction"} {
		response, err := transport.RoundTrip(scopedRequest(t, access.RPCOrigin, method))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
	if calls != 2 || borrows != 2 {
		t.Fatal("receipt reads unavailable")
	}
}
func TestCredentialOriginAndNetworkAdmission(t *testing.T) {
	rt := scopedTransportFunc(func(r *http.Request) (*http.Response, error) { t.Fatal("constructor dispatched"); return nil, nil })
	for _, network := range []string{"mainnet-beta", "devnet"} {
		prefix := "mainnet"
		if network == "devnet" {
			prefix = "devnet"
		}
		for _, origin := range []string{"https://" + prefix + ".helius-rpc.com", "https://solana-" + prefix + ".g.alchemy.com"} {
			access := scopedAccess()
			access.Network = network
			access.RPCOrigin = origin
			client, err := NewControlledAuthenticatedClient(rt, access)
			if err != nil {
				t.Fatal(err)
			}
			wrong := Quote{Network: "testnet"}
			if _, err = client.Prepare(t.Context(), wrong); err == nil {
				t.Fatal("Prepare ignored network")
			}
			if _, err = client.Simulate(t.Context(), wrong); err == nil {
				t.Fatal("Simulate ignored network")
			}
			if _, err = client.Send(t.Context(), wrong, nil); err == nil {
				t.Fatal("Send ignored network")
			}
			if _, err = client.Reconcile(t.Context(), wrong, nil); err == nil {
				t.Fatal("Reconcile ignored network")
			}
		}
	}
	for _, origin := range []string{"https://devnet.helius-rpc.com", "https://mainnet.helius-rpc.com/", "https://mainnet.helius-rpc.com:443", "https://user:secret@mainnet.helius-rpc.com", "https://api.jup.ag", "https://127.0.0.1"} {
		access := scopedAccess()
		access.RPCOrigin = origin
		if _, err := NewAuthenticatedClient(access); err == nil {
			t.Fatal("invalid origin accepted")
		}
	}
	access := scopedAccess()
	access.Network = "testnet"
	if _, err := NewAuthenticatedVerifier(access); err == nil {
		t.Fatal("invalid network accepted")
	}
	access = scopedAccess()
	access.Borrow = nil
	if _, err := NewAuthenticatedClient(access); err == nil {
		t.Fatal("missing borrower accepted")
	}
	access = scopedAccess()
	access.Valid = nil
	if _, err := NewAuthenticatedClient(access); err == nil {
		t.Fatal("missing lease accepted")
	}
	if _, err := NewControlledAuthenticatedClient(nil, scopedAccess()); err == nil {
		t.Fatal("missing controlled transport accepted")
	}
}

type revokeReadBody struct{ valid *bool }

func (b revokeReadBody) Read(p []byte) (int, error) {
	*b.valid = false
	return copy(p, `{"secret":"not accepted"}`), io.EOF
}
func (revokeReadBody) Close() error { return nil }
func TestCredentialLeaseRevocationAndRedaction(t *testing.T) {
	for _, when := range []string{"before-borrow", "after-borrow", "after-transport", "during-body", "transport-error", "borrow-error", "good"} {
		t.Run(when, func(t *testing.T) {
			access := scopedAccess()
			valid := true
			calls, borrows := 0, 0
			var key []byte
			var returned *http.Response
			access.Valid = func() error {
				if !valid {
					return errors.New("private lease detail")
				}
				return nil
			}
			access.Borrow = func(context.Context, string) ([]byte, error) {
				borrows++
				key = []byte(strings.Repeat("s", 32))
				if when == "after-borrow" {
					valid = false
				}
				if when == "borrow-error" {
					return key, errors.New("private key detail")
				}
				return key, nil
			}
			rt := scopedTransportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Query().Get("api-key") != string(key) {
					t.Fatal("missing provider auth")
				}
				if when == "transport-error" {
					return nil, errors.New("private URL " + r.URL.String())
				}
				response := scopedResponse(r)
				if when == "after-transport" {
					valid = false
				}
				if when == "during-body" {
					response.Body = revokeReadBody{&valid}
				}
				returned = response
				return response, nil
			})
			client, err := NewControlledAuthenticatedClient(rt, access)
			if err != nil {
				t.Fatal(err)
			}
			if when == "before-borrow" {
				valid = false
			}
			var out any
			err = client.rpcCall(t.Context(), "getTransaction", []any{}, &out)
			if when == "good" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("lease/failure accepted")
			} else if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), strings.Repeat("s", 32)) {
				t.Fatal("private failure detail leaked")
			}
			for _, b := range key {
				if b != 0 {
					t.Fatal("credential not cleared")
				}
			}
			if when == "before-borrow" && (borrows != 0 || calls != 0) {
				t.Fatal("revoked lease dispatched")
			}
			if (when == "after-borrow" || when == "borrow-error") && calls != 0 {
				t.Fatal("revoked or failed borrow dispatched")
			}
			if when == "good" && returned != nil && (returned.Request.URL.String() != access.RPCOrigin || returned.Request.Header.Get("x-api-key") != "") {
				t.Fatal("credential leaked in returned request")
			}
		})
	}
}
