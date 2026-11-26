package custody

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

const (
	solanaBase58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

	SolanaSystemProgram    = "11111111111111111111111111111111"
	SolanaComputeBudget    = "ComputeBudget111111111111111111111111111111"
	SolanaTokenProgram     = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
	SolanaToken2022        = "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb"
	SolanaATAProgram       = "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"
	SolanaMemoProgram      = "MemoSq4gqABAXKb96qnH8TysNcWxMyWCqXgDLGmfcHr"
	SolanaJupiterV6Program = "JUP6LkbZbjS1jKKwapdHNy74zcZ3tLUZoi5QNyVTaV4"
)

var base58DecodeTable = func() [256]int8 {
	var t [256]int8
	for i := range t {
		t[i] = -1
	}
	for i := 0; i < len(solanaBase58Alphabet); i++ {
		t[solanaBase58Alphabet[i]] = int8(i)
	}
	return t
}()

// Base58Encode encodes raw bytes to canonical Solana Base58.
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
		out = append(out, solanaBase58Alphabet[mod.Int64()])
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

// Base58Decode decodes a canonical Solana Base58 string.
func Base58Decode(s string) ([]byte, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil, errors.New("empty base58 string")
	}
	x := big.NewInt(0)
	base := big.NewInt(58)
	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]
		v := base58DecodeTable[ch]
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

// ValidateSolanaPublicAddress validates a 32-byte Base58 Ed25519 public key.
func ValidateSolanaPublicAddress(addr string) (string, [32]byte, error) {
	var pk [32]byte
	clean := strings.TrimSpace(addr)
	raw, err := Base58Decode(clean)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return "", pk, errors.New("invalid Solana public address (expect 32-byte Base58)")
	}
	if Base58Encode(raw) != clean {
		return "", pk, errors.New("non-canonical Solana address")
	}
	copy(pk[:], raw)
	return clean, pk, nil
}

func encodeShortVec(n int) []byte {
	var out []byte
	v := n
	for {
		elem := byte(v & 0x7f)
		v >>= 7
		if v == 0 {
			out = append(out, elem)
			return out
		}
		out = append(out, elem|0x80)
	}
}

func decodeShortVec(b []byte) (val int, read int, err error) {
	var shift uint
	for i := 0; i < len(b) && i < 3; i++ {
		by := b[i]
		val |= int(by&0x7f) << shift
		if by&0x80 == 0 {
			return val, i + 1, nil
		}
		shift += 7
	}
	return 0, 0, errors.New("invalid shortvec encoding")
}

// BuildUnsignedTransferPacket creates an unsigned Solana legacy transaction wire packet
// (1 zeroed 64-byte signature slot + serialized message) suitable for Wallet Standard signTransaction.
func BuildUnsignedTransferPacket(msgBytes []byte) []byte {
	pkt := make([]byte, 0, 1+ed25519.SignatureSize+len(msgBytes))
	pkt = append(pkt, 1) // 1 required signature
	pkt = append(pkt, make([]byte, ed25519.SignatureSize)...)
	pkt = append(pkt, msgBytes...)
	return pkt
}

// SignLegacyMessage wraps msgBytes with the signer's Ed25519 signature into a wire Solana tx.
func SignLegacyMessage(msgBytes []byte, priv ed25519.PrivateKey) (rawTx []byte, sigBase58 string) {
	sig := ed25519.Sign(priv, msgBytes)
	pkt := make([]byte, 0, 1+ed25519.SignatureSize+len(msgBytes))
	pkt = append(pkt, 1)
	pkt = append(pkt, sig...)
	pkt = append(pkt, msgBytes...)
	return pkt, Base58Encode(sig)
}

// VerifyHardwareSignedTransaction verifies that a hardware/Wallet-Standard signed transaction
// has byte-for-byte identical message bytes to expectedMsgBytes and a valid Ed25519 signature
// for expectedPubKey.
func VerifyHardwareSignedTransaction(expectedMsgBytes []byte, expectedPubKey [32]byte, signedTxBase64 string) (sigBase58 string, rawTx []byte, err error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signedTxBase64))
	if err != nil || len(raw) < 67 {
		return "", nil, fmt.Errorf("%w: invalid signed transaction encoding", ErrTxTampered)
	}
	numSigs, hdrLen, err := decodeShortVec(raw)
	if err != nil || numSigs != 1 {
		return "", nil, fmt.Errorf("%w: unexpected signature count %d", ErrTxTampered, numSigs)
	}
	sigStart := hdrLen
	sigEnd := sigStart + ed25519.SignatureSize
	if len(raw) <= sigEnd {
		return "", nil, fmt.Errorf("%w: truncated signed transaction", ErrTxTampered)
	}
	sig := raw[sigStart:sigEnd]
	msg := raw[sigEnd:]
	if !bytes.Equal(msg, expectedMsgBytes) {
		return "", nil, ErrTxTampered
	}
	if !ed25519.Verify(expectedPubKey[:], msg, sig) {
		return "", nil, fmt.Errorf("%w: ed25519 signature verification failed", ErrTxTampered)
	}
	return Base58Encode(sig), raw, nil
}

