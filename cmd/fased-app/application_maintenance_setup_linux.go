//go:build linux

package main

import (
	"context"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
	"io"
)

func prepareRemoteUpdateAuthorityPlatform(ctx context.Context, m vpsinstall.Manifest, system *custodyinstall.System, out io.Writer) error {
	system.Authorization = "Authorize update maintenance for this Remote installation: a fixed ordinary-owner service, bounded file/service OS rules and start-only app permission. Missing Ubuntu polkitd may be installed. No Fased code runs as root. Automatic/public updates remain off."
	if e := vpsinstall.InstallApplicationMaintenance(ctx, m, system); e != nil {
		return e
	}
	return remoteUpdateAuthorityResult(out)
}
