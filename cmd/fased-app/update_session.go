package main

import (
	"context"
	"errors"
	"net/http"
	"os"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

// Only the already installed immutable AppUID helper can turn its protected OS
// access into an update session. Browser/Tailscale and ordinary operator callers
// cannot read the app socket or pass the helper's config/image/UID checks.
func loadUpdateSession(ctx context.Context, c *core.Client, dir string) error {
	return loadUpdateSessionWithLocal(ctx, c, dir, custodyinstall.InstalledNativeUpdateSessionIdentity)
}
func loadUpdateSessionWithLocal(ctx context.Context, c *core.Client, dir string, validateLocal func(int, string, string) (custodyinstall.Manifest, string, error)) error {
	err := c.LoadSession(ctx, dir)
	if !errors.Is(err, core.ErrUnauthorized) {
		return err
	}
	var health core.Health
	if e := c.Do(ctx, http.MethodGet, "/api/v1/health", nil, &health); e != nil {
		return e
	}
	if health.Connection != nil && health.Connection.Location == "local" && health.Connection.OwnerUID > 0 {
		image, e := os.Executable()
		if e != nil {
			return e
		}
		m, instance, e := validateLocal(health.Connection.OwnerUID, image, dir)
		if e != nil {
			return e
		}
		return prepareLocalReaderSession(ctx, c, m, instance)
	}
	if health.Connection == nil || health.Connection.Location != "remote" || health.Connection.OwnerUID <= 0 {
		return err
	}
	image, e := os.Executable()
	if e != nil {
		return e
	}
	m, e := vpsinstall.ValidateApplicationOwnerHelper(vpsinstall.ConfigPath(health.Connection.OwnerUID), image, os.Getuid())
	if e != nil {
		return e
	}
	if m.Data != dir {
		return errors.New("update session belongs to another installation")
	}
	return loadRemoteHelperSession(ctx, c, m)
}

// Reader callers have already validated the immutable retained target and exact
// operation. Only ephemeral session rows/file change; identities, schema, holds,
// approvals and update journals remain untouched, including while core is paused.
func loadRemoteHelperSession(ctx context.Context, c *core.Client, m vpsinstall.Manifest) error {
	err := c.LoadSession(ctx, m.Data)
	if !errors.Is(err, core.ErrUnauthorized) {
		return err
	}
	if os.Getuid() <= 0 || os.Geteuid() != os.Getuid() || m.AppUID != os.Getuid() || m.OwnerUID <= 0 || m.OwnerUID == m.AppUID {
		return errors.New("verified Remote app helper required")
	}
	token, e := core.IssueMaintenanceSession(ctx, m.Data, c.Endpoint.InstanceID)
	if e != nil {
		return e
	}
	c.Token = token
	var auth struct {
		Authenticated bool   `json:"authenticated"`
		CSRF          string `json:"csrfToken"`
	}
	if e = c.Do(ctx, http.MethodGet, "/api/v1/auth/state", nil, &auth); e != nil {
		return e
	}
	if !auth.Authenticated || auth.CSRF == "" {
		return core.ErrUnauthorized
	}
	c.CSRF = auth.CSRF
	return c.SaveSession(m.Data)
}
