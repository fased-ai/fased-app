package core

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/fased-ai/fased-app/internal/tailnet"
)

// NewRemoteClient is deliberately separate from NewClient's OS-owner socket.
// It grants no local/maintenance IPC authority and follows no redirects/proxy.
func NewRemoteClient(ctx context.Context, origin, instanceID string) (*Client, error) {
	_, _, err := tailnet.Origin(origin)
	if err != nil || len(instanceID) < 16 || len(instanceID) > 128 {
		return nil, errors.New("choose the verified Tailscale instance with fased connect")
	}
	transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, DisableCompression: true}
	return newRemoteClient(ctx, origin, instanceID, transport)
}
func newRemoteClient(ctx context.Context, origin, instanceID string, transport http.RoundTripper) (*Client, error) {
	c := &Client{remote: true, Endpoint: Endpoint{URL: origin, InstanceID: instanceID}, HTTP: &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Remote redirects refused") }}}
	var health Health
	if err := c.Do(ctx, http.MethodGet, "/api/v1/health", nil, &health); err != nil {
		c.Close()
		return nil, err
	}
	if health.InstanceID != instanceID || health.APIVersion != APIVersion || health.Connection == nil || health.Connection.Location != "remote" || health.Connection.Transport != "private-https" {
		c.Close()
		return nil, errors.New("Remote instance identity changed; no credentials sent")
	}
	if err := c.authenticate(ctx, "/api/v1/auth/remote", map[string]string{}); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}
