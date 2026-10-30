package onchain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/fased-ai/fased-app/internal/update"
)

// CredentialAccess borrows an owned, short-lived byte copy for a single request.
// Borrow must distinguish "rpc" from "jupiter"; Valid binds the lease to the
// selected account, network, credential versions and finite owner approval.
// Constructors confer no permission to execute or install an adapter.
type CredentialAccess struct {
	RPCOrigin string
	Network   string
	Borrow    func(context.Context, string) ([]byte, error)
	Valid     func() error
}

// ReceiptVerifier has no preparation, simulation or submission surface. Its
// private transport admits only the two read methods used by Reconcile.
type ReceiptVerifier struct{ client *Client }

func (v *ReceiptVerifier) Reconcile(ctx context.Context, q Quote, packet []byte) (Observation, error) {
	if v == nil || v.client == nil {
		return Observation{}, errors.New("receipt verifier unavailable")
	}
	return v.client.Reconcile(ctx, q, packet)
}

func NewAuthenticatedClient(access CredentialAccess) (*Client, error) {
	return authenticatedClient(access, nil, false)
}
func NewAuthenticatedVerifier(access CredentialAccess) (*ReceiptVerifier, error) {
	c, err := authenticatedClient(access, nil, true)
	if err != nil {
		return nil, err
	}
	return &ReceiptVerifier{client: c}, nil
}

// Controlled constructors accept only trusted in-process test dependencies.
// There is no settings, endpoint, CLI or CA override for ordinary instances.
func NewControlledAuthenticatedClient(rt ControlledTransport, access CredentialAccess) (*Client, error) {
	if rt == nil || !rt.ControlledFixture() {
		return nil, errors.New("controlled transport required")
	}
	return authenticatedClient(access, rt, false)
}
func NewControlledAuthenticatedVerifier(rt ControlledTransport, access CredentialAccess) (*ReceiptVerifier, error) {
	if rt == nil || !rt.ControlledFixture() {
		return nil, errors.New("controlled transport required")
	}
	c, err := authenticatedClient(access, rt, true)
	if err != nil {
		return nil, err
	}
	return &ReceiptVerifier{client: c}, nil
}
func authenticatedClient(access CredentialAccess, next http.RoundTripper, readOnly bool) (*Client, error) {
	provider := credentialRPCProvider(access.RPCOrigin, access.Network)
	if provider == "" || access.Borrow == nil || access.Valid == nil || access.Valid() != nil {
		return nil, errors.New("swap credential access unavailable")
	}
	c, err := NewClient(access.RPCOrigin, "")
	if err != nil {
		return nil, errors.New("swap credential access unavailable")
	}
	if next == nil {
		next = c.http.Transport
	}
	c.network = access.Network
	c.valid = access.Valid
	c.http.Transport = &credentialTransport{access: access, provider: provider, next: next, readOnly: readOnly}
	return c, nil
}
func (c *Client) admitNetwork(q Quote) error {
	if c.network != "" && (q.Network != c.network || c.valid == nil || c.valid() != nil) {
		return errors.New("swap network or credential access unavailable")
	}
	return nil
}
func credentialRPCProvider(origin, network string) string {
	var prefix string
	switch network {
	case "mainnet-beta":
		prefix = "mainnet"
	case "devnet":
		prefix = "devnet"
	default:
		return ""
	}
	switch origin {
	case "https://" + prefix + ".helius-rpc.com":
		return "helius"
	case "https://solana-" + prefix + ".g.alchemy.com":
		return "alchemy"
	}
	return ""
}

type credentialTransport struct {
	access   CredentialAccess
	provider string
	next     http.RoundTripper
	readOnly bool
}

