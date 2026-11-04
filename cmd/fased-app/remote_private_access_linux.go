package main

import (
	"context"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/hostprep"
	"github.com/fased-ai/fased-app/internal/privateaccess"
)

// Only the human administrator's installer/lifecycle command receives this
// temporary OS transport. The app service and ordinary privateaccess.System do
// not acquire sudo or a background signing/configuration authority.
func remotePrivateAccess(system *custodyinstall.System) privateaccess.Manager {
	setup := &hostprep.System{In: system.In, Out: system.Out, Authorization: "Authorize Fased's owned private HTTPS setup through the local Tailscale service. Existing routes, data and authentication are preserved; Fased remains unprivileged."}
	return privateaccess.System().WithServeAuthorization(func(ctx context.Context, data []byte, etag string) error {
		return privateaccess.SetServeWithOS(ctx, data, etag, setup.Run)
	})
}
