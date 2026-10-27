package bundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBundlePinnedManifestAndGuideCompatibility(t *testing.T) {
	dir, m := bundleFixture(t)
	b := writeBundleManifest(t, dir, m)
	sum := sha256.Sum256(b)
	pin := hex.EncodeToString(sum[:])
	if _, err := VerifyPinned(dir, pin); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"not a digest", strings.ToUpper(pin), strings.Repeat("0", 64)} {
		if _, err := VerifyPinned(dir, bad); err == nil {
			t.Fatal("wrong or ambiguous digest accepted")
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "INSTALL.txt"), []byte("legacy engineering guide"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPinned(dir, pin); err != nil {
		t.Fatal("legacy non-executable guide refused:", err)
	}
	var err error
	m.Files["INSTALL.txt"], err = Inspect(filepath.Join(dir, "INSTALL.txt"))
	if err != nil {
		t.Fatal(err)
	}
	b = writeBundleManifest(t, dir, m)
	sum = sha256.Sum256(b)
	if _, err = VerifyPinned(dir, pin); err == nil {
		t.Fatal("changed manifest accepted under old pin")
	}
	if _, err = VerifyPinned(dir, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal("inventoried guide refused:", err)
	}
	if err = os.WriteFile(filepath.Join(dir, "INSTALL.txt"), []byte("changed guide"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Verify(dir); err == nil {
		t.Fatal("changed inventoried guide accepted")
	}
}

func bundleFixture(t *testing.T) (string, Manifest) {
	t.Helper()
	dir := t.TempDir()
	engine, _ := Names()
	m := Manifest{Schema: 1, Platform: runtime.GOOS, Arch: runtime.GOARCH, Version: "fixture", UnsignedEngineering: true, Files: map[string]File{}}
	names := []string{engine, "LICENSE", "NOTICE"}
	for name := range PlatformFiles() {
		names = append(names, name)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fixture "+name), 0700); err != nil {
			t.Fatal(err)
		}
		var err error
		m.Files[name], err = Inspect(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
	}
	writeBundleManifest(t, dir, m)
	return dir, m
}

func writeBundleManifest(t *testing.T, dir string, m Manifest) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "bundle.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBundleRejectsAmbiguousManifest(t *testing.T) {
	for name, change := range map[string]func([]byte) []byte{
		"duplicate identity": func(b []byte) []byte { return append([]byte(`{"schema":0,`), b[1:]...) },
		"escaped alias":      func(b []byte) []byte { return append([]byte(`{"schem\u0061":0,`), b[1:]...) },
		"case alias":         func(b []byte) []byte { return bytes.Replace(b, []byte(`"schema"`), []byte(`"Schema"`), 1) },
		"duplicate file hash": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"sha256":`), []byte(`"sha256":"other","sha256":`), 1)
		},
		"invalid encoding": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"source":""`), []byte{'"', 's', 'o', 'u', 'r', 'c', 'e', '"', ':', '"', 0xff, '"'}, 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir, m := bundleFixture(t)
			b := change(writeBundleManifest(t, dir, m))
			if err := os.WriteFile(filepath.Join(dir, "bundle.json"), b, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Verify(dir); err == nil {
				t.Fatal("ambiguous manifest accepted")
			}
		})
	}
}

func TestBundleRejectsUnclassifiedEntries(t *testing.T) {
	for _, name := range []string{"unexpected.sh", "unexpected-directory", "INSTALL.txt"} {
		t.Run(name, func(t *testing.T) {
			dir, _ := bundleFixture(t)
			var err error
			switch name {
			case "unexpected-directory":
				err = os.Mkdir(filepath.Join(dir, name), 0700)
			case "INSTALL.txt":
				if runtime.GOOS == "windows" {
					t.Skip("Windows symlink privilege is not part of bundle verification")
				}
				err = os.Symlink(filepath.Join(dir, "LICENSE"), filepath.Join(dir, name))
			default:
				err = os.WriteFile(filepath.Join(dir, name), []byte("unlisted executable"), 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Verify(dir); err == nil {
				t.Fatal("unclassified bundle entry accepted")
			}
		})
	}
}

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
