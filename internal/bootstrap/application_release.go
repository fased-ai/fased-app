package bootstrap

import (
	"errors"
	"github.com/fased-ai/fased-app/internal/core"
)

// ApplicationRelease returns only an independently verified, current artifact
// identity. Metadata-only inspection can never construct an executable release.
func (p *Prepared) ApplicationRelease() (*core.ApplicationRelease, error) {
	if p == nil {
		return nil, errors.New("verified application required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.root == nil || p.closed || !p.report.BundleVerified {
		return nil, errors.New("verified bundle required")
	}
	if e := p.revalidate(); e != nil {
		return nil, e
	}
	return &core.ApplicationRelease{EngineSHA256: p.engine.SHA256, Version: p.descriptor.ProductVersion, Sequence: p.descriptor.Sequence, ManifestSHA256: p.report.DescriptorSHA256, Target: p.descriptor.Target}, nil
}

// BindApplicationRelease preserves an already approved legacy-v2 identity only
// when the independently authenticated full descriptor proves that exact alias.
// This keeps old pending leases/recovery intact without changing their records.
func (p *Prepared) BindApplicationRelease(expected core.ApplicationRelease) error {
	if p == nil {
		return errors.New("verified application required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.invoked || p.root == nil || !p.report.BundleVerified {
		return errors.New("fresh verified application required")
	}
	if err := p.revalidate(); err != nil {
		return err
	}
	if expected.EngineSHA256 != p.engine.SHA256 || expected.Version != p.descriptor.ProductVersion || expected.Sequence != p.descriptor.Sequence || expected.Target != p.descriptor.Target || !p.matchesApplicationIdentity(expected.ManifestSHA256) {
		return errors.New("independent release differs from approved content")
	}
	p.report.DescriptorSHA256 = expected.ManifestSHA256
	return nil
}
