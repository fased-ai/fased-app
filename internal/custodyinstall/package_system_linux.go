//go:build linux

package custodyinstall

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
)

const rpmPackageListingQuery = "%{NAME}\\t%{VERSION}\\t%{RELEASE}\\t%{ARCH}\\n[%{FILENAMES}\\t%{FILEMODES:octal}\\t%{FILEUSERNAME}\\t%{FILEGROUPNAME}\\t%{FILEDIGESTS}\\t%{FILELINKTOS}\\t%{FILECAPS}\\t%{FILEFLAGS}\\n]"

// nativePackageFiles is the whole package-owned surface. Instance state is
// deliberately absent, so the OS package manager cannot replace user data.
func nativePackageFiles(p update.MaintenancePackage) (map[string]string, error) {
	if p.Validate() != nil || p.NativePackage == nil || p.NativeDesktop != nil {
		return nil, errors.New("one authenticated browser-first Linux package required")
	}
	files := map[string]string{
		"usr/lib/fased-app/fased-install": p.NativePackage.InstallerSHA256,
		"usr/share/doc/fased-app/LICENSE": "",
		"usr/share/doc/fased-app/NOTICE":  "",
	}
	for _, c := range p.Components {
		files["usr/lib/fased-app/"+c.Artifact.File] = c.Artifact.SHA256
	}
	if len(files) != 5 || files["usr/lib/fased-app/fased-app"] == "" || files["usr/lib/fased-app/fased-signer"] == "" {
		return nil, errors.New("the authenticated package pair is incomplete")
	}
	return files, nil
}

func packageArch() (string, string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64", "x86_64", nil
	case "arm64":
		return "arm64", "aarch64", nil
	default:
		return "", "", errors.New("unsupported native package architecture")
	}
}

func packageTool(ctx context.Context, max int64, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/nonexistent", "LANG=C"}
	cmd.Dir = "/"
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	b, readErr := io.ReadAll(io.LimitReader(out, max+1))
	if int64(len(b)) > max || readErr != nil {
		_ = cmd.Process.Kill()
	}
	if err = errors.Join(readErr, cmd.Wait()); err != nil || int64(len(b)) > max {
		return nil, errors.New("native package inspection failed")
	}
	return b, nil
}

// RPM metadata is checked before invoking rpm; scripts and triggers are also
// disabled on the fixed install command. All package paths are enumerated.
func validateRPMListing(raw []byte, version, arch string, files map[string]string) error {
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) < 2 || lines[0] != "fased-app\t"+version+"\t1\t"+arch {
		return errors.New("RPM package identity differs")
	}
	seen := map[string]bool{}
	for _, line := range lines[1:] {
		fields := strings.Split(line, "\t")
		if len(fields) != 8 || !strings.HasPrefix(fields[0], "/") || fields[2] != "root" || fields[3] != "root" || fields[6] != "(none)" {
			return errors.New("unsafe RPM file metadata")
		}
		name := strings.TrimPrefix(fields[0], "/")
		if path.Clean(name) != name || seen[name] {
			return errors.New("duplicate or invalid RPM path")
		}
		// RPM marks the two packaged documents as documentation automatically.
		// Other file flags can affect replacement and are never admitted here.
		document := name == "usr/share/doc/fased-app/LICENSE" || name == "usr/share/doc/fased-app/NOTICE"
		if fields[7] != "0" && !(document && fields[7] == "2") {
			return errors.New("unsafe RPM file flags")
		}
		seen[name] = true
		switch name {
		case "usr/lib/fased-app", "usr/share/doc/fased-app":
			if fields[1] != "40755" || fields[5] != "" {
				return errors.New("unsafe RPM directory")
			}
		case "usr/bin/fased":
			if fields[1] != "120777" || fields[5] != "../lib/fased-app/fased-app" {
				return errors.New("unsafe RPM command link")
			}
		default:
			expected, ok := files[name]
			mode := "100755"
			if strings.HasPrefix(name, "usr/share/doc/fased-app/") {
				mode = "100644"
			}
			digest, digestErr := hex.DecodeString(fields[4])
			if !ok || fields[1] != mode || fields[5] != "" || digestErr != nil || len(digest) != 32 || hex.EncodeToString(digest) != fields[4] || expected != "" && fields[4] != expected {
				return errors.New("RPM contents differ from the approved pair")
			}
			if expected == "" {
				files[name] = fields[4]
			}
		}
	}
	if len(seen) != len(files)+3 {
		return errors.New("RPM package contents are incomplete")
	}
	return nil
}

