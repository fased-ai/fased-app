package core

import (
	"context"
	"github.com/fased-ai/fased-app/internal/update"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectionColdMetadataNoMigration(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	expected, e := s.UpdateSnapshot(context.Background(), update.NewProbe())
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	p := filepath.Join(dir, "core.sqlite")
	before, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	out, e := InspectFixtureMetadata(context.Background(), dir, update.NewProbe())
	if e != nil || !update.SameCoreMetadata(expected, out) {
		t.Fatal("cold metadata", e)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatal("cold database changed")
	}
	if _, e = InspectFixtureMetadata(context.Background(), t.TempDir(), update.NewProbe()); e == nil {
		t.Fatal("missing database initialized")
	}
}
