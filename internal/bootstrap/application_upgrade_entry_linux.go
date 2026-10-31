//go:build linux

package bootstrap

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

// RunUpgrade reuses Run's pinned held-FD verification/one-invocation gate. It
// calls only the existing owner-maintenance entry, never install-over-existing
// or an artifact/source/credential override. The target independently admits its
// release through its reviewed compiled binding, which remains nil ordinarily.
func (p *Prepared) RunUpgrade(ctx context.Context, o UpgradeOptions, in io.Reader, out, errOut io.Writer) error {
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
