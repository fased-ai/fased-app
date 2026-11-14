package main

import (
	"bytes"
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
		packet, e := os.ReadFile(filepath.Join(dir, bootstrap.AttestedPacketFile))
		if e != nil {
			t.Fatal(e)
		}
		digest := sha256.Sum256(packet)
		parent := filepath.Dir(dir)
		args := []string{"--install-candidate", dir, "--candidate-packet-sha256", hex.EncodeToString(digest[:]), "--profile", "local", "--prefix", filepath.Join(parent, "fresh-app"), "--data-dir", filepath.Join(parent, "fresh-data"), "--start=false"}
		if e = run(args, strings.NewReader(""), io.Discard, io.Discard); e != nil {
			t.Fatal("actual candidate entry did not reach the verified Local wizard", e)
		}
	}
}
