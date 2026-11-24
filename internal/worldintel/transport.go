package worldintel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/news"
)

const defaultFetchTimeout = 10 * time.Second

func interleavePublicIPs(addrs []net.IPAddr) []netip.Addr {
	var v4s, v6s []netip.Addr
	for _, raw := range addrs {
		parsed, ok := netip.AddrFromSlice(raw.IP)
		if !ok {
			continue
		}
		addr := parsed.Unmap()
		if !news.PublicIP(addr) {
			continue
		}
		if addr.Is4() {
			v4s = append(v4s, addr)
		} else {
			v6s = append(v6s, addr)
		}
	}
	out := make([]netip.Addr, 0, len(v4s)+len(v6s))
	for i := 0; i < len(v4s) || i < len(v6s); i++ {
		if i < len(v4s) {
			out = append(out, v4s[i])
		}
		if i < len(v6s) {
			out = append(out, v6s[i])
		}
	}
	return out
}

func dialSafeIntel(ctx context.Context, dialer *net.Dialer, network, addr string, allowLocalhost bool) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if parsed, err := netip.ParseAddr(host); err == nil {
		a := parsed.Unmap()
		if a.IsLoopback() {
			if !allowLocalhost {
				return nil, fmt.Errorf("blocked loopback target %s", a)
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
		}
		if !news.PublicIP(a) {
			return nil, fmt.Errorf("blocked non-public IP %s", a)
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
	}
	resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve host %q: %w", host, err)
	}
	if allowLocalhost && isLoopbackHostname(host) {
		for _, r := range resolved {
			if parsed, ok := netip.AddrFromSlice(r.IP); ok && parsed.Unmap().IsLoopback() {
				if conn, dErr := dialer.DialContext(ctx, network, net.JoinHostPort(parsed.Unmap().String(), port)); dErr == nil {
					return conn, nil
				}
			}
		}
		return nil, fmt.Errorf("no loopback IP available for sidecar host %q", host)
	}
	candidates := interleavePublicIPs(resolved)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no public unicast IP resolved for host %q", host)
	}
	var lastErr error
	for _, ip := range candidates {
		conn, dErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dErr == nil {
			return conn, nil
		}
		lastErr = dErr
	}
	return nil, lastErr
}

func isASCIIWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func newHTTPClient(override http.RoundTripper, srcKind string, allowLocalhost bool) *http.Client {
	transport := override
	if transport == nil {
		dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
		transport = &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialSafeIntel(ctx, dialer, network, addr, allowLocalhost)
			},
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          16,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 8 * time.Second,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		}
	}
	return &http.Client{
		Transport: transport,
		Timeout:   defaultFetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			if _, err := ValidateSourceEndpoint(req.URL.String(), srcKind, allowLocalhost); err != nil {
				return err
			}
			req.Header.Del("Authorization")
			req.Header.Del("Cookie")
			return nil
		},
	}
}

func fetchSourceBytes(ctx context.Context, client *http.Client, override http.RoundTripper, src SourceConfig) ([]byte, error) {
	if _, err := ValidateSourceEndpoint(src.Endpoint, src.Kind, src.AllowLocalhost); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.Endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "fased-worldintel/1.0")
	req.Header.Set("Accept", "application/json, application/xml, application/rss+xml, application/atom+xml, text/xml;q=0.9")

	if client == nil {
		client = newHTTPClient(override, src.Kind, src.Kind == KindSidecar && src.AllowLocalhost)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream HTTP %d from %s", resp.StatusCode, src.ID)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxResponseBytes {
		return nil, errors.New("upstream response exceeded 2 MiB cap")
	}
	return raw, nil
}

func isFixtureEndpoint(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return strings.HasSuffix(host, ".invalid") || host == "fixture.local"
}
