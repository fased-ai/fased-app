package walletvault

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

var wordIndex = func() map[string]int {
	m := make(map[string]int, len(bip39EnglishWords))
	for i, w := range bip39EnglishWords {
		m[w] = i
	}
	return m
}()

// GenerateMnemonic creates a cryptographically random 12-word (128-bit) or
// 24-word (256-bit) BIP-39 English mnemonic phrase.
func GenerateMnemonic(words int) (string, error) {
	var entropyBytes int
	switch words {
	case 12:
		entropyBytes = 16
	case 24:
		entropyBytes = 32
	default:
		return "", fmt.Errorf("mnemonic must be 12 or 24 words (got %d)", words)
	}
	entropy := make([]byte, entropyBytes)
	if _, err := rand.Read(entropy); err != nil {
		return "", err
	}
	defer clear(entropy)
	return EntropyToMnemonic(entropy)
}

// EntropyToMnemonic converts 16-byte (128-bit) or 32-byte (256-bit) entropy
// into its official BIP-39 English mnemonic with appended checksum bits.
func EntropyToMnemonic(entropy []byte) (string, error) {
	if len(entropy) != 16 && len(entropy) != 20 && len(entropy) != 24 && len(entropy) != 28 && len(entropy) != 32 {
		return "", errors.New("invalid BIP-39 entropy length")
	}
	checksumLen := len(entropy) / 4 // in bits
	hash := sha256.Sum256(entropy)

	val := new(big.Int).SetBytes(entropy)
	for i := 0; i < checksumLen; i++ {
		val.Lsh(val, 1)
		byteIdx := i / 8
		bitPos := uint(7 - (i % 8))
		if (hash[byteIdx]>>bitPos)&1 == 1 {
			val.SetBit(val, 0, 1)
		}
	}

	wordCount := (len(entropy)*8 + checksumLen) / 11
	mask := big.NewInt(0x7ff)
	words := make([]string, wordCount)
	tmp := new(big.Int)
	for i := wordCount - 1; i >= 0; i-- {
		tmp.And(val, mask)
		words[i] = bip39EnglishWords[tmp.Int64()]
		val.Rsh(val, 11)
	}
	return strings.Join(words, " "), nil
}

// ValidateMnemonic checks word membership and BIP-39 checksum bits.
func ValidateMnemonic(phrase string) error {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(phrase)))
	n := len(fields)
	if n != 12 && n != 15 && n != 18 && n != 21 && n != 24 {
		return fmt.Errorf("invalid mnemonic word count: %d", n)
	}
	total := new(big.Int)
	for _, w := range fields {
		idx, ok := wordIndex[w]
		if !ok {
			return fmt.Errorf("unknown BIP-39 word %q", w)
		}
		total.Lsh(total, 11)
		total.Or(total, big.NewInt(int64(idx)))
	}
	checksumBits := n / 3
	entropyBytesLen := (n*11 - checksumBits) / 8
	checksumMask := (1 << checksumBits) - 1
	gotChecksum := int(new(big.Int).And(total, big.NewInt(int64(checksumMask))).Int64())

	total.Rsh(total, uint(checksumBits))
	rawEntropy := total.Bytes()
	entropy := make([]byte, entropyBytesLen)
	if len(rawEntropy) > entropyBytesLen {
		return errors.New("mnemonic entropy overflow")
	}
	copy(entropy[entropyBytesLen-len(rawEntropy):], rawEntropy)
	defer clear(entropy)

	hash := sha256.Sum256(entropy)
	wantChecksum := int(hash[0]) >> uint(8-checksumBits)
	if gotChecksum != wantChecksum {
		return errors.New("invalid BIP-39 checksum")
	}
	return nil
}

// MnemonicToSeed derives the 64-byte BIP-39 seed via PBKDF2-HMAC-SHA512.
func MnemonicToSeed(phrase, passphrase string) ([]byte, error) {
	clean := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(phrase))), " ")
	if err := ValidateMnemonic(clean); err != nil {
		return nil, err
	}
	return pbkdf2.Key([]byte(clean), []byte("mnemonic"+passphrase), 2048, 64, sha512.New), nil
}

func hmacSHA512(key, data []byte) []byte {
	mac := hmac.New(sha512.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}
