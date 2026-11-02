package core

import (
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
)

// ApplicationReaderBootstrapRequest is constructed by the independently
// authenticated owner release driver, never a browser or source/path override.
// Its predecessor receipt comes from the original core's protected pause API.
type ApplicationReaderBootstrapRequest struct {
	Version              int                     `json:"version"`
	OperationID          string                  `json:"operationId"`
	OwnerLease           string                  `json:"ownerLease"`
	PreviousEngineSHA256 string                  `json:"previousEngineSha256"`
	Release              ApplicationRelease      `json:"release"`
	Predecessor          update.CorePauseReceipt `json:"predecessor"`
}

type ApplicationReaderBootstrapReceipt struct {
	Version       int                     `json:"version"`
	RequestSHA256 string                  `json:"requestSha256"`
	Original      update.MetadataSnapshot `json:"original"`
	Checkpoint    ApplicationCheckpoint   `json:"checkpoint"`
	Phase         string                  `json:"phase"`
}

func (r ApplicationReaderBootstrapRequest) valid() bool {
	p := r.Predecessor
	return r.validIntent() && p.State == "paused" && update.IDOK(p.PauseID)
}

// A distinct request version prevents older readers from adopting new handoffs.
func ApplicationReaderRequestVersion(schema int) (int, error) {
	switch schema {
	case 33:
		return 1, nil
	case 35:
		return 2, nil
	case 36:
		return 3, nil
	default:
		return 0, errors.New("unsupported installed reader schema")
	}
}

func (r ApplicationReaderBootstrapRequest) validIntent() bool {
	p := r.Predecessor
	source := p.Core.State.Version
	version, err := ApplicationReaderRequestVersion(source)
	return err == nil && r.Version == version && update.IDOK(r.OperationID) && update.IDOK(r.OwnerLease) && hexValue(r.PreviousEngineSHA256, 32) && r.Release.valid() && p.Version == 1 && p.Request != nil && update.ValidPauseRequest(*p.Request) && p.Request.IntentID == r.OperationID && p.Request.Sequence == r.Release.Sequence && p.Request.ManifestSHA256 == r.Release.ManifestSHA256 && p.Request.Expected.State.Version == source && update.SameCoreMetadata(p.Core, p.Request.Expected)
}

func (r ApplicationReaderBootstrapRequest) prepared() bool {
	return r.Version >= 2 && r.validIntent() && r.Predecessor.State == "prepared" && r.Predecessor.PauseID == ""
}
