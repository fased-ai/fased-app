package signer

import (
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
)

// FormatPublicKey and ParsePublicAddress format public metadata only. Neither
// function accepts a seed, imports a key into custody, or grants authority.
func FormatPublicKey(publicKey string) (string, error) {
	key, err := hex.DecodeString(publicKey)
	if err != nil || len(key) != 32 || hex.EncodeToString(key) != publicKey {
		return "", errors.New("invalid public key")
	}
	return base58PublicKey(key), nil
}

func ParsePublicAddress(address string) (string, error) {
	fail := errors.New("enter a valid Solana public address")
	if len(address) < 32 || len(address) > 44 {
		return "", fail
	}
	const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	n, base := new(big.Int), big.NewInt(58)
	for i := range len(address) {
		v := strings.IndexByte(alphabet, address[i])
		if v < 0 {
			return "", fail
		}
		n.Mul(n, base)
		n.Add(n, big.NewInt(int64(v)))
	}
	b := n.Bytes()
	if len(b) > 32 {
		return "", fail
	}
	key := make([]byte, 32)
	copy(key[32-len(b):], b)
	if base58PublicKey(key) != address {
		return "", fail
	}
	return hex.EncodeToString(key), nil
}
