//go:build linux && custody_acceptance_fixture && fased_realhost_fixture

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/enrollment"
	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/walletfixture"
)

func realHostFixtureEnrollment(ctx context.Context, s enrollment.Source, o enrollment.Options, system *custodyinstall.System) (custodyinstall.Manifest, error) {
	owner := os.Getuid()
	if owner <= 0 || os.Geteuid() != owner || (s.Profile == "local" && s.UID != owner) || !hostplatform.FixtureSourceAllowed(owner, s.Data) {
		return custodyinstall.Manifest{}, signer.ErrUnavailable
	}
	return enrollment.RunControlledOwnerCustody(ctx, s, o, system)
}

type realHostFixtureTrust struct {
	Connected          bool   `json:"connected"`
	SeedSHA256         string `json:"seedSha256"`
	CASHA256           string `json:"caSha256"`
	SourceSHA256       string `json:"sourceSha256"`
	TargetSHA256       string `json:"targetSha256"`
	TargetSignerSHA256 string `json:"targetSignerSha256"`
}

func realHostFixtureRead(name string) ([]byte, error) {
	if name != "fixture.json" && name != "seed.json" && name != "ca.pem" {
		return nil, signer.ErrUnavailable
	}
	f, err := update.OpenProtected(filepath.Join("/var/lib/fased-update-fixture", name), update.MaxDocument, 0644)
	if err != nil {
		return nil, err
	}
	b, readErr := io.ReadAll(io.LimitReader(f, update.MaxDocument+1))
	if err = errors.Join(readErr, f.Close()); err != nil || len(b) == 0 || len(b) > update.MaxDocument {
		return nil, signer.ErrUnavailable
	}
	return b, nil
}

func realHostFixtureOptions(directory string, trust realHostFixtureTrust, seed, ca []byte) (bootstrap.Options, error) {
	var binding bootstrap.Seed
	seedHash, caHash := sha256.Sum256(seed), sha256.Sum256(ca)
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || !trust.Connected || len(seed) == 0 || len(ca) == 0 ||
		hex.EncodeToString(seedHash[:]) != trust.SeedSHA256 || hex.EncodeToString(caHash[:]) != trust.CASHA256 ||
		update.StrictJSON(seed, &binding) != nil || binding.Version != 1 || binding.Purpose != bootstrap.Purpose || !binding.DevelopmentOnly ||
		binding.Repository != "fased-ai/fased-app" || binding.Channel != "development" || binding.Origin != "https://127.0.0.1:17449" ||
		binding.Target != (update.Target{OS: "linux", Arch: runtime.GOARCH}) {
		return bootstrap.Options{}, signer.ErrUnavailable
	}
	return bootstrap.Options{Development: true, Seed: append([]byte(nil), seed...), SeedSHA256: trust.SeedSHA256, TLSCA: append([]byte(nil), ca...), Directory: directory}, nil
}

func realHostFixtureCustodyCheck(owner int, dir string) (func(context.Context) (*core.ApplicationRelease, error), error) {
	if os.Getuid() <= 0 || os.Getuid() != os.Geteuid() || !hostplatform.FixtureSourceAllowed(owner, hostplatform.FixtureSourceData) || dir != filepath.Join(custodyinstall.Root(owner), "app-state") {
		return nil, signer.ErrUnavailable
	}
	m, err := custodyinstall.Load(owner)
	if err != nil || !m.Enrollment || m.Mode != signer.OrdinaryCustodyMode || m.Profile != signer.OwnerCustodyProfile || m.Provenance != signer.ControlledCustodyProfile || m.App.UID != os.Getuid() {
		return nil, signer.ErrUnavailable
	}
	trustBytes, err := realHostFixtureRead("fixture.json")
	var trust realHostFixtureTrust
	if err != nil || update.StrictJSON(trustBytes, &trust) != nil || (m.CoreHash != trust.SourceSHA256 && m.CoreHash != trust.TargetSHA256) {
		return nil, signer.ErrUnavailable
	}
	seed, err := realHostFixtureRead("seed.json")
	if err != nil {
		return nil, err
	}
	ca, err := realHostFixtureRead("ca.pem")
	if err != nil {
		return nil, err
	}
	factory := func(directory string) (bootstrap.Options, error) {
		return realHostFixtureOptions(directory, trust, seed, ca)
	}
	parent := filepath.Join(dir, "update-channel")
	if _, err = factory(filepath.Join(parent, "preflight")); err != nil {
		return nil, err
	}
	return custodyChannelCheck(owner, factory, parent, true), nil
}

// This composition is absent from ordinary builds. The fixture may enroll
// only its compiled source workspace and controlled-disposable signer.
func init() {
	compiledOrdinaryEnrollment = realHostFixtureEnrollment
	compiledCustodyUpdateCheck = realHostFixtureCustodyCheck
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
