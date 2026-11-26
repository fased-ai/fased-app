package walletvault

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBIP39WordlistAndStandardVector(t *testing.T) {
	t.Parallel()
	h := sha256.New()
	for _, w := range bip39EnglishWords {
		h.Write([]byte(w + "\n"))
	}
	gotHash := hex.EncodeToString(h.Sum(nil))
	if gotHash != "2f5eed53a4727b4bf8880d8f3f199efc90e58503646d9ff8eff3a2ed3b24dbda" {
		t.Fatalf("unexpected BIP-39 English wordlist SHA-256: %s", gotHash)
	}

	// Standard all-zero 128-bit entropy -> "abandon abandon ... about"
	zero128 := make([]byte, 16)
	m, err := EntropyToMnemonic(zero128)
	if err != nil {
		t.Fatalf("EntropyToMnemonic: %v", err)
	}
	want := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	if m != want {
		t.Fatalf("all-zero mnemonic mismatch:\n got: %q\nwant: %q", m, want)
	}
	if err := ValidateMnemonic(m); err != nil {
		t.Fatalf("ValidateMnemonic valid failed: %v", err)
	}
	if err := ValidateMnemonic(strings.Replace(m, "about", "abandon", 1)); err == nil {
		t.Fatal("expected invalid checksum error")
	}

	pk1, err := KeypairFromMnemonic(want, 0)
	if err != nil {
		t.Fatalf("KeypairFromMnemonic: %v", err)
	}
	pk2, err := KeypairFromMnemonic(want, 0)
	if err != nil {
		t.Fatalf("KeypairFromMnemonic repeat: %v", err)
	}
	addr1 := Base58Encode(pk1.Public().(ed25519.PublicKey))
	addr2 := Base58Encode(pk2.Public().(ed25519.PublicKey))
	if addr1 == "" || addr1 != addr2 {
		t.Fatalf("deterministic derivation mismatch: %q vs %q", addr1, addr2)
	}
	if _, err := ValidateSolanaPublicAddress(addr1); err != nil {
		t.Fatalf("ValidateSolanaPublicAddress: %v", err)
	}
}

func TestVaultCreateImportExportSignDelete(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	svc, err := NewService(dir, func() time.Time { return time.Unix(1764000000, 0) })
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.SetFastKDFForTests()

	w, mnemonic, err := svc.CreateSolanaWallet("Main Phantom", RolePersonal, 12, "hunter2-secret")
	if err != nil {
		t.Fatalf("CreateSolanaWallet: %v", err)
	}
	if len(strings.Fields(mnemonic)) != 12 || w.PublicKey == "" || !w.HasMnemonic {
		t.Fatalf("unexpected created wallet: %+v words=%d", w, len(strings.Fields(mnemonic)))
	}

	// Vault file must never contain plaintext mnemonic.
	onDisk, err := os.ReadFile(filepath.Join(dir, "wallets", "vault.json"))
	if err != nil || strings.Contains(string(onDisk), mnemonic) {
		t.Fatalf("plaintext mnemonic must not appear in vault.json (err=%v)", err)
	}

	// Wrong passphrase fails.
	if _, err := svc.ExportSecret(w.ID, "wrong-passphrase"); err == nil {
		t.Fatal("expected wrong passphrase rejection")
	}

	// Correct passphrase exports mnemonic + Base58 + JSON keypair identically.
	exp, err := svc.ExportSecret(w.ID, "hunter2-secret")
	if err != nil {
		t.Fatalf("ExportSecret: %v", err)
	}
	if exp.Mnemonic != mnemonic || exp.PublicKey != w.PublicKey {
		t.Fatalf("exported secret mismatch")
	}

	// Import via JSON keypair restores same public key cleanly.
	impJSON, err := svc.ImportWallet(ImportInput{
		Name:       "JSON Restored",
		Role:       RoleAgentVault,
		PrivateKey: exp.KeypairJSON,
		Passphrase: "another-password",
	})
	if err != nil || impJSON.PublicKey != w.PublicKey || impJSON.HasMnemonic {
		t.Fatalf("ImportWallet JSON failed: %+v err=%v", impJSON, err)
	}

	// Watch-only import requires no passphrase.
	watch, err := svc.ImportWallet(ImportInput{
		Name:         "Whale Watch",
		WatchAddress: w.PublicKey,
	})
	if err != nil || watch.Kind != KindWatch || watch.PublicKey != w.PublicKey {
		t.Fatalf("Watch import failed: %+v err=%v", watch, err)
	}

	// Sign message works.
	sig, signerPub, err := svc.SignMessage(w.ID, "hunter2-secret", []byte("fased-sign-test"))
	if err != nil || len(sig) != ed25519.SignatureSize || signerPub != w.PublicKey {
		t.Fatalf("SignMessage failed: len=%d pub=%s err=%v", len(sig), signerPub, err)
	}

	list, err := svc.ListWallets()
	if err != nil || len(list) != 3 {
		t.Fatalf("ListWallets expected 3, got %d err=%v", len(list), err)
	}

	if ok, err := svc.DeleteWallet(watch.ID); err != nil || !ok {
		t.Fatalf("DeleteWallet failed: ok=%v err=%v", ok, err)
	}
}
