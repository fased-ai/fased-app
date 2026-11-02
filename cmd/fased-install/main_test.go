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
}
