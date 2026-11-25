package main

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
	"testing"
)

type fakeDirEntry struct {
	name string
	dir  bool
}

func (e fakeDirEntry) Name() string               { return e.name }
func (e fakeDirEntry) IsDir() bool                { return e.dir }
func (e fakeDirEntry) Type() fs.FileMode          { return 0 }
func (e fakeDirEntry) Info() (fs.FileInfo, error) { return nil, errors.New("unused") }

func TestResolveHostPackageInstanceAndFlags(t *testing.T) {
	t.Parallel()
	idA := strings.Repeat("a", 32)
	idB := strings.Repeat("b", 32)

	if _, err := resolveHostPackageInstance(0, "", "", os.ReadDir); err == nil {
		t.Fatal("uid=0 accepted")
	}
	if _, err := resolveHostPackageInstance(1000, "server", "", os.ReadDir); err == nil {
		t.Fatal("invalid location accepted")
	}
	if _, err := resolveHostPackageInstance(1000, "remote", "bad-id", os.ReadDir); err == nil {
		t.Fatal("invalid instance id accepted")
	}

	got, err := resolveHostPackageInstance(1000, "remote", idA, func(string) ([]os.DirEntry, error) {
		t.Fatal("explicit location+instance should not scan directories")
		return nil, nil
	})
	if err != nil || got != (packageInstanceRef{Location: "remote", ID: idA}) {
		t.Fatalf("explicit resolution failed: %+v %v", got, err)
	}

	readEmpty := func(string) ([]os.DirEntry, error) { return nil, os.ErrNotExist }
	if _, err := resolveHostPackageInstance(1000, "", "", readEmpty); err == nil {
		t.Fatal("0 instances accepted")
	}

	readOneRemote := func(p string) ([]os.DirEntry, error) {
		if strings.HasSuffix(p, "/remote") {
			return []os.DirEntry{fakeDirEntry{name: idA, dir: true}, fakeDirEntry{name: "not-hex", dir: true}}, nil
		}
		return nil, os.ErrNotExist
	}
	got, err = resolveHostPackageInstance(1000, "", "", readOneRemote)
	if err != nil || got != (packageInstanceRef{Location: "remote", ID: idA}) {
		t.Fatalf("auto-discovery of unique remote instance failed: %+v %v", got, err)
	}

	readTwo := func(p string) ([]os.DirEntry, error) {
		if strings.HasSuffix(p, "/remote") {
			return []os.DirEntry{fakeDirEntry{name: idA, dir: true}}, nil
		}
		if strings.HasSuffix(p, "/local") {
			return []os.DirEntry{fakeDirEntry{name: idB, dir: true}}, nil
		}
		return nil, os.ErrNotExist
	}
	if _, err := resolveHostPackageInstance(1000, "", "", readTwo); err == nil {
		t.Fatal("ambiguous 2-instance scan accepted")
	}
	got, err = resolveHostPackageInstance(1000, "local", "", readTwo)
	if err != nil || got != (packageInstanceRef{Location: "local", ID: idB}) {
		t.Fatalf("scoped location resolution failed: %+v %v", got, err)
	}

	for _, badArgs := range [][]string{
		nil,
		{"unknown"},
		{"migrate-units", "extra"},
		{"migrate-units", "--location", "server"},
		{"migrate-units", "--instance", "bad"},
	} {
		if err := hostSubcommand(badArgs, io.Discard, io.Discard); err == nil {
			t.Fatalf("unsafe host args accepted: %v", badArgs)
		}
	}
}
