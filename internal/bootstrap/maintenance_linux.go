//go:build linux

package bootstrap

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/releaseops"
	"github.com/fased-ai/fased-app/internal/update"
	"path/filepath"
)

type MaintenanceIdentity struct {
	Version         string
	Source          string
	InstallerSHA256 string
	Core            bundle.File
}

func (p *Prepared) MaintenanceIdentity() (MaintenanceIdentity, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.root == nil || p.options.protectedOwner <= 0 {
		return MaintenanceIdentity{}, errors.New("protected verified acquisition required")
	}
	if err := p.revalidate(); err != nil {
		return MaintenanceIdentity{}, err
	}
	return MaintenanceIdentity{Version: p.descriptor.ProductVersion, Source: p.descriptor.Source, InstallerSHA256: p.descriptor.Release.InstallerSHA256, Core: p.engine}, nil
}

// AcquireMaintenanceInstaller downloads the exact already-attested installer
// asset. It never consults latest again or accepts a caller-supplied location.
func (p *Prepared) AcquireMaintenanceInstaller(ctx context.Context) (string, update.Artifact, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var artifact update.Artifact
	if p.closed || p.root == nil || p.options.protectedOwner <= 0 || p.seed.Version != 4 {
		return "", artifact, errors.New("attested exact installer acquisition required")
	}
	if err := p.revalidate(); err != nil {
		return "", artifact, err
	}
	var published releaseops.PublishedRelease
	if update.StrictJSON(p.metadata["github-release.json"], &published) != nil {
		return "", artifact, errors.New("retained immutable release inventory required")
	}
	name := "fased-install-linux-" + p.descriptor.Target.Arch
	a, ok := published.Assets[name]
	if !ok || a.SHA256 != p.descriptor.Release.InstallerSHA256 || a.Size <= 0 || a.Size > 128<<20 {
		return "", artifact, errors.New("attested installer subject differs")
	}
	var g *releaseops.GitHubTransport
	var err error
	if p.options.githubFixture != nil {
		g, err = p.options.githubFixture(p.seed.GitHub)
	} else {
		g, err = releaseops.NewGitHubTransport(p.seed.GitHub)
	}
	if err != nil {
		return "", artifact, err
	}
	defer g.Close()
	b, err := g.PublishedAsset(ctx, a)
	if err != nil {
		return "", artifact, err
	}
	if int64(len(b)) != a.Size || digest(b) != a.SHA256 {
		return "", artifact, errors.New("exact installer bytes differ")
	}
	if err = writeNew(p.root, "maintenance-installer", b); err != nil {
		return "", artifact, err
	}
	f, err := p.root.OpenFile("maintenance-installer", 0, 0)
	if err != nil {
		return "", artifact, err
	}
	err = f.Chmod(0555)
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		return "", artifact, err
	}
	artifact = update.Artifact{File: name, Size: a.Size, SHA256: a.SHA256}
	return filepath.Join(p.options.Directory, "maintenance-installer"), artifact, nil
}
