package signer

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

// secp256k1N exposes the secp256k1 group order for high-S boundary tests.
var secp256k1N = secp256k1.S256().N

// Keccak256 returns the legacy Keccak-256 digest used by Ethereum and EIP-712.
func Keccak256(parts ...[]byte) [32]byte {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		_, _ = h.Write(p)
	}
	var out [32]byte
	_ = h.Sum(out[:0])
	return out
}

// ECDSASignature is a low-S normalized 65-byte Ethereum/Hyperliquid signature.
type ECDSASignature struct {
	R [32]byte
	S [32]byte
	V uint8
}

func (s ECDSASignature) RHex() string { return "0x" + hex.EncodeToString(s.R[:]) }
func (s ECDSASignature) SHex() string { return "0x" + hex.EncodeToString(s.S[:]) }

// Hex encodes R || S || V as a 65-byte 0x-prefixed hex string.
func (s ECDSASignature) Hex() string {
	var raw [65]byte
	copy(raw[:32], s.R[:])
	copy(raw[32:64], s.S[:])
	raw[64] = s.V
	return "0x" + hex.EncodeToString(raw[:])
}

// ParseECDSASignature parses a 65-byte 0x-prefixed Ethereum signature.
func ParseECDSASignature(raw string) (ECDSASignature, error) {
	if !strings.HasPrefix(raw, "0x") || len(raw) != 132 {
		return ECDSASignature{}, errors.New("invalid ECDSA signature length")
	}
	b, err := hex.DecodeString(raw[2:])
	if err != nil || len(b) != 65 {
		return ECDSASignature{}, errors.New("invalid ECDSA signature hex")
	}
	var sig ECDSASignature
	copy(sig.R[:], b[:32])
	copy(sig.S[:], b[32:64])
	sig.V = b[64]
	if err := validateSignatureComponents(sig); err != nil {
		return ECDSASignature{}, err
	}
	return sig, nil
}

func validateSignatureComponents(sig ECDSASignature) error {
	if sig.V != 27 && sig.V != 28 {
		return errors.New("invalid ECDSA recovery id")
	}
	var r, s secp256k1.ModNScalar
	defer func() {
		r.Zero()
		s.Zero()
	}()
	if r.SetByteSlice(sig.R[:]) || r.IsZero() {
		return errors.New("invalid ECDSA signature r component")
	}
	if s.SetByteSlice(sig.S[:]) || s.IsZero() || s.IsOverHalfOrder() {
		return errors.New("non-canonical high-S or zero ECDSA signature")
	}
	return nil
}

// ValidateSecp256k1PrivateKey checks in constant time that priv is a 32-byte scalar in [1, n-1].
func ValidateSecp256k1PrivateKey(priv []byte) error {
	if len(priv) != 32 {
		return errors.New("secp256k1 private key must be 32 bytes")
	}
	var scalar secp256k1.ModNScalar
	defer scalar.Zero()
	if scalar.SetByteSlice(priv) || scalar.IsZero() {
		return errors.New("secp256k1 private key out of range")
	}
	return nil
}

// DeriveSecp256k1KeyFromSeed derives a domain-separated secp256k1 private key
// from an existing 32-byte vault seed using HMAC-SHA256 without sharing scalar bits
// with Ed25519 Solana keys.
func DeriveSecp256k1KeyFromSeed(seed []byte, domain string) ([]byte, error) {
	if len(seed) != 32 || domain == "" {
		return nil, errors.New("valid 32-byte seed and domain required")
	}
	for counter := range 256 {
		mac := hmac.New(sha256.New, seed)
		_, _ = mac.Write([]byte("fased-signer-secp256k1-v1:"))
		_, _ = mac.Write([]byte(domain))
		_, _ = mac.Write([]byte{0x00, byte(counter)})
		candidate := mac.Sum(nil)
		if ValidateSecp256k1PrivateKey(candidate) == nil {
			return candidate, nil
		}
		clear(candidate)
	}
	return nil, errors.New("failed to derive valid secp256k1 key")
}

// Secp256k1PublicKey derives the uncompressed (65B) and compressed (33B) public keys
// using constant-time scalar base multiplication via secp256k1/v4.
func Secp256k1PublicKey(priv []byte) (uncompressed65 []byte, compressed33 []byte, err error) {
	if err := ValidateSecp256k1PrivateKey(priv); err != nil {
		return nil, nil, err
	}
	privKey := secp256k1.PrivKeyFromBytes(priv)
	defer privKey.Zero()
	pub := privKey.PubKey()
	return pub.SerializeUncompressed(), pub.SerializeCompressed(), nil
}

