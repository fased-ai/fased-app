//go:build linux

package signer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestColdInspectionNoCreateRepairOrAppend(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	if _, e := openStorageReadonly(dir); e == nil {
		t.Fatal("cold reader created missing lock")
	}
	if _, e := os.Stat(filepath.Join(dir, "vault.lock")); !os.IsNotExist(e) {
		t.Fatal("missing lock created")
	}
	os.WriteFile(filepath.Join(dir, "vault.lock"), []byte{}, 0600)
	s, e := openStorageReadonly(dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.create([]byte("no")); e == nil {
		t.Fatal("cold vault write")
	}
	if e = s.appendJournal(&journal{name: "operations.jsonl"}, journalRecord{}); e == nil {
		t.Fatal("cold journal append")
	}
	s.close()
	before := []byte("unknown pending: preserve")
	os.WriteFile(filepath.Join(dir, "vault.pending"), before, 0600)
	if _, e = openStorageReadonly(dir); e == nil {
		t.Fatal("pending publication repaired")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "vault.pending"))
	if string(before) != string(after) {
		t.Fatal("pending state changed")
	}
}
