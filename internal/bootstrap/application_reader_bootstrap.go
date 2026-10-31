package bootstrap

import (
	"errors"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
)

type UpgradeOptions struct {
	Prefix, DataDirectory string
	Remote                bool
}

// ApplicationReaderBootstrapRequest binds the existing predecessor fence to
// independently authenticated target bytes. Construct before Apply; callbacks
// run under the Prepared driver lock and must not recursively acquire it.
func (p *Prepared) ApplicationReaderBootstrapRequest(id, lease, previousEngine string, predecessor update.CorePauseReceipt) (core.ApplicationReaderBootstrapRequest, error) {
	var out core.ApplicationReaderBootstrapRequest
	if p == nil || !update.IDOK(id) || !update.IDOK(lease) || !hashOK(previousEngine) {
		return out, errors.New("exact owner reader-bootstrap identity required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.invoked {
		return out, errors.New("fresh authenticated reader-bootstrap release required")
	}
	if e := p.revalidate(); e != nil {
		return out, e
	}
	if predecessor.Version != 1 || predecessor.State != "paused" || !update.IDOK(predecessor.PauseID) || predecessor.Request == nil || !update.ValidPauseRequest(*predecessor.Request) || predecessor.Request.IntentID != id || predecessor.Request.Sequence != p.report.Sequence || predecessor.Request.ManifestSHA256 != p.report.DescriptorSHA256 || predecessor.Core.State.Version != 33 || predecessor.Request.Expected.State.Version != 33 || !update.SameCoreMetadata(predecessor.Core, predecessor.Request.Expected) {
		return out, errors.New("schema33 predecessor fence differs from authenticated release")
	}
	return core.ApplicationReaderBootstrapRequest{Version: 1, OperationID: id, OwnerLease: lease, PreviousEngineSHA256: previousEngine, Release: core.ApplicationRelease{Version: p.descriptor.ProductVersion, EngineSHA256: p.engine.SHA256, Sequence: p.report.Sequence, ManifestSHA256: p.report.DescriptorSHA256, Target: p.report.Target}, Predecessor: predecessor}, nil
}
