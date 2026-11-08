//go:build linux

package signer

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestRecoveredOrdinaryPairedMaintenanceRestart(t *testing.T) {
	sourceDir := fixtureDir(t)
	source := ordinaryVault(t, sourceDir)
	if err := source.Create(fixturePassword); err != nil {
		t.Fatal(err)
	}
	if err := source.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	document, err := source.BackupHistory(backupPassword)
	if err != nil {
		t.Fatal(err)
	}
	original := *source.Status().Account
	source.Close()
	if err = os.Rename(sourceDir, sourceDir+".offline"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sourceDir + ".offline") })
	dir := fixtureDir(t)
	id := "abababababababababababababababab"
	open := func() *Vault {
		v, e := OpenControlledOrdinaryCustody(dir, id, "devnet")
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = v.Close() })
		return v
	}
	v := open()
	if err = v.RestoreHistory(document, backupPassword, fixturePassword, BackupFingerprint(document)); err != nil {
		t.Fatal(err)
	}
	ref := *v.Status().Account
	if ref.SignerID == original.SignerID || !sameWallet(ref, original) || v.ledger.ref != original {
		t.Fatal("recovery did not preserve the historical identity and create a distinct receiver")
	}
	key, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
	prefix, _ := os.ReadFile(filepath.Join(dir, "operations.log"))
	intent, binding := installationFixture(t, v)
	beforeRefusal, _ := os.ReadFile(filepath.Join(dir, "operations.log"))
	for _, foreign := range []string{original.SignerID, "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"} {
		bad := clone(binding)
		bad.InstallationID = foreign
		if _, e := v.Installation(installCommand(t, v, intent, bad, "active")); e == nil {
			t.Fatal("foreign current deployment acquired an installation")
		}
		i := update.Installation{Version: 1, Binding: bad, Digest: update.InstallDigest(bad), Phase: "active"}
		if e := v.ledger.append(v, journalRecord{Kind: "update-install", ID: intent.ID, Installation: &i}); e == nil {
			t.Fatal("foreign deployment bypassed current-owner append guard")
		}
	}
	afterRefusal, _ := os.ReadFile(filepath.Join(dir, "operations.log"))
	if !bytes.Equal(beforeRefusal, afterRefusal) {
		t.Fatal("refused receiver binding changed history")
	}
	for _, phase := range []string{"active", "healthy-held", "committed-held", "completed"} {
		request := installCommand(t, v, intent, binding, phase)
		if _, err = v.Installation(request); err != nil {
			v.Close()
			t.Fatalf("recovered receiver %s: %v", phase, err)
		}
		v.Close()
		v = open()
		state, e := v.Installation(update.InstallRequest{Action: "status", IntentID: intent.ID})
		if e != nil || state.Phase != phase || *v.Status().Account != ref || v.ledger.ref != original || !v.Status().Locked || !v.Status().RecoveryRequired {
			v.Close()
			t.Fatal("restart lost receiver binding or historical hold", phase, e)
		}
		got, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
		history, _ := os.ReadFile(filepath.Join(dir, "operations.log"))
		if !bytes.Equal(got, key) || !bytes.HasPrefix(history, prefix) {
			v.Close()
			t.Fatal("maintenance rewrote original recovery state")
		}
	}
	if err = v.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err = v.VerifyBackup(document, backupPassword); err != nil {
		t.Fatal("original independent backup no longer verifies", err)
	}
	secondBackup, err := v.BackupHistory(backupPassword)
	if err != nil {
		t.Fatal("recovered maintenance history cannot be backed up", err)
	}
	v.Lock()
	v.Close()
	secondDir := fixtureDir(t)
	secondID := "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
	openSecond := func() *Vault {
		w, e := OpenControlledOrdinaryCustody(secondDir, secondID, "devnet")
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = w.Close() })
		return w
	}
	w := openSecond()
	if err = w.RestoreHistory(secondBackup, backupPassword, fixturePassword, BackupFingerprint(secondBackup)); err != nil {
		t.Fatal("second independent recovery failed", err)
	}
	secondRef := *w.Status().Account
	if secondRef.SignerID != secondID || !sameWallet(secondRef, original) || w.ledger.ref != original {
		t.Fatal("repeated recovery rebound genesis")
	}
	secondKey, _ := os.ReadFile(filepath.Join(secondDir, "vault.json"))
	secondPrefix, _ := os.ReadFile(filepath.Join(secondDir, "operations.log"))
	intent, binding = installationFixtureAtSequence(t, w, 2)
	bad := clone(binding)
	bad.InstallationID = ref.SignerID
	if _, err = w.Installation(installCommand(t, w, intent, bad, "active")); err == nil {
		t.Fatal("a historical receiver acquired current deployment authority")
	}
	for _, phase := range []string{"active", "healthy-held", "committed-held", "completed"} {
		if _, err = w.Installation(installCommand(t, w, intent, binding, phase)); err != nil {
			t.Fatal("second receiver maintenance", phase, err)
		}
		w.Close()
		w = openSecond()
		state, e := w.Installation(update.InstallRequest{Action: "status", IntentID: intent.ID})
		if e != nil || state.Phase != phase || *w.Status().Account != secondRef || w.ledger.ref != original || !w.Status().Locked || !w.Status().RecoveryRequired {
			t.Fatal("second receiver restart lost recovery boundary", phase, e)
		}
	}
	secondAfter, _ := os.ReadFile(filepath.Join(secondDir, "vault.json"))
	secondHistory, _ := os.ReadFile(filepath.Join(secondDir, "operations.log"))
	if !bytes.Equal(secondKey, secondAfter) || !bytes.HasPrefix(secondHistory, secondPrefix) {
		t.Fatal("second maintenance rewrote recovered state")
	}
	snapshot, err := w.UpdateInventory(update.NewProbe())
	if err != nil || snapshot.SequenceFloor != 2 || snapshot.InstalledSequence != 2 || snapshot.ActiveIntentID != "" || !snapshot.RecoveryHold {
		t.Fatal("repeated recovery lost completion floor or hold", snapshot, err)
	}
	if err = w.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err = w.VerifyBackup(secondBackup, backupPassword); err != nil {
		t.Fatal("second independent backup no longer verifies", err)
	}
	w.Lock()
	w.Close()
}

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

