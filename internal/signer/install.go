package signer

import (
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
)

func (l *ledger) applyInstallation(r journalRecord) error {
	p := r.Installation
	if p == nil || !update.ValidInstallation(*p) || l.activeUpdate != r.ID || p.Binding.Index.Binding.Pause.Request == nil || p.Binding.Index.Binding.Pause.Request.IntentID != r.ID {
		return errors.New("invalid durable installation hold")
	}
	i := l.updates[r.ID]
	q := p.Binding.Index.Binding.Pause.Request
	if update.SnapshotDigest("contracts", p.Binding.Contracts) != update.SnapshotDigest("contracts", i.Contracts) {
		return errors.New("installation contracts differ from signed intent")
	}
	if update.SnapshotDigest("pause-request", *q) != update.SnapshotDigest("pause-request", update.PauseFor(i)) {
		return errors.New("installation pause differs from original intent")
	}
	if p.Binding.InstallationID != l.ref.SignerID || i.State != "staged" || i.Sequence != l.updateFloor || i.Sequence != q.Sequence || i.ManifestSHA256 != q.ManifestSHA256 || i.Lifecycle == nil || i.Lifecycle.Entrypoint != 3 || update.SnapshotDigest("artifacts", i.Artifacts) != update.SnapshotDigest("artifacts", []update.Artifact{p.Binding.Index.Binding.Files[0].Artifact, p.Binding.Index.Binding.Files[1].Artifact}) {
		return errors.New("installation differs from staged intent")
	}
	old, ok := l.installations[r.ID]
	if !ok {
		if p.Phase != "active" {
			return errors.New("installation must begin held")
		}
	} else {
		if old.Digest != p.Digest || old.Phase == "rolled-back-held" || p.Phase == "active" || old.Phase == p.Phase {
			return errors.New("invalid installation phase transition")
		}
	}
	l.installations[r.ID] = clone(*p)
	return nil
}

func (v *Vault) Installation(r update.InstallRequest) (*update.Installation, error) {
	if r.Action == "status" {
		v.mu.Lock()
		defer v.mu.Unlock()
		if v.closed || v.store.failedState() || v.ledger == nil {
			return nil, ErrUnavailable
		}
		i, ok := v.ledger.installations[r.IntentID]
		if !ok {
			return nil, errors.New("no installation hold")
		}
		q := i.Binding.Index.Binding.Pause.Request
		if _, e := v.stageIntent(update.StageRequest{IntentID: r.IntentID, Sequence: q.Sequence, ManifestSHA256: q.ManifestSHA256}); e != nil {
			return nil, e
		}
		s, e := v.updateSnapshot(update.NewProbe())
		if e != nil {
			return nil, e
		}
		if !update.SameHeldState(i.Binding.Index.Binding.Signer, s) {
			return nil, errors.New("installation financial hold changed")
		}
		i = clone(i)
		return &i, nil
	}
	if r.Binding == nil || update.ValidateInstallBinding(*r.Binding) != nil || r.Preflight == nil {
		return nil, errors.New("bound installation and fresh preflight required")
	}
	// Preflight independently validates stored artifact bytes and current
	// financial authority. Revalidate again under the append lock below.
	p, e := v.PreflightUpdate(*r.Preflight)
	if e != nil {
		return nil, e
	}
	if !update.RecoveryMatches(r.Binding.Index, *p) || r.Binding.Index.Binding.Pause.Request.IntentID != r.IntentID {
		return nil, errors.New("installation recovery binding mismatch")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	s, e := v.updateSnapshot(p.Signer.ProbeID)
	if e != nil {
		return nil, e
	}
	if r.Binding.InstallationID != v.record.Ref.SignerID {
		return nil, errors.New("installation belongs to another signer identity")
	}
	if update.SnapshotDigest("signer", s) != update.SnapshotDigest("signer", p.Signer) {
		return nil, errors.New("signer changed before installation persistence")
	}
	want := update.Installation{Version: 1, Binding: *r.Binding, Digest: update.InstallDigest(*r.Binding), Phase: r.Action}
	if r.Action != "active" && r.Action != "healthy-held" && r.Action != "rolled-back-held" {
		return nil, errors.New("unsupported installation phase")
	}
	if old, ok := v.ledger.installations[r.IntentID]; ok {
		if old.Digest != want.Digest {
			return nil, errors.New("installation identity cannot be rebound")
		}
		if r.Action == "active" && old.Phase == "healthy-held" {
			out := clone(old)
			return &out, nil
		}
		if old.Phase == want.Phase {
			out := clone(old)
			return &out, nil
		}
	} else if v.ledger.updates[r.IntentID].ManifestExpiresAt <= v.now().Unix() {
		return nil, errors.New("signed manifest expired before installation; abort and review a fresh candidate")
	}
	v.lock()
	if e = v.ledger.append(v, journalRecord{Kind: "update-install", ID: r.IntentID, Installation: &want}); e != nil {
		return nil, e
	}
	out := clone(want)
	return &out, nil
}
