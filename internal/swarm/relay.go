package swarm

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"crypto/ed25519"
)

const (
	maxRelayEndpointLen = 256
	relayTimeout        = 5 * time.Second
	maxRelayRespBytes   = 32 * 1024
	maxRelayConcurrency = 8
)

var (
	tailscaleCGNATPrefix = netip.MustParsePrefix("100.64.0.0/10")
	tailscaleULAPrefix   = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
	rfc2544BenchPrefix   = netip.MustParsePrefix("198.18.0.0/15")
	testNet1Prefix       = netip.MustParsePrefix("192.0.2.0/24")
)

func isAllowedRelayIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if tailscaleCGNATPrefix.Contains(ip) || tailscaleULAPrefix.Contains(ip) {
		return true
	}
	if ip.Is4() && (rfc2544BenchPrefix.Contains(ip) || testNet1Prefix.Contains(ip)) {
		return false
	}
	if ip.IsPrivate() {
		return false
	}
	return ip.IsGlobalUnicast()
}

// ValidatePeerEndpoint validates and canonicalizes an optional HTTPS Tailscale or public Swarm relay endpoint.
func ValidatePeerEndpoint(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if len(trimmed) > maxRelayEndpointLen {
		return "", errors.New("swarm peer endpoint exceeds 256 characters")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid swarm peer endpoint: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return "", errors.New("swarm peer endpoint must use https://")
	}
	if u.User != nil {
		return "", errors.New("swarm peer endpoint must not embed credentials")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("swarm peer endpoint must not contain query or fragment")
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return "", errors.New("swarm peer endpoint host required")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return "", errors.New("swarm peer endpoint cannot target localhost")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !isAllowedRelayIP(ip) {
			return "", fmt.Errorf("swarm peer endpoint IP %s is not a Tailscale or public unicast address", ip)
		}
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	return "https://" + strings.ToLower(u.Host) + path, nil
}

func dialRelayAddress(ctx context.Context, dialer *net.Dialer, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !isAllowedRelayIP(ip) {
			return nil, fmt.Errorf("blocked swarm relay IP %s", ip)
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("resolve swarm peer %q: %w", host, err)
	}
	var allowed []netip.Addr
	for _, ip := range ips {
		if isAllowedRelayIP(ip) {
			allowed = append(allowed, ip)
		}
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("swarm peer %q resolved only disallowed local/private IPs", host)
	}
	var lastErr error
	for _, ip := range allowed {
		conn, dErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dErr == nil {
			return conn, nil
		}
		lastErr = dErr
	}
	return nil, lastErr
}

func newRelayHTTPClient(override http.RoundTripper) *http.Client {
	transport := override
	if transport == nil {
		dialer := &net.Dialer{
			Timeout:   4 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		transport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialRelayAddress(ctx, dialer, network, addr)
			},
			Proxy:                 nil,
			TLSHandshakeTimeout:   4 * time.Second,
			ResponseHeaderTimeout: 4 * time.Second,
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
		}
	}
	return &http.Client{
		Transport: transport,
		Timeout:   relayTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (s *Service) relayEnvelopeToPeers(ctx context.Context, env Envelope) []RelayResult {
	if s == nil || s.store == nil || s.relayClient == nil {
		return nil
	}
	// Guarantee zero outbound network activity in offline FixtureMode tests unless
	// an explicit test RoundTripper is injected.
	if s.fixtureMode && s.relayTransport == nil {
		return nil
	}
	peers, err := s.store.listPeers(ctx)
	if err != nil || len(peers) == 0 {
		return nil
	}
	localPubHex := hex.EncodeToString(s.store.localKey().Public().(ed25519.PublicKey))
	var targets []Peer
	for _, p := range peers {
		if strings.TrimSpace(p.Endpoint) == "" {
			continue
		}
		if strings.EqualFold(p.PubKeyHex, localPubHex) {
			continue
		}
		targets = append(targets, p)
	}
	if len(targets) == 0 {
		return nil
	}

	wireEnv := env
	wireEnv.RelayResults = nil
	payload, err := json.Marshal(wireEnv)
	if err != nil {
		return nil
	}

	results := make([]RelayResult, len(targets))
	sem := make(chan struct{}, maxRelayConcurrency)
	var wg sync.WaitGroup
	for i, peer := range targets {
		wg.Add(1)
		go func(idx int, p Peer) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			res := RelayResult{
				PeerID:   p.PeerID,
				Endpoint: p.Endpoint,
			}
			canon, vErr := ValidatePeerEndpoint(p.Endpoint)
			if vErr != nil || canon == "" {
				if vErr != nil {
					res.Error = vErr.Error()
				} else {
					res.Error = "empty peer endpoint"
				}
				results[idx] = res
				return
			}
			reqCtx, cancel := context.WithTimeout(ctx, relayTimeout)
			defer cancel()
			req, rErr := http.NewRequestWithContext(reqCtx, http.MethodPost, canon+"/api/v1/swarm/relay", bytes.NewReader(payload))
			if rErr != nil {
				res.Error = rErr.Error()
				results[idx] = res
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json")
			req.Header.Set("User-Agent", "fased-swarm-relay/1.0")

			resp, doErr := s.relayClient.Do(req)
			if doErr != nil {
				res.Error = doErr.Error()
				results[idx] = res
				return
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxRelayRespBytes))
			_ = resp.Body.Close()

			res.StatusCode = resp.StatusCode
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				res.Delivered = true
			} else {
				res.Error = fmt.Sprintf("peer returned HTTP %d", resp.StatusCode)
			}
			results[idx] = res
		}(i, peer)
	}
	wg.Wait()
	sort.Slice(results, func(i, j int) bool {
		return results[i].PeerID < results[j].PeerID
	})
	return results
}
