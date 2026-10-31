package main

import (
	"context"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
	"strings"
)

// Reviewed Go composition only. Ordinary builds bind neither a public check
// source nor the independently verifying owner source; no environment/IPC flag
// can activate either. Tests supply controlled sources in their own executable.
var compiledApplicationUpdateCheck func(context.Context) (*core.ApplicationRelease, error)

func bindCompiledRemoteMaintenance(store *core.Store, ownerUID int, check func(context.Context) (*core.ApplicationRelease, error)) error {
	m, e := vpsinstall.ApplicationMaintenanceManifest(ownerUID)
	if e != nil {
		return e
	}
	return store.BindApplicationMaintenance(func(ctx context.Context) (*core.ApplicationRelease, error) {
		if _, e := vpsinstall.ApplicationMaintenanceStatus(ctx, m, strings.Repeat("0", 32)); e != nil {
			return nil, e
		}
		return check(ctx)
	}, func(ctx context.Context, id string) error {
		_, e := vpsinstall.LaunchApplicationMaintenance(ctx, m, id, nil)
		return e
	})
}
