package marketdata

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/netguard"
)

const (
	defaultRequestTimeout = 10 * time.Second
	maxUpstreamBodyBytes  = 2 << 20 // 2 MiB hard cap per upstream response.
	secEDGARUserAgent     = "FasedMarketData/1.0 (desktop@fased.local)"
	defaultUserAgent      = "fased-marketdata/1.0"
)

var allowedUpstreamHosts = map[string]bool{
	"api.hyperliquid.xyz":         true,
	"stats-data.hyperliquid.xyz":  true,
	"api.binance.com":             true,
	"data-api.binance.vision":     true,
	"api.bybit.com":               true,
	"api.kraken.com":              true,
	"www.okx.com":                 true,
	"api.exchange.coinbase.com":   true,
	"www.deribit.com":             true,
	"data.alpaca.markets":         true,
	"api.geckoterminal.com":       true,
	"api.dexscreener.com":         true,
	"mainnet.zklighter.elliot.ai": true,
	"api.testnet.lighter.xyz":     true,
	"fred.stlouisfed.org":         true,
	"data.sec.gov":                true,
	"www.sec.gov":                 true,
}

var allowedUpstreamWSHosts = map[string]bool{
	"api.hyperliquid.xyz":        true,
	"data-stream.binance.vision": true,
	"stream.binance.com":         true,
	"stream.bybit.com":           true,
	"ws.kraken.com":              true,
}

var cgnatPrefix = mustParseCIDR("100.64.0.0/10")

func mustParseCIDR(cidr string) *net.IPNet {
	_, parsed, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}
	return parsed
}

// isPublicUnicastIP enforces global unicast routing and rejects loopback,
// RFC1918 private networks, link-local, multicast, and Tailscale CGNAT ranges.
func isPublicUnicastIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
		if cgnatPrefix.Contains(ip) {
			return false
		}
		if ip[0] == 198 && (ip[1] == 18 || ip[1] == 19) {
			return false // RFC 2544 benchmarking block.
		}
		if ip[0] == 192 && ip[1] == 0 && ip[2] == 2 {
			return false // TEST-NET-1.
		}
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() {
		return false
	}
	return ip.IsGlobalUnicast()
}

func interleavePublicIPs(addrs []net.IPAddr) []net.IPAddr {
	var v4s, v6s []net.IPAddr
	for _, a := range addrs {
		if !isPublicUnicastIP(a.IP) {
			continue
		}
		if a.IP.To4() != nil {
			v4s = append(v4s, a)
		} else {
			v6s = append(v6s, a)
		}
	}
	out := make([]net.IPAddr, 0, len(v4s)+len(v6s))
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

func dialPublicAddress(ctx context.Context, dialer *net.Dialer, network, addr string) (net.Conn, error) {
	var dialFn func(context.Context, string, string) (net.Conn, error)
	if dialer != nil {
		dialFn = dialer.DialContext
	}
	return netguard.SafeDialContext(netguard.Options{
		DialTimeout: 5 * time.Second,
		Dial:        dialFn,
	})(ctx, network, addr)
}

// upstreamClient wraps an SSRF-safe, redirect-disabled, TLS 1.2+ HTTP client.
type upstreamClient struct {
	http       *http.Client
	poolCache  *ttlCache[string]
	poolFlight *callGroup
}

func newUpstreamClient(override http.RoundTripper) *upstreamClient {
	transport := override
	if transport == nil {
		transport = netguard.NewSafeTransport(netguard.Options{
			DialTimeout:           5 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 8 * time.Second,
			MaxIdleConns:          16,
		})
	}
	return &upstreamClient{
		http: &http.Client{
			Transport:     transport,
			Timeout:       defaultRequestTimeout,
			CheckRedirect: netguard.RefuseRedirects,
		},
		poolCache:  newTTLCache[string](256, nil),
		poolFlight: newCallGroup(),
	}
}

func validateUpstreamWSURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream WebSocket URL: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "wss") {
		return nil, fmt.Errorf("refusing non-WSS upstream scheme %q", u.Scheme)
	}
	host := strings.ToLower(u.Hostname())
	if !allowedUpstreamWSHosts[host] {
		return nil, fmt.Errorf("upstream WS host %q is not in marketdata allowlist", host)
	}
	return u, nil
}

func newUpstreamWSHttpClient() *http.Client {
	transport := netguard.NewSafeTransport(netguard.Options{
		DialTimeout:         6 * time.Second,
		TLSHandshakeTimeout: 6 * time.Second,
	})
	transport.ForceAttemptHTTP2 = false
	return &http.Client{
		Transport:     transport,
		CheckRedirect: netguard.RefuseRedirects,
	}
}

func validateUpstreamURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream URL: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return nil, fmt.Errorf("refusing non-HTTPS upstream scheme %q", u.Scheme)
	}
	host := strings.ToLower(u.Hostname())
	if !allowedUpstreamHosts[host] {
		return nil, fmt.Errorf("upstream host %q is not in marketdata allowlist", host)
	}
	return u, nil
}

func (c *upstreamClient) openRequest(ctx context.Context, method, rawURL string, body []byte, contentType string, headers map[string]string) (*http.Response, string, error) {
	u, err := validateUpstreamURL(rawURL)
	if err != nil {
		return nil, "", err
	}
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return nil, "", err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	host := strings.ToLower(u.Hostname())
	if host == "data.sec.gov" || host == "www.sec.gov" || host == "fred.stlouisfed.org" {
		req.Header.Set("User-Agent", secEDGARUserAgent)
	} else {
		req.Header.Set("User-Agent", defaultUserAgent)
	}
	req.Header.Set("Accept", "application/json, text/csv;q=0.9")
	for k, v := range headers {
		if k != "" && v != "" {
			req.Header.Set(k, v)
		}
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		resp.Body.Close()
		return nil, "", errors.New("upstream redirect blocked by security policy")
	}
	return resp, host, nil
}

func (c *upstreamClient) doRequest(ctx context.Context, method, rawURL string, body []byte, contentType string, headers map[string]string) ([]byte, error) {
	resp, host, err := c.openRequest(ctx, method, rawURL, body, contentType, headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	payload, readErr := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamBodyBytes+1))
	if readErr != nil {
		return nil, readErr
	}
	if len(payload) > maxUpstreamBodyBytes {
		return nil, fmt.Errorf("upstream response exceeded %d bytes", maxUpstreamBodyBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream %s returned HTTP %d", host, resp.StatusCode)
	}
	return payload, nil
}

// streamGet enforces SSRF, TLS 1.2+, redirect, status, and bounded read
// controls while letting callers stop reading once top records are parsed.
func (c *upstreamClient) streamGet(ctx context.Context, rawURL string, fn func(io.Reader) error) error {
	resp, host, err := c.openRequest(ctx, http.MethodGet, rawURL, nil, "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upstream %s returned HTTP %d", host, resp.StatusCode)
	}
	return fn(io.LimitReader(resp.Body, maxUpstreamBodyBytes))
}

func (c *upstreamClient) get(ctx context.Context, rawURL string) ([]byte, error) {
	return c.doRequest(ctx, http.MethodGet, rawURL, nil, "", nil)
}

func (c *upstreamClient) getWithHeaders(ctx context.Context, rawURL string, headers map[string]string) ([]byte, error) {
	return c.doRequest(ctx, http.MethodGet, rawURL, nil, "", headers)
}

func (c *upstreamClient) postJSON(ctx context.Context, rawURL string, body []byte) ([]byte, error) {
	return c.doRequest(ctx, http.MethodPost, rawURL, body, "application/json", nil)
}
