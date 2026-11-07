//go:build linux

package signer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestOrdinaryMaintenanceInventoryAndFinancialRefusal(t *testing.T) {
	dir := fixtureDir(t)
	v := ordinaryVault(t, dir)
	if err := v.Create(fixturePassword); err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(filepath.Join(dir, "vault.json"))
	if err != nil {
		t.Fatal(err)
	}
	before := v.Status()
	r := execute(v, Request{Action: "fixture-update-inventory", ProbeID: update.NewProbe()}, true)
	if r.Error != "" || r.UpdateInventory == nil || r.UpdateInventory.Version != 3 || !r.UpdateInventory.RecoveryHold {
		t.Fatalf("ordinary owner maintenance inventory unavailable: %s %+v", r.Error, r.UpdateInventory)
	}
	if execute(v, Request{Action: "fixture-update-inventory", ProbeID: update.NewProbe()}, false).Error == "" {
		t.Fatal("app acquired owner maintenance inventory")
	}
	for _, action := range []string{"fixture-policy", "fixture-grant", "fixture-execute", "swap-sign", "receipt-rpc-save", "export-fixture-seed"} {
		if execute(v, Request{Action: action}, true).Error == "" {
			t.Fatal("financial authority widened", action)
		}
	}
	after, err := os.ReadFile(filepath.Join(dir, "vault.json"))
	if err != nil || !bytes.Equal(key, after) || *v.Status().Account != *before.Account || !v.Status().Locked || !v.Status().RecoveryRequired {
		t.Fatal("inventory changed key or recovery hold", err)
	}
}

func TestOrdinaryPairedMaintenanceRestartRecovery(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "rollback"}[rollback], func(t *testing.T) {
			dir := fixtureDir(t)
			v := ordinaryVault(t, dir)
			if err := v.Create(fixturePassword); err != nil {
				t.Fatal(err)
			}
			key, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
			genesis, _ := os.ReadFile(filepath.Join(dir, "operations.log"))
			ref := *v.Status().Account
			intent, binding := installationFixture(t, v)
			if err := v.Unlock(fixturePassword, 1000000000); err == nil {
				t.Fatal("active intent allowed unlock")
			}
			if _, err := v.BackupHistory(backupPassword); err == nil {
				t.Fatal("active intent escaped portable backup")
			}
			phases := []string{"active", "healthy-held", "committed-held", "completed"}
			if rollback {
				phases = []string{"active", "rolled-back-held"}
			}
			for _, phase := range phases {
				if _, err := v.Installation(installCommand(t, v, intent, binding, phase)); err != nil {
					t.Fatal(phase, err)
				}
				v.Close()
				v = ordinaryVault(t, dir)
				state, err := v.Installation(update.InstallRequest{Action: "status", IntentID: intent.ID})
				if err != nil || state.Phase != phase || *v.Status().Account != ref || !v.Status().Locked || !v.Status().RecoveryRequired {
					t.Fatal("restart lost held update", phase, err)
				}
				got, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
				if !bytes.Equal(got, key) {
					t.Fatal("encrypted key rewritten")
				}
			}
			if rollback {
				if _, err := v.AbortUpdate(intent.ID); err != nil {
					t.Fatal(err)
				}
			}
			snap, err := v.UpdateInventory(update.NewProbe())
			if err != nil || snap.SequenceFloor != 1 || snap.ActiveIntentID != "" || !snap.RecoveryHold {
				t.Fatal("floor or hold lost", snap, err)
			}
			ops, _ := os.ReadFile(filepath.Join(dir, "operations.log"))
			if !bytes.HasPrefix(ops, genesis) {
				t.Fatal("original genesis changed")
			}
			_, records, err := v.store.readJournal("operations.log")
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range records {
				if r.Admission == nil || *r.Admission != *v.record.Admission {
					t.Fatal("ordinary journal domain/provenance lost")
				}
			}
			if err := v.ledger.append(v, journalRecord{Kind: "policy"}); err == nil {
				t.Fatal("ordinary maintenance enabled financial policy")
			}
			if _, err := v.PrepareUpdate(intentFixture(t, v, 1)); err == nil {
				t.Fatal("release floor replay")
			}
			if err := v.Unlock(fixturePassword, 1000000000); err != nil {
				t.Fatal(err)
			}
			backup, err := v.BackupHistory(backupPassword)
			if err != nil {
				t.Fatal(err)
			}
			v.Close()
			recovered := ordinaryVault(t, fixtureDir(t))
			if err := recovered.RestoreHistory(backup, backupPassword, fixturePassword, BackupFingerprint(backup)); err != nil {
				t.Fatal(err)
			}
			after, err := recovered.UpdateInventory(update.NewProbe())
			if err != nil || after.SequenceFloor != snap.SequenceFloor || after.InstalledSequence != snap.InstalledSequence || after.AuthoritySHA256 == snap.AuthoritySHA256 || !after.RecoveryHold {
				t.Fatal("portable recovery lost history/floor", err)
			}
			if *recovered.Status().Account == ref || recovered.Status().Account.PublicKey != ref.PublicKey || !recovered.Status().Locked {
				t.Fatal("recovery reused authority")
			}
		})
	}
}

