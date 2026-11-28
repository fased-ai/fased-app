package onchain

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// Real Phase 1 signed manifest emitted by token/sat and published in wen/public/.well-known/wen-lean-v1.json.
const realPhase1ManifestJSON = `{
  "version": 1,
  "keyId": "2a7999659ee95287425ea6beba53b849857ca93e2ccb014ef699bd77bb7b53cb",
  "payload": {"schema":"wen-lean-v1-deployment-manifest/1","sequence":"1","issuedAt":1795835050,"expiresAt":1827371050,"minFasedVersion":"0.5.0","protocolSchemaHash":"76d3a86010f7e22a02006fdce70b79b7773dd2cd0f0462bef28548bbcb7d5d72","clusters":{"devnet":{"status":"active","genesisHash":"EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG","programId":"WAcbznz9veHLEU4qup2eqvwQcGzLeV7hYvAtfaQTrVY","programDataAddress":"6YHn2R8rauQs5JT1ACd6oyrZ5PsJ2SKEP6oZLdvx1S7z","ledgerId":"DXYmEs4veeHR5sFXMdnsfmrjL3gEuGhuawcQwxgb5V46","mint":"9GYRUQfTjz68Fuab7Mt1r9F8NT7YpV7qKyARw66RRwUE","btcMint":"2bBh5wMSYniLm8jpH9JtR4sxw4BCx7Q2q2AkX2FmGsd9","expectedElfBytes":648416,"expectedElfSha256":"32056042a15a228fe94ac3fc8e0d315d98ce4657576abd787fe186301581b245","deploymentSlot":"527024902","allowedUpgradeAuthorities":["C9y1TCfmUh3DbCpec1GvuuWdV4EYcE2N23tsgcUbgJ4N"],"allowImmutable":true,"minStakeAtoms":"1000000000","lookupTable":"","releaseNotesUrl":"https://docs.wen.network"},"mainnet-beta":{"status":"pending","genesisHash":"5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d","programId":"","programDataAddress":"","ledgerId":"","mint":"","btcMint":"cbbtcf3aa214zXHbiAZQwf4122FBYbraNdFqgw4iMij","expectedElfBytes":650592,"expectedElfSha256":"2d9a48f6ecc4114e11c03c1744b291033f39f11445d3292fa9f8caf12999ffc7","deploymentSlot":"0","allowedUpgradeAuthorities":[],"allowImmutable":true,"minStakeAtoms":"1000000000","lookupTable":"","releaseNotesUrl":"https://docs.wen.network"}}},
  "signature": "dxuO3F1q8M0LqZc1e89iZhJffRB8i6VcB+EAw2a2gihJoaqaJIDoFFfXuvoBNJ2jE5j59ELDGdyyWV3IO23PDQ=="
}`

func signTestManifest(t *testing.T, priv ed25519.PrivateKey, keyID string, payload []byte) []byte {
	t.Helper()
	msg := append([]byte(LV1XManifestDomain), payload...)
	sig := ed25519.Sign(priv, msg)
	raw, err := json.Marshal(LV1XManifestEnvelope{
		Version:   1,
		KeyID:     keyID,
		Payload:   payload,
		Signature: sig,
	})
	if err != nil {
		t.Fatalf("marshal test manifest: %v", err)
	}
	return raw
}

func TestLV1XManifestVerifyRealSignedPhase1AndGuards(t *testing.T) {
	t.Parallel()

	now := int64(1795836000)
	vm, err := VerifyLV1XManifest([]byte(realPhase1ManifestJSON), LV1XManifestVerifyOptions{
		NowUnix:       now,
		MinSequence:   1,
		ClientVersion: "0.0.0-core.1",
	})
	if err != nil {
		t.Fatalf("VerifyLV1XManifest(real Phase 1) failed: %v", err)
	}
	if !vm.Compatible || vm.SequenceNum != 1 {
		t.Fatalf("expected compatible seq=1, got %+v", vm)
	}
	if vm.PayloadSHA256 != "601d2ddf5029edb5fa7bde2388c000d532e8cf8a30c2518714400e960fdd720e" {
		t.Fatalf("unexpected Phase 1 payload sha256: %s", vm.PayloadSHA256)
	}
	if vm.Payload.Clusters["devnet"].Status != "active" || vm.Payload.Clusters["mainnet-beta"].Status != "pending" {
		t.Fatalf("unexpected cluster statuses: %+v", vm.Payload.Clusters)
	}

	// Tampered payload rejected.
	tampered := strings.Replace(realPhase1ManifestJSON, `"sequence":"1"`, `"sequence":"2"`, 1)
	if _, err := VerifyLV1XManifest([]byte(tampered), LV1XManifestVerifyOptions{NowUnix: now}); err == nil {
		t.Fatal("expected tampered manifest failure")
	}

	// Sequence rollback rejected.
	if _, err := VerifyLV1XManifest([]byte(realPhase1ManifestJSON), LV1XManifestVerifyOptions{NowUnix: now, MinSequence: 2}); err == nil {
		t.Fatal("expected rollback rejection")
	}

	// Expired manifest rejected.
	if _, err := VerifyLV1XManifest([]byte(realPhase1ManifestJSON), LV1XManifestVerifyOptions{NowUnix: 1827371051}); err == nil {
		t.Fatal("expected expiration rejection")
	}

	// Old binary (< 0.5.0) triggers fail-closed Compatible=false gate without signature error.
	vmOld, err := VerifyLV1XManifest([]byte(realPhase1ManifestJSON), LV1XManifestVerifyOptions{NowUnix: now, ClientVersion: "0.4.9"})
	if err != nil || vmOld.Compatible || !strings.Contains(vmOld.CompatibilityError, "Update Required") {
		t.Fatalf("expected Update Required for v0.4.9, got compat=%v err=%q %v", vmOld.Compatible, vmOld.CompatibilityError, err)
	}

	// Signed payload with mismatched protocolSchemaHash triggers fail-closed Compatible=false gate.
	pub, priv, _ := ed25519.GenerateKey(nil)
	sum := sha256.Sum256(pub)
	keyID := hex.EncodeToString(sum[:])
	extra := map[string]string{keyID: hex.EncodeToString(pub)}
	changedSchema := LV1XManifestPayload{
		Schema:             LV1XManifestSchema,
		Sequence:           "2",
		IssuedAt:           now - 10,
		ExpiresAt:          now + 3600,
		MinFasedVersion:    "0.5.0",
		ProtocolSchemaHash: strings.Repeat("a", 64),
		Clusters:           vm.Payload.Clusters,
	}
	pBytes, _ := json.Marshal(changedSchema)
	signedBadSchema := signTestManifest(t, priv, keyID, pBytes)
	vmSchema, err := VerifyLV1XManifest(signedBadSchema, LV1XManifestVerifyOptions{
		NowUnix:      now,
		ExtraKeysHex: extra,
	})
	if err != nil || vmSchema.Compatible || !strings.Contains(vmSchema.CompatibilityError, "schema") {
		t.Fatalf("expected schema incompatibility verdict, got compat=%v err=%q %v", vmSchema.Compatible, vmSchema.CompatibilityError, err)
	}
}

func FuzzVerifyLV1XManifest(f *testing.F) {
	f.Add([]byte(realPhase1ManifestJSON))
	f.Add([]byte(`{"version":1,"keyId":"bad"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = VerifyLV1XManifest(data, LV1XManifestVerifyOptions{NowUnix: 1795836000})
	})
}