// EthereumAddressFromPublicKey computes the EIP-55 checksummed Ethereum address
// for an uncompressed 65-byte secp256k1 public key.
func EthereumAddressFromPublicKey(uncompressed65 []byte) (string, error) {
	if len(uncompressed65) != 65 || uncompressed65[0] != 0x04 {
		return "", errors.New("expected 65-byte uncompressed secp256k1 public key")
	}
	pub, err := secp256k1.ParsePubKey(uncompressed65)
	if err != nil {
		return "", errors.New("public key not on secp256k1 curve")
	}
	canon := pub.SerializeUncompressed()
	digest := Keccak256(canon[1:65])
	return eip55Encode(digest[12:32]), nil
}

func eip55Encode(addr20 []byte) string {
	lower := hex.EncodeToString(addr20)
	hash := Keccak256([]byte(lower))
	out := make([]byte, 42)
	out[0], out[1] = '0', 'x'
	for i := 0; i < 40; i++ {
		c := lower[i]
		nibble := (hash[i/2] >> 4) & 0x0f
		if i%2 == 1 {
			nibble = hash[i/2] & 0x0f
		}
		if c >= 'a' && c <= 'f' && nibble >= 8 {
			c -= 'a' - 'A'
		}
		out[2+i] = c
	}
	return string(out)
}

// ValidateEthereumAddress validates a 0x-prefixed 20-byte Ethereum address, enforces
// EIP-55 checksum validity on mixed-case addresses, and returns its canonical EIP-55 form.
func ValidateEthereumAddress(addr string) (string, error) {
	if !strings.HasPrefix(addr, "0x") || len(addr) != 42 {
		return "", errors.New("Ethereum address must be 0x followed by 40 hex chars")
	}
	rawHex := addr[2:]
	decoded, err := hex.DecodeString(rawHex)
	if err != nil || len(decoded) != 20 {
		return "", errors.New("invalid Ethereum address hex")
	}
	checksummed := eip55Encode(decoded)
	lowered := "0x" + strings.ToLower(rawHex)
	uppered := "0x" + strings.ToUpper(rawHex)
	if addr != lowered && addr != uppered && addr != checksummed {
		return "", errors.New("invalid EIP-55 Ethereum address checksum")
	}
	return checksummed, nil
}

// SignSecp256k1Digest computes a constant-time deterministic RFC 6979 low-S ECDSA signature
// with EIP-155 legacy recovery byte V in {27, 28}.
func SignSecp256k1Digest(priv []byte, digest [32]byte) (ECDSASignature, error) {
	if err := ValidateSecp256k1PrivateKey(priv); err != nil {
		return ECDSASignature{}, err
	}
	privKey := secp256k1.PrivKeyFromBytes(priv)
	defer privKey.Zero()

	compact := ecdsa.SignCompact(privKey, digest[:], false)
	defer clear(compact)
	if len(compact) != 65 || (compact[0] != 27 && compact[0] != 28) {
		return ECDSASignature{}, errors.New("non-canonical compact ECDSA signature recovery id")
	}
	var sig ECDSASignature
	sig.V = compact[0]
	copy(sig.R[:], compact[1:33])
	copy(sig.S[:], compact[33:65])
	if err := validateSignatureComponents(sig); err != nil {
		return ECDSASignature{}, err
	}
	return sig, nil
}

// RecoverSecp256k1PublicKey recovers the 65-byte uncompressed public key from a
// low-S 65-byte ECDSA signature and digest.
func RecoverSecp256k1PublicKey(digest [32]byte, sig ECDSASignature) ([]byte, error) {
	if err := validateSignatureComponents(sig); err != nil {
		return nil, err
	}
	var compact [65]byte
	compact[0] = sig.V
	copy(compact[1:33], sig.R[:])
	copy(compact[33:65], sig.S[:])
	pub, compressed, err := ecdsa.RecoverCompact(compact[:], digest[:])
	if err != nil || compressed {
		return nil, errors.New("ecrecover failed on compact secp256k1 signature")
	}
	return pub.SerializeUncompressed(), nil
}

// VerifySecp256k1Signature recovers the signer address and compares it in
// constant time to the canonicalized expected Ethereum address.
func VerifySecp256k1Signature(expectedAddr string, digest [32]byte, sig ECDSASignature) error {
	canonExpected, err := ValidateEthereumAddress(expectedAddr)
	if err != nil {
		return err
	}
	pub, err := RecoverSecp256k1PublicKey(digest, sig)
	if err != nil {
		return err
	}
	recovered, err := EthereumAddressFromPublicKey(pub)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(recovered), []byte(canonExpected)) != 1 {
		return errors.New("ECDSA signature signer address mismatch")
	}
	return nil
}
