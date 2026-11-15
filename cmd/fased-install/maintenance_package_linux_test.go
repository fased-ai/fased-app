//go:build linux

package main

import (
	"io"
	"os"
	"path/filepath"
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
	if err := run(append(append([]string{"maintenance-package-gate"}, args...), "--role", "root"), strings.NewReader(""), io.Discard, io.Discard); err == nil {
		t.Fatal("invalid service gate role was accepted")
	}
}

func TestPackageCandidateRequiresAnOriginalReservation(t *testing.T) {
	parent := t.TempDir()
	directory, reservation := filepath.Join(parent, "candidate"), filepath.Join(parent, "reservation.json")
	if err := requirePackageCandidateReservation(directory, reservation); err != nil {
		t.Fatal("new candidate was refused", err)
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	unique := filepath.Join(directory, "preserve")
	if err := os.WriteFile(unique, []byte("unclassified"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := requirePackageCandidateReservation(directory, reservation); err == nil {
		t.Fatal("a new reservation would classify an existing directory")
	}
	if contents, err := os.ReadFile(unique); err != nil || string(contents) != "unclassified" {
		t.Fatal("unclassified directory changed", err)
	}
	if err := os.WriteFile(reservation, []byte("present"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := requirePackageCandidateReservation(directory, reservation); err != nil {
		t.Fatal("reserved retry was refused before protected reservation validation", err)
	}
}
