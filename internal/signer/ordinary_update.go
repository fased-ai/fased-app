package signer

import (
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
	"os"
)

func (v *Vault) ordinaryUpdateSnapshot(probe string) (update.SignerSnapshot, error) {
	if !update.IDOK(probe) || v.closed || v.store.failedState() || v.record == nil || v.managedRef == nil || v.ledger == nil {
		return update.SignerSnapshot{}, ErrUnavailable
	}
	b, err := v.store.read()
	if err != nil {
		return update.SignerSnapshot{}, err
	}
	var disk envelope
	if decode(b, &disk) != nil || update.SnapshotDigest("vault", disk) != update.SnapshotDigest("vault", *v.record) {
		return update.SignerSnapshot{}, errors.New("durable ordinary vault changed")
	}
	main, records, err := v.store.readJournal("operations.log")
	if err != nil {
		return update.SignerSnapshot{}, err
	}
	ref, admission, err := ordinaryHistory(records)
	if err != nil || !sameWallet(*ref, v.record.Ref) || *ref != *v.managedRef || v.record.Admission == nil || *admission != *v.record.Admission || main.seq != v.ledger.main.seq || main.hash != v.ledger.main.hash {
		return update.SignerSnapshot{}, errors.New("durable ordinary history changed")
	}
	if _, _, err := v.store.readJournal("mock-receipts.log"); !errors.Is(err, os.ErrNotExist) {
		return update.SignerSnapshot{}, ErrUnavailable
	}
	features := []string{"ordinary-key-genesis-v1"}
	if len(records) > 1 {
		features = append(features, "ordinary-maintenance-v1", "install-completion-v1")
	}
	vaultFeatures := []string{"ordinary-custody-admission-v1", "key-only-recovery-hold-v1"}
	if disk.RecoveredHistorySHA256 != "" {
		vaultFeatures = append(vaultFeatures, historyRecoveryFeature)
	}
	if admission.Profile == OwnerCustodyProfile {
		vaultFeatures = append(vaultFeatures, "owner-profile-v1")
	}
	if admission.Provenance == ControlledCustodyProfile {
		vaultFeatures = append(vaultFeatures, "controlled-provenance-v1")
	} else {
		vaultFeatures = append(vaultFeatures, "owner-input-provenance-v1")
	}
	return update.SignerSnapshot{Version: 3, ProbeID: probe, BootID: v.bootID, AuthoritySHA256: update.SnapshotDigest("ordinary-authority", struct {
		Refs      []AccountRef
		Admission CustodyAdmission
	}{[]AccountRef{v.record.Ref, *ref}, *admission}), RecoveryHold: true, SequenceFloor: v.ledger.updateFloor, InstalledSequence: v.ledger.installedSequence, ActiveIntentID: v.ledger.activeUpdate, States: []update.State{
		{Name: "vault", Version: 4, Features: vaultFeatures, Anchor: update.SnapshotDigest("vault-bytes", b)},
		{Name: "operations", Version: 1, Features: features, Anchor: update.SnapshotDigest("ordinary-history-head", struct {
			Sequence int
			Hash     string
		}{main.seq, main.hash})},
	}}, nil
}
