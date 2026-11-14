package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/bootstrap"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitialInstallerHasNoPublicOrImplicitExecutionDefaults(t *testing.T) {
	for _, args := range [][]string{nil, {"--install"}, {"--development"}, {"--development", "--seed-file", "absent", "--seed-sha256", "test", "--output", "/absent", "--prefix", "/app"}} {
		if err := run(args, nil, io.Discard, io.Discard); err == nil {
			t.Fatal("public/implicit bootstrap accepted", args)
		}
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
	} {
		if err := run(args, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "manual candidate update") {
			t.Fatal("mixed or unbound candidate update accepted", args, err)
		}
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
