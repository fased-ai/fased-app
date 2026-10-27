package wenpm1

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strings"
)

// Key is exactly 32 public identity/hash bytes. It never stores a private key.
type Key [32]byte

func ParseKey(s string) (Key, error) {
	var k Key
	if len(s) != 64 || strings.ToLower(s) != s {
		return k, ErrInvalid
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return k, ErrInvalid
	}
	copy(k[:], b)
	return k, nil
}
func (k Key) String() string               { return hex.EncodeToString(k[:]) }
func (k Key) MarshalJSON() ([]byte, error) { return json.Marshal(k.String()) }
func (k *Key) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return ErrInvalid
	}
	v, err := ParseKey(s)
	if err != nil {
		return err
	}
	*k = v
	return nil
}

// Uint128 preserves the complete unsigned wire range without floating point.
type Uint128 struct {
	Lo uint64
	Hi uint64
}

func ParseUint128(s string) (Uint128, error) {
	var out Uint128
	if len(s) == 0 || len(s) > 39 {
		return out, ErrInvalid
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.Sign() < 0 || n.BitLen() > 128 || n.String() != s {
		return out, ErrInvalid
	}
	out.Lo = n.Uint64()
	out.Hi = new(big.Int).Rsh(n, 64).Uint64()
	return out, nil
}
func (n Uint128) String() string {
	v := new(big.Int).Lsh(new(big.Int).SetUint64(n.Hi), 64)
	return v.Or(v, new(big.Int).SetUint64(n.Lo)).String()
}
func (n Uint128) MarshalJSON() ([]byte, error) { return json.Marshal(n.String()) }
func (n *Uint128) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return ErrInvalid
	}
	v, err := ParseUint128(s)
	if err != nil {
		return err
	}
	*n = v
	return nil
}
