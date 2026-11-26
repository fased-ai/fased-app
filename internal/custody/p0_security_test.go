package custody

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type p0RoundTripFunc func(*http.Request) (*http.Response, error)

func (f p0RoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func rpcOKResp(resultJSON string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":` + resultJSON + `}`)),
	}
}

func TestP0InSignerOwnerAuthBlocksCompromisedApp(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	svc, err := NewService(Config{
		DataDir:     dir,
		FixtureMode: true,
		FastKDF:     true,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	appSock := filepath.Join(dir, "app.sock")
	appSrv, err := StartSocketServerAt(svc, appSock, os.Getuid(), 0o600)
	if err != nil {
		t.Fatalf("StartSocketServerAt: %v", err)
	}
	t.Cleanup(func() { _ = appSrv.Close(context.Background()) })

	ownerSock := filepath.Join(dir, "owner.sock")
	ownerSrv, err := StartOwnerSocketServerAt(svc, ownerSock, os.Getuid(), 0o600)
	if err != nil {
		t.Fatalf("StartOwnerSocketServerAt: %v", err)
	}
	t.Cleanup(func() { _ = ownerSrv.Close(context.Background()) })

	appClient, err := NewSocketClient(appSock)
	if err != nil {
		t.Fatalf("NewSocketClient(app): %v", err)
	}
	defer appClient.Close()

	ownerClient, err := NewSocketClient(ownerSock)
	if err != nil {
		t.Fatalf("NewSocketClient(owner): %v", err)
	}
	defer ownerClient.Close()

	// Create wallet and enroll independent owner credential inside fased-signer.
	w, err := appClient.CreateNativeWallet(ctx, "Trading Hot", RoleTrading, OfficialMainnetRPC)
	if err != nil {
		t.Fatalf("CreateNativeWallet: %v", err)
	}
	const ownerPass = "strong-signer-owner-2026"
	if err := appClient.ConfigureOwnerAuth(ctx, ownerPass); err != nil {
		t.Fatalf("ConfigureOwnerAuth: %v", err)
	}
	// Attempting to overwrite owner credential must be refused.
	if err := appClient.ConfigureOwnerAuth(ctx, "attacker-password-999"); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected ErrOwnerAuthRequired overwriting owner auth, got %v", err)
	}

	// Compromised fased-app without owner proof MUST be blocked on all privileged actions.
	if _, _, err := appClient.ExportEncryptedRecovery(ctx, w.ID, "attacker-backup-pass", "attacker"); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected app.sock /backup blocked without owner proof, got %v", err)
	}
	if _, err := appClient.UpdateConnections(ctx, w.ID, WalletConnections{AllowTradeBots: true}, "attacker"); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected app.sock /connections blocked without owner proof, got %v", err)
	}
	if _, err := appClient.SetWalletRPC(ctx, w.ID, OfficialMainnetRPC, "", "attacker"); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected app.sock /rpc blocked without owner proof, got %v", err)
	}
	if _, err := appClient.DeleteWallet(ctx, w.ID); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected app.sock DELETE blocked without owner proof, got %v", err)
	}

	// Freezing is tightening (always allowed); unfreezing requires owner proof!
	if _, err := appClient.SetFreeze(ctx, w.ID, true, "panic", "app"); err != nil {
		t.Fatalf("freeze should succeed without owner proof: %v", err)
	}
	if _, err := appClient.SetFreeze(ctx, w.ID, false, "", "attacker"); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected unfreeze blocked without owner proof, got %v", err)
	}

	// Mint single-use proof scoped to ("unfreeze", w.ID).
	proof, _, err := appClient.AuthorizeOwnerAction(ctx, ownerPass, "unfreeze", w.ID)
	if err != nil {
		t.Fatalf("AuthorizeOwnerAction: %v", err)
	}
	// Using an "unfreeze" proof on "backup" MUST fail!
	if _, _, err := appClient.ExportEncryptedRecovery(WithOwnerProof(ctx, proof), w.ID, "secret-backup-2026", "owner"); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected scope mismatch to reject backup with unfreeze proof, got %v", err)
	}
	// Using it on "unfreeze" succeeds ONCE.
	if _, err := appClient.SetFreeze(WithOwnerProof(ctx, proof), w.ID, false, "", "owner"); err != nil {
		t.Fatalf("unfreeze with valid proof failed: %v", err)
	}
	// Replaying the same single-use proof MUST fail!
	if _, err := appClient.SetFreeze(WithOwnerProof(ctx, proof), w.ID, false, "", "owner"); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected replayed proof to fail, got %v", err)
	}

	// Backup works with a fresh proof scoped to ("backup", w.ID).
	bakProof, _, err := appClient.AuthorizeOwnerAction(ctx, ownerPass, "backup", w.ID)
	if err != nil {
		t.Fatalf("AuthorizeOwnerAction(backup): %v", err)
	}
	if arch, fp, err := appClient.ExportEncryptedRecovery(WithOwnerProof(ctx, bakProof), w.ID, "secret-backup-2026", "owner"); err != nil || len(arch) == 0 || fp == "" {
		t.Fatalf("backup with valid owner proof failed: err=%v fp=%q", err, fp)
	}

	// Bounded-auto bot execution within policy caps succeeds without owner password,
	// whereas manual owner execution still requires owner proof!
	destPub, _, _ := ed25519.GenerateKey(rand.Reader)
	destAddr := Base58Encode(destPub)
	pol := w.Policy
	pol.Frozen = false
	pol.ApprovalMode = ApprovalBoundedAuto
	pol.Operations = []string{"solana.nativeTransfer"}
	pol.Programs = []string{SolanaSystemProgram, SolanaComputeBudget}
	pol.Destinations = []string{destAddr}
	pol.SOLCap.MaxPerTxRaw = 500_000_000
	pol.SOLCap.MaxDailyRaw = 1_000_000_000
	// Owner socket bypasses app-facing owner proof (local owner CLI trust boundary).
	w, err = ownerClient.GetWallet(ctx, w.ID)
	if err != nil {
		t.Fatalf("ownerClient.GetWallet: %v", err)
	}
	w, err = ownerClient.UpdatePolicy(ctx, w.ID, pol, true, w.Policy.PolicyHash, "owner-cli")
	if err != nil {
		t.Fatalf("ownerClient.UpdatePolicy: %v", err)
	}
	w, err = ownerClient.UpdateConnections(ctx, w.ID, WalletConnections{AllowTradeBots: true}, "owner-cli")
	if err != nil {
		t.Fatalf("ownerClient.UpdateConnections: %v", err)
	}

	revManual, err := appClient.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w.ID,
		Operation:   "solana.nativeTransfer",
		Destination: destAddr,
		AmountHuman: "0.1",
	})
	if err != nil {
		t.Fatalf("PrepareReview(manual): %v", err)
	}
	if _, err := appClient.ExecuteNativeReview(ctx, w.ID, revManual.ID, "owner"); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected manual execute without owner proof denied on app.sock, got %v", err)
	}
	if _, err := appClient.ExecuteNativeReview(ctx, w.ID, revManual.ID, "trade-bot"); err != nil {
		t.Fatalf("expected bounded-auto trade-bot execution to succeed within caps, got %v", err)
	}
}

