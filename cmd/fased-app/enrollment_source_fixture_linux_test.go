//go:build linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

// B05 consumes an existing legacy Remote service, independently of the retail
// installation/Tailscale journey. Only the explicit disposable hosted runner
// can construct that source. No corresponding command exists in product builds.
func TestPrepareLegacyRemoteEnrollmentSource(t *testing.T) {
	if os.Getenv("FASED_ENROLLMENT_SOURCE_FIXTURE") != "1" {
		t.Skip("explicit disposable hosted enrollment source only")
	}
	if !legacyRemoteSourceHostedBoundary() {
		t.Fatal("disposable hosted Ubuntu enrollment source required")
	}
	workspace, err := os.Getwd()
	if err != nil || workspace != os.Getenv("GITHUB_WORKSPACE") {
		t.Fatal("fixed hosted workspace required")
	}
	directory := os.Getenv("FASED_ENROLLMENT_SOURCE_BUNDLE")
	allowed := false
	for _, name := range []string{"b05-enrollment-installed", "b05-ordinary-installed"} {
		allowed = allowed || directory == filepath.Join(workspace, ".evidence", name, "unpacked", "fased-app-linux-amd64")
	}
	if !allowed {
		t.Fatal("declared disposable bundle required")
	}
	m, err := bundle.VerifyPinned(directory, os.Getenv("FASED_ENROLLMENT_SOURCE_BUNDLE_SHA"))
	if err != nil || m.Source != os.Getenv("GITHUB_SHA") || !m.UnsignedEngineering || m.Desktop {
		t.Fatal("exact engineering source bundle required", err)
	}
	engine, _ := bundle.Names()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	installed, err := vpsinstall.Install(ctx, vpsinstall.Options{
		Core: filepath.Join(directory, engine), CoreSHA256: m.Files[engine].SHA256,
		Listen: os.Getenv("FASED_ENROLLMENT_SOURCE_LISTEN"), PrepareHost: true, Start: true,
	}, &custodyinstall.System{NonInteractive: true, Out: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	if installed.OwnerUID != os.Getuid() || installed.AppUID == os.Getuid() || installed.AppUID <= 0 || installed.PublicOrigin != "" {
		t.Fatal("legacy source identity changed")
	}
	t.Log("disposable legacy source only; not retail installation or private-access acceptance")
}

func legacyRemoteSourceHostedBoundary() bool {
	if os.Getuid() <= 0 || os.Getuid() != os.Geteuid() || os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" || os.Getenv("FASED_ENROLLMENT_ACCEPTANCE") != "1" {
		return false
	}
	release, err := os.ReadFile("/etc/os-release")
	return err == nil && strings.Contains(string(release), "VERSION_ID=\"24.04\"") && strings.Contains(string(release), "ID=ubuntu")
}

func TestLegacyRemoteSourceRefusesOutsideHostedEnrollment(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "false")
	if legacyRemoteSourceHostedBoundary() {
		t.Fatal("owner machine admitted for disposable source preparation")
	}
}
