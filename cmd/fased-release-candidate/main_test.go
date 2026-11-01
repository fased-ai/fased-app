package main

import (
	"bytes"
	"encoding/json"
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