func TestP0CumulativeSPLDailyCapAndAtomicReserve(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, err := NewService(Config{
		DataDir:     t.TempDir(),
		FixtureMode: true,
		FastKDF:     true,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	w, err := svc.CreateNativeWallet(ctx, "USDC Bot", RoleAgent, OfficialMainnetRPC)
	if err != nil {
		t.Fatalf("CreateNativeWallet: %v", err)
	}
	mintPub, _, _ := ed25519.GenerateKey(rand.Reader)
	mintAddr := Base58Encode(mintPub)
	destPub, _, _ := ed25519.GenerateKey(rand.Reader)
	destAddr := Base58Encode(destPub)

	pol := w.Policy
	pol.Operations = []string{"solana.splTransferChecked"}
	pol.Programs = []string{SolanaSystemProgram, SolanaComputeBudget, SolanaTokenProgram, SolanaATAProgram}
	pol.Destinations = []string{destAddr}
	pol.SOLCap.MaxPerTxRaw = 100_000
	pol.SOLCap.MaxDailyRaw = 1_000_000
	pol.TokenCaps = []AssetCap{{
		Asset:       "solana:spl:" + mintAddr,
		Mint:        mintAddr,
		Symbol:      "USDC",
		Decimals:    6,
		MaxPerTxRaw: 100_000_000, // 100 USDC/tx
		MaxDailyRaw: 150_000_000, // 150 USDC/day
	}}
	if _, err := svc.UpdatePolicy(ctx, w.ID, pol, true, w.Policy.PolicyHash, "owner"); err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}

	// First 100 USDC transfer succeeds and consumes 100_000_000 of the 150_000_000 daily SPL cap.
	rev1, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w.ID,
		Operation:   "solana.splTransferChecked",
		Mint:        mintAddr,
		Decimals:    6,
		Destination: destAddr,
		AmountHuman: "100",
	})
	if err != nil {
		t.Fatalf("PrepareReview #1 failed: %v", err)
	}
	if _, err := svc.ExecuteNativeReview(ctx, w.ID, rev1.ID, "owner"); err != nil {
		t.Fatalf("ExecuteNativeReview #1 failed: %v", err)
	}

	updated, err := svc.GetWallet(ctx, w.ID)
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	if updated.DailyTokenUsage[mintAddr].SpentRaw != 100_000_000 {
		t.Fatalf("expected DailyTokenUsage[%s].SpentRaw=100000000, got %+v", mintAddr, updated.DailyTokenUsage[mintAddr])
	}

	// Second 100 USDC transfer MUST be denied (100 + 100 > 150 daily cap)!
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w.ID,
		Operation:   "solana.splTransferChecked",
		Mint:        mintAddr,
		Decimals:    6,
		Destination: destAddr,
		AmountHuman: "100",
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied exceeding cumulative SPL daily cap, got %v", err)
	}

	// Remaining 50 USDC fits exactly within 150 USDC/day cap.
	rev2, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w.ID,
		Operation:   "solana.splTransferChecked",
		Mint:        mintAddr,
		Decimals:    6,
		Destination: destAddr,
		AmountHuman: "50",
	})
	if err != nil {
		t.Fatalf("expected remaining 50 USDC to fit cap, got %v", err)
	}
	if _, err := svc.ExecuteNativeReview(ctx, w.ID, rev2.ID, "owner"); err != nil {
		t.Fatalf("ExecuteNativeReview #2 failed: %v", err)
	}
}

