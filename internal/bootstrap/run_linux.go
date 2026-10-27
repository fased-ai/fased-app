//go:build linux

package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/localinstall"
)

func owned(i os.FileInfo) bool {
	s, ok := i.Sys().(*syscall.Stat_t)
	return ok && s.Uid == uint32(os.Geteuid())
}

type InstallOptions struct {
	Prefix, DataDirectory string
	Start                 bool
}

// Run uses the already-reviewed ordinary installer/wizard. No arbitrary command,
// update receipt, owner session or financial permission can select the program.
func (p *Prepared) Run(ctx context.Context, options InstallOptions, in io.Reader, out, errOut io.Writer) error {
	if p == nil {
		return errors.New("verified bootstrap required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.root == nil || p.closed || p.invoked || !p.report.BundleVerified || os.Geteuid() == 0 {
		return errors.New("fresh verified development bootstrap and explicit owner invocation required")
	}
	defaults, err := localinstall.DefaultPaths()
	if err != nil {
		return err
	}
	if options.Prefix == "" {
		options.Prefix = defaults.Root
	}
	if options.DataDirectory == "" {
		options.DataDirectory = defaults.Data
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
	args := []string{"install", "--bundle", p.report.BundleDirectory, "--bundle-sha256", p.report.BundleSHA256, "--interactive"}
	if !options.Start {
		args = append(args, "--start=false")
	}
	for _, v := range []struct{ flag, value string }{{"--prefix", options.Prefix}, {"--data-dir", options.DataDirectory}} {
		if v.value != "" {
			if !filepath.IsAbs(v.value) || filepath.Clean(v.value) != v.value {
				return errors.New("explicit installation paths must be absolute and clean")
			}
			for _, pair := range [][2]string{{p.options.Directory, v.value}, {v.value, p.options.Directory}} {
				rel, err := filepath.Rel(pair[0], pair[1])
				if err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
					return errors.New("installation/state must be separate from the verified bootstrap output")
				}
			}
			args = append(args, v.flag, v.value)
		}
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
	p.invoked, p.report.InstallerInvoked = true, true
	return command.Run()
}
