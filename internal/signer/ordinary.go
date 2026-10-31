package signer

import (
	"crypto/ed25519"
	"errors"
	"os"
	"strings"
	"time"
)

const ordinaryNamespace = "ordinary-custody:v1/"
const ordinaryOwnerNamespace = "ordinary-owner:v1/"
const ordinaryOwnerTestNamespace = "ordinary-owner-controlled:v1/"

func ordinaryEnvelope(e envelope) bool {
	return e.Version == 4 && !e.FreshManaged && e.RecoveryRequired && ValidOrdinaryAccountRef(e.Ref) && e.Admission != nil && e.Admission.Valid()
}

func (v *Vault) vaultNetwork() string {
	if v.ordinary {
		if v.admissionContext.Profile == OwnerCustodyProfile {
			if v.admissionContext.Provenance == OwnerKeyProvenance {
				return ordinaryOwnerNamespace + v.network
			}
			return ordinaryOwnerTestNamespace + v.network
		}
		return ordinaryNamespace + v.network
	}
	return v.network
}

// Called only by compiled disposable-key composition. Normal Serve never calls it.
func OpenControlledOrdinaryCustody(dir, signerID, network string) (*Vault, error) {
	if !managedNetwork(network) {
		return nil, ErrUnavailable
	}
	return openVaultNetwork(dir, signerID, false, ordinaryNamespace+network)
}

// OpenOrdinaryCustody is the owner policy path. Normal admission is held.
func OpenOrdinaryCustody(dir, signerID, network string) (*Vault, error) {
	if !OrdinaryOwnerAdmissionEnabled() || !managedNetwork(network) {
		return nil, ErrUnavailable
	}
	return openVaultNetwork(dir, signerID, false, ordinaryOwnerNamespace+network)
}

// OpenControlledOwnerCustody exercises that owner policy with immutable test
// provenance. It is not a production-state conversion or startup override.
func OpenControlledOwnerCustody(dir, signerID, network string) (*Vault, error) {
	if !managedNetwork(network) {
		return nil, ErrUnavailable
	}
	return openVaultNetwork(dir, signerID, false, ordinaryOwnerTestNamespace+network)
}

func (v *Vault) ImportControlledOrdinarySeed(seed, password []byte) error {
	if !v.ordinary || len(seed) != ed25519.SeedSize {
		return ErrUnavailable
	}
	return v.createSeed(seed, password, true)
}

func ordinaryHistory(rs []journalRecord) (*AccountRef, *CustodyAdmission, error) {
	if len(rs) != 1 {
		return nil, nil, errors.New("ordinary custody requires complete key-only genesis; preserve history")
	}
	r := rs[0]
	if r.Kind != "ordinary-key-init" || r.Account == nil || !ValidOrdinaryAccountRef(*r.Account) || r.Admission == nil || !r.Admission.Valid() || !r.MissingHistory {
		return nil, nil, errors.New("ordinary custody provenance unavailable")
	}
	expected := journalRecord{Version: r.Version, Sequence: r.Sequence, Previous: r.Previous, Hash: r.Hash, At: r.At, Kind: r.Kind, Account: r.Account, Admission: r.Admission, MissingHistory: true}
	if recordHash(r) != recordHash(expected) {
		return nil, nil, errors.New("ordinary admission cannot carry authority")
	}
	ref, admission := *r.Account, *r.Admission
	return &ref, &admission, nil
}

func (v *Vault) openOrdinaryHistory() error {
	if _, _, err := v.store.readJournal("mock-receipts.log"); !errors.Is(err, os.ErrNotExist) {
		return ErrUnavailable
	}
	main, rs, err := v.store.readJournal("operations.log")
	if errors.Is(err, os.ErrNotExist) && v.record == nil {
		return nil
	}
	if err != nil {
		return err
	}
	ref, admission, err := ordinaryHistory(rs)
	if err != nil {
		return err
	}
	if ref.Network != v.network || admission.Profile != v.admissionContext.Profile || admission.Provenance != v.admissionContext.Provenance || v.record != nil && (!sameWallet(*ref, v.record.Ref) || v.record.Admission == nil || *admission != *v.record.Admission) {
		return errors.New("ordinary key/history identity mismatch")
	}
	v.managedRef = ref
	v.ledger = newLedger()
	v.ledger.ref = *ref
	v.ledger.main = main
	v.ledger.lastTime = rs[0].At
	return nil
}

func (s *storage) ensureOrdinaryHistory(e envelope) error {
	if !ordinaryEnvelope(e) {
		return ErrUnavailable
	}
	if _, _, err := s.readJournal("mock-receipts.log"); !errors.Is(err, os.ErrNotExist) {
		return ErrUnavailable
	}
	_, rs, err := s.readJournal("operations.log")
	if err == nil {
		ref, admission, err := ordinaryHistory(rs)
		if err != nil {
			return err
		}
		if !sameWallet(*ref, e.Ref) || *admission != *e.Admission {
			return errors.New("ordinary backup conflicts with retained provenance")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.appendJournal(&journal{name: "operations.log"}, journalRecord{Kind: "ordinary-key-init", Account: &e.Ref, Admission: e.Admission, MissingHistory: true, At: time.Now().Unix()})
}

func ordinaryContextForNetwork(network string) (string, CustodyAdmission, bool) {
	for _, binding := range []struct{ prefix, profile, provenance string }{
		{ordinaryNamespace, ControlledCustodyProfile, ControlledCustodyProfile},
		{ordinaryOwnerTestNamespace, OwnerCustodyProfile, ControlledCustodyProfile},
		{ordinaryOwnerNamespace, OwnerCustodyProfile, OwnerKeyProvenance},
	} {
		if strings.HasPrefix(network, binding.prefix) {
			return strings.TrimPrefix(network, binding.prefix), CustodyAdmission{Version: 1, Class: OrdinaryCustodyMode, Profile: binding.profile, Provenance: binding.provenance}, true
		}
	}
	return network, CustodyAdmission{}, false
}
func ordinaryStorageNetwork(network string) (string, bool) {
	public, _, ordinary := ordinaryContextForNetwork(network)
	return public, ordinary
}
