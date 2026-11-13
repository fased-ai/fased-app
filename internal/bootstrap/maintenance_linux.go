//go:build linux

package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/releaseops"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// DiscardMaintenance removes only this invocation's exclusive, still-matching
// acquisition. It is used before any verified acquisition receipt is saved;
// an interrupted or foreign directory is never adopted for cleanup.
func (p *Prepared) DiscardMaintenance() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.root == nil || p.options.protectedOwner <= 0 || p.directoryInfo == nil {
		return errors.New("owned protected acquisition required")
	}
	parent, err := os.OpenRoot(filepath.Dir(p.options.Directory))
	if err != nil {
		return err
	}
	defer parent.Close()
	current, err := parent.Lstat(filepath.Base(p.options.Directory))
	if err != nil || !os.SameFile(p.directoryInfo, current) {
		return errors.New("acquisition directory changed; preserve it")
	}
	held, err := p.root.Stat(".")
	if err != nil || !os.SameFile(current, held) {
		return errors.New("acquisition handle changed; preserve it")
	}
	p.closed = true
	if err = p.root.Close(); err != nil {
		return err
	}
	p.root = nil
	return parent.RemoveAll(filepath.Base(p.options.Directory))
}

type MaintenanceIdentity struct {
	Version         string
	Source          string
	InstallerSHA256 string
	Core            bundle.File
}

