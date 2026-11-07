//go:build linux

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeEnrollmentResumeUnreadableJournalRefuses(t *testing.T) {
	for _, state := range []string{"missing", "malformed", "symlink"} {
		t.Run(state, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			journal := filepath.Join(dir, "fased-app-enrollment", "state.json")
			if err := os.Mkdir(filepath.Dir(journal), 0700); err != nil {
				t.Fatal(err)
			}
			if state == "malformed" {
				if err := os.WriteFile(journal, []byte("{broken"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if state == "symlink" {
				if err := os.Symlink("/dev/null", journal); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			args := []string{"--expected-instance", "retained", "--source", "remote", "--network", "devnet", "--core-sha256", strings.Repeat("e", 64), "--signer-sha256", strings.Repeat("f", 64)}
			if err := nativeEnrollmentResume(args, &out); err == nil || out.Len() != 0 {
				t.Fatal("unclassified journal granted a resume", err, out.String())
			}
		})
	}
}
