package core

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// No runtime URL/CA/transport override exists. Production always uses the
// compiled inert responder; controlled HTTPS is injected only by Go tests.
func checkCredentialRPC(ctx context.Context, in RPCCheckRequest, key []byte, valid func() error) (RPCCheckResult, error) {
	return checkAuthenticatedRPC(ctx, in, key, valid, authenticatedRPCFixture{})
}
func checkAuthenticatedRPC(ctx context.Context, in RPCCheckRequest, key []byte, valid func() error, rt http.RoundTripper) (RPCCheckResult, error) {
	if !testRPCCredential(string(key)) || valid == nil || rt == nil {
		return RPCCheckResult{}, errors.New("RPC credential refused")
	}
	return checkRPC(ctx, in, &rpcCredentialTransport{key: key, valid: valid, next: rt})
}

type rpcCredentialTransport struct {
	key   []byte
	valid func() error
	next  http.RoundTripper
}

func (t *rpcCredentialTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.String() != FixtureRPCURL || r.Method != http.MethodPost || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || t.valid() != nil {
		return nil, errors.New("RPC request refused")
	}
	request := r.Clone(r.Context())
	request.Header.Set("Authorization", "Bearer "+string(t.key))
	defer request.Header.Del("Authorization")
	return t.next.RoundTrip(request)
}

type authenticatedRPCFixture struct{}

func (authenticatedRPCFixture) RoundTrip(r *http.Request) (*http.Response, error) {
	if !testRPCCredential(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return nil, errors.New("RPC authentication refused")
	}
	clean := r.Clone(r.Context())
	clean.Header.Del("Authorization")
	return (fixtureRPCTransport{}).RoundTrip(clean)
}

type rpcHTTPSFixture struct {
	origin    *url.URL
	transport *http.Transport
}

func newRPCHTTPSFixture(endpoint string, roots *x509.CertPool) (*rpcHTTPSFixture, error) {
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Port() == "" || roots == nil || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		return nil, errors.New("trusted loopback HTTPS fixture required")
	}
	return &rpcHTTPSFixture{origin: u, transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots.Clone()}, DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 8192, TLSHandshakeTimeout: time.Second, ResponseHeaderTimeout: 2 * time.Second}}, nil
}
func (t *rpcHTTPSFixture) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.String() != FixtureRPCURL || r.Method != http.MethodPost || r.Header.Get("Cookie") != "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || !testRPCCredential(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) {
		return nil, errors.New("RPC fixture request refused")
	}
	mapped := r.Clone(r.Context())
	defer mapped.Header.Del("Authorization")
	u := *r.URL
	u.Scheme = t.origin.Scheme
	u.Host = t.origin.Host
	mapped.URL = &u
	mapped.Host = t.origin.Host
	return t.transport.RoundTrip(mapped)
}
