//go:build linux && custody_acceptance_fixture && fased_realhost_fixture

package main

import (
	"context"
	"os"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/enrollment"
	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/walletfixture"
)

func realHostFixtureEnrollment(ctx context.Context, s enrollment.Source, o enrollment.Options, system *custodyinstall.System) (custodyinstall.Manifest, error) {
	owner := os.Getuid()
	if owner <= 0 || os.Geteuid() != owner || (s.Profile == "local" && s.UID != owner) || !hostplatform.FixtureSourceAllowed(owner, s.Data) {
		return custodyinstall.Manifest{}, signer.ErrUnavailable
	}
	return enrollment.RunControlledOwnerCustody(ctx, s, o, system)
}

// This composition is absent from ordinary builds. The fixture may enroll
// only its compiled source workspace and controlled-disposable signer.
func init() {
	compiledOrdinaryEnrollment = realHostFixtureEnrollment
	compiledOrdinarySourceAllowed = func(root, data string) bool {
		return hostplatform.FixtureSourceLocationAllowed(os.Getuid(), root, data)
	}
	compiledInstalledWalletRead = func(server *core.Server, path string) (func(), error) {
		if path == "" {
			return nil, nil
		}
		cfg, err := signer.LoadConfig(path)
		if err != nil || cfg.Version != 3 || cfg.Profile != signer.OwnerCustodyProfile || cfg.Provenance != signer.ControlledCustodyProfile || !hostplatform.FixtureSourceAllowed(cfg.OwnerUID, hostplatform.FixtureSourceData) {
			return nil, signer.ErrUnavailable
		}
		transport, closeFixture := walletfixture.New()
		if err = server.ConfigureControlledWalletReads(transport); err != nil {
			closeFixture()
			return nil, err
		}
		return closeFixture, nil
	}
}