func credentialError() error { return errors.New("swap credential request unavailable") }
func (t *credentialTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r != nil && r.Body != nil {
		defer r.Body.Close()
	}
	if t == nil || t.next == nil || t.access.Valid == nil || t.access.Valid() != nil || r == nil || r.URL == nil || r.URL.Scheme != "https" || r.URL.User != nil || r.URL.Fragment != "" || r.URL.Opaque != "" || r.URL.RawPath != "" || r.URL.ForceQuery || r.Host != r.URL.Host || r.RequestURI != "" || len(r.URL.String()) > 4096 {
		return nil, credentialError()
	}
	for name := range r.Header {
		switch strings.ToLower(name) {
		case "authorization", "proxy-authorization", "cookie", "x-api-key":
			return nil, credentialError()
		}
	}
	request := r.Clone(r.Context())
	u := *r.URL
	request.URL = &u
	purpose := ""
	switch {
	case r.URL.String() == t.access.RPCOrigin && r.Method == http.MethodPost:
		if r.Body == nil {
			return nil, credentialError()
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 512*1024+1))
		if err != nil || len(body) > 512*1024 {
			return nil, credentialError()
		}
		var call struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if update.StrictJSONBounded(body, &call, 512*1024) != nil || call.JSONRPC != "2.0" || len(call.ID) == 0 || len(call.Params) == 0 || !allowedCredentialRPC(call.Method, t.readOnly) {
			return nil, credentialError()
		}
		request.Body = io.NopCloser(bytes.NewReader(body))
		request.ContentLength = int64(len(body))
		request.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
		purpose = "rpc"
	case !t.readOnly && r.URL.Scheme+"://"+r.URL.Host+r.URL.Path == buildURL && r.Method == http.MethodGet:
		if r.Body != nil && r.Body != http.NoBody {
			return nil, credentialError()
		}
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(values) != 8 {
			return nil, credentialError()
		}
		for name, entries := range values {
			switch name {
			case "inputMint", "outputMint", "amount", "taker", "slippageBps", "wrapAndUnwrapSol", "maxAccounts", "transactionVersion":
			default:
				return nil, credentialError()
			}
			if len(entries) != 1 || entries[0] == "" {
				return nil, credentialError()
			}
		}
		purpose = "jupiter"
	default:
		return nil, credentialError()
	}
	key, err := t.access.Borrow(r.Context(), purpose)
	defer clear(key)
	if err != nil || !validCredentialBytes(purpose, key) || t.access.Valid() != nil {
		return nil, credentialError()
	}
	if purpose == "jupiter" {
		request.Header.Set("x-api-key", string(key))
	} else {
		switch t.provider {
		case "helius":
			u.Path = "/"
			u.RawQuery = url.Values{"api-key": {string(key)}}.Encode()
		case "alchemy":
			u.Path = "/v2/" + string(key)
		default:
			return nil, credentialError()
		}
	}
	// The network transport sees credentials only after exact-origin admission.
	// Keep its request immutable through response-body closure. Only the clean
	// original is returned to callers; raw transport errors never cross this layer.
	response, err := t.next.RoundTrip(request)
	if err != nil || response == nil || response.Body == nil || t.access.Valid() != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, credentialError()
	}
	response.Request = r
	response.Body = &credentialResponseBody{ReadCloser: response.Body, valid: t.access.Valid}
	return response, nil
}
func validCredentialBytes(purpose string, key []byte) bool {
	if len(key) == 0 || len(key) > 512 {
		return false
	}
	if purpose == "rpc" && (len(key) < 24 || len(key) > 256) {
		return false
	}
	for _, b := range key {
		if b < 33 || b > 126 {
			return false
		}
		if purpose == "rpc" && !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
			return false
		}
	}
	return true
}
func allowedCredentialRPC(method string, readOnly bool) bool {
	switch method {
	case "getSignatureStatuses", "getTransaction":
		return true
	case "getGenesisHash", "getLatestBlockhash", "getAccountInfo", "getMinimumBalanceForRentExemption", "getFeeForMessage", "getBalance", "simulateTransaction", "getBlockHeight", "sendTransaction":
		return !readOnly
	}
	return false
}

type credentialResponseBody struct {
	io.ReadCloser
	valid func() error
}

func (b *credentialResponseBody) Read(p []byte) (int, error) {
	if b.valid() == nil {
		n, err := b.ReadCloser.Read(p)
		if b.valid() == nil {
			if err != nil && err != io.EOF {
				clear(p[:n])
				return 0, credentialError()
			}
			return n, err
		}
		clear(p[:n])
	}
	return 0, credentialError()
}
