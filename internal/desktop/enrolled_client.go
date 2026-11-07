package desktop

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/enrollment"
)

// NewPublicClient reuses the installed Local OS-owner transport for native UI
// and ordinary CLI. It grants no signer or administrative operation.
func NewPublicClient(ctx context.Context, dir string) (*core.Client, bool, error) {
	if runtime.GOOS != "linux" {
		c, err := core.NewClient(ctx, dir)
		return c, false, err
	}
	// CLI callers may use a relative data directory. Resolve it before the
	// enrolled-target inspection, which intentionally requires an absolute path
	// and still checks for symlinks, exact ownership and the source fence.
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, false, err
	}
	m, err := enrollment.EnrolledTarget(dir, os.Getuid())
	if errors.Is(err, os.ErrNotExist) {
		c, err := core.NewClient(ctx, dir)
		return c, false, err
	}
	if err != nil {
		return nil, false, err
	}
	if !m.NativeOwnerAccess || m.Location != "local" || m.Owner != os.Getuid() || custodyinstall.Verify(m) != nil {
		return nil, false, errors.New("protected Local native enrollment required")
	}
	commit, err := custodyinstall.ReadEnrollmentCommit(m)
	if err != nil {
		return nil, false, err
	}
	if err = (&custodyinstall.System{}).EnsureNativeRunning(ctx, m); err != nil {
		return nil, false, err
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&custodyinstall.System{}).NativeChannel(ctx, m)
	}}
	c := &core.Client{Endpoint: core.Endpoint{URL: "http://" + m.Listen, InstanceID: commit.InstanceID}, HTTP: &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirects refused") }}}
	var health core.Health
	if err = c.Do(ctx, "GET", "/api/v1/health", nil, &health); err != nil {
		c.Close()
		return nil, false, err
	}
	if health.InstanceID != commit.InstanceID || health.Connection == nil || health.Connection.Location != "local" || health.Connection.OwnerUID != m.Owner {
		c.Close()
		return nil, false, errors.New("native owner instance mismatch")
	}
	return c, true, nil
}
