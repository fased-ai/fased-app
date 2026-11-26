package custody

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mockSolanaRPCTransport(genesis string, blockhashB58 string, balance uint64, failBroadcast bool) http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(raw, &req)
		var res any
		switch req.Method {
		case "getGenesisHash":
			res = genesis
		case "getBalance":
			res = map[string]any{"value": balance}
		case "getLatestBlockhash":
			res = map[string]any{"value": map[string]any{"blockhash": blockhashB58}}
		case "simulateTransaction":
			res = map[string]any{"value": map[string]any{"err": nil}}
		case "sendTransaction":
			if failBroadcast {
				return nil, errors.New("rpc timeout")
			}
			res = "5xMockSig11111111111111111111111111111111111"
		case "getSignatureStatuses":
			res = map[string]any{"value": []any{map[string]any{"confirmationStatus": "finalized", "err": nil}}}
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": res})
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})
}

func TestCustodyPoliciesRolesAtomicCapsRecoveryAndHardwareSigning(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()
	bh := Base58Encode(make([]byte, 32))
	bh = Base58Encode([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32})

	svc, err := NewService(Config{
		DataDir:      tmpDir,
		FastKDF:      true,
		RPCTransport: mockSolanaRPCTransport(SolanaMainnetGenesis, bh, 5_000_000_000, false),
		Now:          func() time.Time { return time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	// 1. Socket server boots & responds.
	sockSrv, err := StartSocketServer(svc)
	if err != nil {
		t.Fatalf("StartSocketServer: %v", err)
	}
	t.Cleanup(func() { _ = sockSrv.Close(context.Background()) })
	if !ProbeSocket(ctx, tmpDir) {
		t.Fatalf("expected app.sock ProbeSocket=true")
	}

	// 2. Create native agent wallet + vault wallet -> pinned to mainnet-beta from genesis, fail-closed baseline, zero raw key in projection.
	agentW, err := svc.CreateNativeWallet(ctx, "Trading Bot", RoleAgent, "https://mainnet.helius-rpc.com/?api-key=secret123")
	if err != nil {
		t.Fatalf("CreateNativeWallet(agent): %v", err)
	}
	if agentW.RPC.Cluster != "mainnet-beta" || agentW.RPC.Host != "https://mainnet.helius-rpc.com" || strings.Contains(fmt.Sprintf("%+v", agentW), "secret123") {
		t.Fatalf("RPC sanitization/genesis pinning failed: %+v", agentW.RPC)
	}
	vaultW, err := svc.CreateNativeWallet(ctx, "Treasury Vault", RoleVault, "https://mainnet.helius-rpc.com")
	if err != nil {
		t.Fatalf("CreateNativeWallet(vault): %v", err)
	}

	// 3. Vault NEVER allows autonomous connections or loosening out of manual_review.
	if _, err := svc.UpdateConnections(ctx, vaultW.ID, WalletConnections{AgentIDs: []string{"op"}, AllowTradeBots: true}, "owner"); !errors.Is(err, ErrRoleDenied) {
		t.Fatalf("expected ErrRoleDenied binding vault to bots/agents, got %v", err)
	}

	// 4. Fail-closed baseline refuses transfers before explicit policy approval.
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    agentW.ID,
		Operation:   "solana.nativeTransfer",
		Destination: vaultW.Handle,
		AmountHuman: "0.1",
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected fail-closed baseline ErrPolicyDenied, got %v", err)
	}

	// 5. Expanding policy without confirmLoosen fails; with confirmLoosen + matching PolicyHash succeeds.
	expanded := agentW.Policy
	expanded.ApprovalMode = ApprovalBoundedAuto
	expanded.Operations = []string{"solana.nativeTransfer"}
	expanded.Programs = []string{SolanaSystemProgram, SolanaComputeBudget}
	expanded.Destinations = []string{vaultW.Handle}
	expanded.SOLCap.MaxPerTxRaw = 500_000_000   // 0.5 SOL
	expanded.SOLCap.MaxDailyRaw = 1_000_000_000 // 1.0 SOL

	if _, err := svc.UpdatePolicy(ctx, agentW.ID, expanded, false, "", "owner"); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected loosen without confirmation rejected, got %v", err)
	}
	agentW, err = svc.UpdatePolicy(ctx, agentW.ID, expanded, true, agentW.Policy.PolicyHash, "owner")
	if err != nil {
		t.Fatalf("UpdatePolicy(loosen): %v", err)
	}

	// Tightening works WITHOUT confirmLoosen.
	tighter := agentW.Policy
	tighter.SOLCap.MaxPerTxRaw = 300_000_000 // 0.3 SOL
	agentW, err = svc.UpdatePolicy(ctx, agentW.ID, tighter, false, "", "owner")
	if err != nil || agentW.Policy.SOLCap.MaxPerTxRaw != 300_000_000 {
		t.Fatalf("UpdatePolicy(tighten) failed: %+v err=%v", agentW.Policy, err)
	}

	// 6. Connections binding & deterministic ResolveWallet contract.
	agentW, err = svc.UpdateConnections(ctx, agentW.ID, WalletConnections{
		AgentIDs:           []string{"operator"},
		AllowTradeBots:     true,
		AllowTradeManual:   true,
		DefaultAgentWallet: true,
	}, "owner")
	if err != nil {
		t.Fatalf("UpdateConnections(agent): %v", err)
	}
	resolved, err := svc.ResolveWallet(ctx, WalletSelector{AgentID: "operator", IsAutonomous: true})
	if err != nil || resolved.ID != agentW.ID {
		t.Fatalf("ResolveWallet failed: %+v err=%v", resolved, err)
	}

	// 7. Prepare + Execute native transfer tracks daily cap atomically.
	rev, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    agentW.ID,
		Operation:   "solana.nativeTransfer",
		Destination: vaultW.Handle,
		AmountHuman: "0.25",
		IsBotOrAuto: true,
	})
	if err != nil || rev.AmountRaw != 250_000_000 {
		t.Fatalf("PrepareReview failed: %+v err=%v", rev, err)
	}
	entry, err := svc.ExecuteNativeReview(ctx, agentW.ID, rev.ID, "bot")
	if err != nil || entry.TxSignature == "" {
		t.Fatalf("ExecuteNativeReview failed: %+v err=%v", entry, err)
	}
	after, _ := svc.GetWallet(ctx, agentW.ID)
	if after.DailyReservedLamports != 250_000_000 {
		t.Fatalf("expected 250M reserved lamports before reconciliation, got %d", after.DailyReservedLamports)
	}
	reconciled, err := svc.ReconcileLedgerEntry(ctx, agentW.ID, entry.ID)
	if err != nil || reconciled.State != LedgerConfirmed {
		t.Fatalf("ReconcileLedgerEntry failed: %+v err=%v", reconciled, err)
	}
	afterRec, _ := svc.GetWallet(ctx, agentW.ID)
	if afterRec.DailySpentLamports != 250_000_000 || afterRec.DailyReservedLamports != 0 {
		t.Fatalf("expected spent=250M reserved=0 after reconcile, got spent=%d res=%d", afterRec.DailySpentLamports, afterRec.DailyReservedLamports)
	}

	// 8. RPC cluster mismatch rejected cleanly.
	_, _, _, mismatchErr := VerifyAndPinRPC(ctx, mockSolanaRPCTransport(SolanaDevnetGenesis, bh, 0, false), "https://api.devnet.solana.com", "", SolanaMainnetGenesis, false)
	if mismatchErr == nil {
		t.Fatalf("expected RPC genesis mismatch error when swapping mainnet wallet to devnet")
	}

	// 9. Hardware wallet attachment + byte-for-byte tamper check.
	hwPub, hwPriv, _ := ed25519.GenerateKey(rand.Reader)
	hwPubB58 := Base58Encode(hwPub)
	hwW, err := svc.AttachExternalWallet(ctx, CustodyHardware, "Ledger Vault", RoleVault, hwPubB58, "https://mainnet.helius-rpc.com")
	if err != nil {
		t.Fatalf("AttachExternalWallet: %v", err)
	}
	hwPol := hwW.Policy
	hwPol.Operations = []string{"solana.nativeTransfer"}
	hwPol.Programs = []string{SolanaSystemProgram, SolanaComputeBudget}
	hwPol.Destinations = []string{agentW.PublicKey}
	hwPol.SOLCap.MaxPerTxRaw = 500_000_000
	hwPol.SOLCap.MaxDailyRaw = 1_000_000_000
	hwW, err = svc.UpdatePolicy(ctx, hwW.ID, hwPol, true, hwW.Policy.PolicyHash, "owner")
	if err != nil {
		t.Fatalf("UpdatePolicy(hw): %v", err)
	}
	hwRev, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    hwW.ID,
		Operation:   "solana.nativeTransfer",
		Destination: agentW.PublicKey,
		AmountHuman: "0.2",
	})
	if err != nil {
		t.Fatalf("PrepareReview(hw): %v", err)
	}
	msgBytes, _ := base64.StdEncoding.DecodeString(hwRev.UnsignedMsgBase64)
	tamperedMsg := append([]byte(nil), msgBytes...)
	tamperedMsg[len(tamperedMsg)-1] ^= 0xff
	tamperedTx, _ := SignLegacyMessage(tamperedMsg, hwPriv)
	if _, err := svc.SubmitHardwareSignedReview(ctx, hwW.ID, hwRev.ID, base64.StdEncoding.EncodeToString(tamperedTx), "ledger"); !errors.Is(err, ErrTxTampered) {
		t.Fatalf("expected ErrTxTampered on altered hardware tx, got %v", err)
	}
	validTx, _ := SignLegacyMessage(msgBytes, hwPriv)
	hwEntry, err := svc.SubmitHardwareSignedReview(ctx, hwW.ID, hwRev.ID, base64.StdEncoding.EncodeToString(validTx), "ledger")
	if err != nil || hwEntry.State != LedgerBroadcast {
		t.Fatalf("SubmitHardwareSignedReview failed: %+v err=%v", hwEntry, err)
	}

	// 10. Encrypted recovery export/import + CLI raw 0600 export & audit.
	archive, fp, err := svc.ExportEncryptedRecovery(ctx, agentW.ID, "recover-pass-123", "owner")
	if err != nil || fp == "" || strings.Contains(string(archive), "private") {
		t.Fatalf("ExportEncryptedRecovery failed: err=%v fp=%q", err, fp)
	}
	rawOut := filepath.Join(tmpDir, "raw-agent-key.json")
	if err := svc.ExportRawKeypairFile(ctx, agentW.ID, rawOut, false); err == nil {
		t.Fatalf("expected raw export refusal without acknowledge flag")
	}
	if err := svc.ExportRawKeypairFile(ctx, agentW.ID, rawOut, true); err != nil {
		t.Fatalf("ExportRawKeypairFile: %v", err)
	}
	info, _ := os.Stat(rawOut)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0o600 permissions on raw export, got %o", info.Mode().Perm())
	}
	_, _, audits, _ := svc.ListActivity(ctx, agentW.ID)
	sawRawAudit := false
	for _, a := range audits {
		if a.Action == "raw_keypair_exported" {
			sawRawAudit = true
		}
	}
	if !sawRawAudit {
		t.Fatalf("expected raw_keypair_exported in immutable audit log")
	}
}