// ParseSolanaKeypair64 decodes either a Solana CLI 64-byte JSON integer array or a Base58 64-byte keypair.
func ParseSolanaKeypair64(raw []byte) (ed25519.PrivateKey, string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, "", errors.New("empty Solana keypair file")
	}
	if strings.HasPrefix(trimmed, "[") {
		if !strings.HasSuffix(trimmed, "]") {
			return nil, "", errors.New("invalid Solana JSON keypair array")
		}
		inner := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
		parts := strings.Split(inner, ",")
		if len(parts) != ed25519.PrivateKeySize {
			return nil, "", fmt.Errorf("expected %d bytes in Solana JSON keypair, got %d", ed25519.PrivateKeySize, len(parts))
		}
		buf := make([]byte, ed25519.PrivateKeySize)
		for i, p := range parts {
			var v int
			if _, err := fmt.Sscanf(strings.TrimSpace(p), "%d", &v); err != nil || v < 0 || v > 255 {
				clear(buf)
				return nil, "", errors.New("invalid byte element in Solana JSON keypair")
			}
			buf[i] = byte(v)
		}
		priv := ed25519.NewKeyFromSeed(buf[:32])
		if !bytes.Equal(priv[32:], buf[32:]) {
			clear(buf)
			clear(priv)
			return nil, "", errors.New("Solana keypair public key mismatch")
		}
		clear(buf)
		return priv, Base58Encode(priv[32:]), nil
	}
	decoded, err := Base58Decode(trimmed)
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		return nil, "", errors.New("expected 64-byte Solana JSON array or Base58 private key")
	}
	priv := ed25519.NewKeyFromSeed(decoded[:32])
	if !bytes.Equal(priv[32:], decoded[32:]) {
		clear(decoded)
		clear(priv)
		return nil, "", errors.New("Base58 private key public suffix mismatch")
	}
	clear(decoded)
	return priv, Base58Encode(priv[32:]), nil
}

// ParseHumanAmountToRaw converts a decimal amount string with given decimals into uint64 raw units safely.
func ParseHumanAmountToRaw(s string, decimals uint8) (uint64, error) {
	if decimals > 18 {
		return 0, errors.New("token decimals exceed supported bound")
	}
	clean := strings.TrimSpace(s)
	if clean == "" {
		return 0, errors.New("amount required")
	}
	if strings.HasPrefix(clean, "-") {
		return 0, errors.New("amount must be positive")
	}
	parts := strings.Split(clean, ".")
	if len(parts) > 2 {
		return 0, errors.New("invalid decimal amount")
	}
	scale := uint64(1)
	for i := uint8(0); i < decimals; i++ {
		scale *= 10
	}
	var whole uint64
	if parts[0] != "" {
		for _, ch := range parts[0] {
			if ch < '0' || ch > '9' {
				return 0, errors.New("invalid digit in amount")
			}
			d := uint64(ch - '0')
			if whole > (^uint64(0)-d)/10 {
				return 0, errors.New("amount overflows uint64")
			}
			whole = whole*10 + d
		}
	}
	var frac uint64
	if len(parts) == 2 {
		fracStr := parts[1]
		if len(fracStr) > int(decimals) {
			return 0, fmt.Errorf("amount supports at most %d decimal places", decimals)
		}
		for len(fracStr) < int(decimals) {
			fracStr += "0"
		}
		for _, ch := range fracStr {
			if ch < '0' || ch > '9' {
				return 0, errors.New("invalid fractional digit in amount")
			}
			frac = frac*10 + uint64(ch-'0')
		}
	}
	if whole > (^uint64(0)-frac)/scale {
		return 0, errors.New("amount overflows uint64")
	}
	raw := whole*scale + frac
	if raw == 0 {
		return 0, errors.New("amount must be greater than zero")
	}
	return raw, nil
}

// FormatRawAmount renders integer raw units cleanly without float rounding.
func FormatRawAmount(raw uint64, decimals uint8) string {
	if decimals == 0 {
		return fmt.Sprintf("%d", raw)
	}
	scale := uint64(1)
	for i := uint8(0); i < decimals; i++ {
		scale *= 10
	}
	whole := raw / scale
	rem := raw % scale
	if rem == 0 {
		return fmt.Sprintf("%d", whole)
	}
	frac := fmt.Sprintf("%0*d", int(decimals), rem)
	frac = strings.TrimRight(frac, "0")
	return fmt.Sprintf("%d.%s", whole, frac)
}

// ParseHumanSOLToLamports converts a decimal SOL string (up to 9 decimals) into integer lamports safely.
func ParseHumanSOLToLamports(s string) (uint64, error) {
	return ParseHumanAmountToRaw(s, 9)
}

// FormatLamportsToSOL renders integer lamports cleanly in decimal SOL without float rounding.
func FormatLamportsToSOL(lamports uint64) string {
	return FormatRawAmount(lamports, 9)
}

func shortVecLen(n int) []byte {
	return encodeShortVec(n)
}

func u32LE(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}
