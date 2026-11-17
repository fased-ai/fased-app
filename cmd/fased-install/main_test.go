package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/bootstrap"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestInitialInstallerHasNoPublicOrImplicitExecutionDefaults(t *testing.T) {
	for _, args := range [][]string{nil, {"--install"}, {"--development"}, {"--development", "--seed-file", "absent", "--seed-sha256", "test", "--output", "/absent", "--prefix", "/app"}} {
		if err := run(args, nil, io.Discard, io.Discard); err == nil {
			t.Fatal("public/implicit bootstrap accepted", args)
		}
	}
}

func TestFixturePackageIdentityHasNoProductionOverride(t *testing.T) {
	if os.Getuid() <= 0 || os.Getuid() != os.Geteuid() {
		t.Skip("ordinary Local owner required")
	}
	previous := compiledFixturePackageInstance
	t.Cleanup(func() { compiledFixturePackageInstance = previous })
	compiledFixturePackageInstance = nil
	var out bytes.Buffer
	if err := run([]string{"--fixture-package-instance"}, nil, &out, io.Discard); err != nil || out.Len() != 0 {
		t.Fatal("ordinary installer advertised a fixture instance", err)
	}
	want := strings.Repeat("a", 32)
	compiledFixturePackageInstance = func(owner int) (string, error) {
		if owner != os.Getuid() {
			t.Fatal("fixture owner changed")
		}
		return want, nil
	}
	if err := run([]string{"--fixture-package-instance"}, nil, &out, io.Discard); err != nil || strings.TrimSpace(out.String()) != want {
		t.Fatal("compiled fixture instance was not returned", err)
	}
	compiledFixturePackageInstance = func(int) (string, error) { return "invalid", nil }
	if err := run([]string{"--fixture-package-instance"}, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("invalid fixture instance was accepted")
	}
}

func TestInstallerUpgradeRefusesMixedModesBeforeAcquisition(t *testing.T) {
	for _, args := range [][]string{{"--upgrade", "--install"}, {"--remote"}, {"--upgrade", "--start=false"}} {
		if e := run(args, nil, io.Discard, io.Discard); e == nil || !strings.Contains(e.Error(), "choose installation") {
			t.Fatal(args, e)
		}
	}
}

func TestFreshCandidateRejectsUntrustedOrMixedModes(t *testing.T) {
	previous := compiledFirstInstallBinding
	compiledFirstInstallBinding = &firstInstallBinding{Seed: []byte("not read"), SHA256: strings.Repeat("a", 64)}
	t.Cleanup(func() { compiledFirstInstallBinding = previous })
	directory := filepath.Join(t.TempDir(), "candidate")
	packet := strings.Repeat("a", 64)
	for _, args := range [][]string{
		{"--install-candidate", directory, "--profile", "local"},
		{"--install-candidate", directory, "--candidate-packet-sha256", strings.ToUpper(packet), "--profile", "local"},
		{"--install-candidate", directory, "--candidate-packet-sha256", packet, "--profile", "remote"},
		{"--install-candidate", directory, "--candidate-packet-sha256", packet, "--profile", "local", "--upgrade"},
		{"--install-candidate", directory, "--candidate-packet-sha256", packet, "--profile", "local", "--development"},
		{"--install-candidate", directory, "--candidate-packet-sha256", packet, "--profile", "local", "--verify-candidate", directory},
	} {
		if err := run(args, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "fresh Local candidate") {
			t.Fatal("mixed or unbound candidate accepted", args, err)
		}
	}
	if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refusal changed candidate path", err)
	}
}

