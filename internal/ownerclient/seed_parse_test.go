package ownerclient

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/fased-ai/fased-app/internal/onchain"
)

func TestParseImportSeedFormatsAndRejections(t *testing.T) {
	t.Parallel()

	// Use a seed starting with 0x00 so leading-zero Base58 preservation is exercised too.
	seed := make([]byte, 32)
	seed[0] = 0
	for i := 1; i < len(seed); i++ {
		seed[i] = byte(i * 7)
	}
	priv := ed25519.NewKeyFromSeed(seed) // 64 bytes: seed || pub
	keypair := []byte(priv)

	// 1. 64-char hex seed (with surrounding whitespace).
	gotHex, err := parseImportSeed("  " + hex.EncodeToString(seed) + "\n")
	if err != nil || !bytes.Equal(gotHex, seed) {
		t.Fatalf("hex parse failed: got=%x want=%x err=%v", gotHex, seed, err)
	}

	// 2. Solana CLI JSON 64-byte keypair.
	json64, _ := json.Marshal(intsFromBytes(keypair))
	gotJSON64, err := parseImportSeed(string(json64))
	if err != nil || !bytes.Equal(gotJSON64, seed) {
		t.Fatalf("JSON 64 parse failed: err=%v", err)
	}

	// 3. Solana CLI JSON 32-byte seed.
	json32, _ := json.Marshal(intsFromBytes(seed))
	gotJSON32, err := parseImportSeed(string(json32))
	if err != nil || !bytes.Equal(gotJSON32, seed) {
		t.Fatalf("JSON 32 parse failed: err=%v", err)
	}

	// 4. Base58 32-byte seed (preserves leading '1' zero byte).
	b58Seed := onchain.Base58(seed)
	gotB58Seed, err := parseImportSeed(b58Seed)
	if err != nil || !bytes.Equal(gotB58Seed, seed) {
		t.Fatalf("Base58 32 parse failed: input=%s err=%v", b58Seed, err)
	}

	// 5. Base58 64-byte keypair (Phantom / Backpack export).
	b58Pair := onchain.Base58(keypair)
	gotB58Pair, err := parseImportSeed(b58Pair)
	if err != nil || !bytes.Equal(gotB58Pair, seed) {
		t.Fatalf("Base58 64 parse failed: err=%v", err)
	}

	// Negative checks.
	mismatchedPair := bytes.Clone(keypair)
	mismatchedPair[63] ^= 0xff
	mismatchedJSON, _ := json.Marshal(intsFromBytes(mismatchedPair))
	mismatchedB58 := onchain.Base58(mismatchedPair)

	badInputs := []string{
		"",
		"   ",
		hex.EncodeToString(seed[:31]),
		"[1, 2, 3]",
		"[1, 256, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32]",
		string(mismatchedJSON),
		mismatchedB58,
		"0OIlinvalidbase58",
	}
	for _, bad := range badInputs {
		if got, err := parseImportSeed(bad); err == nil {
			t.Fatalf("accepted invalid seed %q -> %x", bad, got)
		}
	}
}

func intsFromBytes(b []byte) []int {
	out := make([]int, len(b))
	for i, v := range b {
		out[i] = int(v)
	}
	return out
}
