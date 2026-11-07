//go:build linux

package signer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// BackupHistory uses the existing portable format while holding the same vault
// mutex as dispatch. Its encrypted key uses the archive password, so independent
// recovery requires no still-running source or additional source-key prompt.
func (v *Vault) BackupHistory(password []byte) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.store.failedState() || v.record == nil || v.ledger == nil || !v.unlocked() {
		return nil, ErrLocked
	}
	if v.ordinary && v.record.Admission.Provenance != ControlledCustodyProfile {
		return nil, ErrUnavailable
	}
	e, err := sealAdmissionHistory(v.seed, password, v.record.Ref, "vault", v.record.RecoveryRequired, v.record.FreshManaged, v.record.Admission, v.record.RecoveredHistorySHA256)
	if err != nil {
		return nil, err
	}
	var d portableData
	defer clearPortable(&d)
	d.files[0], err = json.Marshal(e)
	if err == nil {
		d.files[1], err = portableJournal(v.store, "operations.log")
	}
	if err == nil && v.network == "" {
		d.files[2], err = portableJournal(v.store, "mock-receipts.log")
	}
	if err != nil {
		return nil, err
	}
	d.header = portableHeader{Version: 1, Purpose: portablePurpose, FixtureOnly: true, CapturedAt: v.now().Unix(), ContractSHA256: portableContract(v.record.Ref)}
	d.header.History, err = historyForLocked(v, d.files)
	if err != nil {
		return nil, err
	}
	return sealPortable(d, password)
}
func clearPortable(d *portableData) {
	for _, b := range d.files {
		clear(b)
	}
}

func (v *Vault) verifyHistoryBackup(document, password []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.store.failedState() || v.record == nil || !v.unlocked() {
		return ErrLocked
	}
	d, err := unsealPortable(document, password, BackupFingerprint(document))
	defer clearPortable(&d)
	if err != nil {
		return err
	}
	if err = validatePortable(d, v.store.path); err != nil {
		return err
	}
	var e envelope
	if decode(d.files[0], &e) != nil || !sameWallet(e.Ref, v.record.Ref) {
		return ErrCredential
	}
	seed, err := unseal(e, password, "vault")
	defer clear(seed)
	if err != nil || !bytes.Equal(seed, v.seed) {
		return ErrCredential
	}
	// On a restored wallet the retained history must be the archive's exact
	// history or its validated append-only extension. Never rewind newer data.
	if v.record.RecoveredHistorySHA256 != "" {
		if v.record.RecoveredHistorySHA256 != BackupFingerprint(document) {
			return ErrCredential
		}
		for i, name := range []string{"operations.log", "mock-receipts.log"} {
			if i == 1 && len(d.files[2]) == 0 {
				continue
			}
			if _, _, err = v.store.readJournal(name); err != nil {
				return err
			}
			b, err := portableJournal(v.store, name)
			if err != nil || !bytes.HasPrefix(b, d.files[i+1]) {
				return errors.New("recovered history differs from selected backup")
			}
		}
	}
	return nil
}

