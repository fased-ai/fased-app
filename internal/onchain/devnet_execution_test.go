package onchain

import (
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDevnetMessageBuildAndValidate(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var feePayer [32]byte
	copy(feePayer[:], pub)
	var blockhash [32]byte
	for i := range blockhash {
		blockhash[i] = byte(i + 1)
	}

	msg, err := BuildDevnetComputeMemoMessage(feePayer, blockhash, 120_000, 1500, "fased-devnet-rehearsal")
	if err != nil {
		t.Fatalf("BuildDevnetComputeMemoMessage: %v", err)
	}
	if err := ValidateDevnetMessage(msg, feePayer); err != nil {
		t.Fatalf("ValidateDevnetMessage valid: %v", err)
	}

	var wrongPayer [32]byte
	wrongPayer[0] = 99
	if err := ValidateDevnetMessage(msg, wrongPayer); err == nil {
		t.Fatal("expected fee payer mismatch failure")
	}

	// Reject versioned prefix (0x80)
	verMsg := append([]byte{0x80}, msg[1:]...)
	if err := ValidateDevnetMessage(verMsg, feePayer); err == nil {
		t.Fatal("expected versioned message rejection")
	}

	// Reject non-allowlisted / unknown program key in account table (replace MemoProgram at account index 2)
	badProgMsg := append([]byte(nil), msg...)
	badProgOffset := 3 + 1 + 64
	badProgMsg[badProgOffset] = 0xAA
	if err := ValidateDevnetMessage(badProgMsg, feePayer); err == nil {
		t.Fatal("expected unapproved program ID rejection")
	}

	// Reject trailing bytes
	if err := ValidateDevnetMessage(append(append([]byte(nil), msg...), 0x00), feePayer); err == nil {
		t.Fatal("expected trailing byte rejection")
	}

	sigBytes := ed25519.Sign(priv, msg)
	var sig [64]byte
	copy(sig[:], sigBytes)
	pkt, sigB58 := AssembleSignedLegacyTransaction(sig, msg)
	if len(pkt) != 1+64+len(msg) || pkt[0] != 1 || sigB58 != Base58(sig[:]) {
		t.Fatalf("unexpected assembled packet len=%d sig=%q", len(pkt), sigB58)
	}
	if !ed25519.Verify(pub, msg, sig[:]) {
		t.Fatal("signature verify failed")
	}
}

func TestDevnetExecutorLifecycleAndGenesisGuard(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var feePayer [32]byte
	copy(feePayer[:], pub)
	var hashRaw [32]byte
	for i := range hashRaw {
		hashRaw[i] = byte(i + 7)
	}
	blockhashB58 := Base58(hashRaw[:])

	genesis := SolanaDevnetGenesisHash
	var sendCalls, statusCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			ID     uint64 `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(raw, &req)
		var result any
		switch req.Method {
		case "getGenesisHash":
			result = genesis
		case "getLatestBlockhash":
			result = map[string]any{
				"context": map[string]any{"slot": 12345},
				"value":   map[string]any{"blockhash": blockhashB58, "lastValidBlockHeight": 9000},
			}
		case "getRecentPrioritizationFees":
			result = []map[string]any{
				{"slot": 1, "prioritizationFee": 100},
				{"slot": 2, "prioritizationFee": 500},
				{"slot": 3, "prioritizationFee": 1200},
				{"slot": 4, "prioritizationFee": 2_000_000}, // capped on P75 check
			}
		case "simulateTransaction":
			result = map[string]any{
				"context": map[string]any{"slot": 12346},
				"value":   map[string]any{"err": nil},
			}
		case "getBlockHeight":
			result = uint64(8990)
		case "sendTransaction":
			sendCalls++
			result = ""
		case "getSignatureStatuses":
			statusCalls++
			confirm := "processed"
			if statusCalls >= 3 {
				confirm = "confirmed"
			}
			result = map[string]any{
				"value": []map[string]any{
					{"err": nil, "confirmationStatus": confirm},
				},
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  result,
		})
	}))
	defer srv.Close()

	exec, err := NewDevnetExecutor(srv.URL+"/?api-key=secret123", srv.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	if exec.SanitizedHost() != srv.URL || strings.Contains(exec.SanitizedHost(), "secret123") {
		t.Fatalf("SanitizedHost leaked secret or mismatch: %q", exec.SanitizedHost())
	}

	gotHash, gotRaw, lastValid, slot, err := exec.LatestBlockhash(t.Context())
	if err != nil || gotHash != blockhashB58 || gotRaw != hashRaw || lastValid != 9000 || slot != 12345 {
		t.Fatalf("LatestBlockhash failed: hash=%q lastValid=%d slot=%d err=%v", gotHash, lastValid, slot, err)
	}
	p75, count, err := exec.EstimatePriorityFee(t.Context())
	if err != nil || count != 4 || p75 != 1200 {
		t.Fatalf("EstimatePriorityFee got p75=%d count=%d err=%v", p75, count, err)
	}

	msg, err := BuildDevnetComputeMemoMessage(feePayer, gotRaw, 100_000, p75, "unit-test")
	if err != nil {
		t.Fatal(err)
	}
	// Verify instruction data encodes SetComputeUnitLimit(100_000) & SetComputeUnitPrice(1200)
	var priceCheck [8]byte
	binary.LittleEndian.PutUint64(priceCheck[:], 1200)
	if !strings.Contains(string(msg), string(priceCheck[:])) {
		t.Fatal("expected ComputeBudget price instruction payload")
	}

	var sig [64]byte
	copy(sig[:], ed25519.Sign(priv, msg))
	pkt, sigB58 := AssembleSignedLegacyTransaction(sig, msg)
	simSlot, simErr, err := exec.SimulateTransaction(t.Context(), pkt)
	if err != nil || simErr != "" || simSlot != 12346 {
		t.Fatalf("SimulateTransaction failed: slot=%d simErr=%q err=%v", simSlot, simErr, err)
	}

	res, err := exec.SendWithRebroadcast(t.Context(), pkt, sigB58, lastValid, RebroadcastConfig{
		MaxAttempts:  3,
		InitialDelay: time.Millisecond,
		MaxDelay:     time.Millisecond,
		Sleep:        func(context.Context, time.Duration) error { return nil },
	})
	if err != nil || !res.Confirmed || sendCalls != 2 || res.Attempts != 2 {
		t.Fatalf("SendWithRebroadcast unexpected: %+v sendCalls=%d err=%v", res, sendCalls, err)
	}

	// Mainnet genesis hash must fail closed immediately.
	genesis = "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d"
	if err := exec.VerifyGenesis(t.Context()); err == nil {
		t.Fatal("expected mainnet genesis rejection")
	}

	// MainnetExecutor pinned to SolanaMainnetGenesisHash passes on mainnet genesis and rejects devnet.
	mainExec, err := NewMainnetExecutor(srv.URL, srv.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	if err := mainExec.VerifyGenesis(t.Context()); err != nil {
		t.Fatalf("MainnetExecutor VerifyGenesis failed on mainnet genesis: %v", err)
	}
	genesis = SolanaDevnetGenesisHash
	if err := mainExec.VerifyGenesis(t.Context()); err == nil {
		t.Fatal("expected MainnetExecutor to reject devnet genesis")
	}
}

func TestSolanaMainnetV0ALTValidation(t *testing.T) {
	t.Parallel()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	altKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var feePayer, bh [32]byte
	copy(feePayer[:], pub)
	for i := range bh {
		bh[i] = byte(i + 41)
	}
	jupKey, err := Key(Jupiter)
	if err != nil {
		t.Fatal(err)
	}
	sysKey, err := Key(System)
	if err != nil {
		t.Fatal(err)
	}

	jupData := make([]byte, 40)
	copy(jupData[:8], routeV2[:])
	validV0 := make([]byte, 0, 256)
	validV0 = append(validV0, 0x80, 1, 0, 1, 2)
	validV0 = append(validV0, feePayer[:]...)
	validV0 = append(validV0, jupKey...)
	validV0 = append(validV0, bh[:]...)
	validV0 = append(validV0, 1, 1, 1, 2, byte(len(jupData)))
	validV0 = append(validV0, jupData...)
	validV0 = append(validV0, 1)
	validV0 = append(validV0, altKey...)
	validV0 = append(validV0, 1, 0, 0)
	if err := ValidateSolanaMainnetMessage(validV0, feePayer); err != nil {
		t.Fatalf("expected valid v0/ALT message to pass, got %v", err)
	}

	// Trailing byte rejected.
	if err := ValidateSolanaMainnetMessage(append(append([]byte(nil), validV0...), 0x00), feePayer); err == nil {
		t.Fatal("expected trailing byte rejection in v0 message")
	}

	// Out-of-bounds loaded table index rejected.
	oobV0 := append([]byte(nil), validV0...)
	oobV0[len(oobV0)-1-2-32-len(jupData)-2] = 5 // acctIdx=5 with only 3 total accounts
	if err := ValidateSolanaMainnetMessage(oobV0, feePayer); err == nil {
		t.Fatal("expected out-of-range v0 lookup index rejection")
	}

	// Unauthorized System::Assign opcode rejected in v0 message.
	assignData := make([]byte, 12)
	binary.LittleEndian.PutUint32(assignData[0:4], 1)
	badSysV0 := make([]byte, 0, 192)
	badSysV0 = append(badSysV0, 0x80, 1, 0, 1, 2)
	badSysV0 = append(badSysV0, feePayer[:]...)
	badSysV0 = append(badSysV0, sysKey...)
	badSysV0 = append(badSysV0, bh[:]...)
	badSysV0 = append(badSysV0, 1, 1, 1, 0, byte(len(assignData)))
	badSysV0 = append(badSysV0, assignData...)
	badSysV0 = append(badSysV0, 0) // 0 lookup tables
	if err := ValidateSolanaMainnetMessage(badSysV0, feePayer); err == nil {
		t.Fatal("expected non-Transfer System opcode to be rejected")
	}

	// Non-allowlisted program rejected.
	unknownProg, _, _ := ed25519.GenerateKey(nil)
	badProgV0 := make([]byte, 0, 192)
	badProgV0 = append(badProgV0, 0x80, 1, 0, 1, 2)
	badProgV0 = append(badProgV0, feePayer[:]...)
	badProgV0 = append(badProgV0, unknownProg...)
	badProgV0 = append(badProgV0, bh[:]...)
	badProgV0 = append(badProgV0, 1, 1, 1, 0, 4, 1, 2, 3, 4, 0)
	if err := ValidateSolanaMainnetMessage(badProgV0, feePayer); err == nil {
		t.Fatal("expected unknown program in v0 message to fail")
	}
}
