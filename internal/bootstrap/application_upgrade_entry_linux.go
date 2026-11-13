//go:build linux

package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
	"golang.org/x/sys/unix"
)

// sealVerifiedExecutable binds execution to the verified bytes, including when
// the owner-controlled extracted file changes after its descriptor was opened.
func sealVerifiedExecutable(source io.ReadSeeker, size int64, expected string) (*os.File, error) {
	if source == nil || size <= 0 || size > maxArchiveBytes {
		return nil, errors.New("bounded verified upgrade executable required")
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	fd, err := unix.MemfdCreate("fased-verified-executable", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING|unix.MFD_EXEC)
	if errors.Is(err, unix.EINVAL) {
		// Older kernels make memfds executable without MFD_EXEC.
		fd, err = unix.MemfdCreate("fased-verified-executable", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	}
	if err != nil {
		return nil, err
	}
	sealed := os.NewFile(uintptr(fd), "fased-verified-executable")
	keep := false
	defer func() {
		if !keep {
			sealed.Close()
		}
	}()
	hash := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(sealed, hash), io.LimitReader(source, size+1), make([]byte, 32*1024))
	if err != nil {
		return nil, err
	}
	if n != size || hex.EncodeToString(hash.Sum(nil)) != expected {
		return nil, errors.New("upgrade executable differs from the verified release")
	}
	if err = sealed.Chmod(0500); err != nil {
		return nil, err
	}
	if _, err = unix.FcntlInt(sealed.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL); err != nil {
		return nil, err
	}
	if _, err = sealed.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	keep = true
	return sealed, nil
}

// RunUpgrade reuses Run's pinned held-FD verification/one-invocation gate. It
// calls only the existing owner-maintenance entry, never install-over-existing
// or an artifact/source/credential override. The target independently admits its
// release through its reviewed compiled binding, which remains nil ordinarily.
func (p *Prepared) RunUpgrade(ctx context.Context, o UpgradeOptions, in io.Reader, out, errOut io.Writer) error {
	if m, err := custodyinstall.Load(os.Getuid()); err == nil {
		if o.Prefix != "" || o.DataDirectory != "" || o.Remote != (m.Location == "remote") {
			return errors.New("custody upgrade must retain the exact installed profile and paths")
		}
		r, err := p.ApplicationRelease()
		if err != nil {
			return err
		}
		p.mu.Lock()
		if p.invoked {
			p.mu.Unlock()
			return errors.New("installer already invoked")
		}
		p.invoked = true
		p.report.InstallerInvoked = true
		p.mu.Unlock()
		_, err = maintenance.Call(ctx, maintenance.Request{Version: 1, Action: "custody-update", Custody: &maintenance.CustodyUpdateRequest{OperationID: update.NewProbe(), Sequence: r.Sequence, ReleaseSHA256: r.ManifestSHA256, Bootstrap: true}}, errOut)
		if err == nil {
			_, err = fmt.Fprintln(out, "Fased updated.")
		}
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return p.Run(ctx, InstallOptions{Start: true, upgrade: &o}, in, out, errOut)
}

func (p *Prepared) upgradeArguments(o UpgradeOptions) ([]string, error) {
	args := []string{"maintain-update", "--operation", update.NewProbe()}
	if o.Remote {
		if o.Prefix != "" || o.DataDirectory != "" {
			return nil, errors.New("managed Remote upgrade uses its existing fixed identity")
		}
		m, e := vpsinstall.Load(os.Getuid())
		if e != nil {
			return nil, e
		}
		if e = p.upgradePathBoundary(m.Root, m.Data); e != nil {
			return nil, e
		}
		return append(args, "--remote"), nil
	}
	paths, e := localinstall.DefaultPaths()
	if e != nil {
		return nil, e
	}
	if o.Prefix != "" {
		paths.Root = o.Prefix
	}
	if o.DataDirectory != "" {
		paths.Data = o.DataDirectory
	}
	if e = p.upgradePathBoundary(paths.Root, paths.Data); e != nil {
		return nil, e
	}
	m, e := localinstall.Load(paths)
	if e != nil {
		return nil, e
	}
	if m.Phase != "ready" {
		return nil, errors.New("existing ready Local installation required")
	}
	return append(args, "--prefix", m.Root, "--data-dir", m.Data), nil
}

func (p *Prepared) upgradePathBoundary(paths ...string) error {
	for _, value := range paths {
		if !filepath.IsAbs(value) || filepath.Clean(value) != value {
			return errors.New("existing upgrade paths must be absolute and clean")
		}
		for _, pair := range [][2]string{{p.options.Directory, value}, {value, p.options.Directory}} {
			rel, e := filepath.Rel(pair[0], pair[1])
			if e == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
				return errors.New("installed state must be separate from verified bootstrap output")
			}
		}
	}
	return nil
}
