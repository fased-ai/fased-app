//go:build linux

package ownerclient

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

// A protected desktop is selected only from this owner's installed custody
// root and the original completed release, never from a browser-supplied path.
func protectedNativePackage(executable string) (Package, bool, error) {
	var empty Package
	owner := os.Getuid()
	base := filepath.Join("/var/lib/fased-app-package", strconv.Itoa(owner), "local") + string(filepath.Separator)
	if strings.HasPrefix(executable, base) {
		parts := strings.Split(strings.TrimPrefix(executable, base), string(filepath.Separator))
		if owner <= 0 || os.Geteuid() != owner || len(parts) != 2 || parts[1] != localinstall.DesktopName() {
			return empty, true, errors.New("exact Local package wallet client required")
		}
		root, err := update.PackageInstanceRoot(owner, "local", parts[0])
		if err != nil || executable != filepath.Join(root, localinstall.DesktopName()) {
			return empty, true, errors.New("exact Local package wallet client required")
		}
		current, e1 := os.Stat("/proc/self/exe")
		installed, e2 := os.Lstat(executable)
		if e1 != nil || e2 != nil || !installed.Mode().IsRegular() || !os.SameFile(current, installed) {
			return empty, true, errors.New("native desktop changed; reopen the installed application")
		}
		m, err := custodyinstall.LoadPackageInstance(owner, root)
		if err != nil {
			return empty, true, errors.New("verified Local package required")
		}
		pkg, err := custodyinstall.ProtectedPackageNativeRelease(m, "")
		if err != nil || pkg.Version != core.Version {
			return empty, true, errors.New("native desktop differs from the installed release")
		}
		p := Package{Core: filepath.Join(root, localinstall.EngineName()), Signer: filepath.Join(root, "fased-signer"), CoreSHA256: m.CoreHash, SignerSHA256: m.SignerHash, LocalPaths: localinstall.Paths{Root: root, Data: filepath.Join(root, "app-state")}}
		return p, true, p.verify()
	}
	if owner <= 0 || executable != filepath.Join(custodyinstall.Root(owner), localinstall.DesktopName()) {
		return empty, false, nil
	}
	if os.Geteuid() != owner {
		return empty, true, errors.New("native owner must be unprivileged")
	}
	current, e1 := os.Stat("/proc/self/exe")
	installed, e2 := os.Lstat(executable)
	if e1 != nil || e2 != nil || !installed.Mode().IsRegular() || !os.SameFile(current, installed) {
		return empty, true, errors.New("native desktop changed; reopen the installed application")
	}
	m, err := custodyinstall.Load(owner)
	if err != nil {
		return empty, true, errors.New("verified Local native installation required")
	}
	pkg, err := custodyinstall.ProtectedNativeRelease(m)
	if err != nil || pkg.Version != core.Version {
		return empty, true, errors.New("native desktop differs from the installed release")
	}
	paths, err := localinstall.DefaultPaths()
	if err != nil {
		return empty, true, err
	}
	p := Package{Core: filepath.Join(m.Root, localinstall.EngineName()), Signer: filepath.Join(m.Root, "fased-signer"), CoreSHA256: m.CoreHash, SignerSHA256: m.SignerHash, LocalPaths: paths}
	return p, true, p.verify()
}
