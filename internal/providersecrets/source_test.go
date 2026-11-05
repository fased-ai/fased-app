package providersecrets

import (
	"bytes"
	"testing"
)

func TestSealBindsEveryAuthorityInput(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	secret := []byte("disposable-provider-secret")
	aad := []byte("instance/grant/vault/counters")
	nonce, sealed, e := Seal(key, secret, aad)
	if e != nil || bytes.Contains(sealed, secret) {
		t.Fatal("seal", e)
	}
	plain, e := Open(key, nonce, sealed, aad)
	if e != nil || !bytes.Equal(plain, secret) {
		t.Fatal("open", e)
	}
	clear(plain)
	for _, mutate := range []string{"key", "scope", "nonce", "ciphertext"} {
		k, n, c, a := bytes.Clone(key), bytes.Clone(nonce), bytes.Clone(sealed), bytes.Clone(aad)
		switch mutate {
		case "key":
			k[0] ^= 1
		case "scope":
			a[0] ^= 1
		case "nonce":
			n[0] ^= 1
		case "ciphertext":
			c[0] ^= 1
		}
		if _, e = Open(k, n, c, a); e == nil {
			t.Fatal("authority input not authenticated", mutate)
		}
	}
	if _, e = Open(key, nil, sealed, aad); e == nil {
		t.Fatal("malformed nonce accepted")
	}
}
