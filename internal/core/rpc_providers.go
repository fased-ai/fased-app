package core

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// Public settings contain only a compiled provider origin, never its API key.
func rpcProvider(endpoint, network string) string {
	prefix := ""
	switch network {
	case "mainnet-beta":
		prefix = "mainnet"
	case "devnet":
		prefix = "devnet"
	default:
		return ""
	}
	switch endpoint {
	case "https://" + prefix + ".helius-rpc.com":
		return "helius"
	case "https://solana-" + prefix + ".g.alchemy.com":
		return "alchemy"
	}
	return ""
}
func validRPCSecret(version int, key string) bool {
	if version == 1 {
		return testRPCCredential(key)
	}
	if version != 2 || len(key) < 24 || len(key) > 256 || strings.HasPrefix(key, "fased-rpc-test-") {
		return false
	}
	for _, c := range key {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func validProviderRPC(a Account) bool {
	return (a.Version == 2 && a.Mode == "watch" && a.Signer == nil || (managedNetworkAccount(a) || ordinaryNetworkAccount(a))) && solanaGenesis(a.Network) != "" && a.RPC != nil && a.RPC.Version == 2 && a.RPC.Network == a.Network && rpcProvider(a.RPC.Endpoint, a.Network) != ""
}

type providerRPCTransport struct {
	origin, provider string
	key              []byte
	valid            func() error
	next             *publicRPCTransport
}

func (t *providerRPCTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.next == nil || !t.next.enabled || t.valid == nil || t.valid() != nil || r.URL.String() != t.origin || r.Host != r.URL.Host || r.Method != http.MethodPost || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
		return nil, errors.New("RPC request refused")
	}
	request := r.Clone(r.Context())
	u := *r.URL
	request.URL = &u
	switch t.provider {
	case "helius":
		u.Path = "/"
		u.RawQuery = url.Values{"api-key": {string(t.key)}}.Encode()
	case "alchemy":
		u.Path = "/v2/" + string(t.key)
	default:
		return nil, errors.New("RPC provider refused")
	}
	// Only this adapter can add secrets after origin admission. The underlying
	// transport retains pinned public-IP dialing/TLS; never return its raw URL errors.
	response, err := t.next.transport.RoundTrip(request)
	if err != nil {
		return nil, errors.New("RPC connection failed")
	}
	response.Request = r
	return response, nil
}
func checkProviderRPC(ctx context.Context, in RPCCheckRequest, key []byte, valid func() error, t *publicRPCTransport) (RPCCheckResult, error) {
	provider := rpcProvider(in.Endpoint, in.Network)
	if provider == "" || !validRPCSecret(2, string(key)) || valid == nil || t == nil || !t.enabled {
		return RPCCheckResult{}, errors.New("RPC provider unavailable")
	}
	return checkRPCProtocol(ctx, in, &providerRPCTransport{origin: in.Endpoint, provider: provider, key: key, valid: valid, next: t}, solanaGenesis(in.Network), false)
}
