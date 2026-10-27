//go:build linux

package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

type archiveEntry struct {
	header tar.Header
	data   []byte
}

func archiveFixture(t *testing.T, desktop bool) ([]archiveEntry, Manifest, string) {
	t.Helper()
	dir, m := bundleFixture(t)
	_, desktopName := Names()
	if desktop {
		m.Desktop = true
		if err := os.WriteFile(filepath.Join(dir, desktopName), []byte("desktop fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		var err error
		m.Files[desktopName], err = Inspect(filepath.Join(dir, desktopName))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "INSTALL.txt"), []byte("inert engineering installation guide"), 0600); err != nil {
		t.Fatal(err)
	}
	var err error
	m.Files["INSTALL.txt"], err = Inspect(filepath.Join(dir, "INSTALL.txt"))
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes := writeBundleManifest(t, dir, m)
	pin := sha256.Sum256(manifestBytes)
	names := []string{"bundle.json"}
	for name := range m.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]archiveEntry, 0, len(names))
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, archiveEntry{tar.Header{Name: "fased-app-linux-" + runtime.GOARCH + "/" + name, Size: int64(len(b)), Mode: 0700}, b})
	}
	return entries, m, hex.EncodeToString(pin[:])
}

func archiveBytes(t *testing.T, entries []archiveEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		h := entry.header
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func archiveInput(t *testing.T, b []byte) (string, File) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	id, err := Inspect(p)
	if err != nil {
		t.Fatal(err)
	}
	return p, id
}

func archiveDestination(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "extracted")
}

func TestArchiveVerifiedProducerCompatible(t *testing.T) {
	for _, desktop := range []bool{false, true} {
		t.Run(map[bool]string{false: "headless", true: "desktop"}[desktop], func(t *testing.T) {
			entries, want, pin := archiveFixture(t, desktop)
			// The actual producer omits directory headers; accept its exact layout.
			b := archiveBytes(t, entries)
			input, id := archiveInput(t, b)
			dir := archiveDestination(t)
			got, err := ExtractVerifiedArchive(input, dir, id, pin)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("manifest changed")
			}
			root, err := os.Stat(dir)
			if err != nil || root.Mode().Perm() != 0700 {
				t.Fatal("output must remain private", err)
			}
			engine, desktopName := Names()
			items, err := os.ReadDir(dir)
			if err != nil || len(items) != len(want.Files)+1 {
				t.Fatal("unexpected extracted inventory", err)
			}
			for _, item := range items {
				info, err := item.Info()
				mode := os.FileMode(0600)
				if item.Name() == engine || desktop && item.Name() == desktopName {
					mode = 0700
				}
				if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != mode {
					t.Fatalf("unexpected mode for %s: %v %v", item.Name(), info, err)
				}
			}
			if after, err := Inspect(input); err != nil || after != id {
				t.Fatal("input archive was changed", err)
			}
			if _, err := ExtractVerifiedArchive(input, dir, id, pin); err == nil {
				t.Fatal("existing output adopted")
			}
			if after, err := VerifyPinned(dir, pin); err != nil || !reflect.DeepEqual(after, want) {
				t.Fatal("existing verified output changed on retry", err)
			}
		})
	}
}

func TestArchiveRejectsInvalidIdentities(t *testing.T) {
	entries, _, pin := archiveFixture(t, false)
	input, id := archiveInput(t, archiveBytes(t, entries))
	for _, which := range []string{"missing-pin", "wrong-pin", "uppercase-pin", "bad-pin", "wrong-hash", "short-hash", "uppercase-hash", "wrong-size", "zero-size", "oversize"} {
		t.Run(which, func(t *testing.T) {
			want, inner := id, pin
			switch which {
			case "missing-pin":
				inner = ""
			case "wrong-pin":
				inner = strings.Repeat("0", 64)
			case "uppercase-pin":
				inner = strings.ToUpper(pin)
			case "bad-pin":
				inner = "bad"
			case "wrong-hash":
				want.SHA256 = strings.Repeat("0", 64)
			case "short-hash":
				want.SHA256 = "bad"
			case "uppercase-hash":
				want.SHA256 = strings.ToUpper(id.SHA256)
			case "wrong-size":
				want.Bytes++
			case "zero-size":
				want.Bytes = 0
			case "oversize":
				want.Bytes = maxFileBytes + 1
			}
			dir := archiveDestination(t)
			if _, err := ExtractVerifiedArchive(input, dir, want, inner); err == nil {
				t.Fatal("unbound archive accepted")
			}
			if _, err := os.Lstat(dir); !os.IsNotExist(err) {
				t.Fatal("failed archive left authoritative output", err)
			}
		})
	}
}

