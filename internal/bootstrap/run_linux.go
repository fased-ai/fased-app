//go:build linux

package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

func owned(i os.FileInfo) bool {
	s, ok := i.Sys().(*syscall.Stat_t)
	return ok && s.Uid == uint32(os.Geteuid())
}

type InstallOptions struct {
	WalletSigner, WalletDesktop   *WalletPayload
	WalletInstance, WalletNetwork string
	Prefix, DataDirectory         string
	Start                         bool
	Profile, Listen               string
	upgrade                       *UpgradeOptions
}

// ValidateFreshPackageCandidate binds the verified inputs to the exact
// instance. The protected receiver still independently verifies the package.
func (p *Prepared) ValidateFreshPackageCandidate(location, instance string) error {
	if p == nil || (location != "local" && location != "remote") || os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
		return errors.New("fresh package owner required")
	}
	root, err := update.PackageInstanceRoot(os.Getuid(), location, instance)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.validateFreshPackageCandidate(location, root)
}

func (p *Prepared) validateFreshPackageCandidate(location, root string) error {
	if p.closed || p.root == nil || p.invoked || (!p.report.LinuxPackageDigestsVerified && !p.report.LinuxBundleVerified) || !p.report.ProvenanceVerified ||
		p.seed.Version != 4 || p.options.Development || p.options.protectedOwner != 0 {
		return errors.New("the original fresh package admission is required")
	}
	// An unpublished candidate has its own explicit admission below. For a
	// public Remote release, use the same release/owner-derived instance as the
	// public installer; revalidateHeld still authenticates all held inputs.
	if location == "remote" && p.candidate == nil {
		identity := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00remote", os.Getuid(), p.report.DescriptorSHA256)))
		expected, err := update.PackageInstanceRoot(os.Getuid(), location, hex.EncodeToString(identity[:])[:32])
		if err != nil || root != expected {
			return errors.New("the original Remote package admission is required")
		}
	}
	if p.candidate != nil && (p.candidate.FreshProfile != "package-"+location || p.candidate.FreshOwner != os.Getuid() ||
		p.candidate.FreshRoot != root || p.candidate.FreshData != filepath.Join(root, "app-state") || p.candidate.validate(p.options.clock()) != nil) {
		return errors.New("the original fresh package admission is required")
	}
	return p.revalidateHeld()
}

// VerifyFreshPackageCandidateInstaller also requires the running installer to
// be the exact executable authenticated by the held candidate.
func (p *Prepared) VerifyFreshPackageCandidateInstaller(location, instance string) error {
	if p == nil || (location != "local" && location != "remote") || os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
		return errors.New("fresh package owner required")
	}
	root, err := update.PackageInstanceRoot(os.Getuid(), location, instance)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err = p.validateFreshPackageCandidate(location, root); err != nil {
		return err
	}
	if err = verifyRunningInstaller(p.descriptor.Release.InstallerSHA256); err != nil {
		return errors.New("running installer differs from the authenticated package")
	}
	return nil
}

