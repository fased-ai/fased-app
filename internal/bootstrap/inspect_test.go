package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectHasNoArchiveOrExecutableAuthority(t *testing.T) {
	f := newFirstFixture(t)
	o := f.options()
	descriptor, hash, e := Inspect(t.Context(), o)
	if e != nil || hash == "" || descriptor.Sequence == 0 {
		t.Fatal(descriptor, hash, e)
	}
	if _, e = os.Lstat(o.Directory); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("metadata probe wrote output", e)
	}
	for _, request := range f.requests {
		if strings.Contains(request, ".tar.gz") {
			t.Fatal("already-current metadata check downloaded archive", request)
		}
	}
	p, e := Prepare(t.Context(), o)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.ApplicationRelease(); e != nil {
		t.Fatal(e)
	}
	p.RetainAcquisitionEvidence()
	if e = p.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Lstat(filepath.Join(o.Directory, "download.tar.gz")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("archive not pruned", e)
	}
	if _, e = os.Lstat(filepath.Join(o.Directory, "bundle")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("bundle not pruned", e)
	}
	b, e := os.ReadFile(filepath.Join(o.Directory, "evidence-seed.json"))
	if e != nil || string(b) != string(o.Seed) {
		t.Fatal("original trust not retained", e)
	}
}
