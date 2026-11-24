package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteQuickCheckAndWALCheckpoint(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	if err := sqliteQuickCheck(context.Background(), s.db, "core.sqlite"); err != nil {
		t.Fatalf("quick_check on healthy core.sqlite failed: %v", err)
	}

	// Initialize agents.sqlite too and verify WAL checkpoint covers both DBs cleanly.
	if _, err := s.CreateThread(context.Background(), "wen-operator", "Checkpoint check", "", ""); err != nil {
		t.Fatalf("CreateThread failed: %v", err)
	}
	if err := s.CheckpointWAL(context.Background()); err != nil {
		t.Fatalf("CheckpointWAL failed: %v", err)
	}

	// Reopening an existing store passes startup quick_check cleanly.
	if err := s.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("reopen OpenStore failed: %v", err)
	}
	_ = reopened.Close()

	// Corrupted core.sqlite must be rejected at startup.
	corruptDir := t.TempDir()
	if err := os.Chmod(corruptDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corruptDir, "core.sqlite"), []byte("not-a-valid-sqlite-database-file"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(corruptDir); err == nil {
		t.Fatal("OpenStore unexpectedly opened corrupted core.sqlite")
	}
}
