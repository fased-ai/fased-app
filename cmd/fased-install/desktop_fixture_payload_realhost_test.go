//go:build linux && custody_acceptance_fixture && wallet_payload && wallet_native && fased_realhost_fixture

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestRealHostFixtureSelectsOnlyExactDevelopmentWindow(t *testing.T) {
	fixtureSuccessorVersion = "0.1.12"
	fixtureSuccessorSource = strings.Repeat("a", 40)
	sum := sha256.Sum256(fixtureSuccessorDesktop)
	fixtureSuccessorDesktopSHA256 = hex.EncodeToString(sum[:])
	payload := fixtureSuccessorDesktopPayload()
	if err := bootstrap.WriteWalletPayload(io.Discard, payload); err != nil {
		t.Fatal("compiled fixture window differs", err)
	}
	id := bootstrap.MaintenanceIdentity{Version: payload.Version, Source: payload.Source}
	if selectEmbeddedPayload(id, false, nil, payload) != nil || selectEmbeddedPayload(id, true, nil, payload) != payload {
		t.Fatal("fixture window crossed the production boundary")
	}
	id.Source = strings.Repeat("b", 40)
	if selectEmbeddedPayload(id, true, nil, payload) != nil {
		t.Fatal("foreign fixture window was accepted")
	}
}

func TestRealHostFixtureHelperRequiresItsExactOwner(t *testing.T) {
	id, err := os.ReadFile("/etc/machine-id")
	if err != nil || os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
		t.Skip("ordinary host identity unavailable")
	}
	oldOwner, oldMachine, oldData := hostplatform.FixtureOwnerUID, hostplatform.FixtureMachineSHA256, hostplatform.FixtureSourceData
	t.Cleanup(func() {
		hostplatform.FixtureOwnerUID, hostplatform.FixtureMachineSHA256, hostplatform.FixtureSourceData = oldOwner, oldMachine, oldData
	})
	sum := sha256.Sum256([]byte(strings.TrimSpace(string(id))))
	hostplatform.FixtureOwnerUID, hostplatform.FixtureMachineSHA256 = strconv.Itoa(os.Getuid()), hex.EncodeToString(sum[:])
	hostplatform.FixtureSourceData = filepath.Join(t.TempDir(), "fixture-data")
	if !hostplatform.FixtureHostAllowed(os.Getuid()) {
		t.Skip("canonical Linux machine identity unavailable")
	}
	if !fixtureRealHostAllowed(update.MaintenanceOwnerRoot(os.Getuid())) || fixtureRealHostAllowed(update.MaintenanceOwnerRoot(os.Getuid()+1)) || fixtureRealHostAllowed("/tmp/foreign") {
		t.Fatal("helper accepted a foreign owner path")
	}
}
