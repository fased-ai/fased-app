//go:build linux

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnrollmentRequiresExactSourceAndConsentBeforeDiscovery(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "not-installed")
	valid := []string{"--source", "local", "--expected-instance", strings.Repeat("a", 43), "--network", "devnet", "--signer-binary", "/not/a/signer", "--prefix", missing, "--data-dir", filepath.Join(root, "state"), "--disposable-fixture", "--prepare-host"}
	for _, drop := range []string{"--source", "--expected-instance", "--network", "--signer-binary", "--disposable-fixture", "--prepare-host"} {
		var args []string
		for i := 0; i < len(valid); i++ {
			if valid[i] == drop {
				if drop != "--disposable-fixture" && drop != "--prepare-host" {
					i++
				}
				continue
			}
			args = append(args, valid[i])
		}
		var out bytes.Buffer
		if err := enrollmentCommand(args, strings.NewReader(""), &out, io.Discard); err == nil || out.Len() != 0 {
			t.Fatal("unconsented source accepted", drop, err)
		}
		if _, err := os.Lstat(missing); !os.IsNotExist(err) {
			t.Fatal("source discovery created an installation")
		}
	}
	var out bytes.Buffer
	if err := enrollmentCommand(valid, strings.NewReader(""), &out, io.Discard); err == nil || out.Len() != 0 {
		t.Fatal("missing source adopted", err)
	}
	if _, err := os.Lstat(missing); !os.IsNotExist(err) {
		t.Fatal("missing source was created")
	}
}
