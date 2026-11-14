//go:build linux

package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAttestedCandidateRetainsExactInstallerForMaintenance(t *testing.T) {
	f, options, payloads := attestedFixture(t, "staged")
	candidate := filepath.Join(f.parent, "candidate-pair")
	if err := os.Mkdir(candidate, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range payloads {
		if err := os.WriteFile(filepath.Join(candidate, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var packet AttestedPacket
	if err := json.Unmarshal(payloads[AttestedPacketFile], &packet); err != nil {
		t.Fatal(err)
	}
	admission := CandidateAdmission{PacketSHA256: digest(payloads[AttestedPacketFile]), OperationID: strings.Repeat("1", 32), BaselineSHA256: strings.Repeat("2", 64), ExpiresAt: f.now.Add(10 * time.Minute).Unix()}
	o := Options{Seed: options.Seed, SeedSHA256: options.SeedSHA256, Directory: filepath.Join(f.parent, "held-pair"), now: options.now}
	p, err := PrepareAttestedCandidate(t.Context(), o, candidate, admission)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, _, err = p.AcquireMaintenanceInstaller(t.Context()); err == nil {
		t.Fatal("ordinary candidate gained protected maintenance authority")
	}
	// Simulate the existing protected-owner constructor; this is not OS/root
	// acceptance and cannot grant that authority outside this package test.
	p.options.protectedOwner = 1000
	path, identity, err := p.AcquireMaintenanceInstaller(t.Context())
	if err != nil || identity != packet.Installer {
		t.Fatalf("matching attested installer unavailable: %v", err)
	}
	if err = os.WriteFile(filepath.Join(candidate, packet.Installer.File), []byte("changed source"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || digest(got) != packet.Installer.SHA256 {
		t.Fatal("held installer changed with its source")
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("changed held installer"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = p.AcquireMaintenanceInstaller(t.Context()); err == nil {
		t.Fatal("changed held installer admitted")
	}
}
