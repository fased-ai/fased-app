package signer

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"

	"golang.org/x/crypto/sha3"
)

var (
	secp256k1P     = mustBigHex("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEFFFFFC2F")
	secp256k1N     = mustBigHex("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141")
	secp256k1HalfN = new(big.Int).Rsh(secp256k1N, 1)
	secp256k1Gx    = mustBigHex("79BE667EF9DCBBAC55A06295CE870B07029BFCDB2DCE28D959F2815B16F81798")
	secp256k1Gy    = mustBigHex("483ADA7726A3C4655DA4FBFC0E1108A8FD17B448A68554199C47D08FFB10D4B8")
	secp256k1SqrtE = new(big.Int).Rsh(new(big.Int).Add(secp256k1P, big.NewInt(1)), 2)
	secp256k1B     = big.NewInt(7)
)

func mustBigHex(s string) *big.Int {
	v, ok := new(big.Int).SetString(s, 16)
	if !ok {
		panic("invalid secp256k1 constant")
	}
	return v
}

func wipeBigInt(v *big.Int) {
	if v == nil {
		return
	}
	clear(v.Bits())
	v.SetInt64(0)
}

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
	r := new(big.Int).SetBytes(sig.R[:])
	s := new(big.Int).SetBytes(sig.S[:])
	if r.Sign() <= 0 || r.Cmp(secp256k1N) >= 0 {
		return errors.New("invalid ECDSA signature r component")
	}
	if s.Sign() <= 0 || s.Cmp(secp256k1HalfN) > 0 {
		return errors.New("non-canonical high-S or zero ECDSA signature")
	}
	return nil
}

