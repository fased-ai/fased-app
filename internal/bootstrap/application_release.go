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
