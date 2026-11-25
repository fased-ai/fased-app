package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/releaseprovenance"
	"github.com/fased-ai/fased-app/internal/releaseprovenance/fixture"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCandidateOfflineProvenanceBoundary(t *testing.T) {
	// All cryptographic keys are in-memory disposable fixtures, not publisher keys.
	root, err := filepath.Abs("../../.evidence/ubuntu-release-0.1.0/test-state")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "provenance-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	p := releaseprovenance.Policy{Version: 1, Repository: "fased-ai/fased-app", RepositoryID: "7", OwnerID: "3", Workflow: releaseprovenance.WorkflowPath}
	e := releaseprovenance.Expected{ArchiveName: "fased-app-linux-amd64.tar.gz", ArchiveSHA256: strings.Repeat("a", 64), SourceCommit: strings.Repeat("b", 40), WorkflowCommit: strings.Repeat("b", 40), Tag: "v0.1.0", RunID: "11", RunAttempt: "1"}
	p, proof, err := fixture.New(p, e, time.Now().UTC().Truncate(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, v any) string {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err = os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	policy := write("policy.json", p)
	expected := write("expected.json", e)
	bundle := filepath.Join(dir, "proof.json")
	if err = os.WriteFile(bundle, proof, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"verify-provenance", "--policy-file", policy, "--expected-file", expected, "--bundle-file", bundle}
	var out bytes.Buffer
	if err = run(args, &out); err != nil {
		t.Fatal(err)
	}
	var decision releaseprovenance.Decision
	if json.Unmarshal(out.Bytes(), &decision) != nil || decision.BundleSHA256 == "" {
		t.Fatal("missing actual verified provenance")
	}
	e.Tag = "v0.1.1"
	write("expected.json", e)
	out.Reset()
	if err = run(args, &out); err == nil || out.Len() != 0 {
		t.Fatal("wrong tag accepted or emitted approval")
	}
	for _, args := range [][]string{{"publish"}, {"sign"}, {"activate"}, {"verify-provenance", "--tls-ca-file", "anything"}, {"verify-seed", "--seed-file", bundle, "--seed-sha256", strings.Repeat("a", 64)}} {
		out.Reset()
		if err = run(args, &out); err == nil || out.Len() != 0 {
			t.Fatal("unexpected authority", args, err)
		}
	}
}

func TestFinalizedPublicationOutputIsExclusive(t *testing.T) {
	root, err := filepath.Abs("../../.evidence/single-owner-release/test-state")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", root)
	dir := t.TempDir()
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "metadata")
	release := bootstrap.FinalizedRelease{Files: map[string][]byte{"timestamp.json": []byte("public fixture")}, Publication: bootstrap.ReleasePublication{Version: 1, SigningProfile: bootstrap.SingleOwnerSigningProfile}}
	if err := writeFinalized(out, release); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(out, "publication.json"))
	if err != nil {
		t.Fatal(err)
	}
	var publication bootstrap.ReleasePublication
	if json.Unmarshal(raw, &publication) != nil || publication.SigningProfile != bootstrap.SingleOwnerSigningProfile {
		t.Fatal("publication handoff missing")
	}
	if err = writeFinalized(out, release); err == nil {
		t.Fatal("existing publication overwritten")
	}
	after, err := os.ReadFile(filepath.Join(out, "publication.json"))
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatal("preserved publication changed")
	}
}

func TestFinalizedOutputReadOnlyInspection(t *testing.T) {
	root, err := filepath.Abs("../../.evidence/local-release-signing/test-state")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", root)
	dir := t.TempDir()
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "output")
	release := bootstrap.FinalizedRelease{Files: map[string][]byte{"timestamp.json": []byte("public fixture")}, Publication: bootstrap.ReleasePublication{Version: 1, SigningProfile: bootstrap.SingleOwnerSigningProfile}}
	if err = writeFinalized(output, release); err != nil {
		t.Fatal(err)
	}
	if err = inspectFinalized(output, release); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(output, "timestamp.json")
	if err = os.WriteFile(file, []byte("tampered unique output"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = inspectFinalized(output, release); err == nil {
		t.Fatal("changed output accepted")
	}
	if b, _ := os.ReadFile(file); string(b) != "tampered unique output" {
		t.Fatal("inspection mutated output")
	}
	if err = os.WriteFile(file, release.Files["timestamp.json"], 0600); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(output, "extra")
	if err = os.WriteFile(extra, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = inspectFinalized(output, release); err == nil {
		t.Fatal("unreviewed output accepted")
	}
	if err = os.Remove(extra); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err = inspectFinalized(output, release); err == nil {
		t.Fatal("partial output accepted")
	}
	if err = os.WriteFile(file, release.Files["timestamp.json"], 0644); err != nil {
		t.Fatal(err)
	}
	if err = inspectFinalized(output, release); err == nil {
		t.Fatal("public output permissions accepted")
	}
}

func TestVerifySeedArchFlag(t *testing.T) {
	arm64Path := filepath.Join("..", "..", "release", "public", "linux-arm64-attested-seed.json")
	raw, err := os.ReadFile(arm64Path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	pin := hex.EncodeToString(sum[:])

	var out bytes.Buffer
	if err := run([]string{"verify-seed", "--seed-file", arm64Path, "--seed-sha256", pin, "--arch", "arm64"}, &out); err != nil {
		t.Fatalf("arm64 seed rejected with --arch arm64: %v", err)
	}
	var policy releaseprovenance.Policy
	if json.Unmarshal(out.Bytes(), &policy) != nil || policy.Repository != "fased-ai/fased-app" {
		t.Fatalf("unexpected arm64 verify-seed output: %s", out.String())
	}

	amd64Path := filepath.Join("..", "..", "release", "public", "ubuntu-attested-seed.json")
	amd64Raw, err := os.ReadFile(amd64Path)
	if err != nil {
		t.Fatal(err)
	}
	amd64Sum := sha256.Sum256(amd64Raw)
	amd64Pin := hex.EncodeToString(amd64Sum[:])

	out.Reset()
	if err := run([]string{"verify-seed", "--seed-file", amd64Path, "--seed-sha256", amd64Pin, "--arch", "arm64"}, &out); err == nil {
		t.Fatal("amd64 seed accepted for --arch arm64")
	}
	out.Reset()
	if err := run([]string{"verify-seed", "--seed-file", arm64Path, "--seed-sha256", pin, "--arch", "386"}, &out); err == nil {
		t.Fatal("unsupported --arch accepted")
	}
}
