package localinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func installFixture(t *testing.T) (Paths, string) {
	t.Helper()
	root, err := filepath.Abs("../../.evidence/install-fixtures")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "f-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	engine := filepath.Join(dir, "source-core")
	if err = os.WriteFile(engine, []byte("reconstructible binary fixture, never executed"), 0700); err != nil {
		t.Fatal(err)
	}
	return Paths{Root: filepath.Join(dir, "app"), Data: filepath.Join(dir, "state")}, engine
}
func TestInstallRetryIdentityAndData(t *testing.T) {
	p, engine := installFixture(t)
	m, err := Install(t.Context(), p, Options{Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	if m.Phase != "ready" || m.Autostart {
		t.Fatal("unexpected install state")
	}
	keep := filepath.Join(p.Data, "owner-state")
	if err = os.WriteFile(keep, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(filepath.Join(p.Root, "fased-app"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Install(t.Context(), p, Options{Engine: engine}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(filepath.Join(p.Root, "fased-app"))
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("same identity was rewritten")
	}
	if err = os.WriteFile(engine, []byte("different source"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = Install(t.Context(), p, Options{Engine: engine}); err == nil {
		t.Fatal("unverified update was accepted")
	}
	data, err := os.ReadFile(keep)
	if err != nil || string(data) != "preserve" {
		t.Fatal("app data changed")
	}
	if err = os.WriteFile(filepath.Join(p.Root, "fased-app"), []byte("tampered"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = verify(m); err == nil {
		t.Fatal("tampered executable accepted")
	}
}
func TestInterruptedFirstInstallResumesWithoutAdoptingUnknownFiles(t *testing.T) {
	p, engine := installFixture(t)
	m, err := Install(t.Context(), p, Options{Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	m.Phase = "preparing"
	if err = core.WritePrivateJSON(filepath.Join(p.Root, "install.json"), m); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(p.Root, "fased-app")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(p.Root, ".staging-fased-app"), []byte("partial copy"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = Install(t.Context(), p, Options{Engine: engine}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(p.Root, ".staging-fased-app")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("staging copy remains")
	}
	other, source := installFixture(t)
	if err = os.MkdirAll(other.Root, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(other.Root, "unknown"), []byte("user file"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Install(context.Background(), other, Options{Engine: source}); err == nil {
		t.Fatal("unknown directory was adopted")
	}
}
func TestDefinitionRejectsExposureAndEscapesPaths(t *testing.T) {
	p, _ := installFixture(t)
	if _, err := definition(p, Options{Listen: "0.0.0.0:7331"}); err == nil {
		t.Fatal("public listener allowed")
	}
	if _, err := definition(p, Options{Listen: "127.0.0.1:7331", PublicOrigin: "http://public.example"}); err == nil {
		t.Fatal("insecure proxy origin allowed")
	}
	p.Root += " space%$\""
	unit, err := definition(p, Options{Listen: "127.0.0.1:7331"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), "space%%$$\\\"") || !strings.Contains(string(unit), "NoNewPrivileges=yes") || !strings.Contains(string(unit), "ProtectHome=read-only") || !strings.Contains(string(unit), "ReadWritePaths=") {
		t.Fatal("service quoting or hardening absent")
	}
}
