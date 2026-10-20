package signer

import (
	"errors"
	"runtime"

	"github.com/fased-ai/fased-app/internal/update"
)

func (v *Vault) updateSnapshot(probe string) (update.SignerSnapshot, error) {
	if !update.IDOK(probe) || v.closed || v.store.failedState() || v.record == nil || v.ledger == nil {
		return update.SignerSnapshot{}, ErrUnavailable
	}
	l := v.ledger
	main, _, e := v.store.readJournal("operations.log")
	if e != nil {
		return update.SignerSnapshot{}, e
	}
	mock, _, e := v.store.readJournal("mock-receipts.log")
	if e != nil {
		return update.SignerSnapshot{}, e
	}
	if main.hash != l.main.hash || main.seq != l.main.seq || mock.hash != l.mock.hash || mock.seq != l.mock.seq {
		return update.SignerSnapshot{}, errors.New("durable signer history changed outside running owner")
	}
	b, e := v.store.read()
	if e != nil {
		return update.SignerSnapshot{}, e
	}
	var disk envelope
	if decode(b, &disk) != nil || update.SnapshotDigest("vault", disk) != update.SnapshotDigest("vault", *v.record) {
		return update.SignerSnapshot{}, errors.New("durable vault changed outside running owner")
	}
	spent, held, e := l.totals("")
	if e != nil {
		return update.SignerSnapshot{}, e
	}
	s := update.SignerSnapshot{Version: 1, ProbeID: probe, BootID: v.bootID, AuthoritySHA256: update.SnapshotDigest("authority", []AccountRef{v.record.Ref, l.ref}), Spent: spent, Held: held, RecoveryHold: v.recoveryRequired(), SequenceFloor: l.updateFloor, ActiveIntentID: l.activeUpdate}
	c := UpdateContract()
	for _, contract := range c.State {
		anchor := update.SnapshotDigest("vault-bytes", b)
		if contract.Name == "operations" {
			anchor = update.SnapshotDigest("operations-head", struct {
				Sequence int
				Hash     string
			}{main.seq, main.hash})
		}
		if contract.Name == "mock-receipts" {
			anchor = update.SnapshotDigest("mock-head", struct {
				Sequence int
				Hash     string
			}{mock.seq, mock.hash})
		}
		s.States = append(s.States, update.State{Name: contract.Name, Version: contract.Versions.Min, Features: append([]string{}, contract.Features...), Anchor: anchor})
	}
	for _, op := range l.ops {
		switch op.State {
		case "reserved", "prepared":
			s.Pending++
		case "sending":
			s.Pending++
			s.Unknown++
		}
	}
	return s, nil
}

// UpdateInventory is owner-only over RPC. No key is unlocked or returned.
func (v *Vault) UpdateInventory(probe string) (*update.SignerSnapshot, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	s, e := v.updateSnapshot(probe)
	if e != nil {
		return nil, e
	}
	return &s, nil
}

func (v *Vault) PrepareUpdate(r update.IntentRequest) (*update.Intent, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !update.IDOK(r.ID) || v.closed || v.store.failedState() || v.ledger == nil || v.ledger.missingHistory || len(r.Manifest)+len(r.Trust) > 16384 {
		return nil, errors.New("update requires exact identity and retained authoritative history")
	}
	t, e := update.ParseTrust(r.Trust, v.now().Unix())
	if e != nil {
		return nil, e
	}
	m, digest, e := update.Authenticate(r.Manifest, t, v.now().Unix())
	if e != nil {
		return nil, e
	}
	l := v.ledger
	if old, ok := l.updates[r.ID]; ok {
		if old.ManifestSHA256 != digest || old.Sequence != m.Sequence || update.SnapshotDigest("core", old.Core) != update.SnapshotDigest("core", r.Core) || update.SnapshotDigest("signer", old.Signer) != update.SnapshotDigest("signer", r.Signer) {
			return nil, errors.New("update identity cannot change its bindings")
		}
		out := clone(old)
		return &out, nil
	}
	if l.activeUpdate != "" {
		return nil, errors.New("another update intent owns the exclusive slot")
	}
	s, e := v.updateSnapshot(r.Signer.ProbeID)
	if e != nil {
		return nil, e
	}
	if update.SnapshotDigest("signer", s) != update.SnapshotDigest("signer", r.Signer) {
		return nil, errors.New("stale signer boot, epoch, history or sequence floor")
	}
	target := update.Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
	if m.Target != target {
		return nil, errors.New("manifest target differs from installed signer")
	}
	inv, e := update.JoinSnapshots(r.Core, s, target)
	if e != nil {
		return nil, e
	}
	if _, e = update.Compatible(m, digest, inv); e != nil {
		return nil, e
	}
	i := update.Intent{Version: 1, ID: r.ID, Sequence: m.Sequence, ManifestSHA256: digest, Core: r.Core, Signer: s, CreatedAt: v.now().Unix(), ManifestExpiresAt: m.ExpiresAt, State: "prepared"}
	for _, c := range m.Components {
		i.Contracts = append(i.Contracts, c.Contract)
		i.Artifacts = append(i.Artifacts, c.Artifact)
		if c.Contract.Component == "core" {
			i.Lifecycle = c.Contract.Lifecycle
		}
	}
	v.lock() // Also remain locked if persistence has an uncertain outcome.
	if e = l.append(v, journalRecord{Kind: "update-prepare", Update: &i, At: i.CreatedAt}); e != nil {
		return nil, e
	}
	v.lock() // Abort never automatically unlocks or resumes.
	out := clone(i)
	return &out, nil
}

func (v *Vault) AbortUpdate(id string) (*update.Intent, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !update.IDOK(id) || v.closed || v.store.failedState() || v.ledger == nil {
		return nil, ErrUnavailable
	}
	l := v.ledger
	i, ok := l.updates[id]
	if !ok {
		return nil, errors.New("unknown update intent")
	}
	if i.State == "aborted" {
		out := clone(i)
		return &out, nil
	}
	if e := l.append(v, journalRecord{Kind: "update-abort", ID: id}); e != nil {
		return nil, e
	}
	v.lock()
	out := clone(l.updates[id])
	return &out, nil
}

func (v *Vault) UpdateIntent(id string) (*update.Intent, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.store.failedState() || v.ledger == nil {
		return nil, ErrUnavailable
	}
	if id == "" {
		id = v.ledger.activeUpdate
	}
	i, ok := v.ledger.updates[id]
	if !ok {
		return nil, errors.New("unknown update intent")
	}
	out := clone(i)
	return &out, nil
}
