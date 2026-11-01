//go:build linux

package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/fased-ai/fased-app/internal/bundle"
)

func owned(i os.FileInfo) bool {
	s, ok := i.Sys().(*syscall.Stat_t)
	return ok && s.Uid == uint32(os.Geteuid())
}

type InstallOptions struct {
	Prefix, DataDirectory string
	Start                 bool
	Profile, Listen       string
	upgrade               *UpgradeOptions
}

// Run uses the already-reviewed ordinary installer/wizard. No arbitrary command,
// update receipt, owner session or financial permission can select the program.
func (p *Prepared) Run(ctx context.Context, options InstallOptions, in io.Reader, out, errOut io.Writer) error {
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
	command := exec.CommandContext(ctx, "/proc/self/fd/3", args...)
	command.Args[0] = filepath.Join(p.report.BundleDirectory, engine)
	command.ExtraFiles = []*os.File{f}
	command.Stdin, command.Stdout, command.Stderr = in, out, errOut
	if options.Profile != "" {
		command.Stdout = io.Discard
	}
	p.invoked, p.report.InstallerInvoked = true, true
	return command.Run()
}
