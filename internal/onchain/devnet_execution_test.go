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
}
