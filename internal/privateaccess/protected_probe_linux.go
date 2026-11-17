//go:build linux

package privateaccess

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/tailnet"
)

// ProbeProtectedIngress checks the exact running instance with a read-only
// health request. The caller independently checks and opens the protected
// root-owned listener; this grants no route or device authority.
func ProbeProtectedIngress(ctx context.Context, origin string, owner int, instance string, dial func(context.Context) (net.Conn, error)) error {
	if _, _, err := tailnet.Origin(origin); err != nil || owner <= 0 || instance == "" || dial == nil {
		return errors.New("exact private instance required")
	}
	transport := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) }}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("private health redirect refused") }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/api/v1/health", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Forwarded-Host", strings.TrimPrefix(origin, "https://"))
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-For", "100.64.0.1")
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	b, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	var health core.Health
	if err != nil || len(b) > 64<<10 || response.StatusCode != http.StatusOK || json.Unmarshal(b, &health) != nil || health.Status != "ok" || health.APIVersion != core.APIVersion || health.InstanceID != instance || health.Connection == nil || health.Connection.OwnerUID != owner || health.Connection.Location != "remote" || health.Connection.Transport != "private-https" {
		return errors.New("protected listener does not serve the original Remote instance")
	}
	return nil
}