func inspectDebTar(r io.Reader, control bool, files map[string]string) (map[string][]byte, error) {
	tr := tar.NewReader(r)
	seen, documents := map[string]bool{}, map[string][]byte{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || h.Uid != 0 || h.Gid != 0 || h.Size < 0 || h.Size > 128<<20 || len(h.Xattrs) != 0 {
			return nil, errors.New("unsafe Debian archive")
		}
		for key := range h.PAXRecords {
			if strings.Contains(strings.ToLower(key), "xattr") || strings.HasPrefix(strings.ToLower(key), "security.") {
				return nil, errors.New("Debian extended security attributes are forbidden")
			}
		}
		name := strings.TrimPrefix(h.Name, "./")
		name = strings.TrimSuffix(name, "/")
		if name == "" || name == "." {
			if h.Typeflag != tar.TypeDir || h.Mode != 0755 {
				return nil, errors.New("invalid Debian root entry")
			}
			continue
		}
		if strings.HasPrefix(name, "/") || path.Clean(name) != name || seen[name] {
			return nil, errors.New("duplicate or unsafe Debian path")
		}
		if h.Size > 64<<20 && (control || name != "usr/lib/fased-app/fased-install") {
			return nil, errors.New("oversized Debian package file")
		}
		seen[name] = true
		if control {
			if name != "control" || h.Typeflag != tar.TypeReg || h.Size > 16<<10 {
				return nil, errors.New("Debian maintainer scripts or extra control files are forbidden")
			}
			b, err := io.ReadAll(io.LimitReader(tr, (16<<10)+1))
			if err != nil || int64(len(b)) != h.Size {
				return nil, errors.New("invalid Debian control metadata")
			}
			documents[name] = b
			continue
		}
		switch name {
		case "usr", "usr/bin", "usr/lib", "usr/lib/fased-app", "usr/share", "usr/share/doc", "usr/share/doc/fased-app":
			if h.Typeflag != tar.TypeDir || h.Mode != 0755 {
				return nil, errors.New("unsafe Debian directory")
			}
		case "usr/bin/fased":
			if h.Typeflag != tar.TypeSymlink || h.Linkname != "../lib/fased-app/fased-app" {
				return nil, errors.New("unsafe Debian command link")
			}
		default:
			expected, ok := files[name]
			mode := int64(0755)
			if strings.HasPrefix(name, "usr/share/doc/fased-app/") {
				mode = 0644
			}
			if !ok || h.Typeflag != tar.TypeReg || h.Mode != mode {
				return nil, errors.New("unexpected Debian package file")
			}
			hash := sha256.New()
			n, err := io.CopyBuffer(hash, tr, make([]byte, 32<<10))
			digest := hex.EncodeToString(hash.Sum(nil))
			if err != nil || n != h.Size || expected != "" && digest != expected {
				return nil, errors.New("Debian package differs from the approved pair")
			}
			if expected == "" {
				files[name] = digest
			}
		}
	}
	if control {
		if len(seen) != 1 || documents["control"] == nil {
			return nil, errors.New("one Debian control file required")
		}
	} else {
		for name := range files {
			if !seen[name] {
				return nil, errors.New("Debian package contents are incomplete")
			}
		}
		if !seen["usr/bin/fased"] {
			return nil, errors.New("Debian command link is missing")
		}
	}
	return documents, nil
}

func inspectDebianPackage(ctx context.Context, packagePath, version, arch string, files map[string]string) error {
	return inspectDebianPackageWithTool(ctx, "/usr/bin/dpkg-deb", packagePath, version, arch, files)
}

func inspectDebianPackageWithTool(ctx context.Context, tool, packagePath, version, arch string, files map[string]string) error {
	for _, item := range []struct {
		option  string
		control bool
	}{{"--ctrl-tarfile", true}, {"--fsys-tarfile", false}} {
		cmd := exec.CommandContext(ctx, tool, item.option, packagePath)
		cmd.Env, cmd.Dir = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C"}, "/"
		out, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err = cmd.Start(); err != nil {
			return err
		}
		docs, inspectErr := inspectDebTar(out, item.control, files)
		if inspectErr == nil {
			inspectErr = drainPackageTrailer(out)
		}
		if inspectErr != nil {
			_ = cmd.Process.Kill()
		}
		if err = errors.Join(inspectErr, cmd.Wait()); err != nil {
			return err
		}
		if item.control {
			want := fmt.Sprintf("Package: fased-app\nVersion: %s\nArchitecture: %s\nMaintainer: Fased AI\nDepends: ca-certificates, systemd\nDescription: Fased application\n", version, arch)
			if string(docs["control"]) != want {
				return errors.New("Debian package control metadata differs")
			}
		}
	}
	return nil
}