// Model the immutable predecessor calling the corrected signer directly, without
// passing through any corrected CLI/router. A repair fence is a denial, not a
// new grant, and must survive signer reopen while leaving the staged slot intact.
func TestRecoveredRepairRejectsLegacyRunnerAndPreservesStagedIntent(t *testing.T) {
	source := ordinaryVault(t, fixtureDir(t))
	if e := source.Create(fixturePassword); e != nil {
		t.Fatal(e)
	}
	if e := source.Unlock(fixturePassword, time.Minute); e != nil {
		t.Fatal(e)
	}
	backup, e := source.BackupHistory(backupPassword)
	if e != nil {
		t.Fatal(e)
	}
	original := *source.Status().Account
	source.Close()
	dir := fixtureDir(t)
	id := "edededededededededededededededed"
	open := func() *Vault {
		v, e := OpenControlledOrdinaryCustody(dir, id, "devnet")
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	v := open()
	defer func() { v.Close() }()
	if e = v.RestoreHistory(backup, backupPassword, fixturePassword, BackupFingerprint(backup)); e != nil {
		t.Fatal(e)
	}
	if v.record.Ref.SignerID == original.SignerID || v.ledger.ref != original {
		t.Fatal("not a real independent history recovery")
	}
	intent, binding := installationFixtureAtSequence(t, v, 2)
	command := installCommand(t, v, intent, binding, "active")
	key, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
	journal, _ := os.ReadFile(filepath.Join(dir, "operations.log"))
	fenceDir := filepath.Join(filepath.Dir(dir), "bootstraps", intent.ID)
	if e = os.MkdirAll(fenceDir, 0700); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(fenceDir) })
	// Missing/foreign/unreadable content never grants anything. Any durable fence
	// for this intent closes legacy install admission, including malformed input.
	if e = os.WriteFile(filepath.Join(fenceDir, "forward-only.json"), []byte("root-bound fence"), 0644); e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 2; n++ {
		if _, e = v.Installation(command); e == nil || !strings.Contains(e.Error(), "quarantines") {
			t.Fatal("legacy runner bypassed quarantine", e)
		}
		response := execute(v, Request{Action: "fixture-install-controller", Install: &command}, true)
		if response.Error == "" || !strings.Contains(response.Error, "quarantines") {
			t.Fatal("legacy RPC adapter bypassed quarantine", response.Error)
		}
		if _, e = v.Installation(update.InstallRequest{Action: "status", IntentID: intent.ID}); e == nil {
			t.Fatal("repair fabricated installation hold")
		}
		snap, e := v.UpdateInventory(update.NewProbe())
		if e != nil || snap.SequenceFloor != 2 || snap.ActiveIntentID != intent.ID || !snap.RecoveryHold || !v.Status().Locked {
			t.Fatal("repair reset staged state", snap, e)
		}
		got, _ := os.ReadFile(filepath.Join(dir, "operations.log"))
		gotKey, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
		if !bytes.Equal(got, journal) || !bytes.Equal(gotKey, key) {
			t.Fatal("repair fence mutated key or history")
		}
		v.Close()
		v = open()
	}
	if _, e = v.AbortUpdate(intent.ID); e != nil {
		t.Fatal("separate exact owner abort no longer available", e)
	}
	snap, e := v.UpdateInventory(update.NewProbe())
	if e != nil || snap.SequenceFloor != 2 || snap.ActiveIntentID != "" || !snap.RecoveryHold {
		t.Fatal("abort rewound floor/holds", snap, e)
	}
	// Even after abort, replaying the quarantined old installation stays refused.
	if _, e = v.Installation(command); e == nil {
		t.Fatal("abort revived obsolete installation")
	}
	next, nextBinding := installationFixtureAtSequence(t, v, 3)
	if next.ID == intent.ID {
		t.Fatal("new review reused quarantined identity")
	}
	if _, e = v.Installation(installCommand(t, v, next, nextBinding, "active")); e != nil {
		t.Fatal("quarantine blocked a separately reviewed new transaction", e)
	}
}