func TestPublicPackageRequiresExactOwnerProfileAndRelease(t *testing.T) {
	if os.Getuid() <= 0 || os.Getuid() != os.Geteuid() {
		t.Skip("ordinary Local owner required")
	}
	previous := compiledFirstInstallBinding
	compiledFirstInstallBinding = &firstInstallBinding{Seed: []byte("not read"), SHA256: strings.Repeat("a", 64)}
	t.Cleanup(func() { compiledFirstInstallBinding = previous })
	packet := strings.Repeat("b", 64)
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00local", os.Getuid(), packet)))
	instance := hex.EncodeToString(sum[:])[:32]
	for _, args := range [][]string{
		{"--install-public-package", "--candidate-packet-sha256", packet, "--package-location", "remote", "--package-instance", instance, "--profile", "local"},
		{"--install-public-package", "--candidate-packet-sha256", strings.ToUpper(packet), "--package-location", "local", "--package-instance", instance, "--profile", "local"},
		{"--install-public-package", "--candidate-packet-sha256", packet, "--package-location", "local", "--package-instance", strings.Repeat("c", 32), "--profile", "local"},
		{"--install-public-package", "--candidate-packet-sha256", packet, "--package-location", "local", "--package-instance", instance, "--profile", "local", "--upgrade"},
	} {
		if err := run(args, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "fresh package") {
			t.Fatal("mixed or foreign public package accepted", args, err)
		}
	}
	args := []string{"--install-public-package", "--candidate-packet-sha256", packet, "--package-location", "local", "--package-instance", instance, "--profile", "local"}
	if err := run(args, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "independently pinned first-install seed") {
		t.Fatal("the Local package did not reach the original trust check", err)
	}
	remoteSum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00remote", os.Getuid(), packet)))
	remoteID := hex.EncodeToString(remoteSum[:])[:32]
	args = []string{"--install-public-package", "--candidate-packet-sha256", packet, "--package-location", "remote", "--package-instance", remoteID, "--profile", "remote"}
	if err := run(args, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "independently pinned first-install seed") {
		t.Fatal("the exact Remote package did not reach the trust check", err)
	}
	args[6] = instance
	if err := run(args, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "fresh package") {
		t.Fatal("Remote installation accepted a Local instance identity", err)
	}
}

func TestFreshPackageCandidateRequiresItsExactProfileBeforeAcquisition(t *testing.T) {
	if os.Getuid() <= 0 || os.Getuid() != os.Geteuid() {
		t.Skip("ordinary owner required")
	}
	previous := compiledFirstInstallBinding
	compiledFirstInstallBinding = &firstInstallBinding{Seed: []byte("not read"), SHA256: strings.Repeat("a", 64)}
	t.Cleanup(func() { compiledFirstInstallBinding = previous })
	directory := filepath.Join(t.TempDir(), "candidate")
	packet, instance := strings.Repeat("b", 64), strings.Repeat("c", 32)
	args := []string{"--install-candidate", directory, "--candidate-packet-sha256", packet, "--package-location", "remote", "--package-instance", instance, "--profile", "remote"}
	if err := run(args, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "private absolute candidate directory required") {
		t.Fatal("the exact Remote candidate did not reach candidate validation", err)
	}
	args[len(args)-1] = "local"
	if err := run(args, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "fresh package") {
		t.Fatal("a different profile reached the package readiness gate", err)
	}
	if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("readiness refusal changed the candidate", err)
	}
}

func TestUpdateCandidateRejectsMixedModesBeforeReadingInstallation(t *testing.T) {
	previous := compiledFirstInstallBinding
	compiledFirstInstallBinding = &firstInstallBinding{Seed: []byte("not read"), SHA256: strings.Repeat("a", 64)}
	t.Cleanup(func() { compiledFirstInstallBinding = previous })
	directory := filepath.Join(t.TempDir(), "candidate")
	packet := strings.Repeat("a", 64)
	for _, args := range [][]string{
		{"--stage-candidate", directory},
		{"--stage-candidate", directory, "--candidate-packet-sha256", strings.ToUpper(packet)},
		{"--stage-candidate", directory, "--candidate-packet-sha256", packet, "--profile", "local"},
		{"--stage-candidate", directory, "--candidate-packet-sha256", packet, "--development"},
		{"--stage-candidate", directory, "--candidate-packet-sha256", packet, "--upgrade"},
		{"--stage-candidate", directory, "--candidate-packet-sha256", packet, "--package-location", "local"},
		{"--stage-candidate", directory, "--candidate-packet-sha256", packet, "--package-location", "local", "--package-instance", "../foreign"},
	} {
		if err := run(args, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "manual candidate update") {
			t.Fatal("mixed or unbound candidate update accepted", args, err)
		}
	}
	if err := run([]string{"--package-location", "local", "--package-instance", strings.Repeat("1", 32)}, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "package candidate identity") {
		t.Fatal("package identity was accepted outside candidate staging", err)
	}
}

