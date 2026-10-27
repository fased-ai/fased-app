//go:build linux

package signer

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
	"os"
	"path/filepath"
)

func InspectFixtureCold(c Config, id string) (Response, error) {
	if e := CheckColdIdentity(c); e != nil {
		return Response{}, e
	}
	v, e := openVault(c.StateDir, c.SignerID, true)
	if e != nil {
		return Response{}, e
	}
	defer v.Close()
	i, e := v.UpdateIntent(id)
	if e != nil {
		return Response{}, e
	}
	snap, e := v.UpdateInventory(update.NewProbe())
	return Response{UpdateIntent: i, UpdateInventory: snap, Status: v.Status()}, e
}
func CheckColdIdentity(c Config) error {
	if e := c.Validate(); e != nil {
		return e
	}
	if os.Getuid() != c.SignerUID || os.Geteuid() != c.SignerUID {
		return errors.New("cold fixture requires signer UID")
	}
	if e := VerifyExecutable(); e != nil {
		return e
	}
	return protectedDirectory(c.StateDir, c.SignerUID, 0700)
}
func ServeInspectionFixture(ctx context.Context, c Config) (*Server, error) {
	root := filepath.Dir(c.StateDir)
	if _, e := update.VerifyInstalledInspectionGuard(root, "signer"); e != nil {
		return nil, e
	}
	return serveMode(ctx, c, true)
}