// tar.Reader stops at its end marker, but dpkg-deb can still be writing tar
// padding to the pipe. Drain a bounded, zero-only trailer before waiting for
// the process; reject hidden trailing data instead of hanging or ignoring it.
func drainPackageTrailer(r io.Reader) error {
	const max = 1 << 20
	buf := make([]byte, 32<<10)
	remaining := int64(max + 1)
	for remaining > 0 {
		n, err := r.Read(buf[:min(int64(len(buf)), remaining)])
		for _, b := range buf[:n] {
			if b != 0 {
				return errors.New("unexpected data after Debian archive")
			}
		}
		remaining -= int64(n)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	return errors.New("Debian archive trailer exceeds limit")
}

// verifyNativePackage inspects only the retained, root-protected package that
// the original candidate bound. It accepts no path or command from the UI.
func verifyNativePackage(ctx context.Context, m Manifest, p update.MaintenancePackage) (string, map[string]string, error) {
	files, err := nativePackageFiles(p)
	if err != nil {
		return "", nil, err
	}
	platform, err := hostplatform.Current()
	if err != nil || platform.Family != p.NativePackage.Family {
		return "", nil, errors.New("package family differs from the host")
	}
	debArch, rpmArch, err := packageArch()
	if err != nil {
		return "", nil, err
	}
	ext := ".rpm"
	if platform.Family == "apt" {
		ext = ".deb"
	}
	if p.NativePackage.Artifact.File != "fased-app-linux-"+debArch+ext {
		return "", nil, errors.New("native package architecture differs")
	}
	root, err := update.PackageMaintenanceRoot(m.Owner, m.Location, m.ID)
	if err != nil {
		return "", nil, err
	}
	packagePath := filepath.Join(root, "candidate", p.ReleaseSHA256, p.NativePackage.Artifact.File)
	hash, size, err := maintenance.ProtectedDigest(packagePath, 0600, 128<<20)
	if err != nil || hash != p.NativePackage.Artifact.SHA256 || size != p.NativePackage.Artifact.Size {
		return "", nil, errors.New("the original authenticated Linux package is unavailable")
	}
	if platform.Family == "apt" {
		err = inspectDebianPackage(ctx, packagePath, p.Version, debArch, files)
	} else {
		var listing []byte
		listing, err = packageTool(ctx, 64<<10, "/usr/bin/rpm", "-qp", "--queryformat", rpmPackageListingQuery, "--", packagePath)
		if err == nil {
			err = validateRPMListing(listing, p.Version, rpmArch, files)
		}
		for _, option := range []string{"--obsoletes", "--conflicts"} {
			if err != nil {
				break
			}
			var conflicts []byte
			conflicts, err = packageTool(ctx, 16<<10, "/usr/bin/rpm", "-qp", option, "--", packagePath)
			if err == nil && len(strings.TrimSpace(string(conflicts))) != 0 {
				err = errors.New("RPM may not replace unrelated packages")
			}
		}
		for _, option := range []string{"--scripts", "--triggers"} {
			if err != nil {
				break
			}
			var scripts []byte
			scripts, err = packageTool(ctx, 16<<10, "/usr/bin/rpm", "-qp", option, "--", packagePath)
			if err == nil && len(strings.TrimSpace(string(scripts))) != 0 {
				err = errors.New("RPM maintainer scripts and triggers are forbidden")
			}
		}
	}
	return packagePath, files, err
}

// globalPackageState accepts only files from the recorded predecessor or the
// recorded target. During a journalled replacement, an interrupted package
// manager may have left either exact version or temporarily removed a file.
func globalPackageState(old, wanted map[string]string, interrupted, requireComplete bool) error {
	if len(wanted) != 5 {
		return errors.New("authenticated target package required")
	}
	for _, dir := range []string{"/usr/lib/fased-app", "/usr/share/doc/fased-app"} {
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) && !requireComplete && (old == nil || interrupted) {
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || info.Mode().Perm() != 0755 {
			return errors.New("foreign package directory; preserve it")
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || st.Gid != 0 {
			return errors.New("foreign package directory owner; preserve it")
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name := strings.TrimPrefix(filepath.Join(dir, entry.Name()), "/")
			if _, ok := wanted[name]; !ok {
				return errors.New("unclassified package file; preserve it")
			}
		}
	}
	for name, targetHash := range wanted {
		path := "/" + name
		mode := os.FileMode(0755)
		if strings.HasPrefix(name, "usr/share/doc/fased-app/") {
			mode = 0644
		}
		hash, _, err := maintenance.ProtectedDigest(path, mode, 128<<20)
		if errors.Is(err, os.ErrNotExist) && !requireComplete && (old == nil || interrupted) {
			continue
		}
		if err != nil {
			return errors.New("unavailable or foreign package file; preserve it")
		}
		if targetHash == "" {
			return errors.New("package file digest is unavailable")
		}
		if old == nil {
			if hash != targetHash {
				return errors.New("existing package file differs; preserve it")
			}
		} else if hash != old[name] && (hash != targetHash || !interrupted) {
			return errors.New("package files differ from the recorded operation")
		}
	}
	info, err := os.Lstat("/usr/bin/fased")
	if errors.Is(err, os.ErrNotExist) && !requireComplete && (old == nil || interrupted) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return errors.New("existing Fased command is not the package link; preserve it")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	link, readErr := os.Readlink("/usr/bin/fased")
	if !ok || st.Uid != 0 || st.Gid != 0 || st.Nlink != 1 || readErr != nil || link != "../lib/fased-app/fased-app" {
		return errors.New("foreign Fased command link; preserve it")
	}
	return nil
}

func installedNativePackage(ctx context.Context, family string) (version, status string, err error) {
	var b []byte
	if family == "dnf" {
		b, err = packageTool(ctx, 4096, "/usr/bin/rpm", "-q", "--queryformat", "%{NAME}\\t%{VERSION}\\t%{RELEASE}\\t%{ARCH}\\n", "--", "fased-app")
	} else {
		b, err = packageTool(ctx, 4096, "/usr/bin/dpkg-query", "-W", "-f=${Package}\\t${Version}\\t${Architecture}\\t${Status}\\n", "--", "fased-app")
	}
	if err != nil {
		// A missing package must be distinguished from a broken package DB by
		// the fixed package-manager query's exit status in nativePackagePresent.
		return "", "", err
	}
	fields := strings.Split(strings.TrimSuffix(string(b), "\n"), "\t")
	debArch, rpmArch, archErr := packageArch()
	if archErr != nil || len(fields) != 4 || fields[0] != "fased-app" {
		return "", "", errors.New("installed native package identity differs")
	}
	if family == "dnf" {
		if fields[2] != "1" || fields[3] != rpmArch {
			return "", "", errors.New("installed RPM identity differs")
		}
		return fields[1], "installed", nil
	}
	if fields[2] != debArch {
		return "", "", errors.New("installed Debian architecture differs")
	}
	return fields[1], fields[3], nil
}

// A stopped key-bearing instance may restart its predecessor only while the
// complete system package is still the exact authenticated predecessor.
func verifyInstalledNativePackage(ctx context.Context, m Manifest, p update.MaintenancePackage) error {
	if p.NativePackage == nil {
		return errors.New("the original native package is unavailable")
	}
	_, files, err := verifyNativePackage(ctx, m, p)
	if err != nil {
		return err
	}
	version, status, err := installedNativePackage(ctx, p.NativePackage.Family)
	if err != nil {
		return errors.New("the original native package is not completely installed; preserve recovery")
	}
	return requireInstalledNativeBase(p.NativePackage.Family, p.Version, version, status, func() error {
		return globalPackageState(nil, files, false, true)
	})
}

func requireInstalledNativeBase(family, expected, actual, status string, checkFiles func() error) error {
	if expected == "" || actual != expected || checkFiles == nil ||
		family == "apt" && status != "install ok installed" ||
		family == "dnf" && status != "installed" || family != "apt" && family != "dnf" {
		return errors.New("the original native package is not completely installed; preserve recovery")
	}
	return checkFiles()
}

func nativePackagePresent(ctx context.Context, family string) (bool, error) {
	name, args := "/usr/bin/rpm", []string{"-q", "--", "fased-app"}
	if family == "apt" {
		name, args = "/usr/bin/dpkg-query", []string{"-W", "-f=${Status}", "--", "fased-app"}
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env, cmd.Dir = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C"}, "/"
	output, err := cmd.Output()
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		if family == "dnf" && strings.TrimSpace(string(output)) == "package fased-app is not installed" && len(exit.Stderr) == 0 {
			return false, nil
		}
		if family == "apt" && len(output) == 0 && strings.Contains(string(exit.Stderr), "no packages found matching fased-app") {
			return false, nil
		}
	}
	return false, errors.New("native package database cannot be inspected")
}

func verifyInstalledDebianControls(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return errors.New("installed Debian package metadata is unavailable; preserve it")
	}
	for _, entry := range entries {
		name := entry.Name()
		for _, suffix := range []string{".preinst", ".postinst", ".prerm", ".postrm"} {
			if !strings.HasSuffix(name, suffix) {
				continue
			}
			pkg := strings.TrimSuffix(name, suffix)
			if pkg == "fased-app" || strings.HasPrefix(pkg, "fased-app:") {
				return errors.New("installed Fased package has an unverified maintainer script; preserve it")
			}
		}
	}
	return nil
}

var errIncompleteNativePackageInventory = errors.New("installed package inventory is incomplete; preserve it")

func verifyInstalledPackagePaths(raw []byte, family string, files map[string]string, interrupted bool) error {
	if (family != "apt" && family != "dnf") || len(files) != 5 || len(raw) == 0 {
		return errors.New("installed package inventory is unavailable; preserve it")
	}
	required := map[string]bool{"/usr/bin/fased": false}
	for name := range files {
		required["/"+name] = false
	}
	directories := map[string]bool{"/usr/lib/fased-app": true, "/usr/share/doc/fased-app": true}
	if family == "apt" {
		for _, dir := range []string{"/.", "/usr", "/usr/bin", "/usr/lib", "/usr/share", "/usr/share/doc"} {
			directories[dir] = true
		}
	}
	seen := make(map[string]bool)
	for _, name := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if seen[name] || !strings.HasPrefix(name, "/") || name != "/." && path.Clean(name) != name {
			return errors.New("installed package inventory is ambiguous; preserve it")
		}
		seen[name] = true
		if _, ok := required[name]; ok {
			required[name] = true
		} else if !directories[name] {
			return errors.New("installed package owns an unclassified path; preserve it")
		}
	}
	if !interrupted {
		for _, found := range required {
			if !found {
				return errIncompleteNativePackageInventory
			}
		}
	}
	return nil
}