// VerifyFreshCandidateInstaller refuses before protected staging unless this
// process is the exact installer authenticated by the candidate's proof.
func (p *Prepared) VerifyFreshCandidateInstaller(options InstallOptions) error {
	if p == nil {
		return errors.New("verified candidate required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.root == nil || p.candidate == nil || p.candidate.FreshProfile != "local" {
		return errors.New("fresh Local candidate required")
	}
	// Native candidates need the same complete held-input and installer checks,
	// but this read-only admission must not grant the legacy archive installer
	// authority to execute them. Run and the legacy updater still use revalidate.
	verify := p.revalidate
	if p.report.LinuxPackageDigestsVerified || p.report.LinuxBundleVerified {
		verify = p.revalidateHeld
	}
	if err := verify(); err != nil {
		return err
	}
	return p.checkFreshCandidate(options)
}

func (p *Prepared) checkFreshCandidate(options InstallOptions) error {
	if p.candidate.FreshProfile != "local" || options.Profile != "local" || options.upgrade != nil || options.WalletInstance != "" || options.WalletNetwork != "" {
		return errors.New("candidate installation requires its exact fresh Local admission")
	}
	if err := verifyRunningInstaller(p.descriptor.Release.InstallerSHA256); err != nil {
		return errors.New("running installer differs from the authenticated candidate")
	}
	paths, err := requireFreshCandidatePaths(options)
	if err != nil {
		return err
	}
	if os.Getuid() != p.candidate.FreshOwner || os.Geteuid() != p.candidate.FreshOwner || paths.Root != p.candidate.FreshRoot || paths.Data != p.candidate.FreshData {
		return errors.New("fresh candidate target differs from its owner admission")
	}
	return nil
}

// Run uses the already-reviewed ordinary installer/wizard. No arbitrary command,
// update receipt, owner session or financial permission can select the program.
func (p *Prepared) Run(ctx context.Context, options InstallOptions, in io.Reader, out, errOut io.Writer) (resultErr error) {
	if options.Profile != "" && options.Profile != "remote" && options.Profile != "local" && options.Profile != "connect" {
		return errors.New("invalid installation profile")
	}
	if options.Listen != "" {
		host, port, e := net.SplitHostPort(options.Listen)
		n, ne := strconv.Atoi(port)
		if e != nil || ne != nil || host != "127.0.0.1" || n < 1024 || n > 65535 || strconv.Itoa(n) != port {
			return errors.New("installation requires a fixed loopback listener")
		}
	}
	if p == nil {
		return errors.New("verified bootstrap required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.root == nil || p.closed || p.invoked || !p.report.BundleVerified || os.Geteuid() == 0 {
		return errors.New("fresh verified first-install bootstrap and explicit owner invocation required")
	}
	if err := p.revalidate(); err != nil {
		return err
	}
	if p.candidate != nil {
		if err := p.checkFreshCandidate(options); err != nil {
			return err
		}
	}
	engine, _ := bundle.Names()
	fd, err := syscall.Open(filepath.Join(p.report.BundleDirectory, engine), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), engine)
	defer f.Close()
	actual, err := f.Stat()
	pathInfo, e := os.Lstat(filepath.Join(p.report.BundleDirectory, engine))
	if err != nil || e != nil || !actual.Mode().IsRegular() || !os.SameFile(actual, pathInfo) {
		return errors.New("verified installer executable changed")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maxArchiveBytes+1))
	if err != nil || n != p.engine.Bytes || hex.EncodeToString(h.Sum(nil)) != p.engine.SHA256 {
		return errors.New("held installer executable differs from signed bundle")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	// Recheck all signed bytes after acquiring the exact executable descriptor.
	if err = p.revalidate(); err != nil {
		return err
	}
	args := []string{"install", "--bundle", p.report.BundleDirectory, "--bundle-sha256", p.report.BundleSHA256, "--interactive", "--verification-output", p.options.Directory, "--bootstrap-sequence", strconv.FormatUint(p.report.Sequence, 10), "--bootstrap-release-sha256", p.report.DescriptorSHA256}
	nativeBundle := ""
	if options.WalletDesktop != nil && options.WalletInstance == "" && options.WalletNetwork == "" && options.upgrade == nil && options.Profile != "remote" && options.Profile != "connect" {
		var nativePin string
		var cleanup func() error
		nativeBundle, nativePin, cleanup, err = p.composeNativeBundle(options.WalletDesktop, options.WalletSigner)
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, cleanup()) }()
		args = append(args, "--native-bundle", nativeBundle, "--native-bundle-sha256", nativePin)
	}
	if options.Profile != "" {
		args = append(args, "--profile", options.Profile)
	}
	if options.Listen != "" {
		args = append(args, "--listen", options.Listen)
	}
	if options.upgrade != nil {
		args, err = p.upgradeArguments(*options.upgrade)
		if err != nil {
			return err
		}
	}
	var walletSigner *os.File
	if options.WalletInstance != "" || options.WalletNetwork != "" {
		if !options.Start || options.Profile != "remote" || options.Prefix != "" || options.DataDirectory != "" || options.upgrade != nil {
			return errors.New("Wallets setup is a separate existing-Remote operation")
		}
		args, walletSigner, err = p.walletEnrollmentArguments(options.WalletInstance, options.WalletNetwork, options.WalletSigner)
		if err != nil {
			return err
		}
		defer walletSigner.Close()
	}
	if !options.Start {
		args = append(args, "--start=false")
	}
	// Profile-specific defaults belong to the wizard. Validate only explicit
	// owner overrides here; the restrictive output guard is handed to the same
	// verified installer, which checks actual selected Local/Remote paths before
	// any host preparation. Connect performs no installation/path mutation.
	for _, v := range []struct{ flag, value string }{{"--prefix", options.Prefix}, {"--data-dir", options.DataDirectory}} {
		if v.value == "" {
			continue
		}
		if !filepath.IsAbs(v.value) || filepath.Clean(v.value) != v.value {
			return errors.New("explicit installation paths must be absolute and clean")
		}
		for _, pair := range [][2]string{{p.options.Directory, v.value}, {v.value, p.options.Directory}} {
			rel, e := filepath.Rel(pair[0], pair[1])
			if e == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
				return errors.New("installation/state must be separate from the verified bootstrap output")
			}
		}
		args = append(args, v.flag, v.value)
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	current, err := os.Lstat(filepath.Join(p.report.BundleDirectory, engine))
	if err != nil || !os.SameFile(actual, current) || current.Size() != p.engine.Bytes {
		return errors.New("verified installer path changed before execution")
	}
	executable := f
	if options.upgrade != nil || walletSigner != nil || p.candidate != nil || nativeBundle != "" {
		executable, err = sealVerifiedExecutable(f, p.engine.Bytes, p.engine.SHA256)
		if err != nil {
			return err
		}
		defer executable.Close()
	}
	command := exec.CommandContext(ctx, "/proc/self/fd/3", args...)
	command.Args[0] = filepath.Join(p.report.BundleDirectory, engine)
	command.ExtraFiles = []*os.File{executable}
	if walletSigner != nil {
		command.ExtraFiles = append(command.ExtraFiles, walletSigner)
	}
	command.Stdin, command.Stdout, command.Stderr = in, out, errOut
	if options.Profile != "" {
		command.Stdout = io.Discard
	}
	p.invoked, p.report.InstallerInvoked = true, true
	if err = command.Run(); err != nil {
		return err
	}
	if walletSigner != nil && out != nil {
		fmt.Fprintln(out, "Wallets setup complete.")
	}
	return nil
}

