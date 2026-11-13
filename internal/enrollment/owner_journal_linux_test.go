//go:build linux

package enrollment

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMissingOwnerJournalRefusesPendingWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if _, err := readOwnerJournal(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a fresh enrollment should have no journal", err)
	}
	pending := filepath.Join(dir, ".pending-123")
	want := []byte(`{"phase":"prepared","id":"original"}`)
	if err := os.WriteFile(pending, want, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readOwnerJournal(path); !errors.Is(err, errState) {
		t.Fatal("an unpublished write must hold the original enrollment", err)
	}
	if err := os.WriteFile(path, []byte(`{"phase":"prepared"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readOwnerJournal(path); !errors.Is(err, errState) {
		t.Fatal("a pending write must not be bypassed by an older journal", err)
	}
	if got, err := os.ReadFile(pending); err != nil || string(got) != string(want) {
		t.Fatal("pending record changed", err)
	}
	if err := os.Remove(pending); err != nil {
		t.Fatal(err)
	}
	if _, err := readOwnerJournal(path); err != nil {
		t.Fatal("a committed journal without pending writes should be readable", err)
	}
}
