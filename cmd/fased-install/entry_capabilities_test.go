//go:build !windows

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstalledUpdateEntryCapabilitiesFailClosed(t *testing.T) {
	for _, test := range []struct {
		body string
		ok   bool
	}{
		{`printf '{"version":1,"update":true}\n'`, true},
		{`printf '{"version":"old"}\n'`, false},
		{`exit 2`, false},
		{`printf '{"version":2,"update":true}\n'`, false},
	} {
		path := filepath.Join(t.TempDir(), "core")
		script := "#!/bin/sh\n" + `[ "$1" = version ] && [ "$2" = --entry-json ] || exit 19` + "\n" + test.body + "\n"
		if e := os.WriteFile(path, []byte(script), 0700); e != nil {
			t.Fatal(e)
		}
		if got := installedUpdateEntrySupported(context.Background(), path); got != test.ok {
			t.Fatal(test, got)
		}
	}
}

func TestInitialWizardCompletesOlderInstalledEntry(t *testing.T) {
	for _, tc := range []struct {
		action          string
		supported       bool
		answer          string
		upgrade, prompt bool
	}{
		{"install", false, "y\n", true, true},
		{"install", false, "n\n", false, true},
		{"install", true, "", false, false},
		{"update", false, "", true, false},
		{"open", false, "", false, false},
	} {
		var out bytes.Buffer
		err := entryUpgradeDecision(tc.action, tc.supported, strings.NewReader(tc.answer), &out)
		if errors.Is(err, errEntryUpgradeRequired) != tc.upgrade || (out.Len() > 0) != tc.prompt {
			t.Fatal(tc, err, out.String())
		}
		if tc.prompt && !tc.upgrade && err == nil {
			t.Fatal("declined upgrade reported setup complete")
		}
	}
}
