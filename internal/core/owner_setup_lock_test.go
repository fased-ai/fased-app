package core

import (
	"os"
	"testing"
)

func TestOwnerSetupLockWithOpenStore(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	lock, err := LockOwnerSetup(dir)
	if err != nil {
		t.Fatal("owner setup must work while the core database is open:", err)
	}
	if second, err := LockOwnerSetup(dir); err == nil {
		second.Close()
		lock.Close()
		t.Fatal("concurrent owner setup was not refused")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := LockOwnerSetup(dir)
	if err != nil {
		t.Fatal("owner setup lock was not released:", err)
	}
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}
}
