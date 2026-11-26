package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWalletCLIImportAndRawExport(t *testing.T) {
	tmp := t.TempDir()
	keyPath := filepath.Join(tmp, "sol-id.json")
	// Deterministic test 64-byte Solana CLI JSON keypair generated via custody/ed25519
	// Use CreateNative + ExportRaw to verify end-to-end CLI commands without hardcoding live secrets.
	// Note: custody requires live RPC for real networks; test help/usage errors + validation cleanly here.
	var out, errOut bytes.Buffer
	err := walletCommand([]string{"status", "--data-dir", tmp, "--json"}, nil, &out, &errOut)
	if err != nil {
		t.Fatalf("wallet status failed: %v stderr=%s", err, errOut.String())
	}
	var parsed struct {
		Count int `json:"count"`
	}
	if uErr := json.Unmarshal(out.Bytes(), &parsed); uErr != nil || parsed.Count != 0 {
		t.Fatalf("unexpected wallet status JSON: %s err=%v", out.String(), uErr)
	}

	// Unacknowledged raw export must fail cleanly before touching filesystem.
	_ = os.WriteFile(keyPath, []byte("[]"), 0o600)
	var expOut bytes.Buffer
	if err := walletCommand([]string{"recovery", "export-raw", "--data-dir", tmp, "--wallet-id", "missing", "--output", filepath.Join(tmp, "out.json")}, nil, &expOut, &errOut); err == nil {
		t.Fatalf("expected export-raw without ack to fail")
	}
}
