package vault

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestProductionArgon2GoldenVector(t *testing.T) {
	t.Parallel()
	got := DeriveKey("fased-production-kdf-golden", []byte("0123456789abcdef"), ProdKDFTime, ProdKDFMemory, ProdKDFThreads)
	const want = "248fa69ff535752dee3981879eda4d57446bf602ad00664a286515d2b141a3e9"
	if hex.EncodeToString(got) != want {
		t.Fatalf("argon2 golden vector mismatch: got %x, want %s", got, want)
	}
}

func TestAESGCMRoundTripAndTamperRejection(t *testing.T) {
	t.Parallel()
	key, err := RandomBytes(32)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := RandomBytes(12)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("fased-vault-secret-payload")
	aad := []byte("fased-aad-v1")

	ct, err := SealAESGCM(key, nonce, plain, aad)
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenAESGCM(key, nonce, ct, aad)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("round-trip mismatch: got=%q err=%v", got, err)
	}

	if _, err := OpenAESGCM(key, nonce, ct, []byte("wrong-aad")); err == nil {
		t.Fatal("expected error for wrong AAD")
	}
	tampered := append([]byte(nil), ct...)
	tampered[0] ^= 0xff
	if _, err := OpenAESGCM(key, nonce, tampered, aad); err == nil {
		t.Fatal("expected error for tampered ciphertext")
	}
	if _, err := SealAESGCM(make([]byte, 16), nonce, plain, aad); err == nil {
		t.Fatal("expected error for non-32-byte key")
	}
	if _, err := OpenAESGCM(key, make([]byte, 8), ct, aad); err == nil {
		t.Fatal("expected error for non-12-byte nonce")
	}
	if _, err := OpenAESGCM(key, nonce, make([]byte, 8), aad); err == nil {
		t.Fatal("expected error for short ciphertext")
	}
	if _, err := RandomBytes(0); err == nil {
		t.Fatal("expected error for non-positive RandomBytes")
	}
}
