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
	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/enrollment"
	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestRealHostFixtureNativeCompanion(t *testing.T) {
	id, err := os.ReadFile("/etc/machine-id")
	if err != nil || os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
		t.Skip("ordinary host identity unavailable")
	}
	oldOwner, oldMachine, oldData, oldRoot := hostplatform.FixtureOwnerUID, hostplatform.FixtureMachineSHA256, hostplatform.FixtureSourceData, hostplatform.FixtureSourceRoot
	oldSource, oldDesktop, oldSigner := compiledNativeSource, compiledNativeDesktopSHA256, compiledNativeSignerSHA256
	t.Cleanup(func() {
		hostplatform.FixtureOwnerUID, hostplatform.FixtureMachineSHA256, hostplatform.FixtureSourceData, hostplatform.FixtureSourceRoot = oldOwner, oldMachine, oldData, oldRoot
		compiledNativeSource, compiledNativeDesktopSHA256, compiledNativeSignerSHA256 = oldSource, oldDesktop, oldSigner
	})
	sum := sha256.Sum256([]byte(strings.TrimSpace(string(id))))
	hostplatform.FixtureOwnerUID = strconv.Itoa(os.Getuid())
	hostplatform.FixtureMachineSHA256 = hex.EncodeToString(sum[:])
	hostplatform.FixtureSourceRoot = filepath.Join(t.TempDir(), "install")
	hostplatform.FixtureSourceData = filepath.Join(t.TempDir(), "data")
	if !hostplatform.FixtureHostAllowed(os.Getuid()) {
		t.Skip("canonical Linux machine identity unavailable")
	}
	_, original, _ := installationBundleFixture(t, false)
	staged, native, _ := installationBundleFixture(t, true)
	output := t.TempDir()
	dir := filepath.Join(output, "native-bundle")
	if err = os.Rename(staged, dir); err != nil {
		t.Fatal(err)
	}
	signerPath := filepath.Join(dir, "fased-signer")
	if err = os.WriteFile(signerPath, []byte("disposable signer"), 0700); err != nil {
		t.Fatal(err)
	}
	native.Files["fased-signer"], err = bundle.Inspect(signerPath)
	if err != nil {
		t.Fatal(err)
	}
	original.Source, native.Source = strings.Repeat("a", 40), strings.Repeat("a", 40)
	compiledNativeSource = original.Source
	compiledNativeDesktopSHA256 = native.Files[localinstall.DesktopName()].SHA256
	compiledNativeSignerSHA256 = native.Files["fased-signer"].SHA256
	pin := saveInstallationBundle(t, dir, native)
	paths := localinstall.Paths{Root: hostplatform.FixtureSourceRoot, Data: hostplatform.FixtureSourceData}
	engine := filepath.Join(dir, localinstall.EngineName())
	if _, _, err = verifiedNativeInstallation(dir, output, pin, engine, original, paths); err != nil {
		t.Fatal("bound real-host fixture companion was refused", err)
	}
	for _, foreign := range []localinstall.Paths{{Root: paths.Root + "-other", Data: paths.Data}, {Root: paths.Root, Data: paths.Data + "-other"}} {
		if _, _, err = verifiedNativeInstallation(dir, output, pin, engine, original, foreign); err == nil {
			t.Fatal("fixture companion accepted for a foreign installation")
		}
	}
	hostplatform.FixtureMachineSHA256 = strings.Repeat("0", 64)
	if _, _, err = verifiedNativeInstallation(dir, output, pin, engine, original, paths); err == nil {
		t.Fatal("fixture companion accepted on a foreign host")
	}
	hostplatform.FixtureMachineSHA256 = hex.EncodeToString(sum[:])
	compiledNativeSignerSHA256 = strings.Repeat("0", 64)
	if _, _, err = verifiedNativeInstallation(dir, output, pin, engine, original, paths); err == nil {
		t.Fatal("fixture companion accepted with a different bound signer")
	}
}

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
