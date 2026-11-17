//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

var linuxPackageVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type linuxSystemPackageReceipt struct {
	Version       int                    `json:"version"`
	Product       string                 `json:"product"`
	Source        string                 `json:"source"`
	Architecture  string                 `json:"architecture"`
	Inputs        map[string]bundle.File `json:"inputs"`
	Packages      map[string]bundle.File `json:"packages"`
	PublicRelease bool                   `json:"publicRelease"`
}

// packageLinuxSystem creates scriptless OS packages from the same bound
// binaries. Publisher authentication and installed setup are separate steps.
func packageLinuxSystem(engine, signer, installer, desktop, output, source string) error {
	if os.Getuid() <= 0 || os.Geteuid() != os.Getuid() || !linuxPackageVersion.MatchString(core.Version) || !canonicalSource(source) {
		return errors.New("an unprivileged canonical Linux release build is required")
	}
	rpmArch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	if rpmArch == "" {
		return errors.New("Linux system packages require amd64 or arm64")
	}
	debTool, err := exec.LookPath("dpkg-deb")
	if err != nil {
		return errors.New("dpkg-deb is required to build the Debian package")
	}
	rpmTool, err := exec.LookPath("rpmbuild")
	if err != nil {
		return errors.New("rpmbuild is required to build the RPM package")
	}
	stage, err := os.MkdirTemp("", "fased-linux-package-")
	if err != nil {
		return err
	}
	stageInfo, err := os.Lstat(stage)
	if err != nil {
		return err
	}
	defer func() {
		if current, err := os.Lstat(stage); err == nil && os.SameFile(stageInfo, current) {
			_ = os.RemoveAll(stage)
		}
	}()
	inputs := map[string]string{"fased-app": engine, "fased-signer": signer, "fased-install": installer, "LICENSE": "LICENSE", "NOTICE": "NOTICE"}
	if desktop != "" {
		inputs["fased-desktop"] = desktop
	}
	identities := make(map[string]bundle.File, len(inputs))
	for name, path := range inputs {
		mode := os.FileMode(0755)
		if name == "LICENSE" || name == "NOTICE" {
			mode = 0644
		}
		identities[name], err = stageLinuxPackageInput(path, filepath.Join(stage, name), mode)
		if err != nil {
			return fmt.Errorf("stage %s: %w", name, err)
		}
		if name == "fased-app" || name == "fased-signer" || name == "fased-install" {
			if err = verifyStaticLinuxExecutable(filepath.Join(stage, name)); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	if err = verifyLinuxPackageBinding(stage, source, identities); err != nil {
		return err
	}
	if err = verifyLinuxPairContracts(stage); err != nil {
		return err
	}
	deb, rpm := filepath.Join(stage, "fased.deb"), filepath.Join(stage, "fased.rpm")
	if err = buildDebPackage(stage, debTool, deb, inputs); err != nil {
		return err
	}
	if err = buildRPMPackage(stage, rpmTool, rpm, rpmArch, inputs); err != nil {
		return err
	}
	return publishLinuxPackages(output, source, identities, deb, rpm)
}

// publishLinuxPackages exposes only a complete pair and never replaces an
// existing output. An interrupted build can be retried without adopting an
// incomplete or foreign artifact.
func publishLinuxPackages(output, source string, identities map[string]bundle.File, deb, rpm string) error {
	if err := os.MkdirAll(output, 0700); err != nil {
		return err
	}
	staged, err := os.MkdirTemp(output, ".fased-linux-package-")
	if err != nil {
		return err
	}
	stageInfo, err := os.Lstat(staged)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			if current, err := os.Lstat(staged); err == nil && os.SameFile(stageInfo, current) {
				_ = os.RemoveAll(staged)
			}
		}
	}()
	destination := filepath.Join(output, "fased-linux-system-"+runtime.GOARCH)
	receipt := linuxSystemPackageReceipt{Version: 1, Product: core.Version, Source: source, Architecture: runtime.GOARCH, Inputs: identities, Packages: map[string]bundle.File{}}
	for _, item := range []struct{ format, path string }{{"deb", deb}, {"rpm", rpm}} {
		name := "fased-app-linux-" + runtime.GOARCH + "." + item.format
		receipt.Packages[name], err = stageLinuxPackageInput(item.path, filepath.Join(staged, name), 0600)
		if err != nil {
			return err
		}
	}
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(staged, "packages.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(append(raw, '\n'))
	if err == nil {
		err = file.Sync()
	}
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	dir, err := os.Open(staged)
	if err != nil {
		return err
	}
	if err = errors.Join(dir.Sync(), dir.Close()); err != nil {
		return err
	}
	if err = unix.Renameat2(unix.AT_FDCWD, staged, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE); err != nil {
		return fmt.Errorf("cannot commit Linux packages without replacing existing output: %w", err)
	}
	published = true
	dir, err = os.Open(output)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func canonicalSource(source string) bool {
	b, err := hex.DecodeString(source)
	return err == nil && len(b) == 20 && hex.EncodeToString(b) == source
}

func verifyStaticLinuxExecutable(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		return errors.New("a Linux executable is required")
	}
	defer f.Close()
	machine := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[runtime.GOARCH]
	if f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB || f.Machine != machine || (f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN) {
		return errors.New("Linux executable architecture differs from the package")
	}
	for _, program := range f.Progs {
		if program.Type == elf.PT_INTERP {
			return errors.New("core, signer and installer must not depend on a distribution-specific loader")
		}
	}
	return nil
}

func stageLinuxPackageInput(source, destination string, mode os.FileMode) (bundle.File, error) {
	want, err := bundle.Inspect(source)
	if err != nil || want.Bytes <= 0 {
		return bundle.File{}, errors.New("a bounded regular input is required")
	}
	before, err := os.Lstat(source)
	if err != nil || !before.Mode().IsRegular() {
		return bundle.File{}, errors.New("package input changed")
	}
	in, err := os.Open(source)
	if err != nil {
		return bundle.File{}, err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return bundle.File{}, errors.New("package input changed")
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return bundle.File{}, err
	}
	hash := sha256.New()
	n, copyErr := io.CopyBuffer(io.MultiWriter(out, hash), io.LimitReader(in, want.Bytes+1), make([]byte, 32*1024))
	if copyErr == nil {
		copyErr = out.Chmod(mode)
	}
	if copyErr == nil {
		copyErr = out.Sync()
	}
	copyErr = errors.Join(copyErr, out.Close())
	after, statErr := os.Lstat(source)
	if copyErr != nil || statErr != nil || !os.SameFile(before, after) || n != want.Bytes || hex.EncodeToString(hash.Sum(nil)) != want.SHA256 {
		return bundle.File{}, errors.New("package input changed during staging")
	}
	return want, nil
}

func verifyLinuxPackageBinding(stage, source string, files map[string]bundle.File) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, filepath.Join(stage, "fased-app"), "native-release-binding")
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C"}
	command.Dir = "/"
	pipe, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	if err = command.Start(); err != nil {
		return err
	}
	raw, readErr := io.ReadAll(io.LimitReader(pipe, 4097))
	if readErr != nil || len(raw) > 4096 {
		_ = command.Process.Kill()
	}
	err = errors.Join(readErr, command.Wait())
	var binding bootstrap.NativeReleaseBinding
	if err != nil || len(raw) > 4096 || update.StrictJSON(raw, &binding) != nil || !binding.Valid(core.Version, source) || binding.SignerSHA256 != files["fased-signer"].SHA256 {
		return errors.New("core and signer are not bound to this release")
	}
	desktop, hasDesktop := files["fased-desktop"]
	if hasDesktop && (binding.Version != 1 || desktop.SHA256 != binding.DesktopSHA256) {
		return errors.New("desktop input differs from the core release binding")
	}
	for _, item := range []struct{ option, name string }{{"--emit-embedded-signer", "fased-signer"}, {"--emit-embedded-native", "fased-desktop"}} {
		want, present := files[item.name]
		if item.name == "fased-desktop" && binding.Version == 1 && !present {
			// The browser-first OS package need not install a shared client;
			// its verified installer still contains the exact bound owner client.
			want.SHA256, present = binding.DesktopSHA256, true
		}
		if !present {
			continue
		}
		cmd := exec.CommandContext(ctx, filepath.Join(stage, "fased-install"), item.option)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C"}
		cmd.Dir = "/"
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err = cmd.Start(); err != nil {
			return err
		}
		identity, readErr := bootstrap.ReadWalletPayload(pipe, io.Discard, core.Version, source)
		if readErr != nil {
			_ = cmd.Process.Kill()
		}
		if err = errors.Join(readErr, cmd.Wait()); err != nil || identity.SHA256 != want.SHA256 || want.Bytes != 0 && identity.Bytes != want.Bytes {
			return errors.New("installer payload differs from the bound release")
		}
	}
	return nil
}

