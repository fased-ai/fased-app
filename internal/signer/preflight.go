package signer

import (
	"errors"

	"github.com/fased-ai/fased-app/internal/update"
)

func (v *Vault) heldSnapshot(i update.Intent, probe string) (update.SignerSnapshot, error) {
	s, e := v.updateSnapshot(probe)
	if e != nil {
		return s, e
	}
	if s.AuthoritySHA256 != i.Signer.AuthoritySHA256 || s.Spent != i.Signer.Spent || s.Held != i.Signer.Held || s.Pending != i.Signer.Pending || s.Unknown != i.Signer.Unknown || s.RecoveryHold != i.Signer.RecoveryHold {
		return s, errors.New("financial authority changed while held")
	}
	for _, old := range i.Signer.States {
		for _, now := range s.States {
			if old.Name == now.Name && old.Name != "operations" && update.SnapshotDigest("state", old) != update.SnapshotDigest("state", now) {
				return s, errors.New("signer durable state changed while held")
			}
		}
	}
	return s, nil
}

// PreflightUpdate verifies current evidence only. It cannot install or create a
// financial grant. The owner conductor rereads the actual core pause afterward;
// any future installation must acquire its own exclusive transaction.
func (v *Vault) PreflightUpdate(r update.PreflightRequest) (*update.Preflight, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	i, e := v.stageIntent(update.StageRequest{IntentID: r.IntentID, Sequence: r.Sequence, ManifestSHA256: r.ManifestSHA256})
	if e != nil {
		return nil, e
	}
	if i.State != "staged" || i.StageSHA256 != update.StageDigest(i) || i.Lifecycle.Entrypoint != 2 {
		return nil, errors.New("compatible durable staged intent required")
	}
	if e = update.ValidatePaused(i, r.Pause, r.ProbeID); e != nil {
		return nil, e
	}
	s, e := v.heldSnapshot(i, r.ProbeID)
	if e != nil {
		return nil, e
	}
	if update.SnapshotDigest("signer", s) != update.SnapshotDigest("signer", r.ExpectedSigner) {
		return nil, errors.New("stale preflight signer boot, history, authority or floor")
	}
	files, e := v.store.stageFiles(i, true)
	if e != nil {
		return nil, e
	}
	p := update.Preflight{Version: 1, IntentID: i.ID, Sequence: i.Sequence, ManifestSHA256: i.ManifestSHA256, Pause: r.Pause, Signer: s, Files: files, Verified: true}
	p.BindingSHA256 = update.SnapshotDigest("preflight:v1", p)
	return &p, nil
}
