package custody

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
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
	if after.DailyReservedLamports != 250_005_000 {
		t.Fatalf("expected 250005000 reserved lamports before reconciliation, got %d", after.DailyReservedLamports)
	}
	reconciled, err := svc.ReconcileLedgerEntry(ctx, agentW.ID, entry.ID)
	if err != nil || reconciled.State != LedgerConfirmed {
		t.Fatalf("ReconcileLedgerEntry failed: %+v err=%v", reconciled, err)
	}
	afterRec, _ := svc.GetWallet(ctx, agentW.ID)
	if afterRec.DailySpentLamports != 250_005_000 || afterRec.DailyReservedLamports != 0 {
		t.Fatalf("expected spent=250005000 reserved=0 after reconcile, got spent=%d res=%d", afterRec.DailySpentLamports, afterRec.DailyReservedLamports)
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

type staticKEKSource struct{ key []byte }

func (k staticKEKSource) Key(_ context.Context, _ string, _ bool) ([]byte, error) {
	return append([]byte(nil), k.key...), nil
}

func TestCustodyHardeningSSRFWitnessKDFSimAuditAndFile(t *testing.T) {
	ctx := t.Context()

	// 1. SSRF private/RFC1918/CGNAT/link-local/loopback IP rejection.
	for _, blocked := range []string{"169.254.169.254", "10.0.0.1", "192.168.1.5", "100.64.0.10", "127.0.0.1"} {
		ip := netip.MustParseAddr(blocked)
		if IsPublicUnicastIP(ip) {
			t.Fatalf("expected IsPublicUnicastIP(%s)=false", blocked)
		}
		if blocked != "127.0.0.1" {
			if _, _, err := ValidateSolanaRPCURL("https://" + blocked); err == nil {
				t.Fatalf("expected ValidateSolanaRPCURL(%s) rejected", blocked)
			}
		}
	}
	if !IsPublicUnicastIP(netip.MustParseAddr("8.8.8.8")) {
		t.Fatalf("expected public IP 8.8.8.8 admitted")
	}

	// 2. Witness RPC genesis mismatch & slot skew > 250 rejected.
	skewedTransport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(raw, &req)
		isWitness := strings.Contains(r.URL.Host, "api.mainnet-beta.solana.com")
		var res any
		switch req.Method {
		case "getGenesisHash":
			res = SolanaMainnetGenesis
		case "getSlot":
			if isWitness {
				res = uint64(1000)
			} else {
				res = uint64(1400) // skew = 400 > 250
			}
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": res})
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})
	if _, _, _, err := VerifyAndPinRPC(ctx, skewedTransport, "https://custom-rpc.example.com", "", "", false); err == nil || !strings.Contains(err.Error(), "diverged from witness") {
		t.Fatalf("expected witness slot divergence error, got %v", err)
	}

	// 3. Argon2id KDF DoS bounds, 12-char min recovery passphrase, simulation failure block,
	//    hash-chained audit tamper detection, and O_NOFOLLOW symlink rejection.
	tmpDir := t.TempDir()
	bh := Base58Encode([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32})
	failSimTransport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(raw, &req)
		var res any
		switch req.Method {
		case "getGenesisHash":
			res = SolanaMainnetGenesis
		case "getSlot":
			res = uint64(500)
		case "getLatestBlockhash":
			res = map[string]any{"value": map[string]any{"blockhash": bh}}
		case "simulateTransaction":
			res = map[string]any{"value": map[string]any{"err": "InsufficientFundsForFee"}}
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": res})
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})
	svc, err := NewService(Config{DataDir: tmpDir, FastKDF: true, RPCTransport: failSimTransport})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	w, err := svc.CreateNativeWallet(ctx, "SimGuard", RoleTrading, "https://api.mainnet-beta.solana.com")
	if err != nil {
		t.Fatalf("CreateNativeWallet: %v", err)
	}
	if _, _, err := svc.ExportEncryptedRecovery(ctx, w.ID, "short-11ch", "owner"); err == nil {
		t.Fatalf("expected 12-char min recovery passphrase rejection")
	}
	dosPkg, _ := json.Marshal(RecoveryPackage{
		Version: 1, Format: recoveryFormat, PublicKey: w.PublicKey, KDF: "argon2id",
		KDFTime: 99, KDFMemory: 512000, KDFThreads: 16, Salt: make([]byte, 16), Nonce: make([]byte, 12), Ciphertext: []byte{1, 2, 3},
	})
	if _, err := svc.ImportEncryptedRecovery(ctx, dosPkg, "twelve-chars-pass", "dos", RoleTrading, "https://api.mainnet-beta.solana.com"); err == nil || !strings.Contains(err.Error(), "bounds") {
		t.Fatalf("expected Argon2id KDF bounds rejection, got %v", err)
	}

	destPub, _, _ := ed25519.GenerateKey(rand.Reader)
	pol := w.Policy
	pol.Operations = []string{"solana.nativeTransfer"}
	pol.Programs = []string{SolanaSystemProgram, SolanaComputeBudget}
	pol.Destinations = []string{Base58Encode(destPub)}
	pol.SOLCap.MaxPerTxRaw = 500_000_000
	pol.SOLCap.MaxDailyRaw = 1_000_000_000
	w, err = svc.UpdatePolicy(ctx, w.ID, pol, true, w.Policy.PolicyHash, "owner")
	if err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}
	rev, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID: w.ID, Operation: "solana.nativeTransfer", Destination: Base58Encode(destPub), AmountHuman: "0.1",
	})
	if err != nil {
		t.Fatalf("PrepareReview: %v", err)
	}
	if rev.SimulationOK {
		t.Fatalf("expected SimulationOK=false on failed simulation")
	}
	if _, err := svc.ExecuteNativeReview(ctx, w.ID, rev.ID, "owner"); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected ExecuteNativeReview hard block on failed simulation, got %v", err)
	}

	// File import rejects symlinks & broad permissions.
	kpTarget := filepath.Join(tmpDir, "real-key.json")
	_ = os.WriteFile(kpTarget, []byte("[]"), 0o644)
	if _, err := svc.ImportSolanaKeypairFile(ctx, kpTarget, "badperm", RoleTrading, "https://api.mainnet-beta.solana.com"); err == nil {
		t.Fatalf("expected broad 0644 keypair import rejected")
	}
	kpLink := filepath.Join(tmpDir, "link-key.json")
	_ = os.Symlink(kpTarget, kpLink)
	if _, err := svc.ImportSolanaKeypairFile(ctx, kpLink, "sym", RoleTrading, "https://api.mainnet-beta.solana.com"); err == nil {
		t.Fatalf("expected symlink keypair import rejected via O_NOFOLLOW")
	}

	// Hash-chained audit log detects tampering.
	cnt, err := svc.VerifyAuditChain(ctx)
	if err != nil || cnt < 2 {
		t.Fatalf("VerifyAuditChain failed before tamper: cnt=%d err=%v", cnt, err)
	}
	if _, err := svc.db.ExecContext(ctx, `UPDATE audit_log SET payload_json = replace(payload_json, 'Created', 'Tampered')`); err != nil {
		t.Fatalf("tamper SQL: %v", err)
	}
	if _, err := svc.VerifyAuditChain(ctx); err == nil {
		t.Fatalf("expected VerifyAuditChain to detect row tampering")
	}
}

