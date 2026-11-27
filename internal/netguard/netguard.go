package netguard

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

var (
	ipv6GlobalPrefix  = netip.MustParsePrefix("2000::/3")
	tailscaleV4Prefix = netip.MustParsePrefix("100.64.0.0/10")
	tailscaleV6Prefix = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
	alibabaIMDSIP     = netip.MustParseAddr("100.100.100.200")

	deniedPublicPrefixes = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),       // "This" network (RFC 1122)
		netip.MustParsePrefix("10.0.0.0/8"),      // RFC 1918
		netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT / Tailscale / Alibaba IMDS (RFC 6598)
		netip.MustParsePrefix("127.0.0.0/8"),     // Loopback
		netip.MustParsePrefix("169.254.0.0/16"),  // Link-local / Cloud IMDS
		netip.MustParsePrefix("172.16.0.0/12"),   // RFC 1918
		netip.MustParsePrefix("192.0.0.0/24"),    // IETF Protocol Assignments
		netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
		netip.MustParsePrefix("192.168.0.0/16"),  // RFC 1918
		netip.MustParsePrefix("198.18.0.0/15"),   // Benchmarking
		netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
		netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
		netip.MustParsePrefix("224.0.0.0/4"),     // Multicast
		netip.MustParsePrefix("240.0.0.0/4"),     // Reserved / Broadcast
		netip.MustParsePrefix("64:ff9b::/96"),    // NAT64 well-known prefix (RFC 6052)
		netip.MustParsePrefix("64:ff9b:1::/48"),  // NAT64 local-use prefix (RFC 8215)
		netip.MustParsePrefix("100::/64"),        // Discard-Only (RFC 6666)
		netip.MustParsePrefix("2001::/32"),       // Teredo tunneling (RFC 4380)
		netip.MustParsePrefix("2001:db8::/32"),   // Documentation (RFC 3849)
		netip.MustParsePrefix("2002::/16"),       // 6to4 tunneling (RFC 3056)
		netip.MustParsePrefix("fc00::/7"),        // Unique local IPv6
		netip.MustParsePrefix("fe80::/10"),       // Link-local IPv6
		netip.MustParsePrefix("ff00::/8"),        // Multicast IPv6
	}
)

// Options configures the unified SSRF-safe dialer and HTTP transport.
type Options struct {
	// AllowLoopback permits literal loopback hosts (localhost, 127.0.0.1, ::1)
	// in addition to public unicast IPs. Non-loopback hostnames resolving to loopback
	// are STILL rejected to prevent DNS rebinding.
	AllowLoopback bool
	// LoopbackOnly restricts dials strictly to loopback addresses (e.g., local IBKR gateway).
	LoopbackOnly bool
	// AllowTailscaleHosts permits Tailscale CGNAT/ULA peer IPs ONLY for *.ts.net hostnames
	// while always blocking Alibaba Cloud IMDS (100.100.100.200).
	AllowTailscaleHosts bool

	DialTimeout            time.Duration
	TLSHandshakeTimeout    time.Duration
	ResponseHeaderTimeout  time.Duration
	IdleConnTimeout        time.Duration
	MaxIdleConns           int
	MaxIdleConnsPerHost    int
	MaxResponseHeaderBytes int64
	TLSClientConfig        *tls.Config

	// Lookup overrides DNS resolution for deterministic tests.
	Lookup func(ctx context.Context, network, host string) ([]netip.Addr, error)
	// Dial overrides TCP dialing for deterministic tests.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// IsPublicUnicastIP returns true strictly for routable public IPv4/IPv6 addresses.
// It unmaps IPv4-mapped IPv6 addresses and rejects loopback, RFC1918, CGNAT, IMDS,
// link-local, multicast, unspecified, documentation, reserved, NAT64, 6to4, and Teredo.
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
	if ip.Is6() && !ipv6GlobalPrefix.Contains(ip) {
		return false
	}
	for _, p := range deniedPublicPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// IsTailscalePeerIP reports whether raw is a Tailscale peer address while explicitly
// excluding Alibaba Cloud metadata endpoint 100.100.100.200.
func IsTailscalePeerIP(raw netip.Addr) bool {
	if !raw.IsValid() {
		return false
	}
	ip := raw.Unmap()
	if ip == alibabaIMDSIP {
		return false
	}
	return tailscaleV4Prefix.Contains(ip) || tailscaleV6Prefix.Contains(ip)
}

// IsLoopbackHostname reports whether host is an explicit loopback name or IP literal.
func IsLoopbackHostname(host string) bool {
	h := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(host, ".")))
	if h == "localhost" {
		return true
	}
	if ip, err := netip.ParseAddr(h); err == nil {
		return ip.Unmap().IsLoopback()
	}
	return false
}

// IsTailscaleHostname reports whether host is a MagicDNS *.ts.net hostname.
func IsTailscaleHostname(host string) bool {
	h := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(host, ".")))
	return strings.HasSuffix(h, ".ts.net") && len(h) > len(".ts.net")
}

// SafeDialContext returns a DialContext function enforcing whole-set DNS validation
// and strict IP classification before opening any TCP socket.
func SafeDialContext(opts Options) func(ctx context.Context, network, address string) (net.Conn, error) {
	dialTimeout := opts.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 10 * time.Second
	}
	baseDialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	lookup := opts.Lookup
	if lookup == nil {
		lookup = net.DefaultResolver.LookupNetIP
	}
	dialFn := opts.Dial
	if dialFn == nil {
		dialFn = baseDialer.DialContext
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialChecked(ctx, network, address, opts, lookup, dialFn)
	}
}

