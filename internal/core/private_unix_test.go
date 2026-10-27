//go:build !windows

package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateStateRejectsSymlinksAndBroadPermissions(t *testing.T) {
	dir := fixtureDir(t)
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(dir); err == nil {
		t.Fatal("public state directory accepted")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "original")
	if err := os.WriteFile(other, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(dir, "linked")
	if err := os.Symlink(other, linked); err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateJSON(linked, map[string]bool{"replace": true}); err == nil {
		t.Fatal("symlink target overwritten")
	}
	data, err := os.ReadFile(other)
	if err != nil || string(data) != "preserve" {
		t.Fatal("unrelated file changed")
	}
	if err = os.Chmod(other, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = readPrivateFile(other); err == nil {
		t.Fatal("public credential file read")
	}
}
