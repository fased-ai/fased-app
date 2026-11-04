//go:build linux && fased_candidate_acceptance

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCandidateAcceptanceNeedsCompiledScope(t *testing.T) {
	for _, args := range [][]string{nil, {"--candidate", "/arbitrary"}} {
		if err := candidateAcceptanceCommand(args, io.Discard, io.Discard); err == nil {
			t.Fatal("runtime input or absent compiled authority admitted")
		}
	}
}

func TestCandidateAcceptanceCompletedFixture(t *testing.T) {
	path := os.Getenv("FASED_CANDIDATE_ENTRY_FIXTURE")
	if path == "" {
		t.Skip("controlled parent fixture only")
	}
	var fixture struct {
		Seed       []byte `json:"seed"`
		SeedSHA256 string `json:"seedSha256"`
		candidateAcceptanceBinding
	}
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &fixture) != nil {
		t.Fatal("invalid disposable entry fixture", err)
	}
	compiledApplicationChannel = &applicationChannelBinding{Seed: fixture.Seed, SHA256: fixture.SeedSHA256}
	compiledCandidateAcceptance = &fixture.candidateAcceptanceBinding
	files := []string{filepath.Join(fixture.Local.Root, "install.json"), filepath.Join(fixture.Local.Root, "fased-app"), filepath.Join(filepath.Dir(fixture.Local.Root), "."+filepath.Base(fixture.Local.Root)+"-updates", "current.json")}
	before := map[string][]byte{}
	for _, file := range files {
		before[file], err = os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err = candidateAcceptanceCommand(nil, &out, io.Discard); err != nil || out.String() != "Already current.\n" {
		t.Fatal("repeat did not use read-only current path", out.String(), err)
	}
	for _, file := range files {
		after, err := os.ReadFile(file)
		if err != nil || !bytes.Equal(before[file], after) {
			t.Fatal("repeat changed installed record", file, err)
		}
	}
}
