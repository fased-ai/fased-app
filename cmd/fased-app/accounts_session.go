package main

import (
	"context"
	"errors"
	"net/http"
	"os"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

func loadAccountsSession(ctx context.Context, c *core.Client, dir string) error {
	return loadAccountsSessionWithIdentity(ctx, c, dir, custodyinstall.InstalledRemoteAccountsIdentity)
}

func loadAccountsSessionWithIdentity(ctx context.Context, c *core.Client, dir string, identity func(int, string, string) (custodyinstall.Manifest, string, error)) error {
	err := c.LoadSession(ctx, dir)
	if !errors.Is(err, core.ErrUnauthorized) {
		return err
	}
	var health core.Health
	if e := c.Do(ctx, http.MethodGet, "/api/v1/health", nil, &health); e != nil {
		return e
	}
	if health.Connection == nil || health.Connection.Location != "remote" || health.Connection.OwnerKind != "custody" || health.Connection.OwnerUID <= 0 {
		return err
	}
	image, e := os.Executable()
	if e != nil {
		return e
	}
	m, instance, e := identity(health.Connection.OwnerUID, image, dir)
	if e != nil {
		return e
	}
	if m.Owner != health.Connection.OwnerUID || m.Root+"/app-state" != dir || instance != c.Endpoint.InstanceID || health.InstanceID != instance {
		return errors.New("custody account access belongs to another installation")
	}
	return c.CustodyAccounts(ctx)
}
