package onchain

import (
	"crypto/ed25519"
	"encoding/binary"
	"testing"
)

func TestSolanaSOLAndSPLWithdrawalBuildAndValidate(t *testing.T) {
	t.Parallel()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	destPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var feePayer, dest, mint, blockhash [32]byte
	copy(feePayer[:], pub)
	copy(dest[:], destPub)
	copy(mint[:], mustDecodeKey(t, USDC))
	for i := range blockhash {
		blockhash[i] = byte(i + 11)
	}

	// 1. Native SOL withdrawal message + validation
	solMsg, err := BuildSolanaTransferMessage(feePayer, dest, blockhash, 250_000_000, 100_000, 5_000)
	if err != nil {
		t.Fatalf("BuildSolanaTransferMessage failed: %v", err)
	}
	parsedSOL, err := ValidateWithdrawalMessage(solMsg, feePayer)
	if err != nil {
		t.Fatalf("ValidateWithdrawalMessage(SOL) failed: %v", err)
	}
	if parsedSOL.Kind != "sol" || parsedSOL.Amount != 250_000_000 || parsedSOL.Destination != Base58(dest[:]) {
		t.Fatalf("unexpected parsed SOL withdrawal: %+v", parsedSOL)
	}
	sig := ed25519.Sign(priv, solMsg)
	if !ed25519.Verify(pub, solMsg, sig) {
		t.Fatal("SOL withdrawal signature check failed")
	}

	// Self-transfer rejected
	if _, err := BuildSolanaTransferMessage(feePayer, feePayer, blockhash, 1000, 50_000, 0); err == nil {
		t.Fatal("expected self-transfer SOL withdrawal to fail")
	}
	// Non-transfer System opcode (e.g. Assign=1) rejected
	badSys := append([]byte(nil), solMsg...)
	binary.LittleEndian.PutUint32(badSys[len(badSys)-12:len(badSys)-8], 1)
	if _, err := ValidateWithdrawalMessage(badSys, feePayer); err == nil {
		t.Fatal("expected non-Transfer System opcode to be rejected")
	}

	// 2. SPL TransferChecked with & without CreateIdempotent ATA
	for _, createATA := range []bool{false, true} {
		splMsg, err := BuildSPLTransferMessage(feePayer, dest, mint, blockhash, 12_500_000, 6, createATA, 120_000, 1_500)
		if err != nil {
			t.Fatalf("BuildSPLTransferMessage(createATA=%v) failed: %v", createATA, err)
		}
		parsedSPL, err := ValidateWithdrawalMessage(splMsg, feePayer)
		if err != nil {
			t.Fatalf("ValidateWithdrawalMessage(SPL, createATA=%v) failed: %v", createATA, err)
		}
		wantATA, _ := TokenAccount(Base58(dest[:]), USDC)
		if parsedSPL.Kind != "spl" || parsedSPL.Amount != 12_500_000 || parsedSPL.Decimals != 6 || parsedSPL.Mint != USDC || parsedSPL.Destination != wantATA {
			t.Fatalf("unexpected parsed SPL withdrawal (createATA=%v): %+v", createATA, parsedSPL)
		}
	}
}

func mustDecodeKey(t *testing.T, addr string) []byte {
	t.Helper()
	b, err := Key(addr)
	if err != nil {
		t.Fatalf("Key(%q): %v", addr, err)
	}
	return b
}
