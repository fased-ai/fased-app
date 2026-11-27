package custody

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestWaveS6RecoveryV2AADBlocksVaultRoleDowngrade(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, err := NewService(Config{DataDir: t.TempDir(), FixtureMode: true, FastKDF: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	w, err := svc.CreateNativeWallet(ctx, "Cold Vault", RoleVault, OfficialMainnetRPC)
	if err != nil {
		t.Fatal(err)
	}
	archive, _, err := svc.ExportEncryptedRecovery(ctx, w.ID, "StrongRecoveryPassphrase!2026", "owner")
	if err != nil {
		t.Fatalf("ExportEncryptedRecovery: %v", err)
	}
	var pkg RecoveryPackage
	if err := json.Unmarshal(archive, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Version != 2 {
		t.Fatalf("expected recovery archive v2, got %d", pkg.Version)
	}

	// Tampering plaintext role from "vault" to "trading" MUST fail AES-GCM AAD verification.
	pkg.Role = RoleTrading
	tampered, _ := json.Marshal(pkg)
	svc2, err := NewService(Config{DataDir: t.TempDir(), FixtureMode: true, FastKDF: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc2.Close() })
	if _, err := svc2.ImportEncryptedRecovery(ctx, tampered, "StrongRecoveryPassphrase!2026", "Downgraded", RoleTrading, OfficialMainnetRPC); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("expected AAD verification failure (ErrWrongPassword) on tampered role, got %v", err)
	}

	// Untampered v2 archive restores cleanly into RoleVault.
	restored, err := svc2.ImportEncryptedRecovery(ctx, archive, "StrongRecoveryPassphrase!2026", "Restored Vault", RoleVault, OfficialMainnetRPC)
	if err != nil || restored.Role != RoleVault || restored.PublicKey != w.PublicKey {
		t.Fatalf("expected clean v2 Vault restore, got %+v err=%v", restored, err)
	}
}

func TestWaveS6KEKInstanceIDWebAuthnAndRPCSanitization(t *testing.T) {
	t.Parallel()

	// Cross-platform KEK instance ID must NEVER contain path separators (/ or \).
	for _, dir := range []string{"/Users/alice/Library/Application Support/fased/signer-vault", `C:\Users\alice\AppData\Local\fased\signer-vault`} {
		id := custodyKEKInstanceID(dir)
		if strings.ContainsAny(id, "/\\\r\n\x00") || !strings.HasPrefix(id, "fased-signer-") {
			t.Fatalf("unsafe KEK instance ID %q for %q", id, dir)
		}
	}

	// WebAuthn §5.4.2 rejects IP-literal origins/RPIDs and single-label public suffixes.
	if _, _, err := validateWebAuthnOriginAndRP("127.0.0.1", "http://127.0.0.1:7331"); err == nil {
		t.Fatal("expected IP-literal WebAuthn origin/rpId to be rejected")
	}
	if _, _, err := validateWebAuthnOriginAndRP("com", "https://evil.com"); err == nil {
		t.Fatal("expected single-label RPID 'com' to be rejected")
	}
	if rp, orig, err := validateWebAuthnOriginAndRP("localhost", "http://localhost:7332"); err != nil || rp != "localhost" || orig != "http://localhost:7332" {
		t.Fatalf("expected http://localhost:7332 to pass WebAuthn validation, got rp=%q orig=%q err=%v", rp, orig, err)
	}

	// Ed25519 identity point [1,0,...,0] MUST be rejected by parseCOSEPublicKey.
	idPt := make([]byte, 32)
	idPt[0] = 1
	// CBOR map(4): {1: 1 (OKP), 3: -8 (EdDSA), -1: 6 (Ed25519), -2: bstr(32)}
	coseIdentity := []byte{0xa4, 0x01, 0x01, 0x03, 0x27, 0x20, 0x06, 0x21, 0x58, 0x20}
	coseIdentity = append(coseIdentity, idPt...)
	if _, _, err := parseCOSEPublicKey(coseIdentity); err == nil {
		t.Fatal("expected Ed25519 identity point to be rejected in parseCOSEPublicKey")
	}

	// Duplicate CBOR map keys MUST be rejected.
	dupCBOR := []byte{0xa2, 0x01, 0x01, 0x01, 0x02}
	if _, _, err := parseCOSEPublicKey(dupCBOR); err == nil {
		t.Fatal("expected duplicate CBOR map key rejection")
	}

	// *url.Error RPC sanitization MUST strip query API keys and path secrets.
	rawURL := "https://mainnet.helius-rpc.com/v2/SUPERSECRETKEY?api-key=HELIUS_LEAK_123"
	sanitized := sanitizeRPCError(&url.Error{Op: "Post", URL: rawURL, Err: errors.New("dial timeout")}, rawURL)
	if strings.Contains(sanitized.Error(), "SUPERSECRETKEY") || strings.Contains(sanitized.Error(), "HELIUS_LEAK_123") {
		t.Fatalf("RPC API key leaked in sanitized error: %v", sanitized)
	}
}
