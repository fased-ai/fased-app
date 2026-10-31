package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
)

// LocalApplicationController is an OWNER maintenance-process adapter. It must
// not be run as root or used to escape an app's read-only sandbox. Ordinary app
// constructors never install it. Public source/channel activation remains held.
// Prepared admission and installation/recovery use the same verified bytes.
type LocalApplicationController struct {
	mu               sync.Mutex
	prepared         *Prepared
	paths            localinstall.Paths
	callbacks        localinstall.ApplicationUpdateCallbacks
	expectedManifest string
}

func (p *Prepared) LocalApplicationController(paths localinstall.Paths, h localinstall.ApplicationUpdateCallbacks) (*LocalApplicationController, error) {
	if h.BeforeStop == nil || h.Health == nil || h.RollbackAllowed == nil || h.Complete == nil {
		return nil, errors.New("complete protected update callbacks required")
	}
	if p == nil || os.Geteuid() == 0 {
		return nil, errors.New("verified release and ordinary owner maintenance process required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.invoked || p.root == nil {
		return nil, errors.New("fresh authenticated update release required")
	}
	if e := p.revalidate(); e != nil {
		return nil, e
	}
	m, e := localinstall.Load(paths)
	if e != nil {
		return nil, e
	}
	return &LocalApplicationController{prepared: p, paths: paths, callbacks: h, expectedManifest: applicationBaseline(paths, m, p.report.Sequence, p.report.DescriptorSHA256, p.engine.SHA256)}, nil
}
func (d *LocalApplicationController) Check(ctx context.Context) (*core.ApplicationRelease, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	p := d.prepared
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("verified release closed")
	}
	if e := p.revalidate(); e != nil {
		return nil, e
	}
	m, e := localinstall.Load(d.paths)
	if e != nil {
		return nil, e
	}
	if prior, err := localinstall.ApplicationUpdateState(d.paths); err == nil {
		if !oneTerminalPhase(prior.Phase) {
			return nil, errors.New("an existing installation transaction requires recovery")
		}
		if prior.Sequence > p.report.Sequence || (prior.Sequence == p.report.Sequence && prior.Phase == "rolled-back") {
			return nil, errors.New("release is below the consumed controller floor")
		}
		if prior.Sequence == p.report.Sequence {
			if prior.Phase != "completed" || prior.ReleaseSHA256 != p.report.DescriptorSHA256 || prior.TargetManifestSHA256 != localinstall.ManifestSHA256(m) {
				return nil, errors.New("consumed release has contradictory installed state; inspect recovery")
			}
			if e := verifyApplicationFiles(m); e != nil {
				return nil, e
			}
			return nil, nil
		}

	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if m.ApplicationSequence >= p.report.Sequence {
		if m.ApplicationSequence == p.report.Sequence && m.ApplicationReleaseSHA256 == p.report.DescriptorSHA256 {
			if e := verifyApplicationFiles(m); e != nil {
				return nil, e
			}
			return nil, nil
		}
		return nil, errors.New("release is below the installed sequence floor")
	}

	d.expectedManifest = localinstall.ManifestSHA256(m)
	return &core.ApplicationRelease{EngineSHA256: p.engine.SHA256, Version: p.descriptor.ProductVersion, Sequence: p.report.Sequence, ManifestSHA256: p.report.DescriptorSHA256, Target: p.report.Target}, nil
}
func (d *LocalApplicationController) Apply(ctx context.Context, o core.ApplicationUpdateOperation) (core.ApplicationUpdateReceipt, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.prepared
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || o.Release == nil || o.Release.EngineSHA256 != p.engine.SHA256 || o.Release.Sequence != p.report.Sequence || o.Release.ManifestSHA256 != p.report.DescriptorSHA256 || o.Release.Version != p.descriptor.ProductVersion || o.Release.Target != p.report.Target {
		return core.ApplicationUpdateReceipt{}, fmt.Errorf("%w: update differs from the authenticated release", core.ErrApplicationUpdateNotStarted)
	}
	if e := p.revalidate(); e != nil {
		return core.ApplicationUpdateReceipt{}, fmt.Errorf("%w: %v", core.ErrApplicationUpdateNotStarted, e)
	}
	engine, desktop := bundle.Names()
	candidate := localinstall.ApplicationUpdate{ReaderBootstrap: d.callbacks.AfterStop != nil, ID: o.ID, Version: p.descriptor.ProductVersion, Sequence: p.report.Sequence, ReleaseSHA256: p.report.DescriptorSHA256, ExpectedManifestSHA256: d.expectedManifest, Engine: filepath.Join(p.report.BundleDirectory, engine), EngineSHA256: p.engine.SHA256}
	m, e := localinstall.Load(d.paths)
	if e != nil {
		return core.ApplicationUpdateReceipt{}, e
	}
	if _, ok := m.Files[desktop]; ok {
		manifest, e := bundle.VerifyPinned(p.report.BundleDirectory, p.report.BundleSHA256)
		if e != nil {
			return core.ApplicationUpdateReceipt{}, e
		}
		for name, f := range manifest.Files {
			if name == desktop {
				candidate.Desktop = filepath.Join(p.report.BundleDirectory, desktop)
				candidate.DesktopSHA256 = f.SHA256
			}
		}
	}
	h := d.callbacks
	if stage := h.BeforeStage; stage != nil {
		h.BeforeStage = func(ctx context.Context) error {
			if e := p.revalidate(); e != nil {
				return e
			}
			return stage(ctx)
		}
	}
	before := h.BeforeStop
	h.BeforeStop = func(ctx context.Context, a, b localinstall.Manifest) error {
		if e := p.revalidate(); e != nil {
			return e
		}
		if before == nil {
			return errors.New("protected metadata checkpoint required")
		}
		return before(ctx, a, b)
	}
	if after := h.AfterStop; after != nil {
		h.AfterStop = func(ctx context.Context, a, b localinstall.Manifest) error {
			if e := p.revalidate(); e != nil {
				return e
			}
			return after(ctx, a, b)
		}
	}
	if _, e = localinstall.ReplaceApplication(ctx, d.paths, candidate, h); e != nil {
		// Health failure can have a proven compatible rollback. Preserve that exact
		// terminal receipt instead of inventing an unknown installation outcome.
		receipt, statusErr := d.status(o)
		if statusErr == nil && (receipt.Phase == "rolled-back" || receipt.Phase == "completed") {
			return receipt, nil
		}
		return core.ApplicationUpdateReceipt{}, e
	}
	return d.status(o)
}
func (d *LocalApplicationController) Status(_ context.Context, o core.ApplicationUpdateOperation) (core.ApplicationUpdateReceipt, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status(o)
}
func (d *LocalApplicationController) status(o core.ApplicationUpdateOperation) (core.ApplicationUpdateReceipt, error) {
	s, e := localinstall.ApplicationUpdateState(d.paths)
	if e != nil {
		return core.ApplicationUpdateReceipt{}, e
	}
	if o.Release == nil || s.ID != o.ID || s.Sequence != o.Release.Sequence || s.ReleaseSHA256 != o.Release.ManifestSHA256 {
		return core.ApplicationUpdateReceipt{}, errors.New("controller receipt differs from update")
	}
	m, e := localinstall.Load(d.paths)
	if e != nil {
		return core.ApplicationUpdateReceipt{}, e
	}
	phase := s.Phase
	if phase == "completed" && localinstall.ManifestSHA256(m) != s.TargetManifestSHA256 {
		return core.ApplicationUpdateReceipt{}, errors.New("installed manifest differs from completed update")
	}
	if phase == "rolled-back" && localinstall.ManifestSHA256(m) != s.PreviousManifestSHA256 {
		return core.ApplicationUpdateReceipt{}, errors.New("rollback manifest differs from controller receipt")
	}
	if phase == "completed" || phase == "rolled-back" {
		for name, want := range m.Files {
			if filepath.Base(name) != name || name == "." || name == ".." {
				return core.ApplicationUpdateReceipt{}, errors.New("invalid installed inventory")
			}
			got, e := bundle.Inspect(filepath.Join(m.Root, name))
			if e != nil || got.SHA256 != want {
				return core.ApplicationUpdateReceipt{}, errors.New("installed files differ from durable controller receipt")
			}
		}
	}
	if phase == "prepared" || phase == "healthy-held" || phase == "rolled-back-held" {
		phase = "applying"
	}
	return core.ApplicationUpdateReceipt{EngineSHA256: m.Files["fased-app"], ID: s.ID, ManifestSHA256: s.ReleaseSHA256, Sequence: s.Sequence, Phase: phase}, nil
}

func applicationBaseline(paths localinstall.Paths, m localinstall.Manifest, sequence uint64, release, engine string) string {
	if s, e := localinstall.ApplicationUpdateState(paths); e == nil && s.Sequence == sequence && s.ReleaseSHA256 == release && s.EngineSHA256 == engine && !oneTerminalPhase(s.Phase) {
		return s.PreviousManifestSHA256
	}
	return localinstall.ManifestSHA256(m)
}
func oneTerminalPhase(p string) bool { return p == "completed" || p == "rolled-back" }
func verifyApplicationFiles(m localinstall.Manifest) error {
	for name, want := range m.Files {
		if filepath.Base(name) != name || name == "." || name == ".." {
			return errors.New("invalid installed inventory")
		}
		got, e := bundle.Inspect(filepath.Join(m.Root, name))
		if e != nil || got.SHA256 != want {
			return errors.New("installed bytes differ from authenticated controller inventory")
		}
	}
	return nil
}
