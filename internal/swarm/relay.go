package swarm

import (
	"bytes"
	"context"
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

	"github.com/fased-ai/fased-app/internal/netguard"
)

const (
	maxRelayEndpointLen = 256
	relayTimeout        = 5 * time.Second
	maxRelayRespBytes   = 32 * 1024
	maxRelayConcurrency = 8
)

func isAllowedRelayIP(ip netip.Addr) bool {
	return netguard.IsPublicUnicastIP(ip) || netguard.IsTailscalePeerIP(ip)
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
	var dialFn func(context.Context, string, string) (net.Conn, error)
	if dialer != nil {
		dialFn = dialer.DialContext
	}
	return netguard.SafeDialContext(netguard.Options{
		AllowTailscaleHosts: true,
		DialTimeout:         4 * time.Second,
		Dial:                dialFn,
	})(ctx, network, addr)
}

func newRelayHTTPClient(override http.RoundTripper) *http.Client {
	transport := override
	if transport == nil {
		transport = netguard.NewSafeTransport(netguard.Options{
			AllowTailscaleHosts:   true,
			DialTimeout:           4 * time.Second,
			TLSHandshakeTimeout:   4 * time.Second,
			ResponseHeaderTimeout: 4 * time.Second,
		})
	}
	return &http.Client{
		Transport:     transport,
		Timeout:       relayTimeout,
		CheckRedirect: netguard.RefuseRedirects,
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