// A fresh candidate must not adopt a prior installation or owner data. The
// existing installer also applies its normal path and identity checks.
func FreshCandidatePaths(options InstallOptions) (localinstall.Paths, error) {
	return requireFreshCandidatePaths(options)
}

func requireFreshCandidatePaths(options InstallOptions) (localinstall.Paths, error) {
	paths, err := localinstall.DefaultPaths()
	if err != nil {
		return paths, err
	}
	if options.Prefix != "" {
		paths.Root = options.Prefix
	}
	if options.DataDirectory != "" {
		paths.Data = options.DataDirectory
	}
	for _, path := range []string{paths.Root, paths.Data} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
			return paths, errors.New("fresh candidate requires clean installation and data paths")
		}
		for parent := filepath.Dir(path); parent != "/"; parent = filepath.Dir(parent) {
			info, e := os.Lstat(parent)
			if errors.Is(e, os.ErrNotExist) {
				continue
			}
			if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return paths, errors.New("fresh candidate target has a linked or invalid parent")
			}
		}
		info, e := os.Lstat(path)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !owned(info) || info.Mode().Perm()&0077 != 0 {
			return paths, errors.New("fresh candidate target is not an owned private directory")
		}
		entries, e := os.ReadDir(path)
		if e != nil || len(entries) != 0 {
			return paths, errors.New("fresh candidate target contains existing data; preserve it")
		}
	}
	for _, pair := range [][2]string{{paths.Root, paths.Data}, {paths.Data, paths.Root}} {
		rel, e := filepath.Rel(pair[0], pair[1])
		if e != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return paths, errors.New("fresh candidate installation and data must be separate")
		}
	}
	return paths, nil
}
