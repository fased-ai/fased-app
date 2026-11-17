//go:build linux

package ownerclient

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestLocalPackageOwnerUsesCommittedWorkspace(t *testing.T) {
	const owner = 1400
	workspace := intent().InstanceID
	wantID, otherID := strings.Repeat("a", 32), strings.Repeat("b", 32)
	dir := t.TempDir()
	for _, name := range []string{wantID, otherID} {
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	wantRoot, _ := update.PackageInstanceRoot(owner, "local", wantID)
	load := func(_ int, root string) (custodyinstall.Manifest, error) {
		return custodyinstall.Manifest{Owner: owner, Root: root, ID: filepath.Base(root), Location: "local", Layout: custodyinstall.PackageLayout}, nil
	}
	commit := func(m custodyinstall.Manifest) (custodyinstall.EnrollmentCommit, error) {
		if m.ID == wantID {
			return custodyinstall.EnrollmentCommit{InstanceID: workspace}, nil
		}
		return custodyinstall.EnrollmentCommit{InstanceID: "other"}, nil
	}
	verify := func(m custodyinstall.Manifest) error {
		if m.ID != wantID {
			return errors.New("foreign wallet")
		}
		return nil
	}
	pending := func(int, string, string) (custodyinstall.Manifest, *update.WalletInitialization, error) {
		return custodyinstall.Manifest{}, nil, nil
	}
	got, err := selectLocalPackageOwnerTarget(owner, workspace, entries, load, commit, verify, pending, nil)
	if err != nil || got != filepath.Join(wantRoot, "fased-app") {
		t.Fatal("the committed package was not selected", got, err)
	}
	if _, err = selectLocalPackageOwnerTarget(owner, workspace, entries, load, commit, func(custodyinstall.Manifest) error { return errors.New("pending update") }, pending, nil); err == nil {
		t.Fatal("a pending or unverified package was selected")
	}
	if _, err = selectLocalPackageOwnerTarget(owner, workspace, entries, load, func(custodyinstall.Manifest) (custodyinstall.EnrollmentCommit, error) {
		return custodyinstall.EnrollmentCommit{InstanceID: workspace}, nil
	}, func(custodyinstall.Manifest) error { return nil }, pending, nil); err == nil {
		t.Fatal("ambiguous packages were selected")
	}
	resume := func(_ int, _ string, id string) (custodyinstall.Manifest, *update.WalletInitialization, error) {
		if id == wantID {
			return custodyinstall.Manifest{Root: wantRoot}, &update.WalletInitialization{InstanceID: workspace}, nil
		}
		return custodyinstall.Manifest{}, nil, nil
	}
	got, err = selectLocalPackageOwnerTarget(owner, workspace, entries, load, commit, verify, resume, nil)
	if err != nil || got != filepath.Join(wantRoot, "fased-app") {
		t.Fatal("the original interrupted wallet could not be selected", got, err)
	}
}

func TestLocalPackageOwnerPreservesAnotherPendingInstallation(t *testing.T) {
	const owner = 1400
	workspace := intent().InstanceID
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
	load := func(_ int, root string) (custodyinstall.Manifest, error) {
		return custodyinstall.Manifest{Owner: owner, Root: root, ID: filepath.Base(root), Location: "local", Layout: custodyinstall.PackageLayout}, nil
	}
	commit := func(custodyinstall.Manifest) (custodyinstall.EnrollmentCommit, error) {
		return custodyinstall.EnrollmentCommit{InstanceID: workspace}, nil
	}
	verify := func(custodyinstall.Manifest) error { return nil }
	pending := func(int, string, string) (custodyinstall.Manifest, *update.WalletInitialization, error) {
		return custodyinstall.Manifest{}, nil, nil
	}
	stage := func(uid int, location, name string) (string, error) {
		if uid == owner && location == "local" && name == "."+unfinished+".pending" {
			return unfinished, nil
		}
		return "", errors.New("unclassified stage")
	}
	got, err := selectLocalPackageOwnerTarget(owner, workspace, entries, load, commit, verify, pending, stage)
	if err != nil || got != "/var/lib/fased-app-package/1400/local/"+active+"/fased-app" {
		t.Fatal("an unrelated preserved stage blocked the owner", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "."+unfinished+".pending")); err != nil {
		t.Fatal("the pending installation was changed", err)
	}
	if _, err := selectLocalPackageOwnerTarget(owner, workspace, entries, load, commit, verify, pending, func(int, string, string) (string, error) { return "", errors.New("foreign stage") }); err == nil {
		t.Fatal("an unverified pending stage was ignored")
	}
	if err := os.Mkdir(filepath.Join(dir, "."+active+".pending"), 0700); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(dir)
	if _, err := selectLocalPackageOwnerTarget(owner, workspace, entries, load, commit, verify, pending, func(_ int, _ string, name string) (string, error) {
		return strings.TrimSuffix(strings.TrimPrefix(name, "."), ".pending"), nil
	}); err == nil {
		t.Fatal("an active and pending copy of the same instance were accepted")
	}
	if err := os.Remove(filepath.Join(dir, "."+active+".pending")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, active)); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(dir)
	if _, err := selectLocalPackageOwnerTarget(owner, workspace, entries, load, commit, verify, pending, stage); err == nil {
		t.Fatal("a pending-only installation fell back to another owner")
	}
}
