//go:build linux

package signer

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"
)

// Also copied into the explicitly composed predecessor: advertising the reader
// feature requires real decoding, authenticated unlock and authority refusal.
func TestSwapStateReaderHistoricalHold(t *testing.T) {
	dir := fixtureDir(t)
	id := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	v, err := Open(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = v.Create(fixturePassword); err != nil {
		t.Fatal(err)
	}
	before, err := v.UpdateInventory(randomID())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range before.States {
		if s.Name == "vault" {
			for _, f := range s.Features {
				if f == historyRecoveryFeature {
					t.Fatal("unmarked vault changed active features")
				}
			}
		}
	}
	if err = v.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	e := *v.record
	e.RecoveryRequired = true
	e.RecoveredHistorySHA256 = "1111111111111111111111111111111111111111111111111111111111111111"
	key := argon2.IDKey(fixturePassword, e.Salt, 3, 64*1024, 2, 32)
	block, err := aes.NewCipher(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	a, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	e.Ciphertext = a.Seal(nil, e.Nonce, v.seed, e.aad())
	v.Close()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "vault.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	v, err = Open(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if err = v.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	if !v.Status().RecoveryRequired || v.RecoverFixtureAuthority(*v.Status().Account) == nil {
		t.Fatal("reader restored historical authority")
	}
	if _, err = v.Backup(backupPassword); err == nil {
		t.Fatal("reader discarded historical fence")
	}
	inv, err := v.UpdateInventory(randomID())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range inv.States {
		if s.Name == "vault" {
			for _, f := range s.Features {
				found = found || f == historyRecoveryFeature
			}
		}
	}
	if !found || !inv.RecoveryHold {
		t.Fatal("reader feature/hold missing")
	}
}
