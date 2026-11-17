//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

func TestPackageCommandKeepsOfflineRecoveryInBootstrap(t *testing.T) {
	for _, args := range [][]string{{"update", "recover"}, {"updates", "recover", "--data-dir", "/preserved"}, {"update", "--remote"}, {"update", "status", "--remote=true"}, {"update", "-remote=1"}, {"version", "--entry-json"}, {"native-release-binding"}, {"compatibility"}} {
		_, _, delegate, err := packageCommandTarget(args)
		if err != nil || delegate {
			t.Fatalf("bootstrap recovery or package introspection delegated to an unavailable target: %v %v", args, err)
		}
	}
	for _, args := range [][]string{{"open"}, {"update", "status"}, {"update", "apply"}, {"update", "--remote=false"}, {"update", "--remote", "--remote=false"}, {"agents", "list"}, {"accounts", "list"}, {"version"}} {
		_, _, delegate, err := packageCommandTarget(args)
		if err != nil || !delegate {
			t.Fatalf("ordinary command remained on the bootstrap version: %v %v", args, err)
		}
	}
	for _, args := range [][]string{{"open", "--data-dir", "/original"}, {"update", "apply", "--data-dir=/original"}} {
		data, explicit, delegate, err := packageCommandTarget(args)
		if err != nil || !delegate || !explicit || data != "/original" {
			t.Fatal("instance selection changed", args, data, err)
		}
	}
	for _, args := range [][]string{{"open", "--data-dir"}, {"open", "--data-dir="}, {"open", "--data-dir=/one", "--data-dir=/two"}} {
		if _, _, _, err := packageCommandTarget(args); err == nil {
			t.Fatal("ambiguous instance selection accepted", args)
		}
	}
}

func TestPackageInstanceExecutableSelectsItsOwnData(t *testing.T) {
	const owner = 1000
	id := strings.Repeat("a", 32)
	root := "/var/lib/fased-app-package/1000/local/" + id
	image := filepath.Join(root, "fased-app")
	verify := func(got string) error {
		if got != image {
			return errors.New("unexpected executable")
		}
		return nil
	}
	load := func(uid int, path string) (custodyinstall.Manifest, error) {
		return custodyinstall.Manifest{Layout: custodyinstall.PackageLayout, Owner: uid, Root: path, Location: "local", ID: filepath.Base(path)}, nil
	}
	readDir := func(string) ([]os.DirEntry, error) {
		t.Fatal("an instance executable must not select a different installation")
		return nil, nil
	}
	data, err := packageLocalDataForExecutable("/old-workspace", image, owner, owner, verify, readDir, load)
	if err != nil || data != filepath.Join(root, "app-state") {
		t.Fatal("the instance executable did not select its own workspace", data, err)
	}
	for _, bad := range []struct {
		name, image string
		uid, euid   int
		load        func(int, string) (custodyinstall.Manifest, error)
	}{
		{"other owner", image, owner + 1, owner + 1, load},
		{"elevated", image, owner, 0, load},
		{"different executable", filepath.Join(root, "other"), owner, owner, load},
		{"foreign manifest", image, owner, owner, func(uid int, path string) (custodyinstall.Manifest, error) {
			m, err := load(uid, path)
			m.ID = strings.Repeat("b", 32)
			return m, err
		}},
	} {
		t.Run(bad.name, func(t *testing.T) {
			if _, err := packageLocalDataForExecutable("/old-workspace", bad.image, bad.uid, bad.euid, verify, readDir, bad.load); err == nil {
				t.Fatal("an invalid package executable selected another workspace")
			}
		})
	}
}

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
	data, err := selectPackageLocalData(owner, entries, load, nil)
	if err != nil || data != "/var/lib/fased-app-package/1000/local/"+first+"/app-state" {
		t.Fatal("single verified Local instance was not selected", data, err)
	}
	if _, err = selectPackageLocalData(owner, nil, load, nil); err == nil {
		t.Fatal("missing instance fell back to another installation")
	}
	foreign := func(uid int, root string) (custodyinstall.Manifest, error) {
		m, err := load(uid, root)
		m.Owner++
		return m, err
	}
	if _, err = selectPackageLocalData(owner, entries, foreign, nil); err == nil {
		t.Fatal("foreign owner was selected")
	}
	if err = os.Mkdir(filepath.Join(dir, second), 0700); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(dir)
	if _, err = selectPackageLocalData(owner, entries, load, nil); err == nil {
		t.Fatal("multiple instances were silently selected")
	}
	if err = os.Remove(filepath.Join(dir, second)); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(dir, first), filepath.Join(dir, second)); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(dir)
	if _, err = selectPackageLocalData(owner, entries, load, nil); err == nil {
		t.Fatal("linked instance was selected")
	}
}

func TestPackageDefaultPreservesAnotherPendingInstallation(t *testing.T) {
	const owner = 1000
	active, unfinished := strings.Repeat("a", 32), strings.Repeat("b", 32)
	dir := t.TempDir()
	for _, name := range []string{active, "." + unfinished + ".pending"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	load := func(uid int, root string) (custodyinstall.Manifest, error) {
		return custodyinstall.Manifest{Layout: custodyinstall.PackageLayout, Owner: uid, Root: root, Location: "local", ID: filepath.Base(root)}, nil
	}
	pending := func(uid int, location, name string) (string, error) {
		if uid == owner && location == "local" && name == "."+unfinished+".pending" {
			return unfinished, nil
		}
		return "", errors.New("unclassified stage")
	}
	got, err := selectPackageLocalData(owner, entries, load, pending)
	if err != nil || got != "/var/lib/fased-app-package/1000/local/"+active+"/app-state" {
		t.Fatal("an unrelated preserved stage blocked the existing installation", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "."+unfinished+".pending")); err != nil {
		t.Fatal("the pending installation was changed", err)
	}
	if _, err := selectPackageLocalData(owner, entries, load, func(int, string, string) (string, error) { return "", errors.New("foreign stage") }); err == nil {
		t.Fatal("an unverified pending stage was ignored")
	}
	if err := os.Mkdir(filepath.Join(dir, "."+active+".pending"), 0700); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(dir)
	if _, err := selectPackageLocalData(owner, entries, load, func(_ int, _ string, name string) (string, error) {
		return strings.TrimSuffix(strings.TrimPrefix(name, "."), ".pending"), nil
	}); err == nil {
		t.Fatal("an active and pending copy of the same instance were accepted")
	}
}
