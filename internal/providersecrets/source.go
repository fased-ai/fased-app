// Package providersecrets accesses an OS-held encryption key. It never stores a
// key next to application data and never grants permission to use a provider.
package providersecrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

var ErrUnavailable = errors.New("protected credential storage unavailable")
var ErrMissing = errors.New("protected credential key not created")

const CredentialName = "fased-provider-task-key"

type Source interface {
	Key(context.Context, string, bool) ([]byte, error)
}

type Native struct{}

func Seal(key, plain, aad []byte) ([]byte, []byte, error) {
	if len(key) != 32 {
		return nil, nil, ErrUnavailable
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, g.Seal(nil, nonce, plain, aad), nil
}

func Open(key, nonce, ciphertext, aad []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, ErrUnavailable
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil || len(nonce) != g.NonceSize() {
		return nil, ErrUnavailable
	}
	return g.Open(nil, nonce, ciphertext, aad)
}