// RestoreHistory is owner-only. Historical files never prove source retirement
// or latest chain state. The AEAD-bound marker is a permanent recovery hold;
// legacy readers reject the unknown field, and no authority rebind clears it.
func (v *Vault) RestoreHistory(document, password, newPassword []byte, pin string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.store.failedState() || v.record != nil || (v.ledger == nil && v.managedRef != nil) {
		return errors.New("history recovery requires an empty key slot; preserve existing state")
	}
	if v.ledger != nil && v.ledger.activeUpdate != "" {
		return errors.New("active update requires its original recovery")
	}
	d, err := unsealPortable(document, password, pin)
	defer clearPortable(&d)
	if err != nil {
		return err
	}
	var source envelope
	if decode(d.files[0], &source) != nil || !envelopeForNetwork(source, v.vaultNetwork()) {
		return ErrUnavailable
	}
	if err = validatePortable(d, v.store.path); err != nil {
		return err
	}
	if v.ledger != nil {
		if !sameWallet(v.ledger.ref, source.Ref) {
			return errors.New("backup conflicts with retained wallet history")
		}
		for i, name := range []string{"operations.log", "mock-receipts.log"} {
			if i == 1 && len(d.files[2]) == 0 {
				continue
			}
			if _, _, err = v.store.readJournal(name); err != nil {
				return err
			}
			b, e := portableJournal(v.store, name)
			if e != nil || !bytes.HasPrefix(b, d.files[i+1]) {
				return errors.New("retained history is not an extension of the selected backup")
			}
		}
	}
	seed, err := unseal(source, password, "vault")
	defer clear(seed)
	if err != nil {
		return err
	}
	ref := source.Ref
	ref.SignerID = v.signerID
	ref.Epoch = randomID()
	restored, err := sealAdmissionHistory(seed, newPassword, ref, "vault", true, false, source.Admission, pin)
	if err != nil {
		return err
	}
	// Reuse the original quarantine transaction. Its exact interrupted prefixes
	// resume, foreign files refuse, and original evidence is never rewritten.
	if _, err = RestorePortableSnapshot(filepath.Join(v.store.path, "recovery-history"), document, password, pin); err != nil {
		return err
	}
	return v.save(restored)
}

// Called by the existing vault.pending publication AND startup recovery. No
// secret or source is needed to complete the same already-authorized transaction.
func (s *storage) ensureRecoveredHistory(e envelope) error {
	q, err := os.OpenRoot(filepath.Join(s.path, "recovery-history"))
	if err != nil {
		return err
	}
	defer q.Close()
	rb, err := readPortableFile(q, "receipt.json")
	if err != nil {
		return err
	}
	var receipt PortableReceipt
	if decode(rb, &receipt) != nil || receipt.ArchiveSHA256 != e.RecoveredHistorySHA256 || !receipt.Historical || !receipt.ReadOnly || !receipt.RecoveryRequired || receipt.InstallAllowed || receipt.ResumeAllowed || receipt.WalletSigner || receipt.FinancialExecution || receipt.AutomaticUpdates || !sameWallet(receipt.History.Account, e.Ref) {
		return errors.New("recovery receipt does not bind this wallet")
	}
	var d portableData
	defer clearPortable(&d)
	d.header = portableHeader{Version: receipt.Version, Purpose: receipt.Purpose, FixtureOnly: receipt.FixtureOnly, CapturedAt: receipt.CapturedAt, ContractSHA256: portableContract(receipt.History.Account), History: receipt.History}
	for i, name := range portableNames {
		d.files[i], err = readPortableFile(q, name)
		if err != nil {
			return err
		}
	}
	if receipt.Version != 1 || receipt.Purpose != portablePurpose || !receipt.FixtureOnly {
		return ErrUnavailable
	}
	if err = validatePortable(d, s.path); err != nil {
		return err
	}
	for i, name := range []string{"operations.log", "mock-receipts.log"} {
		if i == 1 && len(d.files[2]) == 0 {
			if _, _, err = s.readJournal(name); !errors.Is(err, os.ErrNotExist) {
				return errors.New("foreign receipt history preserved")
			}
			continue
		}
		if err = s.publishRecoveredHistory(name, d.files[i+1]); err != nil {
			return err
		}
	}
	return nil
}
func (s *storage) publishRecoveredHistory(name string, want []byte) error {
	b, err := portableJournal(s, name)
	if err == nil {
		if _, _, err = s.readJournal(name); err != nil {
			return err
		}
		if !bytes.HasPrefix(b, want) {
			return errors.New("existing financial history is preserved")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Atomic, non-replacing publication: interruption cannot expose a partial
	// final journal. Leftover private temporary files are not adopted as history.
	f, err := os.CreateTemp(s.path, ".recovery-history-")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	n, err := f.Write(want)
	if err == nil && n != len(want) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = unix.Renameat2(s.dir, filepath.Base(temp), s.dir, name, unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	if err = unix.Fsync(s.dir); err != nil {
		return err
	}
	return s.checkpoint("recovery:" + name)
}
