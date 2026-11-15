//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

func TestPackageDefaultSelectsOnlyOneVerifiedLocalInstance(t *testing.T) {
	const owner = 1000
	first, second := strings.Repeat("a", 32), strings.Repeat("b", 32)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, first), 0700); err != nil {
		t.Fatal(err)
	}
	load := func(uid int, root string) (custodyinstall.Manifest, error) {
		return custodyinstall.Manifest{Layout: custodyinstall.PackageLayout, Owner: uid, Root: root, Location: "local", ID: filepath.Base(root)}, nil
	}
	entries, _ := os.ReadDir(dir)
	data, err := selectPackageLocalData(owner, entries, load)
	if err != nil || data != "/var/lib/fased-app-package/1000/local/"+first+"/app-state" {
		t.Fatal("single verified Local instance was not selected", data, err)
	}
	if _, err = selectPackageLocalData(owner, nil, load); err == nil {
		t.Fatal("missing instance fell back to another installation")
	}
	foreign := func(uid int, root string) (custodyinstall.Manifest, error) {
		m, err := load(uid, root)
		m.Owner++
		return m, err
	}
	if _, err = selectPackageLocalData(owner, entries, foreign); err == nil {
		t.Fatal("foreign owner was selected")
	}
	if err = os.Mkdir(filepath.Join(dir, second), 0700); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(dir)
	if _, err = selectPackageLocalData(owner, entries, load); err == nil {
		t.Fatal("multiple instances were silently selected")
	}
	if err = os.Remove(filepath.Join(dir, second)); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(dir, first), filepath.Join(dir, second)); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(dir)
	if _, err = selectPackageLocalData(owner, entries, load); err == nil {
		t.Fatal("linked instance was selected")
	}
}
