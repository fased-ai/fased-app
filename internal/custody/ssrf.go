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

var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

// IsPublicUnicastIP returns true strictly for routable public IPv4/IPv6 addresses.
// Rejects loopback, RFC1918 private, CGNAT (100.64.0.0/10), link-local/metadata,
// multicast, unspecified, and IPv4-in-IPv6 private encapsulations.
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
	if ip.Is4() && cgnatPrefix.Contains(ip) {
		return false
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
	for _, raw := range ips {
		ip := raw.Unmap()
		if ip.IsLoopback() {
			if !allowLoopback {
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
	for _, target := range targets {
		if len(targets) > 6 {
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
