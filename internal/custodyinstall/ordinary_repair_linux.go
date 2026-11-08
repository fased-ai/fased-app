//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"os"

	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

// A repair is not an install of the staged candidate. That candidate may itself
// contain the broken reader. Preserve its transaction and reserve no new floor.
func (t BootstrapTransaction) repairPlan(p update.BootstrapPlan) error {
	if t.Version != 3 || t.Bundle.Version != 3 || t.Base.Version != 3 || t.Repair == nil || !t.Repair.valid() || t.Repair.Base != t.Base {
		return errors.New("exact retained ordinary installation required for repair")
	}
	b := t.Repair.Binding
	q := b.Index.Binding.Pause.Request
	if q == nil || !update.ValidPauseRequest(*q) || b.Index.Binding.Pause.State != "paused" || !update.IDOK(b.Index.Binding.Pause.PauseID) || !update.SameCoreMetadata(b.Index.Binding.Pause.Core, q.Expected) || q.IntentID != t.ID || t.Signer.ActiveIntentID != t.ID || t.Signer.SequenceFloor != q.Sequence || t.Signer.InstalledSequence >= q.Sequence || p.Target.Sequence != q.Sequence || !t.Signer.RecoveryHold || !update.SameOrdinarySnapshot(t.Signer, b.Index.Binding.Signer) || !update.SameCoreMetadata(t.Core, b.Index.Binding.Pause.Core) || update.SnapshotDigest("repair-contracts", b.Contracts) != update.SnapshotDigest("repair-contracts", repairContracts(p.Target)) {
		return errors.New("repair cannot change staged identity, floor, pause or contracts")
	}
	// Recovery indexes intentionally strip transient boot/challenge IDs. Supply
	// structural placeholders only to the shared shape validator; not live proof.
	core, s := t.Core, t.Signer
	core.ProbeID, core.BootID = t.ID, t.ID
	s.ProbeID, s.BootID = t.ID, t.ID
	if _, e := update.JoinSnapshots(core, s, p.Target.Target); e != nil {
		return e
	}
	return nil
}
func repairContracts(p update.Manifest) []update.Contract {
	var cs []update.Contract
	for _, c := range p.Components {
		cs = append(cs, c.Contract)
	}
	return cs
}

// Root-owned exact original transaction is checked on every retry. No hold may
// have begun; a reader fix must not bypass an in-progress financial operation.
func repairOriginal(t BootstrapTransaction, read readHost) error {
	if t.Repair == nil {
		return errors.New("missing original repair transaction")
	}
	f, e := read(t.Repair.dir() + "/transaction.json")
	if e != nil || !known(f, mustJSON(*t.Repair), 0644) {
		return errors.New("retained installation changed before repair")
	}
	for _, phase := range []string{"held", "rollback", "healthy-held", "rolled-back-held", "committed-held", "completed"} {
		if _, e = read(t.Repair.dir() + "/" + phase + ".json"); !errors.Is(e, os.ErrNotExist) {
			return errors.New("repair refuses an installation that already acquired a hold or decision")
		}
	}
	return nil
}

// Only hashes are persisted. The ordinary transaction already binds both public
// snapshots; no key, session, journal record or alternate identity store is added.
func repairColdTrial(ctx context.Context, t BootstrapTransaction, o installOps, c ordinaryBootstrapOps, forward bool) error {
	if e := repairOriginal(t, o.read); e != nil {
		return e
	}
	before, e := c.raw(ctx)
	if e != nil {
		return e
	}
	cold, s, e := c.cold(ctx)
	if e != nil {
		return e
	}
	after, e := c.raw(ctx)
	if e != nil {
		return e
	}
	if before != after || !validHex(before, 32) || !update.SameCoreMetadata(cold, t.Core) || !update.SameOrdinarySnapshot(s, t.Signer) {
		return errors.New("repair cold trial changed protected source state")
	}
	path := t.Dir() + "/repair-source.json"
	f, e := o.read(path)
	record := phaseRecord{1, t.Digest(), before}
	if errors.Is(e, os.ErrNotExist) {
		if forward {
			return errors.New("repair fence lost its original source receipt")
		}
		return publish(ctx, path, t.Base.ID, mustJSON(record), 0644, o.read, o.run)
	}
	if e != nil {
		return e
	}
	// After a post-fence service start SQLite may change physical layout. All
	// logical metadata and signer journal/key anchors must still match above.
	if !forward && !known(f, mustJSON(record), 0644) {
		return errors.New("repair source bytes changed before commitment")
	}
	var original phaseRecord
	if strict(f.Data, &original) != nil || !known(f, mustJSON(original), 0644) || original.Version != 1 || original.Digest != t.Digest() || !validHex(original.Phase, 32) {
		return errors.New("foreign repair source receipt")
	}
	return nil
}

