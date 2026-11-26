package custody

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

var deniedPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "This" network (RFC 1122)
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT / Tailscale (RFC 6598)
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF Protocol Assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("198.18.0.0/15"),   // Benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),     // Reserved
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64 well-known prefix
	netip.MustParsePrefix("2001:db8::/32"),   // Documentation
	netip.MustParsePrefix("2001::/32"),       // Teredo
	netip.MustParsePrefix("2002::/16"),       // 6to4
}

// IsPublicUnicastIP returns true strictly for routable public IPv4/IPv6 addresses.
// Rejects loopback, RFC1918 private, CGNAT (100.64.0.0/10), link-local/metadata,
// multicast, unspecified, reserved/NAT64/documentation, and IPv4-in-IPv6 encapsulations.
func IsPublicUnicastIP(raw netip.Addr) bool {
	if !raw.IsValid() {
		return false
	}
	ip := raw.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, p := range deniedPublicPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

func isLoopbackHostname(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

func isLoopbackURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return isLoopbackHostname(u.Hostname())
}

// NewSafeTransport constructs an HTTP transport that disables proxy env overrides,
// forbids redirects at the client layer, and checks DNS resolution against SSRF
// at dial time before opening any TCP socket.
func NewSafeTransport(allowLoopback bool) *http.Transport {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &http.Transport{
		Proxy:               nil,
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion:       tls.VersionTLS12,
			CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384},
		},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialSSRFSafe(ctx, network, address, allowLoopback, net.DefaultResolver.LookupNetIP, dialer.DialContext)
		},
	}
}

func dialSSRFSafe(
	ctx context.Context,
	network, address string,
	allowLoopback bool,
	lookup func(context.Context, string, string) ([]netip.Addr, error),
	dial func(context.Context, string, string) (net.Conn, error),
) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, errors.New("unsupported network")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return nil, errors.New("invalid network destination")
	}
	if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
		ip = ip.Unmap()
		if ip.IsLoopback() {
			if !allowLoopback {
				return nil, errors.New("SSRF blocked: loopback address denied")
			}
			return dial(ctx, network, net.JoinHostPort(ip.String(), port))
		}
		if !IsPublicUnicastIP(ip) {
			return nil, errors.New("SSRF blocked: non-public IP denied")
		}
		return dial(ctx, network, net.JoinHostPort(ip.String(), port))
	}

	ips, err := lookup(ctx, "ip", host)
	if err != nil || len(ips) == 0 || len(ips) > 32 {
		return nil, errors.New("SSRF guard: DNS resolution unavailable")
	}
	targets := make([]netip.Addr, 0, len(ips))
	literalLoopback := isLoopbackHostname(host)
	for _, raw := range ips {
		ip := raw.Unmap()
		if ip.IsLoopback() {
			if !allowLoopback || !literalLoopback {
				return nil, errors.New("SSRF blocked: DNS resolved to loopback")
			}
			targets = append(targets, ip)
			continue
		}
		if !IsPublicUnicastIP(ip) {
			return nil, errors.New("SSRF blocked: DNS resolved to non-public IP")
		}
		targets = append(targets, ip)
	}
	var lastErr error
	for i, target := range targets {
		if i >= 6 {
			break
		}
		conn, dErr := dial(ctx, network, net.JoinHostPort(target.String(), port))
		if dErr == nil {
			return conn, nil
		}
		lastErr = dErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("connection failed")
}