func ipAllowedForHost(ip netip.Addr, host string, isLiteral bool, opts Options) error {
	ip = ip.Unmap()
	if !ip.IsValid() {
		return errors.New("SSRF blocked: invalid IP address")
	}
	if ip == alibabaIMDSIP {
		return errors.New("SSRF blocked: cloud metadata IP denied")
	}
	if opts.LoopbackOnly {
		if !ip.IsLoopback() || (!isLiteral && !IsLoopbackHostname(host)) {
			return errors.New("SSRF blocked: loopback-only destination required")
		}
		return nil
	}
	if ip.IsLoopback() {
		if !opts.AllowLoopback || (!isLiteral && !IsLoopbackHostname(host)) {
			return errors.New("SSRF blocked: loopback address denied")
		}
		return nil
	}
	if opts.AllowTailscaleHosts && IsTailscaleHostname(host) && IsTailscalePeerIP(ip) {
		return nil
	}
	if !IsPublicUnicastIP(ip) {
		return errors.New("SSRF blocked: non-public IP denied")
	}
	return nil
}

func dialChecked(
	ctx context.Context,
	network, address string,
	opts Options,
	lookup func(context.Context, string, string) ([]netip.Addr, error),
	dial func(context.Context, string, string) (net.Conn, error),
) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, errors.New("SSRF blocked: unsupported network")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return nil, errors.New("SSRF blocked: invalid network destination")
	}
	if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
		ip = ip.Unmap()
		if err := ipAllowedForHost(ip, host, true, opts); err != nil {
			return nil, err
		}
		return dial(ctx, network, net.JoinHostPort(ip.String(), port))
	}

	ips, err := lookup(ctx, "ip", host)
	if err != nil || len(ips) == 0 || len(ips) > 32 {
		return nil, errors.New("SSRF guard: DNS resolution unavailable")
	}
	// Whole-set DNS rejection: if ANY resolved record is non-public/disallowed,
	// reject the entire dial rather than silently skipping poisoned records.
	targets := make([]netip.Addr, 0, len(ips))
	for _, raw := range ips {
		ip := raw.Unmap()
		if err := ipAllowedForHost(ip, host, false, opts); err != nil {
			return nil, err
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

// NewSafeTransport constructs an *http.Transport with proxy env overrides disabled,
// TLS >= 1.2 enforced, full header/handshake/idle timeouts set, and dial-time SSRF protection.
func NewSafeTransport(opts Options) *http.Transport {
	tlsHandshake := opts.TLSHandshakeTimeout
	if tlsHandshake <= 0 {
		tlsHandshake = 10 * time.Second
	}
	respHeader := opts.ResponseHeaderTimeout
	if respHeader <= 0 {
		respHeader = 15 * time.Second
	}
	idleTimeout := opts.IdleConnTimeout
	if idleTimeout <= 0 {
		idleTimeout = 30 * time.Second
	}
	maxIdle := opts.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = 32
	}
	maxIdlePerHost := opts.MaxIdleConnsPerHost
	if maxIdlePerHost <= 0 {
		maxIdlePerHost = 8
	}
	maxRespHdr := opts.MaxResponseHeaderBytes
	if maxRespHdr <= 0 {
		maxRespHdr = 64 << 10
	}
	var tlsCfg *tls.Config
	if opts.TLSClientConfig != nil {
		tlsCfg = opts.TLSClientConfig.Clone()
		if tlsCfg.MinVersion < tls.VersionTLS12 {
			tlsCfg.MinVersion = tls.VersionTLS12
		}
	} else {
		tlsCfg = &tls.Config{
			MinVersion:       tls.VersionTLS12,
			CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384},
		}
	}
	return &http.Transport{
		Proxy:                  nil,
		DialContext:            SafeDialContext(opts),
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           maxIdle,
		MaxIdleConnsPerHost:    maxIdlePerHost,
		IdleConnTimeout:        idleTimeout,
		TLSHandshakeTimeout:    tlsHandshake,
		ResponseHeaderTimeout:  respHeader,
		ExpectContinueTimeout:  1 * time.Second,
		MaxResponseHeaderBytes: maxRespHdr,
		TLSClientConfig:        tlsCfg,
	}
}

// RefuseRedirects is the default CheckRedirect policy that prevents redirect-based SSRF.
func RefuseRedirects(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// NewSafeClient returns an *http.Client backed by NewSafeTransport with redirect refusal.
func NewSafeClient(timeout time.Duration, opts Options) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &http.Client{
		Timeout:       timeout,
		Transport:     NewSafeTransport(opts),
		CheckRedirect: RefuseRedirects,
	}
}

// ValidatePublicHTTPURL validates a user- or peer-supplied URL before making an outbound call.
func ValidatePublicHTTPURL(raw string, allowLoopback bool) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	u, err := url.Parse(trimmed)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return nil, errors.New("invalid URL")
	}
	host := u.Hostname()
	isLoop := IsLoopbackHostname(host)
	if isLoop {
		if !allowLoopback {
			return nil, errors.New("loopback URL is not permitted")
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, errors.New("loopback URL must use http or https")
		}
		return u, nil
	}
	if u.Scheme != "https" {
		return nil, errors.New("public URL must use https")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !IsPublicUnicastIP(ip) {
			return nil, errors.New("non-public IP literal is not permitted")
		}
	}
	return u, nil
}