// Missing metadata is allowed only when no repair was prepared. Foreign or
// partial repair metadata fails closed, never falls back to the obsolete runner.
func refuseRepairedPredecessor(t InstallTransaction, read readHost) error {
	id := t.Binding.Index.Binding.Pause.Request.IntentID
	path := t.Base.Root + "/bootstraps/" + id
	f, e := read(path + "/transaction.json")
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	var repair BootstrapTransaction
	if update.StrictJSON(f.Data, &repair) != nil || !known(f, mustJSON(repair), 0644) {
		return errors.New("foreign predecessor repair; preserve transaction")
	}
	if repair.Version != 3 {
		return nil
	}
	if repair.Repair == nil || update.SnapshotDigest("repair-original", *repair.Repair) != update.SnapshotDigest("repair-original", t) {
		return errors.New("repair belongs to another installation")
	}
	if _, e = repair.Plan(); e != nil {
		return e
	}
	if f, e = read(path + "/cancelled.json"); e == nil && known(f, mustJSON(phaseRecord{1, repair.Digest(), "cancelled"}), 0644) {
		if _, e = read(path + "/forward-only.json"); errors.Is(e, os.ErrNotExist) {
			return nil
		}
	}
	return errors.New("reader repair owns this predecessor; resume that repair; original staged install and rollback remain quarantined")
}

// Startup readiness is not preservation. Re-read the actual paused core and
// signer through existing authenticated typed IPC before publishing completion.
func observeOrdinaryRepair(ctx context.Context, t BootstrapTransaction, pause func(context.Context, update.CorePauseCommand) (update.CorePauseReceipt, error), call func(context.Context, signer.Request) (signer.Response, error)) error {
	probe := update.NewProbe()
	p, e := pause(ctx, update.CorePauseCommand{Action: "status", ProbeID: probe})
	if e != nil {
		return e
	}
	expected := t.Repair.Binding.Index.Binding.Pause
	if p.State != "paused" || p.PauseID != expected.PauseID || p.Request == nil || update.SnapshotDigest("repair-pause", p.Request) != update.SnapshotDigest("repair-pause", expected.Request) || !update.SameCoreMetadata(p.Core, t.Core) {
		return errors.New("repair startup changed protected core pause or metadata")
	}
	r, e := call(ctx, signer.Request{Action: "fixture-update-inventory", ProbeID: probe})
	if e != nil {
		return e
	}
	if r.UpdateInventory == nil || !update.SameOrdinarySnapshot(*r.UpdateInventory, t.Signer) || !r.Status.Locked || r.Status.Account == nil || r.Status.Account.SignerID != t.Base.ID || r.Status.Account.Network != t.Base.Network {
		return errors.New("repair startup changed signer identity, history, floor or hold")
	}
	r, e = call(ctx, signer.Request{Action: "fixture-update-status", IntentID: t.ID})
	if e != nil {
		return e
	}
	i := r.UpdateIntent
	q := expected.Request
	if i == nil || i.ID != t.ID || i.State != "staged" || i.Sequence != q.Sequence || i.ManifestSHA256 != q.ManifestSHA256 || update.SnapshotDigest("repair-contracts", i.Contracts) != update.SnapshotDigest("repair-contracts", t.Repair.Binding.Contracts) {
		return errors.New("repair startup changed staged intent")
	}
	return nil
}
