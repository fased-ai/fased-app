//go:build linux

package bootstrap

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestAttestedFreshCandidateCLIUsesExistingWizard(t *testing.T) {
	installer := filepath.Join(t.TempDir(), "installer.test")
	build := exec.CommandContext(t.Context(), "go", "test", "-c", "-o", installer, "./cmd/fased-install")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile candidate entry fixture: %v %s", err, output)
	}
	t.Setenv("FASED_TEST_CANDIDATE_INSTALLER", installer)
	f, options, payloads := attestedFixture(t, "staged-installer-file")
	directory := filepath.Join(f.parent, "candidate")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range payloads {
		if err := os.WriteFile(filepath.Join(directory, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(directory, "first-install-seed.json"), options.Seed, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), installer, "-test.run=^TestAttestedCandidateCLI$", "-test.count=1")
	command.Dir = filepath.Join("..", "..")
	command.Env = append(os.Environ(), "FASED_ATTESTED_CANDIDATE_FIXTURE="+directory, "FASED_FRESH_CANDIDATE_TEST=1", "XDG_CACHE_HOME="+filepath.Join(f.parent, "cache"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("actual candidate CLI: %v %s", err, output)
	}
	args, err := os.ReadFile(f.arguments)
	if err != nil || !strings.Contains(string(args), "--profile\nlocal\n") || !strings.Contains(string(args), "--interactive\n") {
		t.Fatal("candidate CLI did not reach the Local wizard", err)
	}
}

func TestAttestedCandidateFreshLocalUsesVerifiedInstaller(t *testing.T) {
	f, options, payloads := attestedFixture(t, "staged-running-installer")
	candidate := filepath.Join(f.parent, "fresh-candidate")
	if err := os.Mkdir(candidate, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range payloads {
		if err := os.WriteFile(filepath.Join(candidate, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	install := InstallOptions{Profile: "local", Prefix: filepath.Join(f.parent, "fresh-app"), DataDirectory: filepath.Join(f.parent, "fresh-data"), Start: false}
	a := CandidateAdmission{PacketSHA256: digest(payloads[AttestedPacketFile]), OperationID: strings.Repeat("1", 32), FreshProfile: "local", FreshOwner: os.Getuid(), FreshRoot: install.Prefix, FreshData: install.DataDirectory, ExpiresAt: f.now.Add(10 * time.Minute).Unix()}
	o := Options{Seed: options.Seed, SeedSHA256: options.SeedSHA256, Directory: filepath.Join(f.parent, "fresh-held"), now: options.now}
	for _, changed := range []func(*CandidateAdmission){
		func(v *CandidateAdmission) { v.BaselineSHA256 = strings.Repeat("a", 64) },
		func(v *CandidateAdmission) { v.FreshProfile = "remote" },
		func(v *CandidateAdmission) { v.FreshOwner++ },
		func(v *CandidateAdmission) { v.FreshData = v.FreshRoot },
		func(v *CandidateAdmission) { v.ExpiresAt = f.now.Unix() },
	} {
		invalid := a
		changed(&invalid)
		if p, err := PrepareAttestedCandidate(t.Context(), o, candidate, invalid); err == nil {
			p.Close()
			t.Fatal("fresh candidate accepted another baseline, profile or expired admission")
		}
		if _, err := os.Lstat(o.Directory); !os.IsNotExist(err) {
			t.Fatal("refused admission created acquisition output", err)
		}
	}
	p, err := PrepareAttestedCandidate(t.Context(), o, candidate, a)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err = p.RunUpgrade(t.Context(), UpgradeOptions{}, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "candidate admission") {
		t.Fatal("fresh admission reached bootstrap upgrade", err)
	}
	if _, err = p.ApplicationReaderBootstrapRequest(a.OperationID, update.NewProbe(), strings.Repeat("a", 64), update.CorePauseReceipt{}); err == nil || !strings.Contains(err.Error(), "candidate operation") {
		t.Fatal("fresh admission reached reader bootstrap", err)
	}
	prior := upgradeEntryFixture(t, f)
	hooks := localinstall.ApplicationUpdateCallbacks{
		BeforeStop:      func(context.Context, localinstall.Manifest, localinstall.Manifest) error { return nil },
		Health:          func(context.Context, localinstall.Manifest) error { return nil },
		RollbackAllowed: func(context.Context, localinstall.Manifest, localinstall.Manifest) error { return nil },
		Complete:        func(context.Context, localinstall.Manifest, localinstall.ApplicationUpdateStatus) error { return nil },
	}
	if _, err = p.LocalApplicationController(localinstall.Paths{Root: prior.Prefix, Data: prior.DataDirectory}, hooks); err == nil {
		t.Fatal("fresh admission authorized an existing installation update")
	}
	wrong := install
	wrong.Profile = "remote"
	if err = p.Run(t.Context(), wrong, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("fresh admission authorized another profile")
	}
	wrong = install
	wrong.DataDirectory = filepath.Join(f.parent, "other-data")
	if err = p.Run(t.Context(), wrong, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("fresh admission authorized another data path")
	}
	if err = os.Mkdir(install.DataDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(install.DataDirectory, "existing")
	if err = os.WriteFile(saved, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = p.Run(t.Context(), install, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("fresh admission accepted existing data")
	}
	if data, err := os.ReadFile(saved); err != nil || string(data) != "preserve" {
		t.Fatal("existing data changed", err)
	}
	if err = os.Remove(saved); err != nil {
		t.Fatal(err)
	}
	if err = p.Run(t.Context(), install, nil, io.Discard, io.Discard); err != nil {
		t.Fatal("verified fresh installer did not reach the existing wizard", err)
	}
	args, err := os.ReadFile(f.arguments)
	if err != nil || !strings.Contains(string(args), "--profile\nlocal\n") || !strings.Contains(string(args), "--interactive\n") {
		t.Fatal("normal Local wizard not invoked", err)
	}
	if err = p.Run(t.Context(), install, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("fresh installation replayed")
	}
}

func TestFreshCandidateRejectsLinkedTargetParent(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "linked")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	_, err := requireFreshCandidatePaths(InstallOptions{Prefix: filepath.Join(dir, "app"), DataDirectory: filepath.Join(link, "data")})
	if err == nil {
		t.Fatal("fresh candidate accepted a linked parent")
	}
	if _, err := os.Lstat(filepath.Join(dir, "app")); !os.IsNotExist(err) {
		t.Fatal("refusal created an installation path", err)
	}
}

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
