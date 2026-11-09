//go:build linux

package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
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
	for _, action := range []string{"approve", "receipt-rpc-save", "update-preferences"} {
		if _, err := custodyOwnerFlag(m, action); err == nil {
			t.Fatal("unsupported authority exposed", action)
		}
	}
	if flag, err := custodyOwnerFlag(m, "approve-swap"); err != nil || flag != "--disposable-fixture" {
		t.Fatal("controlled exact trade approval unavailable", flag, err)
	}
	m.Provenance = signer.OwnerKeyProvenance
	if _, err := custodyOwnerFlag(m, "approve-swap"); err == nil {
		t.Fatal("real trade approval enabled")
	}
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

func TestCustodySourceProjectionRequiresMatchingOwnerManifest(t *testing.T) {
	c := signer.Config{OwnerUID: 1000, AppUID: 1001, SignerUID: 1002, SignerID: strings.Repeat("a", 32), Network: "devnet"}
	base := custodyinstall.Manifest{Owner: 1000, Root: "/var/lib/fased-app-custody-1000", ID: c.SignerID, Network: "devnet", Enrollment: true, Location: "local", App: custodyinstall.Identity{UID: 1001}, Signer: custodyinstall.Identity{UID: 1002}}
	for _, location := range []string{"local", "remote"} {
		m := base
		m.Location = location
		if location == "remote" {
			m.PublicOrigin = "https://owner.example.ts.net"
		}
		got, err := custodySourceProjection(m, c, m.Root, m.Root+"/app-state", m.Location, m.PublicOrigin)
		if err != nil || got != location {
			t.Fatal("verified installer source lost", got, err)
		}
		for _, mode := range []string{"owner", "app", "signer", "identity", "network", "root", "data", "location", "origin"} {
			wrong := m
			root, data, loc, origin := m.Root, m.Root+"/app-state", m.Location, m.PublicOrigin
			switch mode {
			case "owner":
				wrong.Owner++
			case "app":
				wrong.App.UID++
			case "signer":
				wrong.Signer.UID++
			case "identity":
				wrong.ID = strings.Repeat("b", 32)
			case "network":
				wrong.Network = "mainnet-beta"
			case "root":
				root += "-foreign"
			case "data":
				data += "-foreign"
			case "location":
				loc = "foreign"
			case "origin":
				origin = "https://foreign.example.ts.net"
			}
			if got, err := custodySourceProjection(wrong, c, root, data, loc, origin); err == nil || got != "" {
				t.Fatal("foreign source accepted", mode, got, err)
			}
		}
	}
	base.Enrollment = false
	base.Location = ""
	base.PublicOrigin = ""
	if got, err := custodySourceProjection(base, c, base.Root, base.Root+"/app-state", "local", ""); err != nil || got != "" {
		t.Fatal("legacy non-enrollment adopted", got, err)
	}
}
