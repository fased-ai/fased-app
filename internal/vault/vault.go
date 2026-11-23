package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"

	"golang.org/x/crypto/argon2"
)

const (
	ProdKDFTime    uint32 = 3
	ProdKDFMemory  uint32 = 64 * 1024
	ProdKDFThreads uint8  = 2
)

// DeriveKey computes a 32-byte Argon2id symmetric key from password and salt.
func DeriveKey(password string, salt []byte, timeCost, memoryKiB uint32, threads uint8) []byte {
	return argon2.IDKey([]byte(password), salt, timeCost, memoryKiB, threads, 32)
}

// SealAESGCM encrypts plaintext with AES-256-GCM using a 32-byte key and 12-byte nonce.
func SealAESGCM(key, nonce, plaintext, aad []byte) ([]byte, error) {
	if len(key) != 32 || len(nonce) != 12 {
		return nil, errors.New("invalid AES-GCM key or nonce length")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Seal(nil, nonce, plaintext, aad), nil
}

// OpenAESGCM decrypts and authenticates AES-256-GCM ciphertext against key, nonce, and AAD.
func OpenAESGCM(key, nonce, ciphertext, aad []byte) ([]byte, error) {
	if len(key) != 32 || len(nonce) != 12 || len(ciphertext) < 16 {
		return nil, errors.New("invalid AES-GCM parameters")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, aad)
}

// RandomBytes returns n cryptographically random bytes from crypto/rand.
func RandomBytes(n int) ([]byte, error) {
	if n <= 0 {
		return nil, errors.New("positive byte length required")
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}
