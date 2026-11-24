package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestDoctorAndBackupRestoreLifecycle(t *testing.T) {
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src-data")
	if err := os.MkdirAll(srcDir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := core.OpenStore(srcDir)
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	th, err := store.CreateThread(t.Context(), "wen-operator", "Backup Continuity Thread", "", "")
	if err != nil {
		t.Fatalf("CreateThread failed: %v", err)
	}

	// 1. fased doctor --json works cleanly against populated dataDir.
	var docOut bytes.Buffer
	if err := run([]string{"doctor", "--data-dir", srcDir, "--json"}, nil, &docOut, io.Discard); err != nil {
		t.Fatalf("doctor failed: %v", err)
	}
	var doc doctorReport
	if err := json.Unmarshal(docOut.Bytes(), &doc); err != nil {
		t.Fatalf("invalid doctor json: %v\n%s", err, docOut.String())
	}
	if !doc.Healthy || !doc.DataDirExists || !doc.DataDirPrivate || len(doc.Databases) != 2 {
		t.Fatalf("unexpected doctor report: %+v", doc)
	}
	for _, db := range doc.Databases {
		if !db.Exists || !db.Healthy || db.QuickCheck != "ok" {
			t.Fatalf("expected healthy database %s, got %+v", db.Name, db)
		}
	}

	// 2. Hot backup succeeds while store is open (VACUUM INTO).
	archivePath := filepath.Join(tmp, "fased-backup.tar.gz")
	var bkOut bytes.Buffer
	if err := run([]string{"backup", "--data-dir", srcDir, "--out", archivePath, "--json"}, nil, &bkOut, io.Discard); err != nil {
		t.Fatalf("backup failed: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// 3. Restore into a fresh directory reproduces both core.sqlite & agents.sqlite cleanly.
	dstDir := filepath.Join(tmp, "dst-data")
	var rsOut bytes.Buffer
	if err := run([]string{"restore", "--from", archivePath, "--data-dir", dstDir, "--json"}, nil, &rsOut, io.Discard); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	restored, err := core.OpenStore(dstDir)
	if err != nil {
		t.Fatalf("OpenStore on restored dir failed: %v", err)
	}
	detail, err := restored.GetThread(t.Context(), th.ID)
	if err != nil || detail.Thread.Title != "Backup Continuity Thread" {
		t.Fatalf("restored thread mismatch: detail=%+v err=%v", detail, err)
	}
	_ = restored.Close()

	// 4. Restore refuses overwriting existing state without --force, succeeds with --force.
	if err := run([]string{"restore", "--from", archivePath, "--data-dir", dstDir}, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("restore without --force unexpectedly overwrote existing core.sqlite")
	}
	if err := run([]string{"restore", "--from", archivePath, "--data-dir", dstDir, "--force"}, nil, io.Discard, io.Discard); err != nil {
		t.Fatalf("restore --force failed: %v", err)
	}

	// 5. Restore refuses overwriting a locked/running instance even with --force.
	lock, err := core.LockInstance(filepath.Join(dstDir, "instance.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := run([]string{"restore", "--from", archivePath, "--data-dir", dstDir, "--force"}, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("restore unexpectedly ran while instance.lock was held")
	}
}
