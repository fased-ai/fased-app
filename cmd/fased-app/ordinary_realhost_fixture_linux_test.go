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
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/enrollment"
	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestRealHostFixtureRefusesForeignSourceBeforeDiscovery(t *testing.T) {
	id, err := os.ReadFile("/etc/machine-id")
	if err != nil || os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
		t.Skip("ordinary host identity unavailable")
	}
	oldOwner, oldMachine, oldData, oldRoot := hostplatform.FixtureOwnerUID, hostplatform.FixtureMachineSHA256, hostplatform.FixtureSourceData, hostplatform.FixtureSourceRoot
	t.Cleanup(func() {
		hostplatform.FixtureOwnerUID, hostplatform.FixtureMachineSHA256, hostplatform.FixtureSourceData, hostplatform.FixtureSourceRoot = oldOwner, oldMachine, oldData, oldRoot
	})
	sum := sha256.Sum256([]byte(strings.TrimSpace(string(id))))
	hostplatform.FixtureOwnerUID = strconv.Itoa(os.Getuid())
	hostplatform.FixtureMachineSHA256 = hex.EncodeToString(sum[:])
	hostplatform.FixtureSourceData = filepath.Join(t.TempDir(), "expected-data")
	hostplatform.FixtureSourceRoot = filepath.Join(t.TempDir(), "expected-install")
	if !hostplatform.FixtureHostAllowed(os.Getuid()) {
		t.Skip("canonical Linux machine identity unavailable")
	}
	if !hostplatform.FixtureSourceLocationAllowed(os.Getuid(), hostplatform.FixtureSourceRoot, hostplatform.FixtureSourceData) {
		t.Fatal("the exact bound installation and workspace were refused")
	}
	foreign := t.TempDir()
	var output bytes.Buffer
	err = enrollmentCommand([]string{"--ordinary-custody", "--source", "local", "--expected-instance", "fixture", "--network", "devnet", "--signer-binary", "signer", "--prepare-host", "--disposable-fixture", "--prefix", filepath.Join(foreign, "missing-install"), "--data-dir", filepath.Join(foreign, "data")}, nil, &output, io.Discard)
	if !errors.Is(err, signer.ErrUnavailable) {
		t.Fatalf("foreign source reached installation discovery: %v", err)
	}
	err = enrollmentCommand([]string{"--ordinary-custody", "--source", "local", "--expected-instance", "fixture", "--network", "devnet", "--signer-binary", "signer", "--prepare-host", "--disposable-fixture", "--prefix", filepath.Join(foreign, "missing-install"), "--data-dir", hostplatform.FixtureSourceData}, nil, &output, io.Discard)
	if !errors.Is(err, signer.ErrUnavailable) {
		t.Fatalf("foreign installation reached discovery: %v", err)
	}
}

func TestRealHostFixtureRefusesAnotherWorkspace(t *testing.T) {
	id, err := os.ReadFile("/etc/machine-id")
	if err != nil || os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
		t.Skip("ordinary host identity unavailable")
	}
	oldOwner, oldMachine, oldData := hostplatform.FixtureOwnerUID, hostplatform.FixtureMachineSHA256, hostplatform.FixtureSourceData
	t.Cleanup(func() {
		hostplatform.FixtureOwnerUID, hostplatform.FixtureMachineSHA256, hostplatform.FixtureSourceData = oldOwner, oldMachine, oldData
	})
	sum := sha256.Sum256([]byte(strings.TrimSpace(string(id))))
	hostplatform.FixtureOwnerUID = strconv.Itoa(os.Getuid())
	hostplatform.FixtureMachineSHA256 = hex.EncodeToString(sum[:])
	hostplatform.FixtureSourceData = filepath.Join(t.TempDir(), "expected-data")
	if !hostplatform.FixtureHostAllowed(os.Getuid()) {
		t.Skip("canonical Linux machine identity unavailable")
	}
	foreign := enrollment.Source{Profile: "local", UID: os.Getuid(), Data: filepath.Join(t.TempDir(), "other-data")}
	if _, err = realHostFixtureEnrollment(t.Context(), foreign, enrollment.Options{}, nil); !errors.Is(err, signer.ErrUnavailable) {
		t.Fatal("foreign workspace was not refused before enrollment", err)
	}
	foreign.Data, foreign.UID = hostplatform.FixtureSourceData, os.Getuid()+1
	if _, err = realHostFixtureEnrollment(t.Context(), foreign, enrollment.Options{}, nil); !errors.Is(err, signer.ErrUnavailable) {
		t.Fatal("foreign Local owner was not refused before enrollment", err)
	}
}

func TestRealHostFixtureHasScopedCustodyUpdateChannel(t *testing.T) {
	if compiledCustodyUpdateCheck == nil {
		t.Fatal("the real-host controlled build has no custody update channel")
	}
	if _, err := compiledCustodyUpdateCheck(os.Getuid()+1, "/tmp/foreign"); !errors.Is(err, signer.ErrUnavailable) {
		t.Fatal("a foreign owner or workspace was accepted", err)
	}
}

func TestRealHostFixtureUpdateTrustIsLoopbackAndBound(t *testing.T) {
	seed := bootstrap.Seed{Binding: bootstrap.Binding{Version: 1, Purpose: bootstrap.Purpose, DevelopmentOnly: true, Repository: "fased-ai/fased-app", Channel: "development", Target: update.Target{OS: "linux", Arch: runtime.GOARCH}}, Origin: "https://127.0.0.1:17449"}
	ca := []byte("disposable local CA")
	makeInputs := func(s bootstrap.Seed) (realHostFixtureTrust, []byte) {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		sh, ch := sha256.Sum256(b), sha256.Sum256(ca)
		return realHostFixtureTrust{Connected: true, SeedSHA256: hex.EncodeToString(sh[:]), CASHA256: hex.EncodeToString(ch[:])}, b
	}
	trust, raw := makeInputs(seed)
	dir := filepath.Join(t.TempDir(), "channel")
	if o, err := realHostFixtureOptions(dir, trust, raw, ca); err != nil || !o.Development || o.Directory != dir {
		t.Fatal("bound loopback channel was refused", err)
	}
	if _, err := realHostFixtureOptions(dir, trust, raw, []byte("changed CA")); !errors.Is(err, signer.ErrUnavailable) {
		t.Fatal("changed CA was accepted", err)
	}
	seed.Origin = "https://example.com"
	trust, raw = makeInputs(seed)
	if _, err := realHostFixtureOptions(dir, trust, raw, ca); !errors.Is(err, signer.ErrUnavailable) {
		t.Fatal("non-loopback fixture channel was accepted", err)
	}
}
