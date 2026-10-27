package bundle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBundleRejectsMixedOrUnexpectedFiles(t *testing.T) {
	root, _ := filepath.Abs("../../.evidence/bundle-fixtures")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	p, e := os.MkdirTemp(root, "f-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(p) })
	en, _ := Names()
	m := Manifest{Schema: 1, Platform: runtime.GOOS, Arch: runtime.GOARCH, Version: "fixture", UnsignedEngineering: true, Files: map[string]File{}}
	names := []string{en, "LICENSE", "NOTICE"}
	for n := range PlatformFiles() {
		names = append(names, n)
	}
	for _, n := range names {
		if e = os.WriteFile(filepath.Join(p, n), []byte("fixture "+n), 0700); e != nil {
			t.Fatal(e)
		}
		m.Files[n], e = Inspect(filepath.Join(p, n))
		if e != nil {
			t.Fatal(e)
		}
	}
	save := func() {
		b, _ := json.Marshal(m)
		if e = os.WriteFile(filepath.Join(p, "bundle.json"), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	save()
	if _, e = Verify(p); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(p, en), []byte("mixed version"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = Verify(p); e == nil {
		t.Fatal("mixed binary accepted")
	}
	m.Files["../escape"] = File{}
	save()
	if _, e = Verify(p); e == nil {
		t.Fatal("untrusted inventory name accepted")
	}
}