// MatchesMaintenanceRelease binds enrollment to the source installation's
// recorded release identity, including its supported legacy serialization.
func (p *Prepared) MatchesMaintenanceRelease(sequence uint64, identity string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.closed && p.root != nil && p.options.protectedOwner > 0 && sequence != 0 && p.report.Sequence == sequence && p.matchesApplicationIdentity(identity)
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
	installedPath := filepath.Join(p.options.Directory, "maintenance-installer")
	if info, e := p.root.Lstat("maintenance-installer"); e == nil {
		st, valid := info.Sys().(*syscall.Stat_t)
		if !valid || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || !info.Mode().IsRegular() || (info.Mode().Perm() != 0600 && info.Mode().Perm() != 0555) || info.Size() < 0 || info.Size() > a.Size {
			return "", artifact, errors.New("foreign retained installer; preserve it")
		}
		got, inspectErr := bundle.Inspect(installedPath)
		if inspectErr == nil && got.SHA256 == a.SHA256 && got.Bytes == a.Size {
			f, e := p.root.Open("maintenance-installer")
			if e != nil {
				return "", artifact, e
			}
			e = errors.Join(f.Chmod(0555), f.Sync(), f.Close())
			if e != nil {
				return "", artifact, e
			}
			return installedPath, update.Artifact{File: name, Size: a.Size, SHA256: a.SHA256}, nil
		}
		if info.Mode().Perm() != 0600 {
			return "", artifact, errors.New("retained installer differs; preserve it")
		}
		// The fixed 0600 file belongs to this exclusive root-private
		// acquisition. It was interrupted before verification completed.
		if e := p.root.Remove("maintenance-installer"); e != nil {
			return "", artifact, e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", artifact, e
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
	return installedPath, artifact, nil
}

// ResumeMaintenance re-verifies the exact completed acquisition left by this
// operation. It never follows the current release or accepts a caller path.
func ResumeMaintenance(ctx context.Context, owner int, o Options) (*Prepared, error) {
	if _, err := maintenance.ValidateProcess(owner); err != nil {
		return nil, err
	}
	parent := filepath.Join(maintenance.OwnerRoot(owner), "fased-app", "install")
	if filepath.Dir(o.Directory) != parent {
		return nil, errors.New("protected acquisition directory required")
	}
	o.protectedOwner = owner
	return resumeMaintenance(ctx, o)
}

func resumeMaintenance(ctx context.Context, o Options) (_ *Prepared, resultErr error) {
	if o.protectedOwner <= 0 || !filepath.IsAbs(o.Directory) || filepath.Clean(o.Directory) != o.Directory {
		return nil, errors.New("protected acquisition identity required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o.Seed, o.TLSCA = append([]byte(nil), o.Seed...), append([]byte(nil), o.TLSCA...)
	seed, err := parseSeed(o.Seed, o.SeedSHA256, o.clock())
	if err != nil || seed.DevelopmentOnly != o.Development {
		return nil, errors.New("protected acquisition trust differs")
	}
	info, err := privateDirectory(o.Directory)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(o.Directory)
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			root.Close()
		}
	}()
	held, err := root.Stat(".")
	if err != nil || !os.SameFile(info, held) {
		return nil, errors.New("acquisition identity changed; preserve it")
	}
	read := func(name string, limit int64) ([]byte, error) {
		entry, err := root.Lstat(name)
		if err != nil || !entry.Mode().IsRegular() || entry.Mode().Perm() != 0600 || entry.Size() < 0 || entry.Size() > limit {
			return nil, errors.New("original acquisition input unavailable")
		}
		f, err := root.Open(name)
		if err != nil {
			return nil, err
		}
		actual, err := f.Stat()
		if err != nil || !os.SameFile(entry, actual) {
			f.Close()
			return nil, errors.New("original acquisition input changed")
		}
		b, err := io.ReadAll(io.LimitReader(f, limit+1))
		err = errors.Join(err, f.Close())
		if err != nil || int64(len(b)) != entry.Size() {
			return nil, errors.New("original acquisition input incomplete")
		}
		return b, nil
	}
	storedSeed, err := read("seed.json", update.MaxDocument)
	if err != nil || !bytes.Equal(storedSeed, o.Seed) {
		return nil, errors.New("original acquisition seed differs")
	}
	names := []string{"descriptor.json"}
	if seed.Version == 4 {
		names = append(names, AttestedPacketFile, "github-release.json", "provenance.sigstore.json")
	} else {
		names = append(names, "timestamp.json", "snapshot.json", "targets.json")
		if seed.Version == 3 {
			names = append(names, "provenance.sigstore.json")
		}
	}
	docs := make(map[string][]byte, len(names))
	for _, name := range names {
		if docs[name], err = read(name, update.MaxDocument); err != nil {
			return nil, err
		}
	}
	var descriptor Descriptor
	if update.StrictJSON(docs["descriptor.json"], &descriptor) != nil {
		return nil, errors.New("original acquisition descriptor invalid")
	}
	raw, err := read("bootstrap.json", update.MaxDocument)
	if err != nil {
		return nil, err
	}
	var report Report
	if update.StrictJSON(raw, &report) != nil {
		return nil, errors.New("original acquisition report invalid")
	}
	p := &Prepared{options: o, seed: seed, descriptor: descriptor, directoryInfo: info, root: root, metadata: docs, startedAt: o.clock()}
	if err = p.revalidate(); err != nil {
		return nil, err
	}
	m, err := bundle.VerifyPinned(filepath.Join(o.Directory, "bundle"), descriptor.BundleSHA256)
	if err != nil {
		return nil, err
	}
	engine, _ := bundle.Names()
	p.engine = m.Files[engine]
	decision, err := productionProof(seed, descriptor, docs["provenance.sigstore.json"], o.clock())
	if err != nil {
		return nil, err
	}
	want := Report{Version: seed.Version, DevelopmentOnly: seed.DevelopmentOnly, Purpose: seed.Purpose, MetadataVerified: true, ArchiveVerified: true, BundleVerified: true, Sequence: descriptor.Sequence, Target: descriptor.Target, SeedSHA256: o.SeedSHA256, DescriptorSHA256: applicationIdentity(descriptor, digest(docs["descriptor.json"])), ArchiveSHA256: descriptor.Archive.SHA256, BundleSHA256: descriptor.BundleSHA256, BundleDirectory: filepath.Join(o.Directory, "bundle"), GitHubTransport: seed.Version >= 2, ProvenanceVerified: seed.Version >= 3, ProvenanceSHA256: decision.BundleSHA256, TrustPolicySHA256: decision.PolicySHA256}
	if report != want {
		return nil, errors.New("original acquisition report differs")
	}
	p.report = report
	return p, nil
}

// DiscardIncompleteMaintenance removes only the exact reserved private
// acquisition when it never published bootstrap.json. It has not produced a
// verified receipt and contains no owner state or signer keys.
func DiscardIncompleteMaintenance(owner int, o Options) error {
	if _, err := maintenance.ValidateProcess(owner); err != nil {
		return err
	}
	parentPath := filepath.Join(maintenance.OwnerRoot(owner), "fased-app", "install")
	if filepath.Dir(o.Directory) != parentPath {
		return errors.New("protected acquisition directory required")
	}
	return discardIncompleteMaintenance(parentPath, o.Directory)
}

func discardIncompleteMaintenance(parentPath, directory string) error {
	info, err := privateDirectory(directory)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	held, err := root.Stat(".")
	if err != nil || !os.SameFile(info, held) {
		root.Close()
		return errors.New("acquisition identity changed; preserve it")
	}
	if _, err = root.Lstat("bootstrap.json"); !errors.Is(err, os.ErrNotExist) {
		root.Close()
		return errors.New("completed or unclassified acquisition must be preserved")
	}
	if err = root.Close(); err != nil {
		return err
	}
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return err
	}
	defer parent.Close()
	current, err := parent.Lstat(filepath.Base(directory))
	if err != nil || !os.SameFile(info, current) {
		return errors.New("acquisition identity changed; preserve it")
	}
	if err = parent.RemoveAll(filepath.Base(directory)); err != nil {
		return err
	}
	d, err := parent.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
