package custody

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Minimal deterministic CBOR encoder strictly for constructing valid test WebAuthn payloads.
func cborUint(major byte, n uint64) []byte {
	switch {
	case n < 24:
		return []byte{(major << 5) | byte(n)}
	case n < 256:
		return []byte{(major << 5) | 24, byte(n)}
	case n < 65536:
		out := []byte{(major << 5) | 25, 0, 0}
		binary.BigEndian.PutUint16(out[1:], uint16(n))
		return out
	default:
		out := []byte{(major << 5) | 26, 0, 0, 0, 0}
		binary.BigEndian.PutUint32(out[1:], uint32(n))
		return out
	}
}

func cborInt(v int64) []byte {
	if v >= 0 {
		return cborUint(0, uint64(v))
	}
	return cborUint(1, uint64(-1-v))
}

func cborBytes(b []byte) []byte {
	return append(cborUint(2, uint64(len(b))), b...)
}

func cborText(s string) []byte {
	return append(cborUint(3, uint64(len(s))), []byte(s)...)
}

func buildTestCOSEES256(pub *ecdsa.PublicKey) []byte {
	xb := make([]byte, 32)
	yb := make([]byte, 32)
	pub.X.FillBytes(xb)
	pub.Y.FillBytes(yb)
	var buf bytes.Buffer
	buf.Write(cborUint(5, 5)) // map(5)
	buf.Write(cborInt(1))
	buf.Write(cborInt(2)) // kty: EC2
	buf.Write(cborInt(3))
	buf.Write(cborInt(coseAlgES256)) // alg: ES256 (-7)
	buf.Write(cborInt(-1))
	buf.Write(cborInt(1)) // crv: P-256
	buf.Write(cborInt(-2))
	buf.Write(cborBytes(xb))
	buf.Write(cborInt(-3))
	buf.Write(cborBytes(yb))
	return buf.Bytes()
}

func buildTestCOSEEd25519(pub ed25519.PublicKey) []byte {
	var buf bytes.Buffer
	buf.Write(cborUint(5, 4)) // map(4)
	buf.Write(cborInt(1))
	buf.Write(cborInt(1)) // kty: OKP
	buf.Write(cborInt(3))
	buf.Write(cborInt(coseAlgEdDSA)) // alg: EdDSA (-8)
	buf.Write(cborInt(-1))
	buf.Write(cborInt(6)) // crv: Ed25519
	buf.Write(cborInt(-2))
	buf.Write(cborBytes(pub))
	return buf.Bytes()
}

func buildTestAttestationObject(rpID string, credID, coseKey []byte, flags byte, signCount uint32) string {
	rpHash := sha256.Sum256([]byte(rpID))
	var ad bytes.Buffer
	ad.Write(rpHash[:])
	ad.WriteByte(flags)
	var cnt [4]byte
	binary.BigEndian.PutUint32(cnt[:], signCount)
	ad.Write(cnt[:])
	ad.Write(make([]byte, 16)) // AAGUID
	var cl [2]byte
	binary.BigEndian.PutUint16(cl[:], uint16(len(credID)))
	ad.Write(cl[:])
	ad.Write(credID)
	ad.Write(coseKey)

	var att bytes.Buffer
	att.Write(cborUint(5, 2)) // map(2)
	att.Write(cborText("fmt"))
	att.Write(cborText("none"))
	att.Write(cborText("authData"))
	att.Write(cborBytes(ad.Bytes()))
	return base64.RawURLEncoding.EncodeToString(att.Bytes())
}

func buildTestClientData(typ, challenge, origin string) (string, []byte) {
	raw, _ := json.Marshal(map[string]any{
		"type":      typ,
		"challenge": challenge,
		"origin":    origin,
	})
	return base64.RawURLEncoding.EncodeToString(raw), raw
}

func buildTestAssertionES256(t *testing.T, priv *ecdsa.PrivateKey, rpID, origin, challenge, credID string, flags byte, signCount uint32) PasskeyAssertFinishRequest {
	t.Helper()
	cdB64, cdRaw := buildTestClientData("webauthn.get", challenge, origin)
	rpHash := sha256.Sum256([]byte(rpID))
	var ad bytes.Buffer
	ad.Write(rpHash[:])
	ad.WriteByte(flags)
	var cnt [4]byte
	binary.BigEndian.PutUint32(cnt[:], signCount)
	ad.Write(cnt[:])
	adBytes := ad.Bytes()

	cdHash := sha256.Sum256(cdRaw)
	payload := append(append([]byte{}, adBytes...), cdHash[:]...)
	digest := sha256.Sum256(payload)
	sig, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return PasskeyAssertFinishRequest{
		Challenge:         challenge,
		CredentialID:      credID,
		ClientDataJSON:    cdB64,
		AuthenticatorData: base64.RawURLEncoding.EncodeToString(adBytes),
		Signature:         base64.RawURLEncoding.EncodeToString(sig),
	}
}