func TestArchiveRejectsForeignOutput(t *testing.T) {
	entries, _, pin := archiveFixture(t, false)
	input, id := archiveInput(t, archiveBytes(t, entries))
	for _, which := range []string{"file", "directory", "symlink", "parent-symlink", "public-parent"} {
		t.Run(which, func(t *testing.T) {
			dir := archiveDestination(t)
			foreign := t.TempDir()
			marker := filepath.Join(foreign, "preserve")
			if err := os.WriteFile(marker, []byte("owner data"), 0600); err != nil {
				t.Fatal(err)
			}
			switch which {
			case "file":
				if err := os.WriteFile(dir, []byte("foreign output"), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(foreign, dir); err != nil {
					t.Fatal(err)
				}
			case "parent-symlink":
				link := filepath.Join(filepath.Dir(dir), "linked")
				if err := os.Symlink(foreign, link); err != nil {
					t.Fatal(err)
				}
				dir = filepath.Join(link, "extracted")
			case "public-parent":
				if err := os.Chmod(filepath.Dir(dir), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ExtractVerifiedArchive(input, dir, id, pin); err == nil {
				t.Fatal("foreign/private output boundary accepted")
			}
			if b, err := os.ReadFile(marker); err != nil || string(b) != "owner data" {
				t.Fatal("foreign data changed", err)
			}
			if which == "file" {
				if b, err := os.ReadFile(dir); err != nil || string(b) != "foreign output" {
					t.Fatal("foreign output changed", err)
				}
			}
			if which == "directory" {
				if children, err := os.ReadDir(dir); err != nil || len(children) != 0 {
					t.Fatal("foreign directory changed", err)
				}
			}
		})
	}
}

func TestArchiveRejectsMalformedMembers(t *testing.T) {
	base, _, pin := archiveFixture(t, false)
	prefix := "fased-app-linux-" + runtime.GOARCH
	cases := map[string]func([]archiveEntry) []archiveEntry{
		"traversal":  func(e []archiveEntry) []archiveEntry { e[0].header.Name = prefix + "/../outside"; return e },
		"absolute":   func(e []archiveEntry) []archiveEntry { e[0].header.Name = "/" + e[0].header.Name; return e },
		"backslash":  func(e []archiveEntry) []archiveEntry { e[0].header.Name = prefix + "/..\\outside"; return e },
		"nested":     func(e []archiveEntry) []archiveEntry { e[0].header.Name = prefix + "/nested/file"; return e },
		"wrong-root": func(e []archiveEntry) []archiveEntry { e[0].header.Name = "fased-app-other/NOTICE"; return e },
		"extra-root": func(e []archiveEntry) []archiveEntry {
			return append(e, archiveEntry{header: tar.Header{Name: "other/extra", Size: 1}, data: []byte("x")})
		},
		"duplicate": func(e []archiveEntry) []archiveEntry { return append(e, e[0]) },
		"extra-file": func(e []archiveEntry) []archiveEntry {
			return append(e, archiveEntry{header: tar.Header{Name: prefix + "/extra", Size: 1}, data: []byte("x")})
		},
		"symlink": func(e []archiveEntry) []archiveEntry {
			e[0].header.Typeflag = tar.TypeSymlink
			e[0].header.Linkname = "../../outside"
			e[0].header.Size = 0
			e[0].data = nil
			return e
		},
		"hardlink": func(e []archiveEntry) []archiveEntry {
			e[0].header.Typeflag = tar.TypeLink
			e[0].header.Linkname = "outside"
			e[0].header.Size = 0
			e[0].data = nil
			return e
		},
		"device": func(e []archiveEntry) []archiveEntry {
			e[0].header.Typeflag = tar.TypeChar
			e[0].header.Size = 0
			e[0].data = nil
			return e
		},
		"fifo": func(e []archiveEntry) []archiveEntry {
			e[0].header.Typeflag = tar.TypeFifo
			e[0].header.Size = 0
			e[0].data = nil
			return e
		},
		"nested-directory": func(e []archiveEntry) []archiveEntry {
			return append([]archiveEntry{{header: tar.Header{Name: prefix + "/nested/", Typeflag: tar.TypeDir}}}, e...)
		},
		"duplicate-root": func(e []archiveEntry) []archiveEntry {
			root := archiveEntry{header: tar.Header{Name: prefix + "/", Typeflag: tar.TypeDir}}
			return append([]archiveEntry{root, root}, e...)
		},
		"pax": func(e []archiveEntry) []archiveEntry {
			e[0].header.Format = tar.FormatPAX
			e[0].header.PAXRecords = map[string]string{"comment": "hidden extension"}
			return e
		},
		"too-many": func(e []archiveEntry) []archiveEntry {
			for i := 0; i < 17; i++ {
				e = append(e, archiveEntry{header: tar.Header{Name: prefix + "/" + strings.Repeat("x", i+1)}})
			}
			return e
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			e := append([]archiveEntry(nil), base...)
			b := archiveBytes(t, change(e))
			assertArchiveRefused(t, b, pin)
		})
	}
	root := archiveEntry{header: tar.Header{Name: prefix + "/", Typeflag: tar.TypeDir}}
	b := archiveBytes(t, append([]archiveEntry{root}, base...))
	input, id := archiveInput(t, b)
	if _, err := ExtractVerifiedArchive(input, archiveDestination(t), id, pin); err != nil {
		t.Fatal("optional root directory header refused", err)
	}
}

func assertArchiveRefused(t *testing.T, b []byte, pin string) {
	t.Helper()
	input, id := archiveInput(t, b)
	dir := archiveDestination(t)
	if _, err := ExtractVerifiedArchive(input, dir, id, pin); err == nil {
		t.Fatal("unsafe archive accepted")
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatal("failed archive left output", err)
	}
	if got, err := Inspect(input); err != nil || got != id {
		t.Fatal("input archive changed", err)
	}
}

func TestArchiveRejectsTruncationAndTrailingData(t *testing.T) {
	entries, _, pin := archiveFixture(t, false)
	good := archiveBytes(t, entries)
	for _, cut := range []int{1, 8, len(good) / 2, len(good) - 1, len(good) - 8} {
		assertArchiveRefused(t, good[:cut], pin)
	}
	assertArchiveRefused(t, append(append([]byte{}, good...), []byte("trailer")...), pin)
	assertArchiveRefused(t, append(append([]byte{}, good...), good...), pin)
	gz, err := gzip.NewReader(bytes.NewReader(good))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range [][]byte{[]byte("tar trailer"), make([]byte, 512)} {
		var b bytes.Buffer
		w := gzip.NewWriter(&b)
		if _, err := w.Write(append(append([]byte{}, raw...), suffix...)); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		assertArchiveRefused(t, b.Bytes(), pin)
	}
}

func TestArchiveRejectsDeclaredSizeBeforeReadingPayload(t *testing.T) {
	_, _, pin := archiveFixture(t, false)
	prefix := "fased-app-linux-" + runtime.GOARCH + "/"
	for _, test := range []struct {
		name, member string
		size         int64
	}{
		{"file", "fased-app", maxFileBytes + 1},
		{"manifest", "bundle.json", maxManifestBytes + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Supply only the header: refusal must be caused by the declared
			// bound, before allocating/copying a bomb's nonexistent payload.
			var raw bytes.Buffer
			tw := tar.NewWriter(&raw)
			if err := tw.WriteHeader(&tar.Header{Name: prefix + test.member, Size: test.size, Mode: 0700}); err != nil {
				t.Fatal(err)
			}
			var compressed bytes.Buffer
			gz := gzip.NewWriter(&compressed)
			if _, err := gz.Write(raw.Bytes()); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			input, id := archiveInput(t, compressed.Bytes())
			dir := archiveDestination(t)
			_, err := ExtractVerifiedArchive(input, dir, id, pin)
			if err == nil || !strings.Contains(err.Error(), "bound") {
				t.Fatal("oversize header was not rejected before payload consumption", err)
			}
			if _, err := os.Lstat(dir); !os.IsNotExist(err) {
				t.Fatal("oversize archive left output", err)
			}
		})
	}
}

func TestArchiveCorruptionCleansAndCanRetry(t *testing.T) {
	entries, want, pin := archiveFixture(t, false)
	good := archiveBytes(t, entries)
	for i := range entries {
		if strings.HasSuffix(entries[i].header.Name, "/fased-app") {
			entries[i].data = append([]byte(nil), entries[i].data...)
			entries[i].data[0] ^= 1
		}
	}
	bad, badID := archiveInput(t, archiveBytes(t, entries))
	dir := archiveDestination(t)
	if _, err := ExtractVerifiedArchive(bad, dir, badID, pin); err == nil {
		t.Fatal("corrupt inner payload accepted")
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatal("bad payload left output", err)
	}
	input, id := archiveInput(t, good)
	if got, err := ExtractVerifiedArchive(input, dir, id, pin); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("verified retry after cleanup refused", err)
	}
}

