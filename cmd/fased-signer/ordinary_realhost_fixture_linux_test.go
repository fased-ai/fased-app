//go:build linux && custody_acceptance_fixture && fased_realhost_fixture

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/signer"
)

func TestRealHostFixtureSignerRefusesUnboundHostAndOwnerKeys(t *testing.T) {
	id, err := os.ReadFile("/etc/machine-id")
	if err != nil || os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
		t.Skip("ordinary host identity unavailable")
	}
	oldOwner, oldMachine, oldData := hostplatform.FixtureOwnerUID, hostplatform.FixtureMachineSHA256, hostplatform.FixtureSourceData
	t.Cleanup(func() {
		hostplatform.FixtureOwnerUID, hostplatform.FixtureMachineSHA256, hostplatform.FixtureSourceData = oldOwner, oldMachine, oldData
	})
	hostplatform.FixtureOwnerUID, hostplatform.FixtureMachineSHA256, hostplatform.FixtureSourceData = "", "", ""
	if err = run([]string{"ordinary-custody-version"}, nil, io.Discard, io.Discard); !errors.Is(err, signer.ErrUnavailable) {
		t.Fatal("unbound fixture signer was accepted", err)
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(string(id))))
	hostplatform.FixtureOwnerUID = strconv.Itoa(os.Getuid())
	hostplatform.FixtureMachineSHA256 = hex.EncodeToString(sum[:])
	hostplatform.FixtureSourceData = filepath.Join(t.TempDir(), "fixture-data")
	if !hostplatform.FixtureHostAllowed(os.Getuid()) {
		t.Skip("canonical Linux machine identity unavailable")
	}
	var out bytes.Buffer
	if err = run([]string{"ordinary-custody-version"}, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var version struct {
		Provenance   string `json:"provenance"`
		WalletSigner bool   `json:"walletSigner"`
	}
	if err = json.Unmarshal(out.Bytes(), &version); err != nil || version.Provenance != signer.ControlledCustodyProfile || version.WalletSigner {
		t.Fatal("fixture signer advertised real-wallet authority", err)
	}
	if compiledFixtureHost(os.Getuid() + 1) {
		t.Fatal("another owner was accepted")
	}
	if _, err = compiledFixtureServe(t.Context(), signer.Config{OwnerUID: os.Getuid(), Provenance: signer.OwnerKeyProvenance}); !errors.Is(err, signer.ErrUnavailable) {
		t.Fatal("owner-key custody was accepted by the fixture", err)
	}
}
