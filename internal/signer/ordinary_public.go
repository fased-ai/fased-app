package signer

import (
	"crypto/ed25519"
	"encoding/hex"
)

const OrdinaryCustodyMode = "ordinary-custody:v1"
const ControlledCustodyProfile = "controlled-disposable:v1"
const OwnerCustodyProfile = "owner-authorized:v1"
const OwnerKeyProvenance = "owner-input:v1"

// CustodyAdmission is authenticated vault/genesis metadata, never a grant.
// The controlled provenance is immutable. No production profile is admitted.
type CustodyAdmission struct {
	Provenance string `json:"provenance"`
	Version    int    `json:"version"`
	Class      string `json:"class"`
	Profile    string `json:"profile"`
	Origin     string `json:"origin"`
}

// ValidContext also describes an empty signer; it does not admit a key.
func (a CustodyAdmission) ValidContext() bool {
	return a.Version == 1 && a.Class == OrdinaryCustodyMode && ((a.Profile == ControlledCustodyProfile && a.Provenance == ControlledCustodyProfile) || (a.Profile == OwnerCustodyProfile && (a.Provenance == ControlledCustodyProfile || a.Provenance == OwnerKeyProvenance)))
}
func (a CustodyAdmission) Valid() bool {
	return a.ValidContext() && (a.Origin == "generated" || a.Origin == "imported")
}

// There is no public runtime override. Enabling the reviewed owner binding
// cannot change the provenance/namespace of an existing controlled key.
func OrdinaryOwnerAdmissionEnabled() bool { return false }

func ValidOrdinaryAccountRef(r AccountRef) bool {
	b, err := hex.DecodeString(r.PublicKey)
	return r.Version == 3 && (r.Network == "devnet" || r.Network == "mainnet-beta") && validID(r.SignerID) && validID(r.WalletID) && validID(r.Epoch) && err == nil && len(b) == ed25519.PublicKeySize && hex.EncodeToString(b) == r.PublicKey
}