func verifyInstalledNativePaths(ctx context.Context, family string, files map[string]string, interrupted bool) error {
	name, args := "/usr/bin/rpm", []string{"-ql", "--", "fased-app"}
	if family == "apt" {
		name, args = "/usr/bin/dpkg-query", []string{"-L", "--", "fased-app"}
	}
	listing, err := packageTool(ctx, 64<<10, name, args...)
	if err != nil {
		return errors.New("installed package inventory is unavailable; preserve it")
	}
	return verifyInstalledPackagePaths(listing, family, files, interrupted)
}

func preflightNativePackage(ctx context.Context, m Manifest, previous *update.MaintenancePackage, target update.MaintenancePackage, interrupted bool) (string, map[string]string, error) {
	packagePath, wanted, err := verifyNativePackage(ctx, m, target)
	if err != nil {
		return "", nil, err
	}
	family := target.NativePackage.Family
	// dpkg may run the old package's prerm/postrm even when the new package
	// has no scripts and triggers are disabled. Never execute unknown retained
	// scripts; this check also applies to interrupted package recovery.
	if family == "apt" {
		if err = verifyInstalledDebianControls("/var/lib/dpkg/info"); err != nil {
			return "", nil, err
		}
	}
	if previous != nil && (previous.NativePackage == nil || previous.NativePackage.Family != family) {
		return "", nil, errors.New("the recorded native package predecessor is required")
	}
	var old map[string]string
	if previous != nil {
		_, old, err = verifyNativePackage(ctx, m, *previous)
		if err != nil {
			return "", nil, err
		}
	}
	present, err := nativePackagePresent(ctx, family)
	if err != nil {
		return "", nil, err
	}
	if present {
		version, status, err := installedNativePackage(ctx, family)
		validDebStatus := status == "install ok installed" || interrupted && (status == "install ok unpacked" || status == "install ok half-configured" || status == "install reinstreq half-installed")
		if err != nil || version != target.Version && (previous == nil || version != previous.Version) || family == "apt" && !validDebStatus {
			return "", nil, errors.New("installed native package differs; preserve it")
		}
		if err = verifyInstalledNativePaths(ctx, family, wanted, interrupted); err != nil {
			return "", nil, err
		}
		if version == target.Version && (family != "apt" || status == "install ok installed") && globalPackageState(nil, wanted, false, true) == nil {
			return packagePath, wanted, nil
		}
	} else if previous != nil && !interrupted {
		return "", nil, errors.New("the recorded native package is missing; preserve it")
	}
	if previous == nil && !present && !interrupted {
		for _, name := range []string{"/usr/bin/fased", "/usr/lib/fased-app", "/usr/share/doc/fased-app"} {
			if _, e := os.Lstat(name); !errors.Is(e, os.ErrNotExist) {
				return "", nil, errors.New("an unclassified Fased package path exists; preserve it")
			}
		}
	}
	if err = globalPackageState(old, wanted, interrupted, false); err != nil {
		return "", nil, err
	}
	return packagePath, wanted, nil
}

