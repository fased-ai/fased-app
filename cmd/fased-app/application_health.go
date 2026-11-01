package main

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/core"
	"time"
)

// A service-manager start precedes socket readiness. Retry only a proved
// unavailable transport; authentication, identity and other failures stay final.
func waitApplicationOwnerHealth(ctx context.Context, read func(context.Context) (core.ApplicationOwnerView, error)) (core.ApplicationOwnerView, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		v, e := read(ctx)
		if e == nil || !errors.Is(e, core.ErrCoreUnavailable) {
			return v, e
		}
		select {
		case <-ctx.Done():
			return core.ApplicationOwnerView{}, ctx.Err()
		case <-tick.C:
		}
	}
}