func TestP0AuditChainV2HMACDetectsRehashForgery(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, err := NewService(Config{
		DataDir:     t.TempDir(),
		FixtureMode: true,
		FastKDF:     true,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	w, err := svc.CreateNativeWallet(ctx, "HMAC Audit", RoleAgent, OfficialMainnetRPC)
	if err != nil {
		t.Fatalf("CreateNativeWallet: %v", err)
	}
	if _, err := svc.SetFreeze(ctx, w.ID, true, "test", "owner"); err != nil {
		t.Fatalf("SetFreeze: %v", err)
	}
	count, err := svc.VerifyAuditChain(ctx)
	if err != nil || count < 2 {
		t.Fatalf("VerifyAuditChain expected >=2 valid v2 entries, got count=%d err=%v", count, err)
	}

	// Attacker with SQLite write access tries to rewrite row 1 with a forged v2 hash using a wrong key.
	var id, raw string
	if err := svc.db.QueryRowContext(ctx, `SELECT id, payload_json FROM audit_log ORDER BY rowid ASC LIMIT 1`).Scan(&id, &raw); err != nil {
		t.Fatalf("scan audit row: %v", err)
	}
	var e AuditEntry
	_ = json.Unmarshal([]byte(raw), &e)
	if !strings.HasPrefix(e.EntryHash, "v2:") {
		t.Fatalf("expected v2 HMAC audit hash, got %q", e.EntryHash)
	}
	e.Summary = "forged summary"
	e.EntryHash = computeAuditEntryHashV2(bytes.Repeat([]byte{0x42}, 32), e.PrevHash, e)
	forgedRaw, _ := json.Marshal(e)
	if _, err := svc.db.ExecContext(ctx, `UPDATE audit_log SET payload_json = ? WHERE id = ?`, string(forgedRaw), id); err != nil {
		t.Fatalf("update forged audit: %v", err)
	}
	if _, err := svc.VerifyAuditChain(ctx); err == nil {
		t.Fatalf("expected VerifyAuditChain to reject forged HMAC audit row")
	}
}

func TestP0ReconcileFailClosedWithoutWitnessCorroboration(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	// Primary RPC lies that tx failed, while independent public witness says confirmed!
	transport := p0RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		var call struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &call)
		isWitness := strings.Contains(req.URL.Host, "api.mainnet-beta.solana.com")
		switch call.Method {
		case "getGenesisHash":
			return rpcOKResp(`"` + SolanaMainnetGenesis + `"`), nil
		case "getSlot":
			return rpcOKResp(`300000000`), nil
		case "getLatestBlockhash":
			bh := Base58Encode(bytes.Repeat([]byte{7}, 32))
			return rpcOKResp(`{"value":{"blockhash":"` + bh + `"}}`), nil
		case "simulateTransaction":
			return rpcOKResp(`{"value":{"err":null}}`), nil
		case "sendTransaction":
			sig := Base58Encode(bytes.Repeat([]byte{9}, 64))
			return rpcOKResp(`"` + sig + `"`), nil
		case "getSignatureStatuses":
			if isWitness {
				return rpcOKResp(`{"value":[{"confirmationStatus":"finalized","err":null}]}`), nil
			}
			// Malicious primary RPC lies that tx failed to try to refund daily cap!
			return rpcOKResp(`{"value":[{"confirmationStatus":"confirmed","err":{"InstructionError":[0,"Custom"]}}]}`), nil
		default:
			return rpcOKResp(`null`), nil
		}
	})

	svc, err := NewService(Config{
		DataDir:      t.TempDir(),
		FixtureMode:  true,
		FastKDF:      true,
		RPCTransport: transport,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	w, err := svc.CreateNativeWallet(ctx, "Witness Guard", RoleAgent, "https://custom-rpc.example.com")
	if err != nil {
		t.Fatalf("CreateNativeWallet: %v", err)
	}
	destPub, _, _ := ed25519.GenerateKey(rand.Reader)
	destAddr := Base58Encode(destPub)
	pol := w.Policy
	pol.Operations = []string{"solana.nativeTransfer"}
	pol.Programs = []string{SolanaSystemProgram, SolanaComputeBudget}
	pol.Destinations = []string{destAddr}
	pol.SOLCap.MaxPerTxRaw = 500_000_000
	pol.SOLCap.MaxDailyRaw = 1_000_000_000
	if _, err := svc.UpdatePolicy(ctx, w.ID, pol, true, w.Policy.PolicyHash, "owner"); err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}

	rev, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w.ID,
		Operation:   "solana.nativeTransfer",
		Destination: destAddr,
		AmountHuman: "0.2",
	})
	if err != nil {
		t.Fatalf("PrepareReview: %v", err)
	}
	entry, err := svc.ExecuteNativeReview(ctx, w.ID, rev.ID, "owner")
	if err != nil {
		t.Fatalf("ExecuteNativeReview: %v", err)
	}

	// Reconcile: malicious primary claims "failed", but public witness confirms "finalized" -> MUST record confirmed & spent!
	rec, err := svc.ReconcileLedgerEntry(ctx, w.ID, entry.ID)
	if err != nil {
		t.Fatalf("ReconcileLedgerEntry: %v", err)
	}
	if rec.State != LedgerConfirmed {
		t.Fatalf("expected witness-corroborated LedgerConfirmed, got %s", rec.State)
	}
	after, _ := svc.GetWallet(ctx, w.ID)
	if after.DailySpentLamports != 200_005_000 || after.DailyReservedLamports != 0 {
		t.Fatalf("expected spent=200005000 reserved=0, got spent=%d reserved=%d", after.DailySpentLamports, after.DailyReservedLamports)
	}
}

func TestP0VerifyHardwareSignedVersionedV0Transaction(t *testing.T) {
	t.Parallel()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var pub32 [32]byte
	copy(pub32[:], pub)

	// Construct a minimal valid Versioned v0 message header (0x80 prefix + numRequiredSignatures=1 + rest).
	v0Msg := append([]byte{0x80, 1, 0, 1}, bytes.Repeat([]byte{0xab}, 64)...)
	unsignedPkt := BuildUnsignedTransferPacket(v0Msg)
	if unsignedPkt[0] != 1 || len(unsignedPkt) != 1+64+len(v0Msg) {
		t.Fatalf("unexpected v0 unsigned packet layout: len=%d first=%d", len(unsignedPkt), unsignedPkt[0])
	}
	signedPkt, expectedSig := SignLegacyMessage(v0Msg, priv)
	gotSig, _, err := VerifyHardwareSignedTransaction(v0Msg, pub32, base64.StdEncoding.EncodeToString(signedPkt))
	if err != nil || gotSig != expectedSig {
		t.Fatalf("VerifyHardwareSignedTransaction(v0) failed: sig=%q err=%v", gotSig, err)
	}
}