func TestWaveS4PasskeyDigestBoundExecutionAndPolicyOverSocket(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	svc, err := NewService(Config{
		DataDir:          dir,
		FixtureMode:      true,
		FastKDF:          true,
		RequireOwnerAuth: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	// Configure initial owner password via direct service/owner boundary.
	ownerPass := "WaveS4-Owner-Pass-2026!"
	if err := svc.ConfigureOwnerAuth(ctx, ownerPass); err != nil {
		t.Fatal(err)
	}

	sockPath := filepath.Join(dir, "app.sock")
	srv, err := StartSocketServerAt(svc, sockPath, os.Getuid(), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close(ctx)

	client, err := NewSocketClient(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	const rpID = "fased.example.ts.net"
	const origin = "https://fased.example.ts.net"

	// 1. Unauthenticated passkey registration over app.sock must fail closed.
	if _, err := client.BeginPasskeyRegistration(ctx, rpID, origin, "rogue"); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected ErrOwnerAuthRequired without owner proof, got %v", err)
	}

	// 2. Register an ES256 (P-256) passkey with valid owner proof.
	regProof1, _, err := client.AuthorizeOwnerAction(ctx, ownerPass, "passkey-register", "*")
	if err != nil {
		t.Fatal(err)
	}
	regOpts, err := client.BeginPasskeyRegistration(WithOwnerProof(ctx, regProof1), rpID, origin, "YubiKey-5C")
	if err != nil {
		t.Fatalf("BeginPasskeyRegistration failed: %v", err)
	}

	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	credIDBytes := []byte("cred-es256-0001")
	credIDB64 := base64.RawURLEncoding.EncodeToString(credIDBytes)
	cdB64, _ := buildTestClientData("webauthn.create", regOpts.Challenge, origin)
	attB64 := buildTestAttestationObject(rpID, credIDBytes, buildTestCOSEES256(&ecPriv.PublicKey), authDataFlagUP|authDataFlagUV|authDataFlagAT, 1)

	regProof2, _, err := client.AuthorizeOwnerAction(ctx, ownerPass, "passkey-register", "*")
	if err != nil {
		t.Fatal(err)
	}
	pk, err := client.FinishPasskeyRegistration(WithOwnerProof(ctx, regProof2), PasskeyRegisterFinishRequest{
		Challenge:         regOpts.Challenge,
		Label:             "YubiKey-5C",
		CredentialID:      credIDB64,
		ClientDataJSON:    cdB64,
		AttestationObject: attB64,
	})
	if err != nil {
		t.Fatalf("FinishPasskeyRegistration ES256 failed: %v", err)
	}
	if pk.Alg != coseAlgES256 || pk.CredentialID != credIDB64 {
		t.Fatalf("unexpected passkey record: %+v", pk)
	}

	// Verify SecurityStatus reflects passkey enrollment.
	sec, err := client.SecurityStatus(ctx)
	if err != nil || !sec.PasskeyEnabled || sec.PasskeyCount != 1 {
		t.Fatalf("expected PasskeyEnabled=true count=1, got %+v err=%v", sec, err)
	}

	// 3. Create native wallet + destination wallet.
	w1, err := client.CreateNativeWallet(ctx, "Trading", RoleTrading, "https://api.devnet.solana.com")
	if err != nil {
		t.Fatal(err)
	}
	w2, err := client.CreateNativeWallet(ctx, "Treasury", RoleVault, "https://api.devnet.solana.com")
	if err != nil {
		t.Fatal(err)
	}

	// 4. Loosen policy using a PASSKEY assertion bound to EXACT nextPolicyHash (zero password!).
	nextPol := w1.Policy
	nextPol.Operations = []string{"solana.nativeTransfer"}
	nextPol.Programs = []string{SolanaSystemProgram, SolanaComputeBudget}
	nextPol.Destinations = []string{w2.PublicKey}
	nextPol.SOLCap.MaxPerTxRaw = 500_000_000
	nextPol.SOLCap.MaxDailyRaw = 1_000_000_000

	expectedNext := nextPol
	expectedNext.Role = w1.Role
	expectedNext.Version = w1.Policy.Version + 1
	nextPolHash := ComputePolicyHash(expectedNext)

	polOpts, err := client.BeginPasskeyAssertion(ctx, rpID, origin, "policy", w1.ID, nextPolHash)
	if err != nil {
		t.Fatalf("BeginPasskeyAssertion policy failed: %v", err)
	}
	polProof, _, boundDig, err := client.FinishPasskeyAssertion(ctx, buildTestAssertionES256(t, ecPriv, rpID, origin, polOpts.Challenge, credIDB64, authDataFlagUP|authDataFlagUV, 2))
	if err != nil {
		t.Fatalf("FinishPasskeyAssertion policy failed: %v", err)
	}
	if boundDig != nextPolHash {
		t.Fatalf("expected bound digest %s, got %s", nextPolHash, boundDig)
	}

	// Attempt to sneak a DIFFERENT looser policy under that passkey proof -> MUST FAIL!
	tamperedPol := nextPol
	tamperedPol.SOLCap.MaxPerTxRaw = 900_000_000
	if _, err := client.UpdatePolicy(WithOwnerProof(ctx, polProof), w1.ID, tamperedPol, true, w1.Policy.PolicyHash, "owner"); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("tampered policy under digest-bound passkey proof must fail with ErrOwnerAuthRequired, got %v", err)
	}

	// Now apply the EXACT policy matching nextPolHash -> MUST SUCCEED!
	w1, err = client.UpdatePolicy(WithOwnerProof(ctx, polProof), w1.ID, nextPol, true, w1.Policy.PolicyHash, "owner")
	if err != nil {
		t.Fatalf("UpdatePolicy with exact passkey digest binding failed: %v", err)
	}

	// 5. Prepare TWO reviews and verify passkey assertion bound to revA CANNOT execute revB!
	revA, err := client.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w1.ID,
		Operation:   "solana.nativeTransfer",
		Destination: w2.PublicKey,
		AmountHuman: "0.01",
	})
	if err != nil {
		t.Fatal(err)
	}
	revB, err := client.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w1.ID,
		Operation:   "solana.nativeTransfer",
		Destination: w2.PublicKey,
		AmountHuman: "0.02",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Execute without bindingDigest must fail at BeginPasskeyAssertion.
	if _, err := client.BeginPasskeyAssertion(ctx, rpID, origin, "execute", w1.ID, ""); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected execute passkey assertion without txDigest to fail, got %v", err)
	}

	execOpts, err := client.BeginPasskeyAssertion(ctx, rpID, origin, "execute", w1.ID, revA.TxDigest)
	if err != nil {
		t.Fatalf("BeginPasskeyAssertion execute failed: %v", err)
	}
	// Missing UV flag must fail!
	if _, _, _, err := client.FinishPasskeyAssertion(ctx, buildTestAssertionES256(t, ecPriv, rpID, origin, execOpts.Challenge, credIDB64, authDataFlagUP, 3)); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected assertion without UV flag to fail, got %v", err)
	}
	// Re-issue challenge since previous challenge was consumed on failed attempt.
	execOpts, err = client.BeginPasskeyAssertion(ctx, rpID, origin, "execute", w1.ID, revA.TxDigest)
	if err != nil {
		t.Fatal(err)
	}
	// Cloned signCount (<= 2) must fail!
	if _, _, _, err := client.FinishPasskeyAssertion(ctx, buildTestAssertionES256(t, ecPriv, rpID, origin, execOpts.Challenge, credIDB64, authDataFlagUP|authDataFlagUV, 2)); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected stale signCount to fail, got %v", err)
	}
	// Valid signCount=3 bound to revA.TxDigest succeeds.
	execOpts, err = client.BeginPasskeyAssertion(ctx, rpID, origin, "execute", w1.ID, revA.TxDigest)
	if err != nil {
		t.Fatal(err)
	}
	execProof, _, _, err := client.FinishPasskeyAssertion(ctx, buildTestAssertionES256(t, ecPriv, rpID, origin, execOpts.Challenge, credIDB64, authDataFlagUP|authDataFlagUV, 3))
	if err != nil {
		t.Fatalf("FinishPasskeyAssertion execute failed: %v", err)
	}

	// Attempting to execute revB with revA's digest-bound proof MUST FAIL!
	if _, err := client.ExecuteNativeReview(WithOwnerProof(ctx, execProof), w1.ID, revB.ID, "owner"); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected revB execution with revA digest-bound proof to fail, got %v", err)
	}
	// Executing revA with revA's exact digest-bound proof MUST SUCCEED!
	entry, err := client.ExecuteNativeReview(WithOwnerProof(ctx, execProof), w1.ID, revA.ID, "owner")
	if err != nil {
		t.Fatalf("ExecuteNativeReview with exact passkey digest binding failed: %v", err)
	}
	if entry.TxSignature == "" {
		t.Fatalf("expected non-empty txSignature, got %+v", entry)
	}

	// 6. Verify Ed25519 (EdDSA / -8) passkey registration & verification also works.
	edPub, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	edCredID := []byte("cred-ed25519-0002")
	edCredB64 := base64.RawURLEncoding.EncodeToString(edCredID)
	regProof3, _, err := client.AuthorizeOwnerAction(ctx, ownerPass, "passkey-register", "*")
	if err != nil {
		t.Fatal(err)
	}
	edRegOpts, err := client.BeginPasskeyRegistration(WithOwnerProof(ctx, regProof3), rpID, origin, "FIDO2-Ed25519")
	if err != nil {
		t.Fatal(err)
	}
	edCDB64, _ := buildTestClientData("webauthn.create", edRegOpts.Challenge, origin)
	edAttB64 := buildTestAttestationObject(rpID, edCredID, buildTestCOSEEd25519(edPub), authDataFlagUP|authDataFlagUV|authDataFlagAT, 0)
	regProof4, _, err := client.AuthorizeOwnerAction(ctx, ownerPass, "passkey-register", "*")
	if err != nil {
		t.Fatal(err)
	}
	edPK, err := client.FinishPasskeyRegistration(WithOwnerProof(ctx, regProof4), PasskeyRegisterFinishRequest{
		Challenge:         edRegOpts.Challenge,
		Label:             "FIDO2-Ed25519",
		CredentialID:      edCredB64,
		ClientDataJSON:    edCDB64,
		AttestationObject: edAttB64,
	})
	if err != nil || edPK.Alg != coseAlgEdDSA {
		t.Fatalf("Ed25519 passkey registration failed: pk=%+v err=%v", edPK, err)
	}

	// Assert with Ed25519 key (signCount=0 both stored and presented is allowed per WebAuthn spec).
	edAssertOpts, err := client.BeginPasskeyAssertion(ctx, rpID, origin, "unfreeze", w1.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	acdB64, acdRaw := buildTestClientData("webauthn.get", edAssertOpts.Challenge, origin)
	rpHash := sha256.Sum256([]byte(rpID))
	adBytes := append(rpHash[:], authDataFlagUP|authDataFlagUV, 0, 0, 0, 0)
	acdHash := sha256.Sum256(acdRaw)
	edSig := ed25519.Sign(edPriv, append(append([]byte{}, adBytes...), acdHash[:]...))
	if _, _, _, err := client.FinishPasskeyAssertion(ctx, PasskeyAssertFinishRequest{
		Challenge:         edAssertOpts.Challenge,
		CredentialID:      edCredB64,
		ClientDataJSON:    acdB64,
		AuthenticatorData: base64.RawURLEncoding.EncodeToString(adBytes),
		Signature:         base64.RawURLEncoding.EncodeToString(edSig),
	}); err != nil {
		t.Fatalf("Ed25519 passkey assertion failed: %v", err)
	}

	// Audit chain must verify cleanly across all passkey events.
	if count, err := client.VerifyAuditChain(ctx); err != nil || count == 0 {
		t.Fatalf("VerifyAuditChain failed: count=%d err=%v", count, err)
	}
	// Confirm review TTL was tightened to <= 90s.
	if ttl := revA.ExpiresAt - revA.CreatedAt; ttl > 90 {
		t.Fatalf("expected review TTL <= 90s, got %ds", ttl)
	}
	if !strings.HasPrefix(revA.TxDigest, "") || len(revA.TxDigest) != 64 {
		t.Fatalf("expected 64-hex txDigest, got %q", revA.TxDigest)
	}
}
