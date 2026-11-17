//go:build linux

package signer

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

// Exercise owner-provenance storage and recovery with disposable keys only.
func TestOwnerHistoryBackupAndRecoveryPreservesProvenance(t *testing.T) {
	const sourceID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const targetID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	open := func(t *testing.T, dir, id string) *Vault {
		t.Helper()
		v, err := OpenOrdinaryCustody(dir, id, "devnet")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = v.Close() })
		return v
	}

	source := open(t, fixtureDir(t), sourceID)
	if err := source.Create(fixturePassword); err != nil {
		t.Fatal(err)
	}
	if err := source.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	original := *source.Status().Account
	freshInventory, err := source.UpdateInventory(update.NewProbe())
	if err != nil || len(freshInventory.States) == 0 || !slices.Contains(freshInventory.States[0].Features, update.OwnerKeyAdmissionFeature) {
		t.Fatal("fresh owner wallet must require an owner-capable update", err)
	}
	backup, err := source.BackupHistory(backupPassword)
	if err != nil {
		t.Fatalf("owner wallet cannot back up its history: %v", err)
	}
	if !bytes.HasPrefix(backup, []byte("FASED-ENCRYPTED-HISTORY:v1\n")) || !IsHistoryBackup(backup) {
		t.Fatal("owner backup must use the owner format, not the fixture format")
	}
	if bytes.Contains(backup, source.seed) || bytes.Contains(backup, fixturePassword) {
		t.Fatal("backup contains a plaintext secret")
	}
	if err = source.VerifyBackup(backup, backupPassword); err != nil {
		t.Fatal("original backup cannot be verified", err)
	}
	if err = source.VerifyBackup(backup, []byte("wrong disposable backup password")); err == nil {
		t.Fatal("wrong backup password was accepted")
	}
	tampered := bytes.Clone(backup)
	tampered[len(tampered)-1] ^= 1
	if err = source.VerifyBackup(tampered, backupPassword); err == nil {
		t.Fatal("tampered owner backup was accepted")
	}
	data, err := unsealPortable(backup, backupPassword, BackupFingerprint(backup))
	if err != nil || data.header.FixtureOnly {
		t.Fatal("owner backup was not identified as owner custody", err)
	}
	data.header.FixtureOnly = true
	data.header.Purpose = portablePurpose
	relabelled, err := sealPortable(data, backupPassword)
	clearPortable(&data)
	if err != nil {
		t.Fatal(err)
	}
	if err = source.Close(); err != nil {
		t.Fatal(err)
	}

	dir := fixtureDir(t)
	target := open(t, dir, targetID)
	if err = target.RestoreHistory(relabelled, backupPassword, fixturePassword, BackupFingerprint(relabelled)); err == nil {
		t.Fatal("owner backup could be relabelled as a fixture")
	}
	if _, err = os.Lstat(filepath.Join(dir, "vault.json")); !os.IsNotExist(err) {
		t.Fatal("refused backup changed the empty destination", err)
	}
	if err = target.RestoreHistory(backup, backupPassword, fixturePassword, BackupFingerprint(backup)); err != nil {
		t.Fatal("independent owner recovery failed", err)
	}
	status := target.Status()
	if status.Account == nil || !sameWallet(*status.Account, original) || status.Account.SignerID != targetID || !status.Locked || !status.RecoveryRequired || status.FixtureOnly || status.FinancialExecution || status.WalletSigner {
		t.Fatal("recovery changed wallet identity, provenance or hold")
	}
	if err = target.RestoreHistory(backup, backupPassword, fixturePassword, BackupFingerprint(backup)); err == nil {
		t.Fatal("recovery overwrote an existing wallet")
	}
	if err = target.Close(); err != nil {
		t.Fatal(err)
	}
	target = open(t, dir, targetID)
	status = target.Status()
	if status.Account == nil || !sameWallet(*status.Account, original) || !status.Locked || !status.RecoveryRequired || status.FixtureOnly {
		t.Fatal("restart changed the recovered wallet or hold")
	}
	if err = target.VerifyBackupLocked(backup, backupPassword, fixturePassword, BackupFingerprint(backup)); err != nil {
		t.Fatal("recovered wallet cannot verify its original backup while locked", err)
	}
	inventory, err := target.UpdateInventory(update.NewProbe())
	if err != nil || len(inventory.States) == 0 || !slices.Contains(inventory.States[0].Features, "owner-history-recovery-v1") || !slices.Contains(inventory.States[0].Features, update.OwnerKeyAdmissionFeature) {
		t.Fatal("update compatibility must require a reader for owner recovery history", err)
	}
	if !OrdinaryOwnerAdmissionEnabled() || !slices.Contains(OrdinaryUpdateContract().State[0].Features, "owner-history-recovery-v1") || !slices.Contains(OrdinaryUpdateContract().State[0].Features, update.OwnerKeyAdmissionFeature) {
		t.Fatal("owner admission must advertise compatible owner-history recovery")
	}
	request := intentFixture(t, target, 1)
	var signed update.Envelope
	var manifest update.Manifest
	if json.Unmarshal(request.Manifest, &signed) != nil || json.Unmarshal(signed.Payload, &manifest) != nil {
		t.Fatal("invalid disposable update manifest")
	}
	joined, err := update.JoinSnapshots(request.Core, request.Signer, manifest.Target)
	if err != nil {
		t.Fatal(err)
	}
	// Model an older signer that lacks the newly enabled owner reader.
	for i := range manifest.Components {
		contract := &manifest.Components[i].Contract
		if contract.Component == "signer" {
			contract.State[0].Features = slices.DeleteFunc(contract.State[0].Features, func(f string) bool { return f == "owner-history-recovery-v1" || f == update.OwnerKeyAdmissionFeature })
		}
	}
	if _, err = update.Compatible(manifest, update.SnapshotDigest("test", 1), joined); err == nil {
		t.Fatal("a signer without the owner-history reader was accepted")
	}
	for i := range manifest.Components {
		contract := &manifest.Components[i].Contract
		if contract.Component == "signer" {
			contract.State[0].Features = append(contract.State[0].Features, "owner-history-recovery-v1", update.OwnerKeyAdmissionFeature)
		}
	}
	if _, err = update.Compatible(manifest, update.SnapshotDigest("test", 1), joined); err != nil {
		t.Fatal("compatible owner-history reader was refused", err)
	}
	for _, feature := range []string{"owner-history-recovery-v1", update.OwnerKeyAdmissionFeature} {
		for i := range manifest.Components {
			contract := &manifest.Components[i].Contract
			if contract.Component != "signer" {
				continue
			}
			features := slices.Clone(contract.State[0].Features)
			contract.State[0].Features = slices.DeleteFunc(contract.State[0].Features, func(f string) bool { return f == feature })
			if _, err = update.Compatible(manifest, update.SnapshotDigest("test", 1), joined); err == nil {
				t.Fatal("signer without required owner state support was accepted", feature)
			}
			contract.State[0].Features = features
		}
	}
	keyBefore, err := os.ReadFile(filepath.Join(dir, "vault.json"))
	if err != nil {
		t.Fatal(err)
	}
	historyBefore, err := os.ReadFile(filepath.Join(dir, "operations.log"))
	if err != nil {
		t.Fatal(err)
	}
	// The signed test manifest uses the owner's declared reader contract.
	intent, binding := installationFixtureWithContract(t, target, 1, nil)
	for _, phase := range []string{"active", "healthy-held", "committed-held", "completed"} {
		if _, err = target.Installation(installCommand(t, target, intent, binding, phase)); err != nil {
			t.Fatal("controlled owner update failed", phase, err)
		}
		if err = target.Close(); err != nil {
			t.Fatal(err)
		}
		target = open(t, dir, targetID)
		got := target.Status()
		currentKey, keyErr := os.ReadFile(filepath.Join(dir, "vault.json"))
		currentHistory, historyErr := os.ReadFile(filepath.Join(dir, "operations.log"))
		if got.Account == nil || !sameWallet(*got.Account, original) || !got.Locked || !got.RecoveryRequired || got.FixtureOnly || got.FinancialExecution || got.WalletSigner || keyErr != nil || historyErr != nil || !bytes.Equal(currentKey, keyBefore) || !bytes.HasPrefix(currentHistory, historyBefore) {
			t.Fatal("update restart changed the wallet, original history or hold", phase)
		}
	}
	if err = target.VerifyBackupLocked(backup, backupPassword, fixturePassword, BackupFingerprint(backup)); err != nil {
		t.Fatal("completed owner update lost the original backup", err)
	}
	for _, phase := range []string{"staged", "recovery:operations.log", "published"} {
		t.Run(phase, func(t *testing.T) {
			interruptedDir := fixtureDir(t)
			v := open(t, interruptedDir, targetID)
			v.store.crash = func(at string) error {
				if at == phase {
					return errors.New("disposable interruption")
				}
				return nil
			}
			if err := v.RestoreHistory(backup, backupPassword, fixturePassword, BackupFingerprint(backup)); err == nil {
				t.Fatal("recovery did not reach the selected interruption")
			}
			if err := v.Close(); err != nil {
				t.Fatal(err)
			}
			v = open(t, interruptedDir, targetID)
			got := v.Status()
			if got.Account == nil || !sameWallet(*got.Account, original) || !got.Locked || !got.RecoveryRequired || got.FixtureOnly || got.FinancialExecution || got.WalletSigner {
				t.Fatal("restart did not preserve the original wallet and hold")
			}
			if err := v.VerifyBackupLocked(backup, backupPassword, fixturePassword, BackupFingerprint(backup)); err != nil {
				t.Fatal("interrupted recovery lost its original backup", err)
			}
		})
	}

	controlled, err := OpenControlledOwnerCustody(fixtureDir(t), targetID, "devnet")
	if err != nil {
		t.Fatal(err)
	}
	defer controlled.Close()
	if err = controlled.RestoreHistory(backup, backupPassword, fixturePassword, BackupFingerprint(backup)); err == nil {
		t.Fatal("owner backup entered a disposable fixture namespace")
	}
	if err = controlled.Create(fixturePassword); err != nil {
		t.Fatal(err)
	}
	if err = controlled.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	fixtureBackup, err := controlled.BackupHistory(backupPassword)
	if err != nil {
		t.Fatal("existing fixture backup failed", err)
	}
	fixture, err := unsealPortable(fixtureBackup, backupPassword, BackupFingerprint(fixtureBackup))
	if err != nil || !fixture.header.FixtureOnly {
		t.Fatal("fixture backup lost its provenance", err)
	}
	fixture.header.FixtureOnly = false
	fixture.header.Purpose = ownerPortablePurpose
	relabelledFixture, err := sealPortable(fixture, backupPassword)
	clearPortable(&fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err = controlled.VerifyBackup(relabelledFixture, backupPassword); err == nil {
		t.Fatal("fixture backup could be relabelled as an owner wallet")
	}
}
