// Package tailnet reads identity from the local Tailscale daemon. It has no
// enrollment, operator, Serve, firewall, wallet or execution authority.
package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

type Identity struct {
	UserID int64  `json:"userId"`
	NodeID string `json:"nodeId"`
	Login  string `json:"login"`
	Name   string `json:"name"`
}

type Resolver func(context.Context, string) (Identity, error)

func WhoIs(ctx context.Context, peer string) (Identity, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialDaemon(ctx)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("identity redirects refused") }}
	return resolve(ctx, client, peer, time.Now())
}

func resolve(ctx context.Context, client *http.Client, peer string, now time.Time) (Identity, error) {
	var empty Identity
	ip, err := netip.ParseAddr(peer)
	if err != nil || !PrivateAddress(peer) {
		return empty, errors.New("private client identity required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://local-tailscaled.sock/localapi/v0/whois?addr="+url.QueryEscape(ip.String()), nil)
	if err != nil {
		return empty, err
	}
	response, err := client.Do(req)
	if err != nil {
		return empty, errors.New("Tailscale identity unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return empty, errors.New("Tailscale client is not authorized")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(data) > 65536 {
		return empty, errors.New("invalid Tailscale identity response")
	}
	var result struct {
		Node struct {
			StableID  string
			User      int64
			Name      string
			Tags      []string
			KeyExpiry time.Time
			Expired   bool
			Addresses []netip.Prefix
		}
		UserProfile struct {
			ID        int64
			LoginName string
		}
	}
	if json.Unmarshal(data, &result) != nil || result.Node.StableID == "" || len(result.Node.StableID) > 128 || result.Node.User <= 0 || result.Node.User != result.UserProfile.ID || result.UserProfile.LoginName == "" || len(result.Node.Tags) > 0 || result.Node.Expired || (!result.Node.KeyExpiry.IsZero() && !now.Before(result.Node.KeyExpiry)) {
		return empty, errors.New("an active personal Tailscale device is required")
	}
	found := false
	for _, address := range result.Node.Addresses {
		if address.Addr() == ip {
			found = true
		}
	}
	if !found {
		return empty, errors.New("Tailscale client address changed")
	}
	return Identity{UserID: result.Node.User, NodeID: result.Node.StableID, Login: result.UserProfile.LoginName, Name: result.Node.Name}, nil
}

func PrivateAddress(peer string) bool {
	ip, err := netip.ParseAddr(peer)
	return err == nil && (netip.MustParsePrefix("100.64.0.0/10").Contains(ip) || netip.MustParsePrefix("fd7a:115c:a1e0::/48").Contains(ip))
}