func TestRetailInstallerUsesPublisherBindingWithoutCustomerTrustForms(t *testing.T) {
	if _, e := retailInstallerOptions(nil, t.TempDir()); e == nil || strings.Contains(e.Error(), "seed") {
		t.Fatal("customer exposed to internal trust fields", e)
	}
	root := t.TempDir()
	o, e := retailInstallerOptions(&firstInstallBinding{Seed: []byte("disposable public root fixture"), SHA256: strings.Repeat("a", 64)}, root)
	if e != nil || o.Development || len(o.TLSCA) != 0 || !strings.HasPrefix(o.Directory, filepath.Join(root, "fased-app", "install", "verified-")) {
		t.Fatal(o, e)
	}
	if _, e = os.Stat(o.Directory); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("output created before authenticated acquisition", e)
	}
}

// Invoked from the bootstrap cryptographic fixture: exercise this actual CLI
// with public disposable trust only, never a runtime endpoint/seed override.
func TestAttestedCandidateCLI(t *testing.T) {
	dir := os.Getenv("FASED_ATTESTED_CANDIDATE_FIXTURE")
	if dir == "" {
		t.Skip("explicit disposable candidate not supplied")
	}
	seed, err := os.ReadFile(filepath.Join(dir, "first-install-seed.json"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(seed)
	previous := compiledFirstInstallBinding
	compiledFirstInstallBinding = &firstInstallBinding{Seed: seed, SHA256: hex.EncodeToString(sum[:])}
	defer func() { compiledFirstInstallBinding = previous }()
	var out bytes.Buffer
	if err = run([]string{"--verify-candidate", dir}, strings.NewReader(""), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var report bootstrap.Report
	if json.Unmarshal(out.Bytes(), &report) != nil || report.Version != 4 || !report.BundleVerified || !report.ProvenanceVerified || report.MetadataVerified || report.InstallerInvoked {
		t.Fatal(out.String())
	}
	if os.Getenv("FASED_NATIVE_PACKAGE_CANDIDATE") == "1" && !report.LinuxPackageDigestsVerified {
		t.Fatal("native package inspection did not verify both package identities")
	}
	if os.Getenv("FASED_NATIVE_PACKAGE_CANDIDATE") == "1" && runtime.GOOS == "linux" {
		packet, e := os.ReadFile(filepath.Join(dir, bootstrap.AttestedPacketFile))
		if e != nil {
			t.Fatal(e)
		}
		digest := sha256.Sum256(packet)
		admission := bootstrap.CandidateAdmission{PacketSHA256: hex.EncodeToString(digest[:]), OperationID: strings.Repeat("1", 32), BaselineSHA256: strings.Repeat("2", 64), ExpiresAt: time.Now().Add(10 * time.Minute).Unix()}
		parent := t.TempDir()
		if e = os.Chmod(parent, 0700); e != nil {
			t.Fatal(e)
		}
		options := bootstrap.Options{Seed: seed, SeedSHA256: hex.EncodeToString(sum[:]), Directory: filepath.Join(parent, "held")}
		prepared, e := bootstrap.PrepareAttestedCandidate(t.Context(), options, dir, admission)
		if e != nil {
			t.Fatal("native candidate could not be retained", e)
		}
		defer prepared.Close()
		if e = stageAttestedCandidate(t.Context(), prepared, admission.PacketSHA256, "update", io.Discard); e == nil || !strings.Contains(e.Error(), "package-aware") {
			t.Fatal("native candidate reached the legacy maintenance helper", e)
		}
		t.Setenv("XDG_CACHE_HOME", filepath.Join(parent, "cache"))
		app, data := filepath.Join(parent, "app"), filepath.Join(parent, "data")
		args := []string{"--install-candidate", dir, "--candidate-packet-sha256", admission.PacketSHA256, "--profile", "local", "--prefix", app, "--data-dir", data, "--start=false"}
		if e = run(args, strings.NewReader(""), io.Discard, io.Discard); e == nil || !strings.Contains(e.Error(), "running installer differs") {
			t.Fatal("native candidate reached the legacy installer entry", e)
		}
		for _, path := range []string{app, data} {
			if _, e = os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("held native entry changed an installation target", e)
			}
		}
	}
	if err = run([]string{"--verify-candidate", dir, "--install"}, strings.NewReader(""), io.Discard, io.Discard); err == nil {
		t.Fatal("candidate verification accepted execution")
	}
	if os.Getenv("FASED_FRESH_CANDIDATE_MISMATCH") == "1" {
		t.Setenv("XDG_CACHE_HOME", t.TempDir())
		packet, e := os.ReadFile(filepath.Join(dir, bootstrap.AttestedPacketFile))
		if e != nil {
			t.Fatal(e)
		}
		digest := sha256.Sum256(packet)
		parent := filepath.Dir(dir)
		app, data := filepath.Join(parent, "mismatch-app"), filepath.Join(parent, "mismatch-data")
		args := []string{"--install-candidate", dir, "--candidate-packet-sha256", hex.EncodeToString(digest[:]), "--profile", "local", "--prefix", app, "--data-dir", data, "--start=false"}
		if e = run(args, strings.NewReader(""), io.Discard, io.Discard); e == nil || !strings.Contains(e.Error(), "running installer differs") {
			t.Fatal("candidate accepted a different installer", e)
		}
		for _, path := range []string{app, data} {
			if _, e = os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("installer mismatch changed the target", e)
			}
		}
	}
	if os.Getenv("FASED_FRESH_CANDIDATE_TEST") == "1" {
		previousStage := candidateMaintenanceStage
		staged := false
		candidateMaintenanceStage = func(ctx context.Context, p *bootstrap.Prepared, packet, scope string, _ io.Writer) error {
			if p == nil || packet == "" || scope != "fresh" {
				return errors.New("wrong candidate staging request")
			}
			var stream bytes.Buffer
			if err := p.WriteCandidate(ctx, &stream); err != nil {
				return err
			}
			destination := t.TempDir()
			if err := os.Chmod(destination, 0700); err != nil {
				return err
			}
			if err := bootstrap.ReceiveCandidate(ctx, &stream, destination, packet); err != nil {
				return err
			}
			if _, err := bootstrap.VerifyAttestedCandidate(bootstrap.Options{Seed: seed, SeedSHA256: hex.EncodeToString(sum[:])}, destination); err != nil {
				return err
			}
			staged = true
			return nil
		}
		defer func() { candidateMaintenanceStage = previousStage }()
		packet, e := os.ReadFile(filepath.Join(dir, bootstrap.AttestedPacketFile))
		if e != nil {
			t.Fatal(e)
		}
		digest := sha256.Sum256(packet)
		parent := filepath.Dir(dir)
		app := filepath.Join(parent, "fresh-app")
		args := []string{"--install-candidate", dir, "--candidate-packet-sha256", hex.EncodeToString(digest[:]), "--profile", "local", "--prefix", app, "--data-dir", filepath.Join(parent, "fresh-data"), "--start=false"}
		if e = os.Mkdir(app, 0700); e != nil {
			t.Fatal(e)
		}
		sentinel := filepath.Join(app, "preserve")
		if e = os.WriteFile(sentinel, []byte("original"), 0600); e != nil {
			t.Fatal(e)
		}
		if e = run(args, strings.NewReader(""), io.Discard, io.Discard); e == nil || staged {
			t.Fatal("existing data reached protected candidate staging", e)
		}
		if b, e := os.ReadFile(sentinel); e != nil || string(b) != "original" {
			t.Fatal("existing data changed", e)
		}
		if e = os.Remove(sentinel); e != nil {
			t.Fatal(e)
		}
		if e = run(args, strings.NewReader(""), io.Discard, io.Discard); e != nil {
			t.Fatal("actual candidate entry did not reach the verified Local wizard", e)
		}
		if !staged {
			t.Fatal("candidate entry did not request protected staging")
		}
	}
}
