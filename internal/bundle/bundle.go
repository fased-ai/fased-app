// Package bundle validates an already acquired platform bundle. Digests detect
// corrupt/mixed files, not publisher authenticity; public bootstrap trust is a
// separate release gate. This module never downloads or executes a package.
package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/fased-ai/fased-app/internal/update"
)

const maxFileBytes = 128 * 1024 * 1024
const maxManifestBytes = 32 * 1024

type File struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type Manifest struct {
	Schema              int             `json:"schema"`
	Platform            string          `json:"platform"`
	Arch                string          `json:"arch"`
	Version             string          `json:"version"`
	Source              string          `json:"source"`
	Desktop             bool            `json:"desktop"`
	UnsignedEngineering bool            `json:"unsignedEngineering"`
	Files               map[string]File `json:"files"`
}

func Names() (string, string) {
	if runtime.GOOS == "windows" {
		return "fased-app.exe", "fased-desktop.exe"
	}
	return "fased-app", "fased-desktop"
}

// PlatformFiles keeps OS policy data separately licensed from the executable.
func PlatformFiles() map[string]string {
	if runtime.GOOS != "linux" {
		return nil
	}
	return map[string]string{"bwrap-userns-restrict": "packaging/linux/apparmor/bwrap-userns-restrict", "APPARMOR-COPYING": "packaging/linux/apparmor/COPYING", "APPARMOR-COPYRIGHT": "packaging/linux/apparmor/ubuntu-copyright", "APPARMOR-SOURCE.md": "packaging/linux/apparmor/README.md"}
}
func Inspect(path string) (File, error) {
	f, size, e := openRegular(path, maxFileBytes)
	if e != nil {
		return File{}, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, maxFileBytes+1))
	if n != size || n > maxFileBytes {
		return File{}, errors.New("bundle file changed size or exceeds 128 MiB")
	}
	return File{hex.EncodeToString(h.Sum(nil)), n}, e
}

func openRegular(path string, limit int64) (*os.File, int64, error) {
	i, err := os.Lstat(path)
	if err != nil {
		return nil, 0, err
	}
	if !i.Mode().IsRegular() || i.Size() > limit {
		return nil, 0, errors.New("bundle input must be a bounded regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(i, opened) || opened.Size() != i.Size() {
		f.Close()
		return nil, 0, errors.New("bundle input changed while opening")
	}
	return f, i.Size(), nil
}

func Verify(dir string) (Manifest, error) {
	return VerifyPinned(dir, "")
}

// VerifyPinned binds the exact manifest bytes when an independently obtained
// digest is supplied. It does not establish a publisher identity or release
// channel. Schema 2 is a distinct release-format integrity contract, not proof
// of publisher trust; that authorization belongs to the independent bootstrap.
func VerifyPinned(dir, manifestSHA256 string) (Manifest, error) {
	var m Manifest
	if manifestSHA256 != "" {
		b, err := hex.DecodeString(manifestSHA256)
		if err != nil || len(b) != sha256.Size || hex.EncodeToString(b) != manifestSHA256 {
			return m, errors.New("bundle digest must be 64 lowercase hexadecimal characters")
		}
	}
	f, size, e := openRegular(filepath.Join(dir, "bundle.json"), maxManifestBytes)
	if e != nil {
		return m, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if e != nil || int64(len(b)) != size || len(b) > maxManifestBytes {
		return m, errors.New("invalid or changed bundle manifest size")
	}
	if manifestSHA256 != "" {
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != manifestSHA256 {
			return m, errors.New("bundle manifest does not match the independently obtained digest")
		}
	}
	if e = update.StrictJSON(b, &m); e != nil {
		return m, e
	}
	engineering := m.Schema == 1 && m.UnsignedEngineering
	release := m.Schema == 2 && !m.UnsignedEngineering && !m.Desktop && m.Platform == "linux" && m.Arch == "amd64"
	if (!engineering && !release) || m.Platform != runtime.GOOS || m.Arch != runtime.GOARCH || m.Version == "" {
		return m, errors.New("unsupported bundle identity or release mode")
	}
	if release {
		if _, present := m.Files["INSTALL.txt"]; !present {
			return m, errors.New("release bundle requires the guide in its verified inventory")
		}
	}
	engine, desktop := Names()
	allowed := map[string]bool{engine: true, "LICENSE": true, "NOTICE": true}
	for name := range PlatformFiles() {
		allowed[name] = true
	}
	if m.Desktop {
		allowed[desktop] = true
	}
	if _, present := m.Files["INSTALL.txt"]; present {
		allowed["INSTALL.txt"] = true
	}
	if len(allowed) != len(m.Files) {
		return m, errors.New("incomplete or unexpected bundle inventory")
	}
	for name, want := range m.Files {
		if !allowed[name] {
			return m, errors.New("unknown bundle file")
		}
		got, e := Inspect(filepath.Join(dir, name))
		if e != nil || got != want {
			return m, fmt.Errorf("bundle file %s is corrupt or mismatched", name)
		}
	}
	entries, e := os.ReadDir(dir)
	if e != nil {
		return m, e
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "bundle.json" || allowed[name] {
			continue
		}
		// Older engineering archives included this non-executable guide outside
		// the inventory. Retain read compatibility without following symlinks.
		if name == "INSTALL.txt" && engineering {
			guide, err := Inspect(filepath.Join(dir, name))
			if err == nil && guide.Bytes <= maxManifestBytes {
				continue
			}
		}
		return m, errors.New("unclassified entry in bundle directory")
	}
	return m, nil
}