func TestOrdinaryOwnerHandoffAndConsecutiveMaintenance(t *testing.T) {
	dir := fixtureDir(t)
	open := func() *Vault {
		t.Helper()
		v, e := OpenControlledOwnerCustody(dir, "00112233445566778899aabbccddeeff", "devnet")
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	v := open()
	if e := v.Create(fixturePassword); e != nil {
		t.Fatal(e)
	}
	key, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
	genesis, _ := os.ReadFile(filepath.Join(dir, "operations.log"))
	ref := *v.Status().Account
	before, e := v.UpdateInventory(update.NewProbe())
	if e != nil {
		t.Fatal(e)
	}
	receipt := update.OrdinaryHandoffReceipt{Version: 1, ID: update.NewProbe(), Sequence: 1, ManifestSHA256: update.SnapshotDigest("m", 1), AuthoritySHA256: before.AuthoritySHA256, GenesisSHA256: before.States[1].Anchor, PreviousCore: update.SnapshotDigest("oldcore", 1), PreviousSigner: update.SnapshotDigest("oldsigner", 1), TargetCore: update.SnapshotDigest("core", 2), TargetSigner: update.SnapshotDigest("signer", 2)}
	if _, e = v.commitOrdinaryHandoff(*before, receipt); e != nil {
		t.Fatal(e)
	}
	v.Close()
	v = open()
	if _, e = v.commitOrdinaryHandoff(*before, receipt); e != nil {
		t.Fatal("idempotent restart handoff", e)
	}
	_, rs, e := v.store.readJournal("operations.log")
	if e != nil || len(rs) != 2 || rs[1].Kind != "ordinary-reader-handoff" {
		t.Fatal("duplicate handoff", e)
	}
	foreign := receipt
	foreign.ManifestSHA256 = update.SnapshotDigest("foreign", 1)
	if _, e = v.commitOrdinaryHandoff(*before, foreign); e == nil {
		t.Fatal("foreign handoff accepted")
	}
	if _, e = v.PrepareUpdate(intentFixture(t, v, 1)); e == nil {
		t.Fatal("handoff floor replay accepted")
	}
	sameID := intentFixture(t, v, 2)
	sameID.ID = receipt.ID
	if _, e = v.PrepareUpdate(sameID); e == nil {
		t.Fatal("handoff operation identity reused by ordinary update")
	}
	for _, seq := range []uint64{2, 3} {
		req := intentFixture(t, v, seq)
		i, e := v.PrepareUpdate(req)
		if e != nil {
			t.Fatal("consecutive prepare", seq, e)
		}
		v.Close()
		v = open()
		if _, e = v.AbortUpdate(i.ID); e != nil {
			t.Fatal("consecutive abort", seq, e)
		}
		v.Close()
		v = open()
	}
	after, e := v.UpdateInventory(update.NewProbe())
	if e != nil || after.SequenceFloor != 3 || after.InstalledSequence != 0 || !after.RecoveryHold || after.ActiveIntentID != "" {
		t.Fatal("floor/hold lost", after, e)
	}
	if _, e = v.commitOrdinaryHandoff(*before, receipt); e == nil {
		t.Fatal("old handoff reentered after later maintenance")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
	ops, _ := os.ReadFile(filepath.Join(dir, "operations.log"))
	if !bytes.Equal(got, key) || !bytes.HasPrefix(ops, genesis) || *v.Status().Account != ref || !v.Status().Locked {
		t.Fatal("key/genesis/identity mutated")
	}
	if e = v.Unlock(fixturePassword, 1000000000); e != nil {
		t.Fatal(e)
	}
	backup, e := v.BackupHistory(backupPassword)
	if e != nil {
		t.Fatal(e)
	}
	v.Close()
	receiver, e := OpenControlledOwnerCustody(fixtureDir(t), "ffeeddccbbaa99887766554433221100", "devnet")
	if e != nil {
		t.Fatal(e)
	}
	defer receiver.Close()
	if e = receiver.RestoreHistory(backup, backupPassword, fixturePassword, BackupFingerprint(backup)); e != nil {
		t.Fatal("handoff backup recovery", e)
	}
	recovered, e := receiver.UpdateInventory(update.NewProbe())
	if e != nil || recovered.SequenceFloor != 3 || recovered.InstalledSequence != 0 || !recovered.RecoveryHold || recovered.AuthoritySHA256 == before.AuthoritySHA256 {
		t.Fatal("handoff recovery floors", e)
	}
	if _, e = receiver.PrepareUpdate(intentFixture(t, receiver, 3)); e == nil {
		t.Fatal("recovery rewound floor")
	}
}

func TestOrdinaryMaintenanceRejectsForeignJournalFields(t *testing.T) {
	v := ordinaryVault(t, fixtureDir(t))
	defer v.Close()
	if e := v.Create(fixturePassword); e != nil {
		t.Fatal(e)
	}
	before, _ := v.UpdateInventory(update.NewProbe())
	for _, r := range []journalRecord{{Kind: "policy"}, {Kind: "grant"}, {Kind: "onchain-sign"}, {Kind: "onchain-rpc-credential"}, {Kind: "update-abort", ID: update.NewProbe(), Outcome: "financial"}, {Kind: "ordinary-reader-handoff", OrdinaryHandoff: &update.OrdinaryHandoffReceipt{Version: 1}}} {
		if e := v.ledger.append(v, r); e == nil {
			t.Fatal("foreign authority/fields admitted", r.Kind)
		}
	}
	after, e := v.UpdateInventory(update.NewProbe())
	if e != nil || !update.SameOrdinarySnapshot(*before, *after) {
		t.Fatal("refused append changed state", e)
	}
}

func TestOrdinaryHandoffJournalInterruptionDoesNotDuplicate(t *testing.T) {
	for _, phase := range []string{"before", "written", "durable"} {
		t.Run(phase, func(t *testing.T) {
			dir := fixtureDir(t)
			v := ordinaryVault(t, dir)
			if e := v.Create(fixturePassword); e != nil {
				t.Fatal(e)
			}
			before, e := v.UpdateInventory(update.NewProbe())
			if e != nil {
				t.Fatal(e)
			}
			h := update.OrdinaryHandoffReceipt{Version: 1, ID: update.NewProbe(), Sequence: 9, ManifestSHA256: update.SnapshotDigest("m", 1), AuthoritySHA256: before.AuthoritySHA256, GenesisSHA256: before.States[1].Anchor, PreviousCore: update.SnapshotDigest("c", 1), PreviousSigner: update.SnapshotDigest("s", 1), TargetCore: update.SnapshotDigest("c", 2), TargetSigner: update.SnapshotDigest("s", 2)}
			v.store.crash = func(at string) error {
				if at == "journal:ordinary-reader-handoff:"+phase {
					return os.ErrPermission
				}
				return nil
			}
			if _, e = v.commitOrdinaryHandoff(*before, h); e == nil {
				t.Fatal("fault missed")
			}
			v.Close()
			v = ordinaryVault(t, dir)
			defer v.Close()
			if _, e = v.commitOrdinaryHandoff(*before, h); e != nil {
				t.Fatal("same receipt restart", e)
			}
			_, rs, e := v.store.readJournal("operations.log")
			if e != nil || len(rs) != 2 || v.ledger.updateFloor != 9 {
				t.Fatal("duplicate/rewound handoff", e)
			}
		})
	}
}

func TestOrdinaryHandoffMissingMetadataBlocksMutation(t *testing.T) {
	dir := fixtureDir(t)
	c := Config{Version: 3, StateDir: filepath.Join(dir, "signer-state")}
	if !ordinaryHandoffRPCAllowed(c, "unlock") {
		t.Fatal("idle custody blocked")
	}
	id := update.NewProbe()
	raw := []byte(`{"version":1,"digest":"` + update.SnapshotDigest("digest", 1) + `","phase":"` + id + `"}`)
	if e := os.WriteFile(filepath.Join(dir, "update-active.json"), raw, 0644); e != nil {
		t.Fatal(e)
	}
	for _, action := range []string{"unlock", "create", "fixture-update-prepare", "fixture-install-controller"} {
		if ordinaryHandoffRPCAllowed(c, action) {
			t.Fatal("unclassified active controller admitted mutation", action)
		}
	}
	if !ordinaryHandoffRPCAllowed(c, "status") || !ordinaryHandoffRPCAllowed(c, "lock") {
		t.Fatal("held health/lock lost")
	}
	if e := os.MkdirAll(filepath.Join(dir, "bootstraps", id), 0755); e != nil {
		t.Fatal(e)
	}
	if ordinaryHandoffRPCAllowed(c, "fixture-update-prepare") {
		t.Fatal("missing handoff descriptor failed open")
	}
}

func TestOrdinaryRPCMaintenanceUsesActualSignerHold(t *testing.T) {
	dir := fixtureDir(t)
	state := filepath.Join(dir, "signer-state")
	os.Mkdir(state, 0700)
	v, e := OpenControlledOwnerCustody(state, "00112233445566778899aabbccddeeff", "devnet")
	if e != nil {
		t.Fatal(e)
	}
	defer v.Close()
	if e = v.Create(fixturePassword); e != nil {
		t.Fatal(e)
	}
	i, b := installationFixture(t, v)
	c := Config{Version: 3, OwnerUID: 1000, AppUID: 1001, SignerUID: 1002, SignerID: v.signerID, Mode: OrdinaryCustodyMode, Profile: OwnerCustodyProfile, Provenance: ControlledCustodyProfile, Network: "devnet", StateDir: state}
	base := map[string]any{"version": 3, "owner": c.OwnerUID, "app": map[string]int{"uid": c.AppUID}, "signer": map[string]int{"uid": c.SignerUID}, "id": c.SignerID, "root": dir, "coreHash": b.PreviousCore, "signerHash": b.PreviousSigner, "mode": c.Mode, "profile": c.Profile, "provenance": c.Provenance, "network": c.Network, "nativeOwnerAccess": true}
	raw, _ := json.Marshal(base)
	b.PreviousManifest = metadataFixtureHash(raw)
	target := map[string]any{}
	for k, value := range base {
		target[k] = value
	}
	target["coreHash"], target["signerHash"] = b.TargetCore, b.TargetSigner
	if _, e = v.Installation(installCommand(t, v, i, b, "active")); e != nil {
		t.Fatal(e)
	}
	txn := map[string]any{"version": 1, "base": base, "target": target, "binding": b}
	path := filepath.Join(dir, "updates", i.ID)
	os.MkdirAll(path, 0755)
	write := func(p string, value any) {
		t.Helper()
		data, _ := json.Marshal(value)
		if e := os.WriteFile(p, data, 0644); e != nil {
			t.Fatal(e)
		}
	}
	marker := map[string]any{"version": 1, "digest": update.InstallDigest(b), "phase": i.ID}
	write(filepath.Join(dir, "update-active.json"), marker)
	write(filepath.Join(path, "transaction.json"), txn)
	if !ordinaryHandoffRPCAllowed(c, "fixture-install-controller", v) {
		t.Fatal("actual normal update continuation blocked")
	}
	// An unchanged, cancelled cold trial may retain this namespace without
	// ever reserving the ID in signer history. The actual new normal hold wins.
	cancelled := filepath.Join(dir, "bootstraps", i.ID)
	if e = os.MkdirAll(cancelled, 0755); e != nil {
		t.Fatal(e)
	}
	write(filepath.Join(cancelled, "cancelled.json"), map[string]any{"version": 1, "digest": update.SnapshotDigest("cancelled trial", 1), "phase": "cancelled"})
	if !ordinaryHandoffRPCAllowed(c, "fixture-install-controller", v) {
		t.Fatal("cancelled trial stranded a same-ID normal controller")
	}
	if ordinaryHandoffRPCAllowed(c, "unlock", v) || ordinaryHandoffRPCAllowed(c, "fixture-update-prepare", v) {
		t.Fatal("active normal maintenance widened authority")
	}
	target["nativeOwnerAccess"] = false
	write(filepath.Join(path, "transaction.json"), txn)
	if ordinaryHandoffRPCAllowed(c, "fixture-install-controller", v) {
		t.Fatal("foreign topology acquired signer continuation")
	}
	target["nativeOwnerAccess"] = true
	write(filepath.Join(path, "transaction.json"), txn)
	marker["digest"] = update.SnapshotDigest("unbound hold", 1)
	write(filepath.Join(dir, "update-active.json"), marker)
	if ordinaryHandoffRPCAllowed(c, "fixture-install-controller", v) {
		t.Fatal("root metadata replaced authoritative signer receipt")
	}
}
