package onchain

import (
	"crypto/sha256"
	"errors"
	"math/big"
	"strings"

	"filippo.io/edwards25519"
)

const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

func Base58(raw []byte) string {
	n := new(big.Int).SetBytes(raw)
	base := big.NewInt(58)
	out := []byte{}
	for n.Sign() > 0 {
		r := new(big.Int)
		n.QuoRem(n, base, r)
		out = append(out, alphabet[r.Int64()])
	}
	for _, b := range raw {
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
func Key(s string) ([]byte, error) {
	if len(s) < 32 || len(s) > 44 {
		return nil, errors.New("invalid Solana address")
	}
	n := new(big.Int)
	for _, c := range s {
		i := strings.IndexRune(alphabet, c)
		if i < 0 {
			return nil, errors.New("invalid Solana address")
		}
		n.Mul(n, big.NewInt(58))
		n.Add(n, big.NewInt(int64(i)))
	}
	b := n.Bytes()
	leading := 0
	for _, c := range s {
		if c != '1' {
			break
		}
		leading++
	}
	out := append(make([]byte, leading), b...)
	if len(out) != 32 || Base58(out) != s {
		return nil, errors.New("noncanonical Solana address")
	}
	return out, nil
}
func TokenAccount(owner, mint string) (string, error) {
	o, e := Key(owner)
	if e != nil {
		return "", e
	}
	m, e := Key(mint)
	if e != nil {
		return "", e
	}
	t, _ := Key(Token)
	p, _ := Key(AssociatedToken)
	for bump := 255; bump >= 0; bump-- {
		data := append(append(append(append([]byte{}, o...), t...), m...), byte(bump))
		data = append(data, p...)
		data = append(data, []byte("ProgramDerivedAddress")...)
		h := sha256.Sum256(data)
		if _, e := new(edwards25519.Point).SetBytes(h[:]); e != nil {
			return Base58(h[:]), nil
		}
	}
	return "", errors.New("token account derivation failed")
}
