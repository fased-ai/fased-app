package bootstrap

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplicationUpdateAdmissionBindsSignedBytesWithoutExecution(t *testing.T) {
	f := newFirstFixture(t)
	p, e := Prepare(t.Context(), f.options())
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	paths := localinstall.Paths{Root: filepath.Join(f.parent, "installed"), Data: filepath.Join(f.parent, "app-state")}
	for _, d := range []string{paths.Root, paths.Data} {
		if e = os.Mkdir(d, 0700); e != nil {
			t.Fatal(e)
		}
	}
	old := []byte("old inert application")
	unit := []byte("owned fixture unit")
	if e = os.WriteFile(filepath.Join(paths.Root, "fased-app"), old, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(paths.Root, "fased-app.service"), unit, 0600); e != nil {
		t.Fatal(e)
	}
	m := localinstall.Manifest{Schema: 1, Phase: "ready", Version: "old", Root: paths.Root, Data: paths.Data, Files: map[string]string{"fased-app": digest(old), "fased-app.service": digest(unit)}}
	if e = core.WritePrivateJSON(filepath.Join(paths.Root, "install.json"), m); e != nil {
		t.Fatal(e)
	}
	callbacks := 0
	h := localinstall.ApplicationUpdateCallbacks{BeforeStop: func(context.Context, localinstall.Manifest, localinstall.Manifest) error { callbacks++; return nil }, Health: func(context.Context, localinstall.Manifest) error { callbacks++; return nil }, RollbackAllowed: func(context.Context, localinstall.Manifest, localinstall.Manifest) error { callbacks++; return nil }, Complete: func(context.Context, localinstall.Manifest, localinstall.ApplicationUpdateStatus) error {
		callbacks++
		return nil
	}}
	driver, e := p.LocalApplicationController(paths, h)
	if e != nil {
		t.Fatal(e)
	}
	release, e := driver.Check(t.Context())
	if e != nil || release == nil || release.EngineSHA256 != p.engine.SHA256 || release.ManifestSHA256 != p.report.DescriptorSHA256 {
		t.Fatal(release, e)
	}
	changed := *release
	changed.EngineSHA256 = strings.Repeat("f", 64)
	if _, e = driver.Apply(t.Context(), core.ApplicationUpdateOperation{ID: strings.Repeat("1", 32), Release: &changed}); !errors.Is(e, core.ErrApplicationUpdateNotStarted) {
		t.Fatal("changed engine admitted", e)
	}
	if callbacks != 0 {
		t.Fatal("unapproved callback")
	}
	if _, e = os.Stat(f.marker); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("fixture artifact executed", e)
	}
	target := m
	target.Files = map[string]string{"fased-app": p.engine.SHA256, "fased-app.service": digest(unit)}
	target.Version = p.descriptor.ProductVersion
	target.ApplicationSequence = p.report.Sequence
	target.ApplicationReleaseSHA256 = p.report.DescriptorSHA256
	status := localinstall.ApplicationUpdateStatus{Version: 1, ID: strings.Repeat("2", 32), Phase: "completed", Sequence: p.report.Sequence, ReleaseSHA256: p.report.DescriptorSHA256, EngineSHA256: p.engine.SHA256, PreviousManifestSHA256: localinstall.ManifestSHA256(m), TargetManifestSHA256: localinstall.ManifestSHA256(target)}
	journalDir := filepath.Join(filepath.Dir(paths.Root), "."+filepath.Base(paths.Root)+"-updates")
	if e = os.MkdirAll(journalDir, 0700); e != nil {
		t.Fatal(e)
	}
	journalPath := filepath.Join(journalDir, "current.json")
	if e = core.WritePrivateJSON(journalPath, map[string]any{"status": status, "previous": m, "target": target}); e != nil {
		t.Fatal(e)
	}
	if r, e := driver.Check(t.Context()); e == nil || r != nil {
		t.Fatal("consumed completed release reoffered against contradictory manifest", r, e)
	}
	if e = os.Remove(journalPath); e != nil {
		t.Fatal(e)
	}
	engine, _ := bundle.Names()
	if e = os.WriteFile(filepath.Join(p.report.BundleDirectory, engine), []byte("tampered"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = driver.Check(t.Context()); e == nil {
		t.Fatal("changed signed bytes admitted")
	}
	got, e := os.ReadFile(filepath.Join(paths.Root, "fased-app"))
	if e != nil || string(got) != string(old) {
		t.Fatal("installed code changed before approval", e)
	}
}
