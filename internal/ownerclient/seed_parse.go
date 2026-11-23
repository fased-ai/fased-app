package ownerclient

import (
	"bytes"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// parseImportSeed decodes a Solana secret seed or 64-byte keypair from 64-char
// hex, Solana CLI JSON byte array ([32] or [64]), or Base58 ([32] or [64]),
// returning a fresh 32-byte seed while scrubbing temporary buffers.
func parseImportSeed(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("secret seed required")
	}
	if strings.HasPrefix(trimmed, "[") {
		if len(trimmed) > 1024 {
			return nil, errors.New("Solana JSON keypair exceeds bound")
		}
		var nums []int
		if err := json.Unmarshal([]byte(trimmed), &nums); err != nil {
			return nil, errors.New("invalid Solana JSON keypair")
		}
		defer clear(nums)
		if len(nums) != 32 && len(nums) != 64 {
			return nil, errors.New("Solana JSON keypair must contain 32 or 64 bytes")
		}
		buf := make([]byte, len(nums))
		defer clear(buf)
		for i, n := range nums {
			if n < 0 || n > 255 {
				return nil, errors.New("Solana JSON keypair byte out of range")
			}
			buf[i] = byte(n)
		}
		return validateAndExtractSeed(buf)
	}
	if len(trimmed) == 64 {
		decoded, err := hex.DecodeString(trimmed)
		if err != nil {
			return nil, errors.New("invalid hex seed")
		}
		defer clear(decoded)
		return validateAndExtractSeed(decoded)
	}
	decoded, err := decodeBase58(trimmed)
	if err != nil {
		return nil, err
	}
	defer clear(decoded)
	return validateAndExtractSeed(decoded)
}

func validateAndExtractSeed(buf []byte) ([]byte, error) {
	if len(buf) != 32 && len(buf) != 64 {
		return nil, errors.New("seed must decode to 32 or 64 bytes")
	}
	if len(buf) == 64 {
		priv := ed25519.NewKeyFromSeed(buf[:32])
		defer clear(priv)
		if subtle.ConstantTimeCompare(priv.Public().(ed25519.PublicKey), buf[32:]) != 1 {
			return nil, errors.New("keypair public key mismatch")
		}
	}
	return bytes.Clone(buf[:32]), nil
}

func decodeBase58(s string) ([]byte, error) {
	if len(s) == 0 || len(s) > 128 {
		return nil, errors.New("invalid Base58 length")
	}
	digits := make([]byte, len(s))
	defer clear(digits)
	zeros := 0
	for zeros < len(s) && s[zeros] == '1' {
		zeros++
	}
	length := 0
	for i := zeros; i < len(s); i++ {
		carry := strings.IndexByte(base58Alphabet, s[i])
		if carry < 0 {
			return nil, errors.New("invalid Base58 character")
		}
		k := 0
		for j := len(digits) - 1; j >= 0 && (carry != 0 || k < length); j-- {
			carry += 58 * int(digits[j])
			digits[j] = byte(carry % 256)
			carry /= 256
			k++
		}
		length = k
	}
	out := make([]byte, zeros+length)
	copy(out[zeros:], digits[len(digits)-length:])
	return out, nil
}
