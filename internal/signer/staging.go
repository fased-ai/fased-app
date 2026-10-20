package signer

import (
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
)

func (v *Vault) stageIntent(r update.StageRequest) (update.Intent, error) {
	if v.closed || v.store.failedState() || v.ledger == nil || v.ledger.missingHistory {
		return update.Intent{}, ErrUnavailable
	}
	i, ok := v.ledger.updates[r.IntentID]
	if !ok || v.ledger.activeUpdate != i.ID || i.Sequence != v.ledger.updateFloor || r.Sequence != i.Sequence || r.ManifestSHA256 != i.ManifestSHA256 || !update.Stageable(i) || (i.State != "prepared" && i.State != "staged") {
		return update.Intent{}, errors.New("stale or inactive staging intent, digest or floor")
	}
	return i, nil
}

// StageUpdate is owner-only. Chunks are inert private data, never executables.
// A receipt is journaled only after full rehash and fresh service bindings.
func (v *Vault) StageUpdate(action string, r update.StageRequest) (*update.StageStatus, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	i, e := v.stageIntent(r)
	if e != nil {
		return nil, e
	}
	if action == "fixture-update-stage-write" {
		if i.State != "prepared" {
			return nil, errors.New("completed staging is immutable")
		}
		index := -1
		for n, a := range i.Artifacts {
			if a.File == r.File {
				index = n
			}
		}
		if index < 0 || len(r.Data) == 0 || len(r.Data) > update.StageChunk || r.Offset < 0 || r.Offset > i.Artifacts[index].Size-int64(len(r.Data)) {
			return nil, errors.New("invalid bounded artifact chunk")
		}
		if e = v.store.stageWrite(i.ID, index, r.Offset, r.Data); e != nil {
			return nil, e
		}
	} else if action != "fixture-update-stage-status" && action != "fixture-update-stage-finish" {
		return nil, errors.New("unsupported staging action")
	}
	verify := action == "fixture-update-stage-finish" || i.State == "staged"
	files, e := v.store.stageFiles(i, verify)
	if e != nil {
		return nil, e
	}
	if action == "fixture-update-stage-finish" {
		if r.Core == nil || update.SnapshotDigest("core", *r.Core) != update.SnapshotDigest("core", i.Core) {
			return nil, errors.New("core metadata/boot changed; abort and review a fresh intent")
		}
		if _, e = v.heldSnapshot(i, i.Signer.ProbeID); e != nil {
			return nil, e
		}
		if i.State != "staged" {
			if e = v.ledger.append(v, journalRecord{Kind: "update-staged", ID: i.ID, OriginalHash: update.StageDigest(i)}); e != nil {
				return nil, e
			}
			i = v.ledger.updates[i.ID]
		}
	}
	return &update.StageStatus{IntentID: i.ID, Sequence: i.Sequence, ManifestSHA256: i.ManifestSHA256, State: i.State, Files: files, Verified: i.State == "staged"}, nil
}