// applyNativePackage executes only the already inspected exact package through
// the host's fixed package manager. Scripts and unrelated triggers are barred.
func applyNativePackage(ctx context.Context, m Manifest, previous *update.MaintenancePackage, target update.MaintenancePackage, interrupted bool) (result error) {
	lock, err := maintenance.PackageSystemLock(ctx)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	return applyNativePackageLocked(ctx, m, previous, target, interrupted)
}

// The caller holds the shared package lock through its predecessor check and
// replacement. This entry also lets the update keep that lock while stopping.
func applyNativePackageLocked(ctx context.Context, m Manifest, previous *update.MaintenancePackage, target update.MaintenancePackage, interrupted bool) error {
	packagePath, wanted, err := preflightNativePackage(ctx, m, previous, target, interrupted)
	if err != nil {
		return err
	}
	family := target.NativePackage.Family
	present, err := nativePackagePresent(ctx, family)
	if err != nil {
		return err
	}
	if present {
		version, status, e := installedNativePackage(ctx, family)
		if e != nil {
			return e
		}
		validDebStatus := status == "install ok installed" || interrupted && (status == "install ok unpacked" || status == "install ok half-configured" || status == "install reinstreq half-installed")
		if version != target.Version && (previous == nil || version != previous.Version) || family == "apt" && !validDebStatus {
			return errors.New("installed native package differs; preserve it")
		}
		if err = verifyInstalledNativePaths(ctx, family, wanted, interrupted); err != nil {
			return err
		}
		if version == target.Version && (family != "apt" || status == "install ok installed") && globalPackageState(nil, wanted, false, true) == nil {
			err = verifyInstalledNativePaths(ctx, family, wanted, false)
			if err == nil {
				return nil
			}
			// A journalled interruption may have left the package manager's
			// inventory incomplete even when the files match. Reinstall the
			// same verified target; never report that state as complete.
			if !interrupted || !errors.Is(err, errIncompleteNativePackageInventory) {
				return err
			}
		}
	} else if previous != nil && !interrupted {
		return errors.New("the recorded native package is missing; preserve it")
	}
	if family == "apt" {
		if err = verifyInstalledDebianControls("/var/lib/dpkg/info"); err != nil {
			return err
		}
	}
	name, args := nativePackageInstallCommand(family, packagePath)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env, cmd.Dir = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/nonexistent", "LANG=C", "DEBIAN_FRONTEND=noninteractive"}, "/"
	if err = cmd.Run(); err != nil {
		return errors.New("native package replacement requires recovery")
	}
	version, status, err := installedNativePackage(ctx, family)
	if err != nil || version != target.Version || family == "apt" && status != "install ok installed" {
		return errors.New("native package installation is unconfirmed")
	}
	if err = globalPackageState(nil, wanted, false, true); err != nil {
		return err
	}
	return verifyInstalledNativePaths(ctx, family, wanted, false)
}

// Provenance and the exact package digest are verified before this command.
// RPM's separate signature policy must not require a second signing system.
func nativePackageInstallCommand(family, packagePath string) (string, []string) {
	if family == "apt" {
		return "/usr/bin/dpkg", []string{"--no-triggers", "--install", "--", packagePath}
	}
	return "/usr/bin/rpm", []string{"-U", "--replacepkgs", "--noscripts", "--notriggers", "--noplugins", "--nosignature", "--", packagePath}
}
