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
	"time"
)

func TestInstalledUpdateEntryCapabilitiesFailClosed(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		ok   bool
	}{
		{"legacy generic entry", `printf '{"version":1,"update":true}\n'`, false},
		{"packet1 only", `printf '{"version":1,"update":true,"releasePacketVersions":[1]}\n'`, false},
		{"packet1 and packet2", `printf '{"version":1,"update":true,"releasePacketVersions":[1,2]}\n'`, true},
		{"update disabled", `printf '{"version":1,"update":false,"releasePacketVersions":[1,2]}\n'`, false},
		{"missing update", `printf '{"version":1,"releasePacketVersions":[1,2]}\n'`, false},
		{"missing version", `printf '{"update":true,"releasePacketVersions":[1,2]}\n'`, false},
		{"malformed version", `printf '{"version":"old"}\n'`, false},
		{"malformed packet versions", `printf '{"version":1,"update":true,"releasePacketVersions":["2"]}\n'`, false},
		{"null packet version", `printf '{"version":1,"update":true,"releasePacketVersions":[1,null,2]}\n'`, false},
		{"invalid trailing packet version", `printf '{"version":1,"update":true,"releasePacketVersions":[2,0]}\n'`, false},
		{"empty packet versions", `printf '{"version":1,"update":true,"releasePacketVersions":[]}\n'`, false},
		{"trailing response", `printf '{"version":1,"update":true,"releasePacketVersions":[1,2]}{}\n'`, false},
		{"failed command", `exit 2`, false},
		{"future entry ABI", `printf '{"version":2,"update":true,"releasePacketVersions":[1,2]}\n'`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "core")
			script := "#!/bin/sh\n" + `[ "$#" -eq 2 ] && [ "$1" = version ] && [ "$2" = --entry-json ] || exit 19` + "\n" + test.body + "\n"
			if e := os.WriteFile(path, []byte(script), 0700); e != nil {
				t.Fatal(e)
			}
			if got := installedUpdateEntrySupported(context.Background(), path); got != test.ok {
				t.Fatalf("entry support = %v, want %v", got, test.ok)
			}
		})
	}
}

func TestInstalledUpdateEntryCapabilityProbeBounds(t *testing.T) {
	const capability = `{"version":1,"update":true,"releasePacketVersions":[1,2]}`
	for _, size := range []int{8192, 8193} {
		path := filepath.Join(t.TempDir(), "core")
		body := capability + strings.Repeat(" ", size-len(capability))
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s' '"+body+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
		if got := installedUpdateEntrySupported(t.Context(), path); got != (size == 8192) {
			t.Fatalf("%d-byte capability response support = %v", size, got)
		}
	}
	path := filepath.Join(t.TempDir(), "core")
	if installedUpdateEntrySupported(t.Context(), path) {
		t.Fatal("missing executable reported capability support")
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if installedUpdateEntrySupported(ctx, path) {
		t.Fatal("timed-out executable reported capability support")
	}
	if ctx.Err() == nil {
		t.Fatal("probe did not reach the caller's cancellation deadline")
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
