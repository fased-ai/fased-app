package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/releaseops"
	"github.com/fased-ai/fased-app/internal/update"
)

type Options struct {
	Development   bool
	Seed          []byte
	SeedSHA256    string
	Directory     string                                                             // Absent child of a real private current-user directory.
	TLSCA         []byte                                                             // Explicit development fixture CA, never disabled TLS.
	now           func() time.Time                                                   // Test-only clock; no CLI or production override.
	githubFixture func(releaseops.GitHubPolicy) (*releaseops.GitHubTransport, error) // Go-only controlled fixture, never CLI/JSON.
}

type Report struct {
	Version          int           `json:"version"`
	DevelopmentOnly  bool          `json:"developmentOnly"`
	Purpose          string        `json:"purpose"`
	MetadataVerified bool          `json:"metadataVerified"`
	ArchiveVerified  bool          `json:"archiveVerified"`
	BundleVerified   bool          `json:"bundleVerified"`
	InstallerInvoked bool          `json:"installerInvoked"`
	Sequence         uint64        `json:"sequence,string"`
	Target           update.Target `json:"target"`
	SeedSHA256       string        `json:"seedSha256"`
	DescriptorSHA256 string        `json:"descriptorSha256"`
	ArchiveSHA256    string        `json:"archiveSha256"`
	BundleSHA256     string        `json:"bundleSha256"`
	BundleDirectory  string        `json:"bundleDirectory"`
	PublicActivation bool          `json:"publicActivation"`
	GitHubTransport  bool          `json:"githubTransport,omitzero"`
}

// Prepared is deliberately not a JSON-install permit. Run revalidates its
// original in-memory inputs, final expiry and every disk identity before exec.
type Prepared struct {
	mu            sync.Mutex
	options       Options
	report        Report
	seed          Seed
	descriptor    Descriptor
	engine        bundle.File
	directoryInfo os.FileInfo
	root          *os.Root
	metadata      map[string][]byte
	invoked       bool
	closed        bool
	startedAt     time.Time
}

func (p *Prepared) Report() Report { p.mu.Lock(); defer p.mu.Unlock(); return p.report }
func (p *Prepared) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	return p.root.Close()
}
func (o Options) clock() time.Time {
	if o.now != nil {
		return o.now().UTC()
	}
	return time.Now().UTC()
}

func writeNew(root *os.Root, name string, b []byte) error {
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	return errors.Join(err, f.Sync(), f.Close())
}

func fetch(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	r, err := update.DevelopmentAcquisitionResponse(ctx, client, url, limit)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	err = errors.Join(err, r.Body.Close())
	if err != nil || int64(len(b)) > limit {
		return nil, errors.New("incomplete or oversized bootstrap metadata")
	}
	return b, nil
}

func privateDirectory(path string) (os.FileInfo, error) {
	i, err := os.Lstat(path)
	if err != nil || !i.IsDir() || i.Mode().Perm() != 0700 || !owned(i) {
		return nil, errors.New("bootstrap requires a real private 0700 directory owned by this user")
	}
	return i, nil
}

