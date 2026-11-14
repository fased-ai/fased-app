package signer

import (
	"errors"

	"github.com/fased-ai/fased-app/internal/update"
)

// ErrNoInstallationHold is an authenticated signer's explicit absence of an
// installation receipt. Transport failures must not be treated as this state.
var ErrNoInstallationHold = errors.New("no installation hold")

// ValidateInstallationReplacement checks signer-owned authority only. The
// caller must hold a separate OS boundary that prevents signer state changes
// between this check and the privileged replacement.
func ValidateInstallationReplacement(i *update.Installation, intent, digest, direction, component, source string) error {
	if i == nil || !update.ValidInstallation(*i) || i.Binding.Index.Binding.Pause.Request == nil || i.Binding.Index.Binding.Pause.Request.IntentID != intent || i.Digest != digest || (i.InstalledSequenceCommitted && direction != "completion" && direction != "cleanup-complete") {
		return errors.New("exact uncommitted signer installation required")
	}
	var want string
	switch direction {
	case "forward":
		if i.Phase != "active" {
			return errors.New("forward replacement requires the active hold")
		}
		if component == "core" {
			want = i.Binding.TargetCore
		} else if component == "signer" {
			want = i.Binding.TargetSigner
		}
	case "rollback":
		if i.Phase != "active" && i.Phase != "healthy-held" {
			return errors.New("rollback replacement requires an uncommitted hold")
		}
		if component == "core" {
			want = i.Binding.PreviousCore
		} else if component == "signer" {
			want = i.Binding.PreviousSigner
		}
	case "completion":
		if i.Phase != "committed-held" || !i.InstalledSequenceCommitted || component != "core" {
			return errors.New("completion requires the committed signer hold")
		}
		want = i.Binding.TargetCore
	case "cleanup-rollback", "cleanup-complete":
		if (direction == "cleanup-rollback" && i.Phase != "rolled-back-held") || (direction == "cleanup-complete" && i.Phase != "completed") || component != "installation" {
			return errors.New("cleanup requires a final signer-held result")
		}
		want = i.Digest
	default:
		return errors.New("unsupported installation direction")
	}
	if want == "" || source != want {
		return errors.New("replacement component or source differs from the signer hold")
	}
	return nil
}

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
	// Deployment ownership is checked against the current vault before append.
	// Replay must retain then-valid deployment IDs: independent recovery keeps
	// this historical genesis reference while changing the receiving signer.
	if i.State != "staged" || i.Sequence != l.updateFloor || i.Sequence != q.Sequence || i.ManifestSHA256 != q.ManifestSHA256 || i.Lifecycle == nil || i.Lifecycle.Entrypoint < 3 || i.Lifecycle.Entrypoint > 4 || update.SnapshotDigest("artifacts", i.Artifacts) != update.SnapshotDigest("artifacts", []update.Artifact{p.Binding.Index.Binding.Files[0].Artifact, p.Binding.Index.Binding.Files[1].Artifact}) {
		return errors.New("installation differs from staged intent")
	}
	old, ok := l.installations[r.ID]
	if !ok {
		if p.Phase != "active" {
			return errors.New("installation must begin held")
		}
	} else {
		allowed := (old.Phase == "active" && (p.Phase == "healthy-held" || p.Phase == "rolled-back-held")) ||
			(old.Phase == "healthy-held" && (p.Phase == "rolled-back-held" || p.Phase == "committed-held")) ||
			(old.Phase == "committed-held" && p.Phase == "completed")
		if old.Digest != p.Digest || !allowed {
			return errors.New("invalid installation phase transition")
		}
	}
	if p.InstalledSequenceCommitted {
		if i.Lifecycle.Entrypoint != 4 {
			return errors.New("completion reader required")
		}
		if p.Phase == "committed-held" {
			if i.Sequence <= l.installedSequence {
				return errors.New("installed sequence cannot rewind or repeat")
			}
			l.installedSequence = i.Sequence
		} else {
			if l.installedSequence != i.Sequence {
				return errors.New("installed sequence differs")
			}
			i.State = "installed"
			l.updates[r.ID] = i
			l.activeUpdate = ""
		}
	}
	l.installations[r.ID] = clone(*p)
	return nil
}

func (v *Vault) Installation(r update.InstallRequest) (*update.Installation, error) {
	// A legacy runner can bypass new CLI routing. A durable cold-repair fence is
	// a denial only, never signer authority: do not let it reinstall the obsolete
	// staged pair. Status and a separately reviewed owner abort remain available.
	if v.ordinary && r.Action != "status" && update.IDOK(r.IntentID) && v.store.readerRepairQuarantined(r.IntentID) {
		return nil, errors.New("reader repair quarantines this staged installation; preserve intent and floor")
	}

	if r.Action == "status" || r.Action == "completed" {
		v.mu.Lock()
		if v.closed || v.store.failedState() || v.ledger == nil {
			v.mu.Unlock()
			return nil, ErrUnavailable
		}
		old, ok := v.ledger.installations[r.IntentID]
		if r.Action == "status" || (ok && old.Phase == "completed") {
			out, e := v.installationStatus(r.IntentID)
			if e == nil && r.Action != "status" && (r.Binding == nil || update.InstallDigest(*r.Binding) != out.Digest) {
				e = errors.New("completion binding mismatch")
			}
			v.mu.Unlock()
			return out, e
		}
		v.mu.Unlock()
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
	want.InstalledSequenceCommitted = r.Action == "committed-held" || r.Action == "completed"
	if !update.ValidInstallation(want) {
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

// A historical root receipt is insufficient: inspect the current durable journal,
// authority and floors even on an idempotent retry after active intent release.
func (v *Vault) installationStatus(id string) (*update.Installation, error) {
	i, ok := v.ledger.installations[id]
	if !ok {
		return nil, ErrNoInstallationHold
	}
	s, e := v.updateSnapshot(update.NewProbe())
	if e != nil {
		return nil, e
	}
	q := i.Binding.Index.Binding.Pause.Request
	if q == nil || s.SequenceFloor != q.Sequence || s.AuthoritySHA256 != i.Binding.Index.Binding.Signer.AuthoritySHA256 {
		return nil, errors.New("installation superseded or authority changed")
	}
	if i.Phase == "completed" {
		if s.ActiveIntentID != "" || s.InstalledSequence != q.Sequence || v.ledger.updates[id].State != "installed" {
			return nil, errors.New("installation is not current")
		}
	} else {
		if _, e = v.stageIntent(update.StageRequest{IntentID: id, Sequence: q.Sequence, ManifestSHA256: q.ManifestSHA256}); e != nil {
			return nil, e
		}
		if i.InstalledSequenceCommitted {
			if s.InstalledSequence != q.Sequence {
				return nil, errors.New("installed counter changed")
			}
			s.InstalledSequence = i.Binding.Index.Binding.Signer.InstalledSequence
		}
		if !update.SameHeldState(i.Binding.Index.Binding.Signer, s) {
			return nil, errors.New("installation financial hold changed")
		}
	}
	out := clone(i)
	return &out, nil
}
