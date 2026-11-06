package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestInstalledProviderFixtureReachesProtectedValidation(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{"serve", "--ai-fixture", "--instance-location", "remote", "--require-sandbox", "--installation-root", "/never-existing-fased-provider-test", "--remote-config", "/never-existing-fased-provider-test/install.json", "--public-origin", "https://fased-test.example.ts.net"}, os.Stdin, &out, &out)
	if err == nil || strings.Contains(err.Error(), "development fixtures are local-only") || out.Len() != 0 {
		t.Fatal("installed provider fixture did not reach protected installation validation", err, out.String())
	}
}

func TestInstalledProviderFixtureRejectsUnprotectedCombinations(t *testing.T) {
	for _, extra := range [][]string{
		{"--instance-location", "remote"},
		{"--session-fixture"},
		{"--public-origin", "https://fased-test.example.ts.net"},
		{"--require-sandbox"},
		{"--custody-config", "/never-existing-fased-signer.json"},
		{"--wen-research-origin", "https://fased-test.example.ts.net"},
	} {
		dir := t.TempDir() + "/never-created"
		args := append([]string{"serve", "--data-dir", dir, "--ai-fixture"}, extra...)
		var out bytes.Buffer
		if err := run(args, os.Stdin, &out, &out); err == nil || out.Len() != 0 {
			t.Fatal("unprotected provider mode started", extra, err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("rejected startup mutated state", extra, err)
		}
	}
}
