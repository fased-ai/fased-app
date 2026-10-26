package signer

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"os"
	"time"
)

// The network-bound key lifecycle is available only to an explicit version-2
// disposable custody configuration. Ordinary installers still generate version 1.
// Version 2 is custody only and never satisfies fixture grant authority.
func managedNetwork(network string) bool       { return network == "mainnet-beta" || network == "devnet" }
func ValidManagedAccountRef(r AccountRef) bool { return managedRefValid(r) }

func managedRefValid(r AccountRef) bool {
	b, err := hex.DecodeString(r.PublicKey)
	return r.Version == 2 && managedNetwork(r.Network) && validID(r.SignerID) && validID(r.WalletID) && validID(r.Epoch) && err == nil && len(b) == 32 && hex.EncodeToString(b) == r.PublicKey
}
func validEnvelopeIdentity(e envelope) bool {
	return e.Version == 1 && validRef(e.Ref) || e.Version == 2 && e.RecoveryRequired && managedRefValid(e.Ref)
}
func envelopeForNetwork(e envelope, network string) bool {
	if network == "" {
		return e.Version == 1 && validRef(e.Ref)
	}
	return e.Version == 2 && managedRefValid(e.Ref) && e.Ref.Network == network
}

func (v *Vault) importManagedSeed(seed, password []byte) error {
	if !managedNetwork(v.network) || len(seed) != ed25519.SeedSize {
		return ErrUnavailable
	}
	return v.createSeed(seed, password, true)
}

// There are no financial operations in this format. Preserve an immutable key
// lifecycle record rather than minting fixture grants or mock transaction history.
func managedHistory(records []journalRecord) (*AccountRef, error) {
	if len(records) != 1 {
		return nil, errors.New("incomplete managed key history")
	}
	r := records[0]
	if r.Kind != "managed-key-init" || r.Account == nil || !managedRefValid(*r.Account) || !r.MissingHistory {
		return nil, errors.New("invalid managed key history")
	}
	expected := journalRecord{Version: r.Version, Sequence: r.Sequence, Previous: r.Previous, Hash: r.Hash, At: r.At, Kind: r.Kind, Account: r.Account, MissingHistory: true}
	if recordHash(r) != recordHash(expected) {
		return nil, errors.New("managed key history cannot carry financial authority")
	}
	ref := *r.Account
	return &ref, nil
}
func (v *Vault) openManagedHistory() error {
	// A mixed fixture/network directory is never adopted or silently repaired.
	if _, _, err := v.store.readJournal("mock-receipts.log"); !errors.Is(err, os.ErrNotExist) {
		return errors.New("mixed or unreadable managed key history")
	}
	_, records, err := v.store.readJournal("operations.log")
	if errors.Is(err, os.ErrNotExist) && v.record == nil {
		return nil
	}
	if err != nil {
		return err
	}
	ref, err := managedHistory(records)
	if err != nil {
		return err
	}
	if ref.Network != v.network || v.record != nil && !sameWallet(*ref, v.record.Ref) {
		return errors.New("managed key history identity mismatch")
	}
	v.managedRef = ref
	return nil
}
func (s *storage) ensureManagedHistory(e envelope) error {
	if _, _, err := s.readJournal("mock-receipts.log"); !errors.Is(err, os.ErrNotExist) {
		return errors.New("fixture history cannot become managed custody")
	}
	_, records, err := s.readJournal("operations.log")
	if err == nil {
		ref, err := managedHistory(records)
		if err != nil {
			return err
		}
		if !sameWallet(*ref, e.Ref) {
			return errors.New("managed backup conflicts with retained key history")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.appendJournal(&journal{name: "operations.log"}, journalRecord{Kind: "managed-key-init", Account: &e.Ref, MissingHistory: true, At: time.Now().Unix()})
}
