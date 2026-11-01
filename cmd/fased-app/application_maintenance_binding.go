package main

import (
	"context"
	"fmt"
	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
	"path/filepath"
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
	}, true)
}

func bindCompiledLocalMaintenance(store *core.Store, root, data string, check func(context.Context) (*core.ApplicationRelease, error)) error {
	m, e := localinstall.Load(localinstall.Paths{Root: root, Data: data})
	if e != nil {
		return e
	}
	runner := localinstall.ApplicationMaintenanceRunner(m)
	return store.BindApplicationMaintenance(func(ctx context.Context) (*core.ApplicationRelease, error) {
		if e := localinstall.ApplicationMaintenanceReady(ctx, m, strings.Repeat("0", 32), runner); e != nil {
			return nil, e
		}
		if e := localinstall.CheckApplicationMaintenanceHost(ctx, m, strings.Repeat("0", 32)); e != nil {
			return nil, e
		}
		return check(ctx)
	}, func(ctx context.Context, id string) error {
		if !update.IDOK(id) {
			return fmt.Errorf("invalid update operation")
		}
		return localinstall.StartApplicationMaintenance(ctx, m, id, runner)
	}, true)
}

func installedChannelCheck(source, root, data string, ownerUID int) (func(context.Context) (*core.ApplicationRelease, error), error) {
	if compiledApplicationUpdateCheck != nil {
		return compiledApplicationUpdateCheck, nil
	}
	if compiledApplicationChannel == nil {
		return nil, nil
	}
	factory := func(directory string) (bootstrap.Options, error) {
		return channelOptions(compiledApplicationChannel, directory)
	}
	if source == "local" {
		m, e := localinstall.Load(localinstall.Paths{Root: root, Data: data})
		if e != nil {
			return nil, e
		}
		return channelCheck(factory, filepath.Join(data, "update-channel"), m.ApplicationSequence, m.ApplicationReleaseSHA256), nil
	}
	if source == "remote" {
		m, e := vpsinstall.ApplicationMaintenanceManifest(ownerUID)
		if e != nil {
			return nil, e
		}
		return channelCheck(factory, filepath.Join(data, "update-channel"), m.ApplicationSequence, m.ApplicationReleaseSHA256), nil
	}
	return nil, nil
}
