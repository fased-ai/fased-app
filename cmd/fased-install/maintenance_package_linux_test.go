//go:build linux

package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestPackageMaintenanceEntryRejectsOtherArgumentsAndUnprivilegedCalls(t *testing.T) {
	id := strings.Repeat("a", 32)
	for _, args := range [][]string{
		{"--owner", "1400", "--location", "local", "--instance", "../" + id},
		{"--owner", "01400", "--location", "local", "--instance", id},
		{"--owner", "1400", "--location", "remote", "--instance", strings.ToUpper(id)},
		{"--owner", "1400", "--location", "local", "--instance", id, "--path", "/tmp"},
	} {
		if _, _, _, err := packageMaintenanceArguments(args); err == nil {
			t.Fatalf("foreign package identity accepted: %v", args)
		}
	}
	if os.Geteuid() == 0 {
		t.Skip("ordinary-owner refusal requires an unprivileged test runner")
	}
	args := []string{"--owner", "1400", "--location", "local", "--instance", id}
	if err := run(append([]string{"maintenance-package-register"}, args...), strings.NewReader(""), io.Discard, io.Discard); err == nil {
		t.Fatal("unprivileged registration was accepted")
	}
	if err := run(append([]string{"maintenance-package-request"}, args...), strings.NewReader(`{"version":1,"action":"status"}`), io.Discard, io.Discard); err == nil {
		t.Fatal("unprivileged request was accepted")
	}
}