// Prepare has no defaults for trust, endpoint or output. It neither reads app
// state nor installs/executes anything. Old update/rehearsal seeds are refused.
func Prepare(ctx context.Context, o Options) (prepared *Prepared, err error) {
	if !o.Development || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() == 0 {
		return nil, errors.New("explicit ordinary-user Linux amd64 development bootstrap required; public bootstrap is disabled")
	}
	o.Seed, o.TLSCA = append([]byte(nil), o.Seed...), append([]byte(nil), o.TLSCA...)
	seed, err := parseSeed(o.Seed, o.SeedSHA256, o.clock())
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(o.Directory) || filepath.Clean(o.Directory) != o.Directory {
		return nil, errors.New("absolute clean exclusive bootstrap output required")
	}
	parent, base := filepath.Dir(o.Directory), filepath.Base(o.Directory)
	before, err := privateDirectory(parent)
	if err != nil {
		return nil, err
	}
	parentRoot, err := os.OpenRoot(parent)
	if err != nil {
		return nil, err
	}
	defer parentRoot.Close()
	after, err := parentRoot.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		return nil, errors.New("bootstrap output parent changed")
	}
	if _, err = parentRoot.Lstat(base); !os.IsNotExist(err) {
		return nil, errors.New("bootstrap output exists; no overwrite or unchecked resume")
	}
	source, err := initialDownloads(o, seed)
	if err != nil {
		return nil, err
	}
	defer source.close()
	ctx, cancel := context.WithTimeout(ctx, source.window)
	defer cancel()
	start := o.clock()
	docs := map[string][]byte{}
	docs["timestamp.json"], err = source.metadata(ctx, "timestamp.json", update.MaxDocument)
	if err != nil {
		return nil, err
	}
	chain, err := initialChain(seed, docs["timestamp.json"], nil, nil, start)
	if err != nil {
		return nil, err
	}
	name, err := versioned(chain.Timestamp.Signed.Meta["snapshot.json"].Version, "snapshot")
	if err != nil {
		return nil, err
	}
	docs["snapshot.json"], err = source.metadata(ctx, name, update.MaxDocument)
	if err != nil {
		return nil, err
	}
	chain, err = initialChain(seed, docs["timestamp.json"], docs["snapshot.json"], nil, start)
	if err != nil {
		return nil, err
	}
	name, err = versioned(chain.Snapshot.Signed.Meta["targets.json"].Version, "targets")
	if err != nil {
		return nil, err
	}
	docs["targets.json"], err = source.metadata(ctx, name, update.MaxDocument)
	if err != nil {
		return nil, err
	}
	chain, err = initialChain(seed, docs["timestamp.json"], docs["snapshot.json"], docs["targets.json"], start)
	if err != nil {
		return nil, err
	}
	targets := chain.Targets["targets"].Signed.Targets
	descriptorPath := "targets/bootstrap/" + hex.EncodeToString(targets[descriptorTarget].Hashes["sha256"]) + ".descriptor.json"
	docs["descriptor.json"], err = source.metadata(ctx, descriptorPath, targets[descriptorTarget].Length)
	if err != nil {
		return nil, err
	}
	descriptor, err := verifyDescriptor(docs["descriptor.json"], seed, chain)
	if err != nil {
		return nil, err
	}
	if err = parentRoot.Mkdir(base, 0700); err != nil {
		return nil, err
	}
	created, err := parentRoot.Lstat(base)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err == nil {
			return
		}
		if prepared != nil {
			_ = prepared.Close()
		}
		current, e := parentRoot.Lstat(base)
		if e == nil && os.SameFile(created, current) {
			err = errors.Join(err, parentRoot.RemoveAll(base))
		} else {
			err = errors.Join(err, errors.New("bootstrap output identity changed; preserve for inspection"))
		}
		prepared = nil
	}()
	root, err := parentRoot.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	prepared = &Prepared{options: o, seed: seed, descriptor: descriptor, directoryInfo: created, root: root, metadata: docs, startedAt: start}
	body, err := source.archive(ctx, descriptor)
	if err != nil {
		return prepared, err
	}
	f, err := root.OpenFile("download.tar.gz", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		body.Close()
		return prepared, err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(body, descriptor.Archive.Size+1))
	err = errors.Join(copyErr, body.Close(), f.Sync(), f.Close())
	if err != nil || n != descriptor.Archive.Size || hex.EncodeToString(h.Sum(nil)) != descriptor.Archive.SHA256 {
		return prepared, errors.New("bootstrap archive incomplete or mismatched")
	}
	m, err := bundle.ExtractVerifiedArchive(filepath.Join(o.Directory, "download.tar.gz"), filepath.Join(o.Directory, "bundle"), bundle.File{SHA256: descriptor.Archive.SHA256, Bytes: descriptor.Archive.Size}, descriptor.BundleSHA256)
	if err != nil {
		return prepared, err
	}
	if m.Version != descriptor.ProductVersion || m.Source != descriptor.Source {
		return prepared, errors.New("inner bundle version/source differs from signed descriptor")
	}
	engine, _ := bundle.Names()
	prepared.engine = m.Files[engine]
	if err = prepared.revalidate(); err != nil {
		return prepared, err
	}
	if err = ctx.Err(); err != nil {
		return prepared, err
	}
	for name, b := range docs {
		if err = writeNew(root, name, b); err != nil {
			return prepared, err
		}
	}
	if err = writeNew(root, "seed.json", o.Seed); err != nil {
		return prepared, err
	}
	prepared.report = Report{Version: seed.Version, DevelopmentOnly: true, Purpose: seed.Purpose, MetadataVerified: true, ArchiveVerified: true, BundleVerified: true, Sequence: descriptor.Sequence, Target: descriptor.Target, SeedSHA256: o.SeedSHA256, DescriptorSHA256: digest(docs["descriptor.json"]), ArchiveSHA256: descriptor.Archive.SHA256, BundleSHA256: descriptor.BundleSHA256, BundleDirectory: filepath.Join(o.Directory, "bundle"), GitHubTransport: seed.Version == 2}
	b, err := json.Marshal(prepared.report)
	if err != nil {
		return prepared, err
	}
	if err = writeNew(root, "bootstrap.json", b); err != nil {
		return prepared, err
	}
	return prepared, nil
}

func (p *Prepared) revalidate() error {
	now := p.options.clock()
	if now.Before(p.startedAt) {
		return errors.New("bootstrap clock moved backwards")
	}
	info, err := privateDirectory(p.options.Directory)
	if err != nil || !os.SameFile(p.directoryInfo, info) {
		return errors.New("bootstrap directory identity changed")
	}
	seed, err := parseSeed(p.options.Seed, p.options.SeedSHA256, now)
	if err != nil {
		return err
	}
	chain, err := initialChain(seed, p.metadata["timestamp.json"], p.metadata["snapshot.json"], p.metadata["targets.json"], now)
	if err != nil {
		return err
	}
	d, err := verifyDescriptor(p.metadata["descriptor.json"], seed, chain)
	if err != nil || d != p.descriptor {
		return errors.New("bootstrap descriptor identity changed")
	}
	archive, err := bundle.Inspect(filepath.Join(p.options.Directory, "download.tar.gz"))
	if err != nil || archive.SHA256 != d.Archive.SHA256 || archive.Bytes != d.Archive.Size {
		return errors.New("bootstrap archive changed after verification")
	}
	m, err := bundle.VerifyPinned(filepath.Join(p.options.Directory, "bundle"), d.BundleSHA256)
	if err != nil {
		return err
	}
	if m.Version != d.ProductVersion || m.Source != d.Source {
		return fmt.Errorf("bootstrap source/version changed")
	}
	return nil
}
