//go:build linux && custody_acceptance_fixture && fased_realhost_fixture

package main

import (
	"context"
	"strconv"

	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/signer"
)

func init() {
	compiledFixtureHost = func(owner int) bool {
		if owner == 0 {
			owner, _ = strconv.Atoi(hostplatform.FixtureOwnerUID)
		}
		return hostplatform.FixtureSourceAllowed(owner, hostplatform.FixtureSourceData)
	}
	compiledFixtureServe = func(ctx context.Context, c signer.Config) (*signer.Server, error) {
		if !compiledFixtureHost(c.OwnerUID) || c.Provenance != signer.ControlledCustodyProfile {
			return nil, signer.ErrUnavailable
		}
		return signer.ServeControlledOwnerCustody(ctx, c)
	}
}
