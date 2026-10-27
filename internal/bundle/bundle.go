// Package bundle validates an already acquired platform bundle. Digests detect
// corrupt/mixed files, not publisher authenticity; public bootstrap trust is a
// separate release gate. This module never downloads or executes a package.
package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

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
	i, e := os.Lstat(path)
	if e != nil {
		return File{}, e
	}
	if !i.Mode().IsRegular() || i.Size() > 128*1024*1024 {
		return File{}, errors.New("bundle file must be a regular file at most 128 MiB")
	}
	f, e := os.Open(path)
	if e != nil {
		return File{}, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	return File{hex.EncodeToString(h.Sum(nil)), n}, e
}
func Verify(dir string) (Manifest, error) {
	var m Manifest
	p := filepath.Join(dir, "bundle.json")
	i, e := os.Lstat(p)
	if e != nil {
		return m, e
	}
	if !i.Mode().IsRegular() || i.Size() > 32*1024 {
		return m, errors.New("invalid bundle manifest file")
	}
	f, e := os.Open(p)
	if e != nil {
		return m, e
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if e = d.Decode(&m); e != nil {
		return m, e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return m, errors.New("trailing bundle data")
	}
	if m.Schema != 1 || m.Platform != runtime.GOOS || m.Arch != runtime.GOARCH || m.Version == "" || !m.UnsignedEngineering {
		return m, errors.New("unsupported bundle identity or release mode")
	}
	engine, desktop := Names()
	allowed := map[string]bool{engine: true, "LICENSE": true, "NOTICE": true}
	for name := range PlatformFiles() {
		allowed[name] = true
	}
	if m.Desktop {
		allowed[desktop] = true
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
	return m, nil
}
