//go:build linux

package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/onchaintest"
	"github.com/fased-ai/fased-app/internal/signer"
)

func TestInstalledOrdinarySwapAccountUsesAuthoritativeCustody(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "signer")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	keyID := strings.Repeat("a", 32)
	v, err := signer.OpenControlledOwnerCustody(dir, keyID, "mainnet-beta")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if err = v.ConfigureControlledOrdinarySwapObserver(onchaintest.Client(&onchaintest.Chain{})); err != nil {
		t.Fatal(err)
	}
	if err = v.Create([]byte("disposable installed consumer password")); err != nil {
		t.Fatal(err)
	}
	c := signer.Config{Version: 3, Mode: signer.OrdinaryCustodyMode, Profile: signer.OwnerCustodyProfile, Provenance: signer.ControlledCustodyProfile, Network: "mainnet-beta", SignerID: keyID, OwnerUID: 1000, AppUID: 1001, SignerUID: 1002, StateDir: dir, SocketDir: dir + "-socket"}
	if err = installedSwapDriverConfig(c, false); err != nil {
		t.Fatal("retained controlled receiver refused", err)
	}
	status := v.Status()
	account, err := installedSwapAccount(c, status, strings.Repeat("c", 32))
	if err != nil {
		t.Fatal("ordinary installed account refused", err)
	}
	if account.Version != 4 || account.Mode != "managed" || account.Signer == nil || *account.Signer != *status.Account {
		t.Fatal("ordinary custody was relabeled as legacy fixture")
	}
	s := fixtureStore(t)
	s.custodyOwnerSupported = true
	s.custodyNetwork = c.Network
	s.fixture = func(context.Context, signer.Request) (signer.Response, error) {
		return signer.Response{Status: v.Status()}, nil
	}
	r, err := s.accountsRecord(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	r.Accounts = append(r.Accounts, account)
	if _, err = s.UpdateAccounts(t.Context(), r); err != nil {
		t.Fatal("production account consumer", err)
	}
	for _, name := range []string{"owner-input", "wrong-signer", "wrong-network", "missing-capability", "update-held", "live-execution", "missing-account", "missing-custody", "wrong-custody-profile", "wrong-custody-provenance"} {
		t.Run(name, func(t *testing.T) {
			bad, st := c, status
			switch name {
			case "owner-input":
				bad.Provenance = signer.OwnerKeyProvenance
			case "wrong-signer":
				bad.SignerID = strings.Repeat("e", 32)
			case "wrong-network":
				bad.Network = "devnet"
			case "missing-capability":
				st.SwapReady = false
			case "update-held":
				st.UpdateHeld = true
			case "live-execution":
				st.FinancialExecution = true
			case "missing-account":
				st.Account = nil
			case "missing-custody":
				st.Custody = nil
			case "wrong-custody-profile", "wrong-custody-provenance":
				copy := *st.Custody
				st.Custody = &copy
				if name == "wrong-custody-profile" {
					st.Custody.Profile = signer.ControlledCustodyProfile
				} else {
					st.Custody.Provenance = signer.OwnerKeyProvenance
				}
			}
			if name == "owner-input" && installedSwapDriverConfig(bad, false) == nil {
				t.Fatal("owner-input installed receiver admitted")
			}
			if _, err := installedSwapAccount(bad, st, account.ID); err == nil {
				t.Fatal("invalid installed authority admitted")
			}
		})
	}
}

func TestInstalledSwapConsumerPreservesLegacyAndMarkerlessRefusal(t *testing.T) {
	legacyDir := filepath.Join(t.TempDir(), "legacy")
	if err := os.Mkdir(legacyDir, 0700); err != nil {
		t.Fatal(err)
	}
	legacy, err := signer.OpenManagedSwapFixture(legacyDir, strings.Repeat("a", 32), "mainnet-beta", onchaintest.Client(&onchaintest.Chain{}))
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if err = legacy.Create([]byte("disposable legacy consumer password")); err != nil {
		t.Fatal(err)
	}
	status := legacy.Status()
	if status.Account == nil {
		t.Fatal("legacy account absent")
	}
	ref := *status.Account
	c := signer.Config{Version: 2, Mode: signer.ManagedFixtureMode, Network: ref.Network, SignerID: ref.SignerID, OwnerUID: 1000, AppUID: 1001, SignerUID: 1002, StateDir: "/state", SocketDir: "/socket"}
	if err = installedSwapDriverConfig(c, false); err == nil {
		t.Fatal("legacy CI-only guard removed")
	}
	if err = installedSwapDriverConfig(c, true); err != nil {
		t.Fatal("legacy CI receiver refused", err)
	}
	a, err := installedSwapAccount(c, status, strings.Repeat("c", 32))
	if err != nil || a.Version != 3 || a.Mode != "fixture-managed" {
		t.Fatal("legacy installed contract changed", err)
	}
	dir := filepath.Join(t.TempDir(), "markerless")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	v, err := signer.OpenControlledOwnerCustody(dir, strings.Repeat("a", 32), "mainnet-beta")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if err = v.Create([]byte("disposable existing-key consumer password")); err != nil {
		t.Fatal(err)
	}
	if err = v.ConfigureControlledOrdinarySwapObserver(onchaintest.Client(&onchaintest.Chain{})); err != nil {
		t.Fatal(err)
	}
	c.Version, c.Mode, c.Profile, c.Provenance, c.Network, c.SignerID = 3, signer.OrdinaryCustodyMode, signer.OwnerCustodyProfile, signer.ControlledCustodyProfile, "mainnet-beta", strings.Repeat("a", 32)
	if v.Status().SwapReady {
		t.Fatal("existing key gained creation marker")
	}
	if _, err = installedSwapAccount(c, v.Status(), a.ID); err == nil {
		t.Fatal("markerless installed account admitted")
	}
}
