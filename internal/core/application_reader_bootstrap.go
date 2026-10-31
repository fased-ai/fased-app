package core

import "github.com/fased-ai/fased-app/internal/update"

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
	return r.Version == 1 && update.IDOK(r.OperationID) && update.IDOK(r.OwnerLease) && hexValue(r.PreviousEngineSHA256, 32) && r.Release.valid() && p.Version == 1 && p.State == "paused" && update.IDOK(p.PauseID) && p.Request != nil && update.ValidPauseRequest(*p.Request) && p.Request.IntentID == r.OperationID && p.Request.Sequence == r.Release.Sequence && p.Request.ManifestSHA256 == r.Release.ManifestSHA256 && p.Core.State.Version == 33 && p.Request.Expected.State.Version == 33 && update.SameCoreMetadata(p.Core, p.Request.Expected)
}
