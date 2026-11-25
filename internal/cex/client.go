package cex

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const maxResponseBytes = 512 * 1024

var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

// Client is an SSRF-hardened TLS 1.2+ HTTP client scoped exclusively to sandbox/testnet
// CEX origins while CEXActivationEnabled is false.
type Client struct {
	http *http.Client
}

func isPublicUnicastIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast() && !ip.IsUnspecified() &&
		!cgnatPrefix.Contains(ip)
}

func interleavePublicIPs(ips []netip.Addr) ([]netip.Addr, error) {
	if len(ips) == 0 {
		return nil, errors.New("cex upstream address unavailable")
	}
	var v4, v6 []netip.Addr
	for _, ip := range ips {
		if !isPublicUnicastIP(ip) {
			return nil, errors.New("private or loopback cex address denied")
		}
		if ip.Is4() {
			v4 = append(v4, ip)
		} else {
			v6 = append(v6, ip)
		}
	}
	out := make([]netip.Addr, 0, min(len(ips), 6))
	for i := 0; len(out) < 6 && (i < len(v4) || i < len(v6)); i++ {
		if i < len(v4) {
			out = append(out, v4[i])
		}
		if len(out) < 6 && i < len(v6) {
			out = append(out, v6[i])
		}
	}
	return out, nil
}

func dialPublicAddress(
	ctx context.Context,
	network, address string,
	lookup func(context.Context, string, string) ([]netip.Addr, error),
	dial func(context.Context, string, string) (net.Conn, error),
) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	rawIPs, err := lookup(ctx, "ip", host)
	if err != nil {
		return nil, errors.New("cex upstream address unavailable")
	}
	targets, err := interleavePublicIPs(rawIPs)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, ip := range targets {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		conn, dErr := dial(attemptCtx, network, net.JoinHostPort(ip.String(), port))
		cancel()
		if dErr == nil {
			return conn, nil
		}
		lastErr = dErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("cex upstream address unavailable")
}

// NewClient constructs a hardened TLS 1.2+ client with SSRF private-IP filtering and disabled redirects.
func NewClient() *Client {
	dialer := &net.Dialer{Timeout: 4 * time.Second}
	resolver := net.DefaultResolver
	t := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
		Proxy:                 nil,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialPublicAddress(ctx, network, address, resolver.LookupNetIP, dialer.DialContext)
		},
	}
	return &Client{
		http: &http.Client{
			Timeout:   8 * time.Second,
			Transport: t,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// ValidateTestnetURL ensures requests stay pinned to the configured venue testnet/sandbox origin.
func ValidateTestnetURL(venue, rawURL string) error {
	spec, ok := LookupVenueSpec(venue)
	if !ok {
		return errors.New("unsupported cex venue")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return errors.New("invalid cex url")
	}
	if "https://"+strings.ToLower(u.Host) != spec.TestnetOrigin {
		return errors.New("untrusted or non-testnet cex origin denied")
	}
	return nil
}

// ReadBoundedBody reads at most maxResponseBytes from an HTTP response body.
func ReadBoundedBody(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, maxResponseBytes))
}
