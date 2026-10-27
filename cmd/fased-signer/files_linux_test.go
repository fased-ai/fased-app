//go:build linux

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func ownerFixture(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../.evidence/p3-signer")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "owner-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if filepath.Dir(dir) != root {
			t.Fatal("cleanup root")
		}
		os.RemoveAll(dir)
	})
	return dir
}
func TestOwnerBackupFilesExclusiveAndPrivate(t *testing.T) {
	dir := ownerFixture(t)
	path := filepath.Join(dir, "backup.json")
	payload := []byte("disposable encrypted fixture")
	if err := writeOwnerFile(path, payload); err != nil {
		t.Fatal(err)
	}
	if err := writeOwnerFile(path, []byte("overwrite")); err == nil {
		t.Fatal("backup overwritten")
	}
	b, err := readOwnerFile(path)
	if err != nil || !bytes.Equal(b, payload) {
		t.Fatal("readback")
	}
	os.Chmod(path, 0644)
	if _, err = readOwnerFile(path); err == nil {
		t.Fatal("broad backup accepted")
	}
	os.Chmod(path, 0600)
	link := filepath.Join(dir, "link.json")
	os.Symlink(path, link)
	if _, err = readOwnerFile(link); err == nil {
		t.Fatal("symlink backup accepted")
	}
}
func TestOwnerOutputRejectsWritableOrSymlinkAncestors(t *testing.T) {
	dir := ownerFixture(t)
	parent := filepath.Join(dir, "writable")
	os.Mkdir(parent, 0777)
	os.Chmod(parent, 0777)
	private := filepath.Join(parent, "private")
	os.Mkdir(private, 0700)
	if err := writeOwnerFile(filepath.Join(private, "secret"), []byte("disposable fixture")); err == nil {
		t.Fatal("writable parent can substitute output directory")
	}
	link := filepath.Join(dir, "alias")
	os.Symlink(private, link)
	if err := writeOwnerFile(filepath.Join(link, "other"), []byte("disposable fixture")); err == nil {
		t.Fatal("symlink output directory accepted")
	}
}