func TestOrdinaryRepairObservationNeverAuthorizesMutation(t *testing.T) {
	v := ordinaryVault(t, fixtureDir(t))
	if e := v.Create(fixturePassword); e != nil {
		t.Fatal(e)
	}
	defer v.Close()
	intent, binding := installationFixture(t, v)
	old := update.Manifest{Version: 1, Product: "fased-app", Channel: "development", Sequence: intent.Sequence, IssuedAt: time.Now().Unix() - 5, ExpiresAt: time.Now().Unix() + 300, Target: update.Target{OS: "linux", Arch: "amd64"}}
	next := old
	for _, c := range intent.Contracts {
		file := "fased-app"
		if c.Component == "signer" {
			file = "fased-signer"
		}
		old.Components = append(old.Components, update.Component{Version: "0.0.0-fixture", Contract: c, Artifact: update.Artifact{File: file, Size: 1, SHA256: metadataFixtureHash([]byte("old-" + file))}})
		next.Components = append(next.Components, update.Component{Version: "0.0.0-fixture", Contract: c, Artifact: update.Artifact{File: file, Size: 1, SHA256: metadataFixtureHash([]byte("repair-" + file))}})
	}
	prior, _ := json.Marshal(old)
	next.BootstrapFrom = metadataFixtureHash(prior)
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	sign := func(m update.Manifest) []byte {
		b, _ := json.Marshal(m)
		r, _ := json.Marshal(update.Envelope{Version: 1, KeyID: update.KeyID(pub), Payload: b, Signature: ed25519.Sign(key, append([]byte(update.SignatureDomain), b...))})
		return r
	}
	trust, _ := json.Marshal(update.Trust{Version: 1, Purpose: update.Purpose, Keys: []update.TrustKey{{ID: update.KeyID(pub), PublicKey: pub, NotBefore: old.IssuedAt - 10, NotAfter: old.ExpiresAt + 10}}})
	cfg := Config{Version: 3, StateDir: v.store.path, SignerID: v.signerID, OwnerUID: 1000, AppUID: 1001, SignerUID: 1002, Network: "devnet"}
	base := map[string]any{"version": 3, "id": cfg.SignerID, "root": filepath.Dir(cfg.StateDir), "owner": cfg.OwnerUID, "app": map[string]int{"uid": cfg.AppUID}, "signer": map[string]int{"uid": cfg.SignerUID}, "network": cfg.Network}
	tx := map[string]any{"version": 3, "id": intent.ID, "base": base, "target": base, "bundle": update.BootstrapBundle{Version: 3, Maintenance: sign(old), Target: sign(next)}, "trust": trust, "reviewedAt": time.Now().Unix(), "core": intent.Core, "signer": intent.Signer, "repair": map[string]any{"binding": binding}}
	raw, _ := json.Marshal(tx)
	digest := update.SnapshotDigest("reader-bootstrap:v1", json.RawMessage(raw))
	for _, action := range []string{"fixture-update-inventory", "fixture-update-status", "fixture-install-controller", "fixture-update-abort", "fixture-update-prepare", "unlock", "swap-sign"} {
		got := ordinaryRepairObservationAllowed(cfg, action, intent.ID, digest, raw)
		want := action == "fixture-update-inventory" || action == "fixture-update-status"
		if got != want {
			t.Fatal("repair read-only action policy", action, got)
		}
	}
	foreign := cfg
	foreign.AppUID++
	for _, test := range []struct {
		c          Config
		id, digest string
		raw        []byte
	}{{foreign, intent.ID, digest, raw}, {cfg, randomID(), digest, raw}, {cfg, intent.ID, strings.Repeat("a", 64), raw}, {cfg, intent.ID, digest, []byte("malformed")}} {
		if ordinaryRepairObservationAllowed(test.c, "fixture-update-inventory", test.id, test.digest, test.raw) {
			t.Fatal("foreign repair observation allowed")
		}
	}
}
