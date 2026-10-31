package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
	"os"
	"path/filepath"
	"sync"
)

// RemoteApplicationController runs only in the existing administrator's manual
// owner process. It creates no noninteractive privilege or automatic actor.
type RemoteApplicationController struct {
	mu sync.Mutex
	p  *Prepared
	m  vpsinstall.Manifest
	h  vpsinstall.ApplicationUpdateCallbacks
	s  *custodyinstall.System
}

func (p *Prepared) RemoteApplicationController(m vpsinstall.Manifest, h vpsinstall.ApplicationUpdateCallbacks, s *custodyinstall.System) (*RemoteApplicationController, error) {
	if p == nil || os.Geteuid() == 0 || os.Getuid() != m.OwnerUID || h.BeforeStop == nil || h.Health == nil || h.RollbackAllowed == nil || h.Complete == nil {
		return nil, errors.New("existing ordinary administrator and protected callbacks required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.invoked {
		return nil, errors.New("fresh verified release required")
	}
	if e := p.revalidate(); e != nil {
		return nil, e
	}
	return &RemoteApplicationController{p: p, m: m, h: h, s: s}, nil
}
func (d *RemoteApplicationController) Check(context.Context) (*core.ApplicationRelease, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.p
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("verified release closed")
	}
	if e := p.revalidate(); e != nil {
		return nil, e
	}
	if prior, e := vpsinstall.ApplicationUpdateState(d.m.OwnerUID); e == nil {
		if !oneTerminalPhase(prior.Phase) || prior.Sequence > p.report.Sequence {
			return nil, errors.New("existing Remote transaction requires recovery or newer release")
		}
		if prior.Sequence == p.report.Sequence {
			if prior.Phase == "completed" && prior.ReleaseSHA256 == p.report.DescriptorSHA256 && prior.TargetManifestSHA256 == vpsinstall.ManifestSHA256(d.m) {
				got, err := bundle.Inspect(d.m.Core)
				if err != nil || got.SHA256 != d.m.CoreSHA256 {
					return nil, errors.New("Remote completed bytes differ from receipt")
				}
				return nil, nil
			}
			return nil, errors.New("consumed Remote release cannot become a new job")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	if d.m.ApplicationSequence >= p.report.Sequence {
		if d.m.ApplicationSequence == p.report.Sequence && d.m.ApplicationReleaseSHA256 == p.report.DescriptorSHA256 {
			got, e := bundle.Inspect(d.m.Core)
			if e == nil && got.SHA256 == d.m.CoreSHA256 {
				return nil, nil
			}
		}
		return nil, errors.New("release is below Remote installed floor or current bytes differ")
	}
	return &core.ApplicationRelease{Version: p.descriptor.ProductVersion, EngineSHA256: p.engine.SHA256, Sequence: p.report.Sequence, ManifestSHA256: p.report.DescriptorSHA256, Target: p.report.Target}, nil
}
func (d *RemoteApplicationController) Apply(ctx context.Context, o core.ApplicationUpdateOperation) (core.ApplicationUpdateReceipt, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.p
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || o.Release == nil || o.Release.EngineSHA256 != p.engine.SHA256 || o.Release.Sequence != p.report.Sequence || o.Release.ManifestSHA256 != p.report.DescriptorSHA256 || o.Release.Version != p.descriptor.ProductVersion || o.Release.Target != p.report.Target {
		return core.ApplicationUpdateReceipt{}, fmt.Errorf("%w: Remote release mismatch", core.ErrApplicationUpdateNotStarted)
	}
	if e := p.revalidate(); e != nil {
		return core.ApplicationUpdateReceipt{}, fmt.Errorf("%w: Remote admission expired", core.ErrApplicationUpdateNotStarted)
	}
	c := vpsinstall.ApplicationUpdate{ReaderBootstrap: d.h.AfterStop != nil, ID: o.ID, Version: p.descriptor.ProductVersion, Sequence: p.report.Sequence, ReleaseSHA256: p.report.DescriptorSHA256, ExpectedManifestSHA256: vpsinstall.ManifestSHA256(d.m), Engine: filepath.Join(p.report.BundleDirectory, "fased-app"), EngineSHA256: p.engine.SHA256}
	h := d.h
	if stage := h.BeforeStage; stage != nil {
		h.BeforeStage = func(ctx context.Context) error {
			if e := p.revalidate(); e != nil {
				return e
			}
			return stage(ctx)
		}
	}
	before := h.BeforeStop
	h.BeforeStop = func(ctx context.Context, a, b vpsinstall.Manifest) error {
		if e := p.revalidate(); e != nil {
			return e
		}
		return before(ctx, a, b)
	}
	if after := h.AfterStop; after != nil {
		h.AfterStop = func(ctx context.Context, a, b vpsinstall.Manifest) error {
			if e := p.revalidate(); e != nil {
				return e
			}
			return after(ctx, a, b)
		}
	}
	m, e := vpsinstall.ReplaceApplication(ctx, d.m, c, h, d.s)
	if e != nil {
		r, re := d.status(o)
		if re == nil && oneTerminalPhase(r.Phase) {
			return r, nil
		}
		return core.ApplicationUpdateReceipt{}, e
	}
	d.m = m
	return d.status(o)
}
func (d *RemoteApplicationController) Status(_ context.Context, o core.ApplicationUpdateOperation) (core.ApplicationUpdateReceipt, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status(o)
}
func (d *RemoteApplicationController) status(o core.ApplicationUpdateOperation) (core.ApplicationUpdateReceipt, error) {
	s, e := vpsinstall.ApplicationUpdateState(d.m.OwnerUID)
	if e != nil {
		return core.ApplicationUpdateReceipt{}, e
	}
	if o.Release == nil || s.ID != o.ID || s.Sequence != o.Release.Sequence || s.ReleaseSHA256 != o.Release.ManifestSHA256 {
		return core.ApplicationUpdateReceipt{}, errors.New("Remote receipt identity mismatch")
	}
	phase := s.Phase
	if phase == "prepared" || phase == "healthy-held" || phase == "rolled-back-held" {
		phase = "applying"
	}
	current, e := vpsinstall.Load(d.m.OwnerUID)
	if e != nil {
		return core.ApplicationUpdateReceipt{}, e
	}
	if phase == "completed" && vpsinstall.ManifestSHA256(current) != s.TargetManifestSHA256 || phase == "rolled-back" && vpsinstall.ManifestSHA256(current) != s.PreviousManifestSHA256 {
		return core.ApplicationUpdateReceipt{}, errors.New("Remote installed receipt does not match current generation")
	}
	if oneTerminalPhase(phase) {
		got, e := bundle.Inspect(current.Core)
		if e != nil || got.SHA256 != current.CoreSHA256 {
			return core.ApplicationUpdateReceipt{}, errors.New("Remote receipt executable changed")
		}
	}
	return core.ApplicationUpdateReceipt{ID: s.ID, Sequence: s.Sequence, ManifestSHA256: s.ReleaseSHA256, EngineSHA256: current.CoreSHA256, Phase: phase}, nil
}
