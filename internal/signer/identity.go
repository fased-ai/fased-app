package signer

import (
	"encoding/hex"
	"math/big"
)

// PublicIdentity is an address-format projection, not a chain account or RPC
// verification. Network custody identities grant no signing authority.
type PublicIdentity struct {
	Version     int    `json:"version"`
	Chain       string `json:"chain"`
	Address     string `json:"address"`
	Network     string `json:"network"`
	FixtureOnly bool   `json:"fixtureOnly"`
}

func (r AccountRef) PublicIdentity() *PublicIdentity {
	if !validRef(r) && !managedRefValid(r) {
		return nil
	}
	key, _ := hex.DecodeString(r.PublicKey)
	return &PublicIdentity{Version: r.Version, Chain: "solana", Address: base58PublicKey(key), Network: r.Network, FixtureOnly: true}
}

// Base58 is public address formatting only; no key generation or cryptography.
func base58PublicKey(key []byte) string {
	const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	n := new(big.Int).SetBytes(key)
	base, remainder := big.NewInt(58), new(big.Int)
	out := make([]byte, 0, 44)
	for n.Sign() > 0 {
		n.QuoRem(n, base, remainder)
		out = append(out, alphabet[remainder.Int64()])
	}
	for _, b := range key {
		if b != 0 {
			break
		}
		out = append(out, '1')
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

// PublicStatus decorates an authenticated status for UI/CLI output only. Never
// add this field to the strict version-1 signer IPC response.
type PublicStatus struct {
	Status
	PublicIdentity *PublicIdentity `json:"publicIdentity,omitempty"`
}

func (s Status) PublicView() PublicStatus {
	p := PublicStatus{Status: s}
	if s.Account != nil {
		p.PublicIdentity = s.Account.PublicIdentity()
	}
	return p
}
