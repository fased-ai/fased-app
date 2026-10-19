package privateaccess

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"runtime"
	"time"
)

// The Serve LocalAPI is version-sensitive. Require its ETag/If-Match contract,
// preserve an exact endpoint inventory and fail closed on unknown responses.
// https://github.com/tailscale/tailscale/blob/main/client/local/serve.go
func localRequest(ctx context.Context, method string, data []byte, etag string) ([]byte, string, error) {
	if runtime.GOOS != "linux" {
		return nil, "", errors.New("guided Tailscale Serve configuration currently targets Linux VPS only")
	}
	t := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/tailscale/tailscaled.sock")
	}}
	defer t.CloseIdleConnections()
	c := &http.Client{Transport: t, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("local daemon redirected") }}
	r, e := http.NewRequestWithContext(ctx, method, "http://local-tailscaled.sock/localapi/v0/serve-config", bytes.NewReader(data))
	if e != nil {
		return nil, "", e
	}
	r.Header.Set("Content-Type", "application/json")
	if method == http.MethodPost {
		if etag == "" {
			return nil, "", errors.New("missing conditional identity")
		}
		r.Header.Set("If-Match", etag)
	}
	resp, e := c.Do(r)
	if e != nil {
		return nil, "", errors.New("Tailscale local daemon unavailable; use SSH access or check its installation")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", errors.New("Tailscale refused conditional Serve access: confirm this OS user's operator permission and tailnet HTTPS; a concurrent configuration change must be inspected before retry")
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	if e != nil || len(b) > 1024*1024 {
		return nil, "", errors.New("invalid daemon response")
	}
	return b, resp.Header.Get("ETag"), nil
}
func systemConfig(ctx context.Context) ([]byte, string, error) {
	return localRequest(ctx, http.MethodGet, nil, "")
}
func systemSetConfig(ctx context.Context, b []byte, etag string) error {
	_, _, e := localRequest(ctx, http.MethodPost, b, etag)
	return e
}