// ValidateSecp256k1PrivateKey checks that priv is a 32-byte scalar in [1, n-1].
func ValidateSecp256k1PrivateKey(priv []byte) error {
	if len(priv) != 32 {
		return errors.New("secp256k1 private key must be 32 bytes")
	}
	d := new(big.Int).SetBytes(priv)
	defer wipeBigInt(d)
	if d.Sign() <= 0 || d.Cmp(secp256k1N) >= 0 {
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

func secp256k1IsOnCurve(x, y *big.Int) bool {
	if x == nil || y == nil || x.Sign() < 0 || x.Cmp(secp256k1P) >= 0 || y.Sign() < 0 || y.Cmp(secp256k1P) >= 0 {
		return false
	}
	y2 := new(big.Int).Mul(y, y)
	y2.Mod(y2, secp256k1P)
	x3 := new(big.Int).Mul(x, x)
	x3.Mod(x3, secp256k1P)
	x3.Mul(x3, x)
	x3.Mod(x3, secp256k1P)
	x3.Add(x3, secp256k1B)
	x3.Mod(x3, secp256k1P)
	return y2.Cmp(x3) == 0
}

func secp256k1Double(x1, y1 *big.Int) (*big.Int, *big.Int) {
	if x1 == nil || y1 == nil || y1.Sign() == 0 {
		return nil, nil
	}
	x2 := new(big.Int).Mul(x1, x1)
	x2.Mod(x2, secp256k1P)
	threeX2 := new(big.Int).Mul(big.NewInt(3), x2)
	threeX2.Mod(threeX2, secp256k1P)

	twoY := new(big.Int).Mul(big.NewInt(2), y1)
	twoY.Mod(twoY, secp256k1P)
	invTwoY := new(big.Int).ModInverse(twoY, secp256k1P)
	if invTwoY == nil {
		return nil, nil
	}
	lambda := new(big.Int).Mul(threeX2, invTwoY)
	lambda.Mod(lambda, secp256k1P)

	x3 := new(big.Int).Mul(lambda, lambda)
	x3.Sub(x3, new(big.Int).Mul(big.NewInt(2), x1))
	x3.Mod(x3, secp256k1P)

	y3 := new(big.Int).Sub(x1, x3)
	y3.Mul(lambda, y3)
	y3.Sub(y3, y1)
	y3.Mod(y3, secp256k1P)
	return x3, y3
}

func secp256k1Add(x1, y1, x2, y2 *big.Int) (*big.Int, *big.Int) {
	if x1 == nil || y1 == nil {
		return x2, y2
	}
	if x2 == nil || y2 == nil {
		return x1, y1
	}
	if x1.Cmp(x2) == 0 {
		if y1.Cmp(y2) == 0 {
			return secp256k1Double(x1, y1)
		}
		return nil, nil
	}
	dx := new(big.Int).Sub(x2, x1)
	dx.Mod(dx, secp256k1P)
	invDX := new(big.Int).ModInverse(dx, secp256k1P)
	if invDX == nil {
		return nil, nil
	}
	dy := new(big.Int).Sub(y2, y1)
	dy.Mod(dy, secp256k1P)
	lambda := new(big.Int).Mul(dy, invDX)
	lambda.Mod(lambda, secp256k1P)

	x3 := new(big.Int).Mul(lambda, lambda)
	x3.Sub(x3, x1)
	x3.Sub(x3, x2)
	x3.Mod(x3, secp256k1P)

	y3 := new(big.Int).Sub(x1, x3)
	y3.Mul(lambda, y3)
	y3.Sub(y3, y1)
	y3.Mod(y3, secp256k1P)
	return x3, y3
}

func secp256k1ScalarMult(bx, by, k *big.Int) (*big.Int, *big.Int) {
	if k == nil || k.Sign() <= 0 {
		return nil, nil
	}
	var rx, ry *big.Int
	qx, qy := new(big.Int).Set(bx), new(big.Int).Set(by)
	for i := 0; i < 256; i++ {
		if k.Bit(i) == 1 {
			rx, ry = secp256k1Add(rx, ry, qx, qy)
		}
		qx, qy = secp256k1Double(qx, qy)
	}
	return rx, ry
}

func secp256k1ScalarBaseMult(k *big.Int) (*big.Int, *big.Int) {
	return secp256k1ScalarMult(secp256k1Gx, secp256k1Gy, k)
}

func encodePoint(x, y *big.Int) (uncompressed65 []byte, compressed33 []byte) {
	uncompressed65 = make([]byte, 65)
	uncompressed65[0] = 0x04
	x.FillBytes(uncompressed65[1:33])
	y.FillBytes(uncompressed65[33:65])

	compressed33 = make([]byte, 33)
	compressed33[0] = 0x02
	if y.Bit(0) == 1 {
		compressed33[0] = 0x03
	}
	x.FillBytes(compressed33[1:33])
	return uncompressed65, compressed33
}

// Secp256k1PublicKey derives the uncompressed (65B) and compressed (33B) public keys.
func Secp256k1PublicKey(priv []byte) (uncompressed65 []byte, compressed33 []byte, err error) {
	if err := ValidateSecp256k1PrivateKey(priv); err != nil {
		return nil, nil, err
	}
	d := new(big.Int).SetBytes(priv)
	defer wipeBigInt(d)
	px, py := secp256k1ScalarBaseMult(d)
	if !secp256k1IsOnCurve(px, py) {
		return nil, nil, errors.New("derived secp256k1 public key off curve")
	}
	u, c := encodePoint(px, py)
	return u, c, nil
}

// EthereumAddressFromPublicKey computes the EIP-55 checksummed Ethereum address
// for an uncompressed 65-byte secp256k1 public key.
func EthereumAddressFromPublicKey(uncompressed65 []byte) (string, error) {
	if len(uncompressed65) != 65 || uncompressed65[0] != 0x04 {
		return "", errors.New("expected 65-byte uncompressed secp256k1 public key")
	}
	x := new(big.Int).SetBytes(uncompressed65[1:33])
	y := new(big.Int).SetBytes(uncompressed65[33:65])
	if !secp256k1IsOnCurve(x, y) {
		return "", errors.New("public key not on secp256k1 curve")
	}
	digest := Keccak256(uncompressed65[1:65])
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

func hmacSHA256(key []byte, parts ...[]byte) []byte {
	mac := hmac.New(sha256.New, key)
	for _, p := range parts {
		_, _ = mac.Write(p)
	}
	return mac.Sum(nil)
}

// SignSecp256k1Digest computes a deterministic RFC 6979 low-S ECDSA signature
// with EIP-155 legacy recovery byte V in {27, 28}.
func SignSecp256k1Digest(priv []byte, digest [32]byte) (ECDSASignature, error) {
	if err := ValidateSecp256k1PrivateKey(priv); err != nil {
		return ECDSASignature{}, err
	}
	d := new(big.Int).SetBytes(priv)
	defer wipeBigInt(d)

	z := new(big.Int).SetBytes(digest[:])
	z.Mod(z, secp256k1N)
	var h1Octets [32]byte
	z.FillBytes(h1Octets[:])

	var xOctets [32]byte
	copy(xOctets[:], priv)
	defer clear(xOctets[:])

	// RFC 6979 Section 3.2 initialization with SHA-256.
	v := make([]byte, 32)
	for i := range v {
		v[i] = 0x01
	}
	kKey := make([]byte, 32)
	kKey = hmacSHA256(kKey, v, []byte{0x00}, xOctets[:], h1Octets[:])
	v = hmacSHA256(kKey, v)
	kKey = hmacSHA256(kKey, v, []byte{0x01}, xOctets[:], h1Octets[:])
	v = hmacSHA256(kKey, v)

	for attempt := 0; attempt < 256; attempt++ {
		v = hmacSHA256(kKey, v)
		k := new(big.Int).SetBytes(v)
		if k.Sign() > 0 && k.Cmp(secp256k1N) < 0 {
			rx, ry := secp256k1ScalarBaseMult(k)
			// Only accept rx < n so Ethereum V remains canonical in {27, 28}.
			if rx != nil && rx.Sign() > 0 && rx.Cmp(secp256k1N) < 0 {
				kInv := new(big.Int).ModInverse(k, secp256k1N)
				s := new(big.Int).Mul(rx, d)
				s.Add(s, z)
				s.Mod(s, secp256k1N)
				s.Mul(s, kInv)
				s.Mod(s, secp256k1N)
				wipeBigInt(kInv)
				if s.Sign() != 0 {
					recID := uint8(ry.Bit(0))
					if s.Cmp(secp256k1HalfN) > 0 {
						s.Sub(secp256k1N, s)
						recID ^= 1
					}
					var sig ECDSASignature
					rx.FillBytes(sig.R[:])
					s.FillBytes(sig.S[:])
					sig.V = 27 + recID
					wipeBigInt(k)
					clear(kKey)
					clear(v)
					return sig, nil
				}
			}
		}
		wipeBigInt(k)
		kKey = hmacSHA256(kKey, v, []byte{0x00})
		v = hmacSHA256(kKey, v)
	}
	clear(kKey)
	clear(v)
	return ECDSASignature{}, errors.New("RFC 6979 nonce generation exhausted")
}

// RecoverSecp256k1PublicKey recovers the 65-byte uncompressed public key from a
// low-S 65-byte ECDSA signature and digest.
func RecoverSecp256k1PublicKey(digest [32]byte, sig ECDSASignature) ([]byte, error) {
	if err := validateSignatureComponents(sig); err != nil {
		return nil, err
	}
	recBit := uint(sig.V - 27)
	r := new(big.Int).SetBytes(sig.R[:])
	s := new(big.Int).SetBytes(sig.S[:])
	if r.Cmp(secp256k1P) >= 0 {
		return nil, errors.New("signature r exceeds field prime")
	}

	// Solve y^2 = r^3 + 7 (mod p) using p ≡ 3 (mod 4).
	x3 := new(big.Int).Mul(r, r)
	x3.Mod(x3, secp256k1P)
	x3.Mul(x3, r)
	x3.Mod(x3, secp256k1P)
	x3.Add(x3, secp256k1B)
	x3.Mod(x3, secp256k1P)

	y := new(big.Int).Exp(x3, secp256k1SqrtE, secp256k1P)
	yCheck := new(big.Int).Mul(y, y)
	yCheck.Mod(yCheck, secp256k1P)
	if yCheck.Cmp(x3) != 0 {
		return nil, errors.New("signature r has no valid secp256k1 point")
	}
	if y.Bit(0) != recBit {
		y.Sub(secp256k1P, y)
	}

	rInv := new(big.Int).ModInverse(r, secp256k1N)
	if rInv == nil {
		return nil, errors.New("signature r not invertible")
	}
	z := new(big.Int).SetBytes(digest[:])
	z.Mod(z, secp256k1N)

	// Q = r^-1 * (s*R - z*G) = u1*G + u2*R where u1 = (-z * r^-1) mod n, u2 = (s * r^-1) mod n.
	u1 := new(big.Int).Neg(z)
	u1.Mod(u1, secp256k1N)
	u1.Mul(u1, rInv)
	u1.Mod(u1, secp256k1N)

	u2 := new(big.Int).Mul(s, rInv)
	u2.Mod(u2, secp256k1N)

	x1, y1 := secp256k1ScalarBaseMult(u1)
	x2, y2 := secp256k1ScalarMult(r, y, u2)
	qx, qy := secp256k1Add(x1, y1, x2, y2)
	if !secp256k1IsOnCurve(qx, qy) {
		return nil, errors.New("ecrecover produced point off curve")
	}
	uncompressed, _ := encodePoint(qx, qy)
	return uncompressed, nil
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
