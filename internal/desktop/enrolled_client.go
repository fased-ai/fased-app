package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/enrollment"
)

// Start the predecessor only when the source directory is not fenced by an
// enrollment. A damaged or pending fence must fail closed, not revive it.
func OriginalLocalStartAllowed(data string, owner int) (bool, error) {
	if runtime.GOOS != "linux" {
		return true, nil
	}
	_, err := enrollment.EnrolledTarget(data, owner)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	return false, err
}

// NewPublicClient reuses the installed Local OS-owner transport for native UI
// and ordinary CLI. It grants no signer or administrative operation.
func NewPublicClient(ctx context.Context, dir string) (*core.Client, bool, error) {
	c, enrolled, _, err := newPublicClient(ctx, dir)
	return c, enrolled, err
}

// newPublicClient also returns the freshly verified health response for the
// native handler. It is never reused across requests or used as authority.
func newPublicClient(ctx context.Context, dir string) (*core.Client, bool, json.RawMessage, error) {
	if runtime.GOOS != "linux" {
		c, err := core.NewClient(ctx, dir)
		return c, false, nil, err
	}
	// CLI callers may use a relative data directory. Resolve it before the
	// enrolled-target inspection, which intentionally requires an absolute path
	// and still checks for symlinks, exact ownership and the source fence.
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, false, nil, err
	}
	m, err := enrollment.EnrolledTarget(dir, os.Getuid())
	if errors.Is(err, os.ErrNotExist) {
		c, err := core.NewClient(ctx, dir)
		return c, false, nil, err
	}
	if err != nil {
		return nil, false, nil, err
	}
	// EnrolledTarget verifies the installed image and source fence; the
	// service-start and native-channel boundaries verify them again. Avoid a
	// second identical verification between those independent checks.
	if !m.NativeOwnerAccess || m.Location != "local" || m.Owner != os.Getuid() {
		return nil, false, nil, errors.New("protected Local native enrollment required")
	}
	commit, err := custodyinstall.ReadEnrollmentCommit(m)
	if err != nil {
		return nil, false, nil, err
	}
	if err = (&custodyinstall.System{}).EnsureNativeRunning(ctx, m); err != nil {
		return nil, false, nil, err
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		// A paired update may replace the two executable hashes while retaining
		// the same enrolled instance. Reload only the root-owned manifest and
		// require the original topology and enrollment commit before redialing.
		current, err := custodyinstall.Load(m.Owner)
		if err != nil {
			return nil, err
		}
		currentCommit, err := custodyinstall.ReadEnrollmentCommit(current)
		if err != nil || !sameNativeEnrollment(m, current, commit, currentCommit) {
			return nil, errors.New("native owner instance changed")
		}
		return (&custodyinstall.System{}).NativeChannel(ctx, current)
	}}
	c := &core.Client{Endpoint: core.Endpoint{URL: "http://" + m.Listen, InstanceID: commit.InstanceID}, HTTP: &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirects refused") }}}
	health, err := verifiedNativeHealth(ctx, c, commit.InstanceID, m.Owner)
	if err != nil {
		c.Close()
		return nil, false, nil, err
	}
	return c, true, health, nil
}

func sameNativeEnrollment(original, current custodyinstall.Manifest, originalCommit, currentCommit custodyinstall.EnrollmentCommit) bool {
	return custodyinstall.EnrollmentIdentity(original) == custodyinstall.EnrollmentIdentity(current) && originalCommit == currentCommit
}

func verifiedNativeHealth(ctx context.Context, c *core.Client, instance string, owner int) (json.RawMessage, error) {
	if c == nil || c.HTTP == nil || instance == "" || owner <= 0 || c.Endpoint.InstanceID != instance {
		return nil, errors.New("native owner instance mismatch")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Endpoint.URL+"/api/v1/health", nil)
	if err != nil {
		return nil, err
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		return nil, errors.New("invalid native owner health response")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 32*1024+1))
	var health core.Health
	if err != nil || len(data) > 32*1024 || json.Unmarshal(data, &health) != nil || health.Status != "ok" || health.APIVersion != core.APIVersion || health.InstanceID != instance || health.Connection == nil || health.Connection.Location != "local" || health.Connection.Transport != "loopback" || health.Connection.OwnerUID != owner {
		return nil, errors.New("native owner instance mismatch")
	}
	return data, nil
}
