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

func TestInspectionColdRefusesPendingRollbackJournal(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	p := filepath.Join(dir, "core.sqlite")
	before, _ := os.ReadFile(p)
	if e = os.WriteFile(p+"-journal", []byte("uncertain transaction"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = InspectFixtureColdMetadata(context.Background(), dir, update.NewProbe()); e == nil {
		t.Fatal("pending rollback journal admitted")
	}
	after, _ := os.ReadFile(p)
	if string(after) != string(before) {
		t.Fatal("pending database repaired")
	}
}

func TestInspectionColdRefusesForeignWAL(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	p := filepath.Join(dir, "core.sqlite-wal")
	target := filepath.Join(dir, "foreign-empty")
	os.WriteFile(target, nil, 0600)
	if e = os.Symlink(target, p); e != nil {
		t.Fatal(e)
	}
	if _, e = InspectFixtureColdMetadata(context.Background(), dir, update.NewProbe()); e == nil {
		t.Fatal("symlink WAL admitted")
	}
}

func TestColdPauseGuardRequiresOriginalDurableFence(t *testing.T) {
	s := fixtureStore(t)
	command := pauseCommand(t, s, 1)
	receipt, err := s.UpdatePause(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	dir := s.dir
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := InspectFixtureColdMetadata(t.Context(), dir, update.NewProbe())
	if err != nil {
		t.Fatal(err)
	}
	requestDigest := update.SnapshotDigest("pause-request", *command.Request)
	instanceDigest := update.SnapshotDigest("atomic-instance:v1", cold.Core.InstanceID)
	if err := ValidateFixturePauseGuard(cold, "paused", command.Request.IntentID, receipt.PauseID, requestDigest, instanceDigest); err != nil {
		t.Fatal(err)
	}
	if err := ValidateFixturePauseGuard(cold, "released", command.Request.IntentID, receipt.PauseID, requestDigest, instanceDigest); err == nil {
		t.Fatal("paused core admitted as released")
	}
	if err := ValidateFixturePauseGuard(cold, "paused", update.NewProbe(), receipt.PauseID, requestDigest, instanceDigest); err == nil {
		t.Fatal("foreign intent accepted")
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "release", ProbeID: command.ProbeID, Aborted: aborted(command)}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err = InspectFixtureColdMetadata(t.Context(), dir, update.NewProbe())
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateFixturePauseGuard(cold, "released", command.Request.IntentID, receipt.PauseID, requestDigest, instanceDigest); err != nil {
		t.Fatal(err)
	}
}
