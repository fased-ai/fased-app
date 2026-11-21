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

const maxPublicRPCDialTargets = 6

func orderPublicRPCTargets(ips []netip.Addr) []netip.Addr {
	var v4, v6 []netip.Addr
	seen := make(map[netip.Addr]struct{}, len(ips))
	for _, raw := range ips {
		ip := raw.Unmap()
		if _, ok := seen[ip]; ok {
			continue
		}
		seen[ip] = struct{}{}
		if ip.Is4() {
			v4 = append(v4, ip)
		} else if ip.Is6() {
			v6 = append(v6, ip)
		}
	}
	first, second := v4, v6
	if len(ips) > 0 && ips[0].Unmap().Is6() {
		first, second = v6, v4
	}
	targets := make([]netip.Addr, 0, min(len(first)+len(second), maxPublicRPCDialTargets))
	for i := 0; len(targets) < maxPublicRPCDialTargets && (i < len(first) || i < len(second)); i++ {
		if i < len(first) {
			targets = append(targets, first[i])
		}
		if len(targets) < maxPublicRPCDialTargets && i < len(second) {
			targets = append(targets, second[i])
		}
	}
	return targets
}

// Resolve once per connection, reject the entire answer set if any IP is not
// public, then dial checked numeric addresses across IPv4/IPv6. TLS still verifies the DNS name.
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
	targets := orderPublicRPCTargets(ips)
	for _, ip := range targets {
		if ctx.Err() != nil {
			break
		}
		attemptCtx := ctx
		var cancel context.CancelFunc
		if len(targets) > 1 {
			attemptCtx, cancel = context.WithTimeout(ctx, 2*time.Second)
		}
		conn, dialErr := dial(attemptCtx, network, net.JoinHostPort(ip.String(), port))
		if cancel != nil {
			cancel()
		}
		if dialErr == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, errors.New("RPC connection failed")
}

// Live read-only Solana RPC admission and official keyless public endpoints are
// enabled for verified balance, token and activity reads. Wallet signing,
// financial execution, swaps/venues and mining remain closed.
const rpcActivationEnabled = true

type publicRPCTransport struct {
	enabled   bool
	transport *http.Transport
}

func newPublicRPCTransport() *publicRPCTransport {
	d := &net.Dialer{Timeout: 5 * time.Second}
	return &publicRPCTransport{
		enabled: rpcActivationEnabled,
		transport: &http.Transport{
			Proxy: nil, DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 8192,
			// Use standard TLS 1.3/1.2 elliptic curves so public Solana Mainnet RPC proxies
			// that reject Go's default hybrid PQ key-share ClientHello negotiate cleanly.
			TLSClientConfig: &tls.Config{
				MinVersion:       tls.VersionTLS12,
				CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384},
			},
			TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				return dialPublicRPC(ctx, network, address, net.DefaultResolver.LookupNetIP, d.DialContext)
			},
		},
	}
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
		return RPCCheckResult{}, errors.New("only the disposable RPC endpoint and network are available")
	}
	return checkRPCProtocol(ctx, in, t, genesis, false)
}