// The identity binding above says which signer was included. The executable
// contracts additionally reject a pair that cannot perform ordinary custody
// maintenance; neither check grants a wallet or update authority.
func verifyLinuxPairContracts(stage string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var app struct {
		Version            int             `json:"version"`
		Target             update.Target   `json:"target"`
		Contract           update.Contract `json:"contract"`
		WalletSigner       bool            `json:"walletSigner"`
		FinancialExecution bool            `json:"financialExecution"`
		UpdaterEnabled     bool            `json:"updaterEnabled"`
	}
	var signer update.Contract
	if readLinuxBinaryJSON(ctx, filepath.Join(stage, "fased-app"), &app, "updates", "ordinary-custody-compatibility") != nil ||
		readLinuxBinaryJSON(ctx, filepath.Join(stage, "fased-signer"), &signer, "ordinary-custody-compatibility") != nil ||
		app.Version != 1 || app.Target != (update.Target{OS: "linux", Arch: runtime.GOARCH}) || app.WalletSigner || app.FinancialExecution || app.UpdaterEnabled ||
		!update.OrdinaryContracts([]update.Contract{app.Contract, signer}) {
		return errors.New("core and signer do not advertise a compatible ordinary custody maintenance pair")
	}
	return nil
}

func readLinuxBinaryJSON(ctx context.Context, path string, target any, args ...string) error {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C"}
	cmd.Dir = "/"
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	raw, readErr := io.ReadAll(io.LimitReader(pipe, 64*1024+1))
	if readErr != nil || len(raw) > 64*1024 {
		_ = cmd.Process.Kill()
	}
	if err = errors.Join(readErr, cmd.Wait()); err != nil {
		return err
	}
	if len(raw) > 64*1024 {
		return errors.New("executable compatibility response is too large")
	}
	return update.StrictJSON(raw, target)
}

