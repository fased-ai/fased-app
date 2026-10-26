package core

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Canonical cluster identities: Solana validator cluster documentation.
// Genesis proves the endpoint's reported cluster, not provider honesty or finality.
func solanaGenesis(network string) string {
	switch network {
	case "mainnet-beta":
		return "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d"
	case "devnet":
		return "EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG"
	default:
		return ""
	}
}

// Credential-bearing URLs are never public account metadata. Provider-specific
// secret routing needs separate encrypted admission; no query/path token here.
func publicRPCURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || strings.ContainsAny(raw, "?#") || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Host == "" || u.Host != u.Hostname() || u.Port() != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || u.String() != raw {
		return nil, errors.New("RPC endpoint refused")
	}
	host := u.Hostname()
	if host != strings.ToLower(host) || len(host) > 253 || !strings.Contains(host, ".") || net.ParseIP(host) != nil {
		return nil, errors.New("RPC host refused")
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".invalid", ".test", ".example", ".onion"} {
		if strings.HasSuffix(host, suffix) {
			return nil, errors.New("RPC host refused")
		}
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return nil, errors.New("RPC host refused")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return nil, errors.New("RPC host refused")
			}
		}
	}
	return u, nil
}

type rpcLookup func(context.Context, string, string) ([]netip.Addr, error)
type rpcDial func(context.Context, string, string) (net.Conn, error)

// Resolve once per connection, reject the entire answer set if any IP is not
// public, then dial a checked numeric address. TLS still verifies the DNS name.
func dialPublicRPC(ctx context.Context, network, address string, lookup rpcLookup, dial rpcDial) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port != "443" || network != "tcp" {
		return nil, errors.New("RPC destination refused")
	}
	if _, err = publicRPCURL("https://" + host); err != nil {
		return nil, err
	}
	ips, err := lookup(ctx, "ip", host)
	if err != nil || len(ips) == 0 || len(ips) > 32 {
		return nil, errors.New("RPC DNS unavailable")
	}
	for _, ip := range ips {
		if !newsPublicIP(ip) {
			return nil, errors.New("RPC destination refused")
		}
	}
	conn, err := dial(ctx, network, net.JoinHostPort(ips[0].Unmap().String(), port))
	if err != nil {
		return nil, errors.New("RPC connection failed")
	}
	return conn, nil
}

type publicRPCTransport struct {
	enabled   bool // Deliberately false in every ordinary constructor. Tests only.
	transport *http.Transport
}

func newPublicRPCTransport() *publicRPCTransport {
	d := &net.Dialer{Timeout: time.Second}
	return &publicRPCTransport{transport: &http.Transport{
		Proxy: nil, DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 8192,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: time.Second, ResponseHeaderTimeout: 2 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialPublicRPC(ctx, network, address, net.DefaultResolver.LookupNetIP, d.DialContext)
		},
	}}
}
func (t *publicRPCTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !t.enabled {
		return nil, errors.New("live RPC disabled")
	}
	if _, err := publicRPCURL(r.URL.String()); err != nil || r.Method != http.MethodPost || r.Host != r.URL.Host || len(r.Header.Values("Authorization")) != 0 || len(r.Header.Values("Cookie")) != 0 {
		return nil, errors.New("RPC request refused")
	}
	return t.transport.RoundTrip(r)
}
func checkPublicRPC(ctx context.Context, in RPCCheckRequest, t *publicRPCTransport) (RPCCheckResult, error) {
	genesis := solanaGenesis(in.Network)
	if _, err := publicRPCURL(in.Endpoint); err != nil || genesis == "" || t == nil || !t.enabled {
		return RPCCheckResult{}, errors.New("live RPC disabled or endpoint unavailable")
	}
	return checkRPCProtocol(ctx, in, t, genesis, false)
}
