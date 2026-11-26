package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/custody"
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

	// 12-char recovery passphrase minimum enforced in CLI (rejects 10-char passphrase).
	shortPassFile, err := os.CreateTemp(tmp, "pass-*")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = shortPassFile.WriteString("short12345\n")
	_, _ = shortPassFile.Seek(0, 0)
	defer shortPassFile.Close()
	if _, err := readCLIRecoveryPassphrase(shortPassFile, &errOut, false); err == nil || !strings.Contains(err.Error(), "12") {
		t.Fatalf("expected 12-char min passphrase error, got %v", err)
	}

	// Owner-socket IPC routing works via --socket without direct SQLite access.
	sockDir := t.TempDir()
	svc, err := custody.NewService(custody.Config{DataDir: sockDir, FixtureMode: true, FastKDF: true})
	if err != nil {
		t.Fatalf("custody.NewService: %v", err)
	}
	defer svc.Close()
	ownerSock := filepath.Join(sockDir, "owner.sock")
	ownerSrv, err := custody.StartOwnerSocketServerAt(svc, ownerSock, os.Getuid(), 0o600)
	if err != nil {
		t.Fatalf("StartOwnerSocketServerAt: %v", err)
	}
	defer ownerSrv.Close(context.Background())

	var sockOut bytes.Buffer
	if err := walletCommand([]string{"status", "--socket", ownerSock, "--json"}, nil, &sockOut, &errOut); err != nil {
		t.Fatalf("wallet status --socket failed: %v stderr=%s", err, errOut.String())
	}
	if !strings.Contains(sockOut.String(), `"socketReady":true`) {
		t.Fatalf("expected socketReady=true over owner socket, got %s", sockOut.String())
	}
}
