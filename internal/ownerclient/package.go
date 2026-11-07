package ownerclient

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
)

// Package is selected from the native executable's own verified image, never
// from a browser request or a caller-supplied signer path.
type Package struct {
	Core, Signer, CoreSHA256, SignerSHA256 string
	LocalPaths                             localinstall.Paths
}

func NativePackage(executable string) (Package, error) {
	var p Package
	if runtime.GOOS != "linux" {
		return p, errors.New("protected enrollment requires the Linux native client")
	}
	root := filepath.Dir(executable)
	if filepath.Base(executable) != localinstall.DesktopName() {
		return p, errors.New("open Wallets from the installed Fased native client")
	}
	if m, err := bundle.Verify(root); err == nil && m.Desktop && m.Version == core.Version && m.Files["fased-signer"].SHA256 != "" {
		p = Package{Core: filepath.Join(root, localinstall.EngineName()), Signer: filepath.Join(root, "fased-signer"), CoreSHA256: m.Files[localinstall.EngineName()].SHA256, SignerSHA256: m.Files["fased-signer"].SHA256}
		p.LocalPaths, err = localinstall.DefaultPaths()
		return p, err
	}
	// Installed native images keep the helper in the same protected inventory.
	b, err := core.ReadPrivateFile(filepath.Join(root, "install.json"))
	var m localinstall.Manifest
	if err != nil || decode(b, &m) != nil || m.Root != root || m.Version != core.Version || m.Files["fased-signer"] == "" || m.Files[localinstall.DesktopName()] == "" {
		return p, errors.New("update the native client to a package with protected Wallets support")
	}
	paths := localinstall.Paths{Root: m.Root, Data: m.Data}
	if _, err = localinstall.Load(paths); err != nil {
		return p, err
	}
	p = Package{Core: filepath.Join(root, localinstall.EngineName()), Signer: filepath.Join(root, "fased-signer"), CoreSHA256: m.Files[localinstall.EngineName()], SignerSHA256: m.Files["fased-signer"], LocalPaths: paths}
	return p, p.verify()
}

func (p Package) verify() error {
	if p.Core == p.Signer || !filepath.IsAbs(p.Core) || !filepath.IsAbs(p.Signer) {
		return errors.New("distinct native core and signer images are required")
	}
	for path, want := range map[string]string{p.Core: p.CoreSHA256, p.Signer: p.SignerSHA256} {
		info, err := os.Lstat(path)
		got, e := bundle.Inspect(path)
		if err != nil || !info.Mode().IsRegular() || e != nil || want == "" || got.SHA256 != want {
			return errors.New("native enrollment payload changed; preserve the original package")
		}
	}
	return nil
}
