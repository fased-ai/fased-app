//go:build linux

package vpsinstall

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRootFileDigestStreamsExecutableWithoutSizeAllocation(t *testing.T) {
	for _, size := range []int64{1 << 20, 45 << 20} {
		path := filepath.Join(t.TempDir(), "core")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.Truncate(size); err != nil {
			t.Fatal(err)
		}
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
		o := hostOps{
			rootParents: func(string) error { return nil },
			read:        func(string) (hostFile, error) { return hostFile{}, errors.New("whole-image read forbidden") },
			content: func(path string, consume func(hostFile, int64, io.Reader) error) error {
				return fileContent(path, func(metadata hostFile, length int64, reader io.Reader) error {
					// Ordinary-user fixture only; production enforces the real root identity.
					metadata.UID, metadata.GID = 0, 0
					return consume(metadata, length, reader)
				})
			},
		}
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		metadata, got, sum, err := rootFileDigest(o, path)
		runtime.ReadMemStats(&after)
		if err != nil || got != size || len(sum) != 64 || metadata.Data != nil {
			t.Fatal("streamed identity failed", got, sum, err)
		}
		allocated := after.TotalAlloc - before.TotalAlloc
		t.Logf("size=%d allocated=%d", size, allocated)
		if allocated > 512<<10 {
			t.Fatal("verification allocation scales with executable size", allocated)
		}
	}
}

func TestFileContentRejectsSymlinkAndConcurrentChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core")
	if err := os.WriteFile(path, []byte("original"), 0755); err != nil {
		t.Fatal(err)
	}
	link := path + "-link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := fileContent(link, func(hostFile, int64, io.Reader) error { return nil }); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := fileContent(path, func(_ hostFile, _ int64, reader io.Reader) error {
		if _, err := io.Copy(io.Discard, reader); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("different length"), 0755)
	}); err == nil {
		t.Fatal("changed held input accepted")
	}
}

func TestApplicationOwnerRetainedStreamsAndPreservesPendingRules(t *testing.T) {
	for _, kind := range []string{"none", "empty", "prefix", "complete", "corrupt", "overlong", "mode", "owner", "missing"} {
		t.Run(kind, func(t *testing.T) {
			f, j, image := retainedOwnerFixture(t)
			dir := strings.TrimSuffix(image, "/previous-core")
			pending := dir + "/.replacement-fased-app-" + strings.Repeat("a", 32) + ".pending"
			value := f.h.files[image]
			switch kind {
			case "empty":
				value.Data = nil
			case "prefix":
				value.Data = value.Data[:len(value.Data)/2]
			case "corrupt":
				value.Data = []byte("foreign prefix")
			case "overlong":
				value.Data = append(bytes.Clone(value.Data), 'x')
			case "mode":
				value.Mode = 0775
			case "owner":
				value.UID = j.Previous.OwnerUID
			case "missing":
				delete(f.h.files, dir+"/next-install.json")
			}
			if kind != "none" && kind != "missing" {
				f.h.files[pending] = value
			}
			o := f.o
			originalRead := o.read
			o.read = func(path string) (hostFile, error) {
				if path == image || path == dir+"/next-core" || path == pending {
					return hostFile{}, errors.New("whole-image read forbidden")
				}
				return originalRead(path)
			}
			o.content = func(path string, consume func(hostFile, int64, io.Reader) error) error {
				value, ok := f.h.files[path]
				if !ok {
					return os.ErrNotExist
				}
				data := value.Data
				value.Data = nil
				return consume(value, int64(len(data)), bytes.NewReader(data))
			}
			err := verifyApplicationOwnerRetained(o, j, dir)
			wantPass := kind == "none" || kind == "empty" || kind == "prefix" || kind == "complete"
			if (err == nil) != wantPass {
				t.Fatal("retained admission changed", err)
			}
		})
	}
}