func TestCustodyKEKSocketIsolationSPLAndJupiterV0(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()
	kek := make([]byte, 32)
	_, _ = rand.Read(kek)

	// 1. OS Keychain KEK wrapping seals master key into .sealed & removes bare master key.
	svc, err := NewService(Config{
		DataDir:      tmpDir,
		FixtureMode:  true,
		FastKDF:      true,
		RequireOSKEK: true,
		KEKSource:    staticKEKSource{key: kek},
	})
	if err != nil {
		t.Fatalf("NewService with KEK: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	if _, err := os.Stat(filepath.Join(svc.Dir(), sealedMasterKeyName)); err != nil {
		t.Fatalf("expected sealed master key: %v", err)
	}
	if _, err := os.Stat(filepath.Join(svc.Dir(), masterKeyName)); !os.IsNotExist(err) {
		t.Fatalf("expected bare master key absent when sealed, got err=%v", err)
	}

	// 2. Socket server with SO_PEERCRED UID verification + SocketClient round-trip + SPL TransferChecked + Jupiter v0 ALT.
	sockPath := filepath.Join(tmpDir, "isolated.sock")
	srv, err := StartSocketServerAt(svc, sockPath, os.Getuid(), 0o600)
	if err != nil {
		t.Fatalf("StartSocketServerAt: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close(context.Background()) })

	client, err := NewSocketClient(sockPath)
	if err != nil {
		t.Fatalf("NewSocketClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	sec, err := client.SecurityStatus(ctx)
	if err != nil || !sec.ProcessIsolated || sec.KEKProtection != KEKOSKeychain {
		t.Fatalf("unexpected socket SecurityStatus: %+v err=%v", sec, err)
	}

	mintPub, _, _ := ed25519.GenerateKey(rand.Reader)
	mintAddr := Base58Encode(mintPub)
	destPub, _, _ := ed25519.GenerateKey(rand.Reader)
	destAddr := Base58Encode(destPub)

	w, err := client.CreateNativeWallet(ctx, "Desk Hot", RoleTrading, "https://api.mainnet-beta.solana.com")
	if err != nil {
		t.Fatalf("Socket CreateNativeWallet: %v", err)
	}
	pol := w.Policy
	pol.Operations = []string{"solana.splTransferChecked", "jupiter.swap"}
	pol.Programs = []string{SolanaSystemProgram, SolanaComputeBudget, SolanaTokenProgram, SolanaATAProgram, SolanaJupiterV6Program}
	pol.Destinations = []string{destAddr}
	pol.SOLCap.MaxPerTxRaw = 100_000_000
	pol.SOLCap.MaxDailyRaw = 500_000_000
	pol.MaxSlippageBps = 100
	pol.TokenCaps = []AssetCap{{
		Asset: "solana:spl:" + mintAddr, Mint: mintAddr, Symbol: "USDC", Decimals: 6,
		MaxPerTxRaw: 50_000_000, MaxDailyRaw: 100_000_000,
	}}
	w, err = client.UpdatePolicy(ctx, w.ID, pol, true, w.Policy.PolicyHash, "owner")
	if err != nil {
		t.Fatalf("Socket UpdatePolicy: %v", err)
	}

	// SPL TransferChecked over socket.
	splRev, err := client.PrepareReview(ctx, PrepareReviewRequest{
		WalletID: w.ID, Operation: "solana.splTransferChecked", Destination: destAddr,
		Mint: mintAddr, Decimals: 6, AmountHuman: "12.5", CreateDestATA: true,
	})
	if err != nil || splRev.AmountRaw != 12_500_000 || splRev.Symbol != "USDC" {
		t.Fatalf("SPL PrepareReview failed: %+v err=%v", splRev, err)
	}
	splEntry, err := client.ExecuteNativeReview(ctx, w.ID, splRev.ID, "owner")
	if err != nil || splEntry.State != LedgerConfirmed {
		t.Fatalf("SPL ExecuteNativeReview failed: %+v err=%v", splEntry, err)
	}

	// Jupiter v0 ALT swap message verification & slippage block.
	bh := Base58Encode([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32})
	altPub, _, _ := ed25519.GenerateKey(rand.Reader)
	poolPub, _, _ := ed25519.GenerateKey(rand.Reader)
	srcATA, _ := onchain.TokenAccount(w.PublicKey, onchain.SOL)
	dstATA, _ := onchain.TokenAccount(w.PublicKey, mintAddr)
	swapData := make([]byte, 39)
	copy(swapData[:8], []byte{187, 100, 250, 204, 49, 196, 175, 20}) // onchain routeV2 discriminator
	binary.LittleEndian.PutUint64(swapData[8:], 20_000_000)
	binary.LittleEndian.PutUint64(swapData[16:], 1_000_000)
	binary.LittleEndian.PutUint16(swapData[24:], 50)
	binary.LittleEndian.PutUint32(swapData[30:], 1)
	quote := onchain.Quote{
		Version:       onchain.Version,
		Owner:         w.PublicKey,
		InputMint:     onchain.SOL,
		OutputMint:    mintAddr,
		Amount:        "20000000",
		Output:        "1000000",
		MinimumOutput: "990000",
		SlippageBPS:    50,
		MaxFeeLamports: 100_000,
		FeeLamports:    5000,
		Blockhash:     bh,
		AddressLookupTables: []onchain.AddressLookupTable{{
			AccountKey: Base58Encode(altPub),
			Addresses:  []string{Base58Encode(poolPub)},
		}},
		Instructions: []onchain.Instruction{{
			ProgramID: SolanaJupiterV6Program,
			Accounts: []onchain.Meta{
				{Pubkey: w.PublicKey, IsSigner: true, IsWritable: true},
				{Pubkey: srcATA, IsWritable: true},
				{Pubkey: dstATA, IsWritable: true},
				{Pubkey: onchain.SOL},
				{Pubkey: mintAddr},
				{Pubkey: SolanaTokenProgram},
				{Pubkey: SolanaTokenProgram},
				{Pubkey: SolanaJupiterV6Program},
				{Pubkey: onchain.EventAuthority},
				{Pubkey: SolanaJupiterV6Program},
				{Pubkey: Base58Encode(poolPub), IsWritable: true},
			},
			Data: base64.StdEncoding.EncodeToString(swapData),
		}},
	}
	jupRev, err := client.PrepareReview(ctx, PrepareReviewRequest{
		WalletID: w.ID, Operation: "jupiter.swap", OutputMint: mintAddr, JupiterQuote: &quote,
	})
	if err != nil {
		t.Fatalf("Jupiter v0 PrepareReview failed: %v", err)
	}
	rawMsg, _ := base64.StdEncoding.DecodeString(jupRev.UnsignedMsgBase64)
	if len(rawMsg) == 0 || rawMsg[0] != 0x80 {
		t.Fatalf("expected versioned v0 message (prefix 0x80), got %x", rawMsg)
	}
	quote.SlippageBPS = 500
	if _, err := client.PrepareReview(ctx, PrepareReviewRequest{
		WalletID: w.ID, Operation: "jupiter.swap", OutputMint: mintAddr, JupiterQuote: &quote,
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected excessive Jupiter slippage rejected, got %v", err)
	}

	// Mismatched SO_PEERCRED UID refuses client requests.
	badSock := filepath.Join(tmpDir, "denied.sock")
	deniedSrv, err := StartSocketServerAt(svc, badSock, os.Getuid()+9999, 0o600)
	if err != nil {
		t.Fatalf("StartSocketServerAt(denied): %v", err)
	}
	defer deniedSrv.Close(context.Background())
	deniedClient, _ := NewSocketClient(badSock)
	defer deniedClient.Close()
	cctx, ccancel := context.WithTimeout(ctx, 2*time.Second)
	defer ccancel()
	if _, err := deniedClient.ListWallets(cctx); err == nil {
		t.Fatalf("expected SO_PEERCRED UID mismatch to fail socket request")
	}
}