func TestArchiveRejectsUninventoriedLegacyGuide(t *testing.T) {
	dir, m := bundleFixture(t)
	manifestBytes := writeBundleManifest(t, dir, m)
	pin := sha256.Sum256(manifestBytes)
	names := []string{"bundle.json"}
	for name := range m.Files {
		names = append(names, name)
	}
	var entries []archiveEntry
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, archiveEntry{tar.Header{Name: "fased-app-linux-" + runtime.GOARCH + "/" + name, Size: int64(len(b)), Mode: 0700}, b})
	}
	entries = append(entries, archiveEntry{tar.Header{Name: "fased-app-linux-" + runtime.GOARCH + "/INSTALL.txt", Size: 5}, []byte("guide")})
	assertArchiveRefused(t, archiveBytes(t, entries), hex.EncodeToString(pin[:]))
}

type archiveZeros struct{}

func (archiveZeros) Read(b []byte) (int, error) {
	clear(b)
	return len(b), nil
}

func TestArchiveRejectsExpandedTotal(t *testing.T) {
	// A small compressed fixture would expand to two allowed 128 MiB files,
	// then one byte beyond the aggregate bound. No large in-memory fixture is
	// retained, and the failed extraction must remove all temporary payloads.
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	prefix := "fased-app-linux-" + runtime.GOARCH + "/"
	for _, name := range []string{"first", "second"} {
		if err := tw.WriteHeader(&tar.Header{Name: prefix + name, Size: maxFileBytes, Mode: 0700}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.CopyN(tw, archiveZeros{}, maxFileBytes); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.WriteHeader(&tar.Header{Name: prefix + "overflow", Size: 1, Mode: 0700}); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	input, id := archiveInput(t, b.Bytes())
	dir := archiveDestination(t)
	_, err := ExtractVerifiedArchive(input, dir, id, strings.Repeat("a", 64))
	if err == nil || !strings.Contains(err.Error(), "bound") {
		t.Fatal("aggregate expansion bound did not stop payload consumption", err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatal("expanded failed payloads were retained", err)
	}
}