func buildDebPackage(stage, tool, target string, files map[string]string) error {
	root := filepath.Join(stage, "deb-root")
	for _, dir := range []string{"DEBIAN", "usr/bin", "usr/lib/fased-app", "usr/share/doc/fased-app"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			return err
		}
	}
	control := fmt.Sprintf("Package: fased-app\nVersion: %s\nArchitecture: %s\nMaintainer: Fased AI\nDepends: ca-certificates, systemd\nDescription: Fased application\n", core.Version, runtime.GOARCH)
	if err := os.WriteFile(filepath.Join(root, "DEBIAN/control"), []byte(control), 0644); err != nil {
		return err
	}
	for name := range files {
		dir := "usr/lib/fased-app"
		if name == "LICENSE" || name == "NOTICE" {
			dir = "usr/share/doc/fased-app"
		}
		if err := os.Link(filepath.Join(stage, name), filepath.Join(root, dir, name)); err != nil {
			return err
		}
	}
	if err := os.Symlink("../lib/fased-app/fased-app", filepath.Join(root, "usr/bin/fased")); err != nil {
		return err
	}
	return runLinuxPackageTool(stage, tool, "--root-owner-group", "-Zgzip", "--build", root, target)
}

func buildRPMPackage(stage, tool, target, arch string, files map[string]string) error {
	top := filepath.Join(stage, "rpmbuild")
	for _, dir := range []string{"SOURCES", "SPECS", "BUILD", "RPMS", "SRPMS", "TMP"} {
		if err := os.MkdirAll(filepath.Join(top, dir), 0755); err != nil {
			return err
		}
	}
	var install, listing strings.Builder
	for _, name := range []string{"fased-app", "fased-signer", "fased-install", "fased-desktop", "LICENSE", "NOTICE"} {
		if _, ok := files[name]; !ok {
			continue
		}
		if err := os.Link(filepath.Join(stage, name), filepath.Join(top, "SOURCES", name)); err != nil {
			return err
		}
		dir, mode := "/usr/lib/fased-app", "0755"
		if name == "LICENSE" || name == "NOTICE" {
			dir, mode = "/usr/share/doc/fased-app", "0644"
		}
		fmt.Fprintf(&install, "install -m %s \"%%{_sourcedir}/%s\" \"%%{buildroot}%s/%s\"\n", mode, name, dir, name)
		fmt.Fprintf(&listing, "%%attr(%s,root,root) %s/%s\n", mode, dir, name)
	}
	spec := fmt.Sprintf("Name: fased-app\nVersion: %s\nRelease: 1\nSummary: Fased application\nLicense: LicenseRef-PolyForm-Perimeter-1.0.0\nBuildArch: %s\nAutoReqProv: no\nRequires: ca-certificates, systemd\n\n%%description\nFased application.\n\n%%install\ninstall -d \"%%{buildroot}/usr/bin\" \"%%{buildroot}/usr/lib/fased-app\" \"%%{buildroot}/usr/share/doc/fased-app\"\nln -s ../lib/fased-app/fased-app \"%%{buildroot}/usr/bin/fased\"\n%s\n%%files\n/usr/bin/fased\n%%dir /usr/lib/fased-app\n%%dir /usr/share/doc/fased-app\n%s", core.Version, arch, install.String(), listing.String())
	specPath := filepath.Join(top, "SPECS", "fased-app.spec")
	if err := os.WriteFile(specPath, []byte(spec), 0600); err != nil {
		return err
	}
	if err := runLinuxPackageTool(stage, tool, "--define", "_topdir "+top, "--define", "_tmppath "+filepath.Join(top, "TMP"), "--define", "_build_id_links none", "--define", "__os_install_post %{nil}", "-bb", specPath); err != nil {
		return err
	}
	path := filepath.Join(top, "RPMS", arch, fmt.Sprintf("fased-app-%s-1.%s.rpm", core.Version, arch))
	return os.Rename(path, target)
}

func runLinuxPackageTool(stage, tool string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Dir = stage
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + stage, "TMPDIR=" + stage, "LANG=C", "SOURCE_DATE_EPOCH=0"}
	output, err := cmd.CombinedOutput()
	if err != nil {
		if len(output) > 4096 {
			output = output[len(output)-4096:]
		}
		return fmt.Errorf("package builder failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
