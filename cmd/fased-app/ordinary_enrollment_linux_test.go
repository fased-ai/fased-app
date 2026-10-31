//go:build linux

package main

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/enrollment"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/walletfixture"
)

func configureOrdinaryEnrollmentFixture() {
	compiledOrdinaryEnrollment = enrollment.RunControlledOwnerCustody
	compiledInstalledWalletRead = func(server *core.Server, path string) (func(), error) {
		// Ordinary Local/Remote startup passes an empty optional identity.
		// Match core.Start: no signer configuration means no fixture transport.
		if path == "" {
			return nil, nil
		}
		cfg, err := signer.LoadConfig(path)
		if err != nil {
			return nil, err
		}
		if cfg.Version != 3 || cfg.Profile != signer.OwnerCustodyProfile || cfg.Provenance != signer.ControlledCustodyProfile {
			return nil, nil
		}
		transport, closeFixture := walletfixture.New()
		if err := server.ConfigureControlledWalletReads(transport); err != nil {
			closeFixture()
			return nil, err
		}
		return closeFixture, nil
	}
}

func TestOrdinaryEnrollmentOwnerBoundary(t *testing.T) {
	held := compiledOrdinaryEnrollment
	compiledOrdinaryEnrollment = nil
	defer func() { compiledOrdinaryEnrollment = held }()
	var out bytes.Buffer
	if err := enrollmentCommand([]string{"--ordinary-custody"}, nil, &out, io.Discard); !errors.Is(err, signer.ErrUnavailable) || out.Len() != 0 {
		t.Fatal("ordinary admission crossed source/credential boundary", err)
	}
	m := custodyinstall.Manifest{Version: 3, Mode: signer.OrdinaryCustodyMode, Profile: signer.OwnerCustodyProfile, Provenance: signer.ControlledCustodyProfile}
	for _, action := range []string{"setup", "import", "recover"} {
		if flag, err := custodyOwnerFlag(m, action); err != nil || flag != "--disposable-fixture" {
			t.Fatal(action, flag, err)
		}
	}
	for _, action := range []string{"approve", "approve-swap", "receipt-rpc-save", "update-preferences"} {
		if _, err := custodyOwnerFlag(m, action); err == nil {
			t.Fatal("unsupported authority exposed", action)
		}
	}
	m.Provenance = signer.OwnerKeyProvenance
	if _, err := custodyOwnerFlag(m, "import"); !errors.Is(err, signer.ErrUnavailable) {
		t.Fatal("real key admission enabled")
	}
}

func TestCustodyReadInputCannotBecomeOwnerOrMutation(t *testing.T) {
	for _, args := range [][]string{
		{"accounts", "--disposable-fixture", "--read-stdin", "--request-stdin"},
		{"accounts", "--disposable-fixture", "--read-stdin", "--password-stdin"},
		{"login", "--disposable-fixture", "--read-stdin"},
		{"recover", "--disposable-fixture", "--read-stdin"},
	} {
		var out bytes.Buffer
		if err := custodyCommand(args, nil, &out, io.Discard); err == nil || err.Error() != "wallet read input requires accounts only; no update or login input" || out.Len() != 0 {
			t.Fatal(args, err)
		}
	}
}

func TestOrdinaryReadFixtureSkipsAbsentIdentity(t *testing.T) {
	if compiledInstalledWalletRead == nil {
		t.Fatal("compiled acceptance hook missing")
	}
	closeFixture, err := compiledInstalledWalletRead(nil, "")
	if err != nil || closeFixture != nil {
		t.Fatal("unconfigured startup must not load a signer or install a transport", err)
	}
}

func TestCustodyRuntimeOwnerIdentity(t *testing.T) {
	for _, row := range []struct {
		name, location    string
		version, expected int
	}{
		{"legacy-local", "local", 1, 0},
		{"legacy-remote", "remote", 1, 1001},
		{"managed-local", "local", 2, 1001},
		{"managed-remote", "remote", 2, 1001},
		{"ordinary-local", "local", 3, 1001},
		{"ordinary-remote", "remote", 3, 1001},
	} {
		t.Run(row.name, func(t *testing.T) {
			config := signer.Config{Version: row.version, OwnerUID: 1001, AppUID: 999}
			if got := custodyRuntimeOwnerUID(config, row.location); got != row.expected {
				t.Fatalf("connection owner %d, want protected owner %d", got, row.expected)
			}
		})
	}
}
