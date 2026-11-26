package walletvault

import (
	"crypto/ed25519"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var base58DecodeMap = func() [256]int8 {
	var m [256]int8
	for i := range m {
		m[i] = -1
	}
	for i := 0; i < len(base58Alphabet); i++ {
		m[base58Alphabet[i]] = int8(i)
	}
	return m
}()

// Base58Encode encodes raw bytes using the Bitcoin/Solana Base58 alphabet.
func Base58Encode(input []byte) string {
	if len(input) == 0 {
		return ""
	}
	x := new(big.Int).SetBytes(input)
	base := big.NewInt(58)
	zero := big.NewInt(0)
	mod := new(big.Int)
	var out []byte
	for x.Cmp(zero) > 0 {
		x.DivMod(x, base, mod)
		out = append(out, base58Alphabet[mod.Int64()])
	}
	for _, b := range input {
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

// Base58Decode decodes a Bitcoin/Solana Base58 string.
func Base58Decode(s string) ([]byte, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil, errors.New("empty base58 string")
	}
	x := big.NewInt(0)
	base := big.NewInt(58)
	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]
		v := base58DecodeMap[ch]
		if v < 0 {
			return nil, fmt.Errorf("invalid base58 character %q", ch)
		}
		x.Mul(x, base)
		x.Add(x, big.NewInt(int64(v)))
	}
	decoded := x.Bytes()
	leadingOnes := 0
	for i := 0; i < len(trimmed) && trimmed[i] == '1'; i++ {
		leadingOnes++
	}
	out := make([]byte, leadingOnes+len(decoded))
	copy(out[leadingOnes:], decoded)
	return out, nil
}

// ValidateSolanaPublicAddress verifies a Base58 32-byte Ed25519 Solana address.
func ValidateSolanaPublicAddress(addr string) (string, error) {
	clean := strings.TrimSpace(addr)
	b, err := Base58Decode(clean)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return "", errors.New("invalid Solana public address")
	}
	if Base58Encode(b) != clean {
		return "", errors.New("non-canonical Solana address")
	}
	return clean, nil
}

// DeriveSLIP0010Ed25519 derives an Ed25519 32-byte seed along hardened BIP-32/SLIP-0010 indices.
func DeriveSLIP0010Ed25519(bip39Seed []byte, path ...uint32) ([]byte, error) {
	if len(bip39Seed) == 0 {
		return nil, errors.New("seed required")
	}
	sum := hmacSHA512([]byte("ed25519 seed"), bip39Seed)
	key := append([]byte(nil), sum[:32]...)
	chain := append([]byte(nil), sum[32:]...)
	defer clear(chain)

	data := make([]byte, 1+32+4)
	defer clear(data)
	for _, idx := range path {
		hardened := idx | 0x80000000
		data[0] = 0x00
		copy(data[1:33], key)
		binary.BigEndian.PutUint32(data[33:37], hardened)
		next := hmacSHA512(chain, data)
		clear(key)
		clear(chain)
		key = append(key[:0], next[:32]...)
		chain = append(chain[:0], next[32:]...)
	}
	return key, nil
}

// KeypairFromMnemonic derives standard Solana m/44'/501'/account'/0' Ed25519 keypair.
func KeypairFromMnemonic(phrase string, account uint32) (ed25519.PrivateKey, error) {
	seed, err := MnemonicToSeed(phrase, "")
	if err != nil {
		return nil, err
	}
	defer clear(seed)
	privSeed, err := DeriveSLIP0010Ed25519(seed, 44, 501, account, 0)
	if err != nil {
		return nil, err
	}
	defer clear(privSeed)
	return ed25519.NewKeyFromSeed(privSeed), nil
}

// KeypairFromPrivateInput accepts either Base58 secret key (64 or 32 bytes) or Solana CLI JSON byte array.
func KeypairFromPrivateInput(raw string) (ed25519.PrivateKey, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "[") {
		var nums []int
		if err := json.Unmarshal([]byte(trimmed), &nums); err != nil {
			return nil, errors.New("invalid Solana JSON keypair")
		}
		if len(nums) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("Solana JSON keypair must contain 64 bytes (got %d)", len(nums))
		}
		priv := make([]byte, ed25519.PrivateKeySize)
		for i, n := range nums {
			if n < 0 || n > 255 {
				clear(priv)
				return nil, errors.New("invalid byte value in JSON keypair")
			}
			priv[i] = byte(n)
		}
		pk := ed25519.PrivateKey(priv)
		expectedPub := ed25519.NewKeyFromSeed(pk.Seed()).Public().(ed25519.PublicKey)
		if string(pk.Public().(ed25519.PublicKey)) != string(expectedPub) {
			clear(priv)
			return nil, errors.New("corrupt Ed25519 public bytes in JSON keypair")
		}
		return pk, nil
	}
	b, err := Base58Decode(trimmed)
	if err != nil {
		return nil, errors.New("invalid Base58 private key")
	}
	switch len(b) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(b), nil
	case ed25519.PrivateKeySize:
		pk := ed25519.PrivateKey(b)
		expectedPub := ed25519.NewKeyFromSeed(pk.Seed()).Public().(ed25519.PublicKey)
		if string(pk.Public().(ed25519.PublicKey)) != string(expectedPub) {
			clear(b)
			return nil, errors.New("corrupt Ed25519 private key")
		}
		return pk, nil
	default:
		clear(b)
		return nil, fmt.Errorf("invalid private key length: %d", len(b))
	}
}

// KeypairToSolanaJSON renders a 64-byte Solana CLI compatible JSON keypair string.
func KeypairToSolanaJSON(pk ed25519.PrivateKey) string {
	ints := make([]int, len(pk))
	for i, b := range pk {
		ints[i] = int(b)
	}
	raw, _ := json.Marshal(ints)
	return string(raw)
}
