// Development/CI packaging tool; never installed or run by an end user.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/core"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	engine := flag.String("engine", ".evidence/bin/fased-app", "native core binary")
	desktop := flag.String("desktop", "", "optional matching native desktop binary")
	out := flag.String("out", ".evidence/packages", "generated package output directory")
	source := flag.String("source", "working-tree", "source revision identity")
	flag.Parse()
	if e := os.MkdirAll(*out, 0700); e != nil {
		return e
	}
	en, dn := bundle.Names()
	name := "fased-app-" + runtime.GOOS + "-" + runtime.GOARCH
	stage := filepath.Join(*out, name)
	if _, e := os.Lstat(stage); !os.IsNotExist(e) {
		return fmt.Errorf("package staging already exists; inspect/remove only prior generated output: %s", stage)
	}
	if e := os.Mkdir(stage, 0700); e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	payload := stage
	prefix := name
	if runtime.GOOS == "darwin" && *desktop != "" {
		prefix = "Fased App.app"
		payload = filepath.Join(stage, "Contents", "MacOS")
		if e := os.MkdirAll(payload, 0700); e != nil {
			return e
		}
		plist := `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>org.fased.app</string><key>CFBundleName</key><string>Fased App</string><key>CFBundleExecutable</key><string>fased-desktop</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleShortVersionString</key><string>0.0.0</string><key>NSHighResolutionCapable</key><true/></dict></plist>`
		if e := os.WriteFile(filepath.Join(stage, "Contents", "Info.plist"), []byte(plist), 0600); e != nil {
			return e
		}
	}
	files := map[string]string{en: *engine, "LICENSE": "LICENSE", "NOTICE": "NOTICE"}
	if *desktop != "" {
		files[dn] = *desktop
	}
	m := bundle.Manifest{Schema: 1, Platform: runtime.GOOS, Arch: runtime.GOARCH, Version: core.Version, Source: *source, Desktop: *desktop != "", UnsignedEngineering: true, Files: map[string]bundle.File{}}
	for n, p := range files {
		src, e := os.Open(p)
		if e != nil {
			return e
		}
		dst, e := os.OpenFile(filepath.Join(payload, n), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
		if e != nil {
			src.Close()
			return e
		}
		_, e = io.Copy(dst, src)
		src.Close()
		closeErr := dst.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		id, e := bundle.Inspect(filepath.Join(payload, n))
		if e != nil {
			return e
		}
		m.Files[n] = id
	}
	b, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(payload, "bundle.json"), append(b, '\n'), 0600); e != nil {
		return e
	}
	if _, e = bundle.Verify(payload); e != nil {
		return e
	}
	guide := []byte("Unsigned engineering package; not a public signed release.\nTerminal: ./" + en + " install --bundle . --interactive\nVPS: ./" + en + " install --bundle . --vps --autostart --access ssh\nDesktop: open the bundled Fased desktop client; approve installation for this OS user.\nNo Git, Go, npm, RPC, AI or wallet account is needed at runtime.\nSee https://github.com/fased-ai/fased-app for supported-host and release status.\n")
	if e = os.WriteFile(filepath.Join(stage, "INSTALL.txt"), guide, 0600); e != nil {
		return e
	}
	ext := ".tar.gz"
	if runtime.GOOS != "linux" {
		ext = ".zip"
	}
	target := filepath.Join(*out, name+ext)
	f, e := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	success := false
	defer func() {
		f.Close()
		if !success {
			os.Remove(target)
		}
	}()
	paths := []string{}
	if e = filepath.WalkDir(stage, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	}); e != nil {
		return e
	}
	sort.Strings(paths)
	if ext == ".zip" {
		w := zip.NewWriter(f)
		for _, p := range paths {
			rel, _ := filepath.Rel(stage, p)
			h := &zip.FileHeader{Name: filepath.ToSlash(filepath.Join(prefix, rel)), Method: zip.Deflate}
			h.SetMode(0700)
			entry, e := w.CreateHeader(h)
			if e != nil {
				return e
			}
			if e = copyTo(entry, p); e != nil {
				return e
			}
		}
		e = w.Close()
	} else {
		g := gzip.NewWriter(f)
		w := tar.NewWriter(g)
		for _, p := range paths {
			i, e := os.Stat(p)
			if e != nil {
				return e
			}
			rel, _ := filepath.Rel(stage, p)
			h := &tar.Header{Name: filepath.ToSlash(filepath.Join(prefix, rel)), Size: i.Size(), Mode: 0700, ModTime: time.Unix(0, 0)}
			if e = w.WriteHeader(h); e != nil {
				return e
			}
			if e = copyTo(w, p); e != nil {
				return e
			}
		}
		if e = w.Close(); e == nil {
			e = g.Close()
		}
	}
	if e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	id, e := bundle.Inspect(target)
	if e != nil {
		return e
	}
	receipt := map[string]any{"archive": target, "identity": id, "manifest": m, "publicRelease": false}
	b, e = json.MarshalIndent(receipt, "", "  ")
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(*out, name+".json"), append(b, '\n'), 0600); e != nil {
		return e
	}
	success = true
	fmt.Println(target)
	return nil
}
func copyTo(w io.Writer, path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	_, e = io.Copy(w, f)
	return e
}
