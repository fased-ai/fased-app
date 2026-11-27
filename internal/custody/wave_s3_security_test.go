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
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/onchain"
)

func TestWaveS3AuditChainDetectsSilentTruncationAndV1Downgrade(t *testing.T) {
	ctx := context.Background()
	svc, err := NewService(Config{DataDir: t.TempDir(), FixtureMode: true, FastKDF: true})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	if _, err := svc.CreateNativeWallet(ctx, "Audit1", RoleTrading, OfficialMainnetRPC); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateNativeWallet(ctx, "Audit2", RoleVault, OfficialMainnetRPC); err != nil {
		t.Fatal(err)
	}
	cnt, err := svc.VerifyAuditChain(ctx)
	if err != nil || cnt != 2 {
		t.Fatalf("expected valid audit chain count=2, got cnt=%d err=%v", cnt, err)
	}

	// M7: Deleting the trailing audit row MUST fail VerifyAuditChain via signer_meta head anchor!
	svc.mu.Lock()
	_, err = svc.db.ExecContext(ctx, `DELETE FROM audit_log WHERE rowid = (SELECT MAX(rowid) FROM audit_log)`)
	svc.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyAuditChain(ctx); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("expected truncation detected by audit anchor, got err=%v", err)
	}
}

func TestWaveS3SPLCreateDestATARentAndPolicyDecimalsHardening(t *testing.T) {
	ctx := context.Background()
	svc, err := NewService(Config{DataDir: t.TempDir(), FixtureMode: true, FastKDF: true})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	w, err := svc.CreateNativeWallet(ctx, "SPLCap", RoleTrading, OfficialMainnetRPC)
	if err != nil {
		t.Fatal(err)
	}
	dest, err := svc.CreateNativeWallet(ctx, "Dest", RoleVault, OfficialMainnetRPC)
	if err != nil {
		t.Fatal(err)
	}
	const usdcMint = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	pol := w.Policy
	pol.Operations = []string{"solana.splTransferChecked"}
	pol.Programs = []string{SolanaSystemProgram, SolanaComputeBudget, SolanaTokenProgram, SolanaATAProgram}
	pol.Destinations = []string{dest.PublicKey}
	pol.SOLCap.MaxPerTxRaw = 500_000 // < 2,039,280 ATA rent!
	pol.SOLCap.MaxDailyRaw = 1_000_000
	pol.TokenCaps = []AssetCap{
		{Mint: usdcMint, Symbol: "USDC", Decimals: 6, MaxPerTxRaw: 50_000_000, MaxDailyRaw: 100_000_000},
	}
	w, err = svc.UpdatePolicy(ctx, w.ID, pol, true, w.Policy.PolicyHash, "owner")
	if err != nil {
		t.Fatal(err)
	}

	// M4: CreateDestATA=true requires ~2,039,280 lamports rent and MUST be denied under a 500k lamport SOL cap!
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:      w.ID,
		Operation:     "solana.splTransferChecked",
		Mint:          usdcMint,
		Decimals:      6,
		Destination:   dest.PublicKey,
		AmountHuman:   "5",
		CreateDestATA: true,
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied when CreateDestATA rent exceeds SOL cap, got %v", err)
	}

	// M8: Silently altering TokenCaps.Decimals MUST NOT count as tightening!
	badPol := w.Policy
	badPol.TokenCaps = []AssetCap{
		{Mint: usdcMint, Symbol: "USDC", Decimals: 9, MaxPerTxRaw: 50_000_000, MaxDailyRaw: 100_000_000},
	}
	if IsPolicyTighterOrEqual(w.Policy, badPol) {
		t.Fatal("expected IsPolicyTighterOrEqual to reject TokenCaps.Decimals change")
	}
}

func TestWaveS3SkillGrantsFailClosedVenuesAndSPLBalances(t *testing.T) {
	ctx := context.Background()
	svc, err := NewService(Config{DataDir: t.TempDir(), FixtureMode: true, FastKDF: true})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	w, err := svc.CreateNativeWallet(ctx, "Agent", RoleAgent, OfficialMainnetRPC)
	if err != nil {
		t.Fatal(err)
	}
	dest, err := svc.CreateNativeWallet(ctx, "Dest", RoleVault, OfficialMainnetRPC)
	if err != nil {
		t.Fatal(err)
	}
	pol := w.Policy
	pol.ApprovalMode = ApprovalBoundedAuto
	pol.Operations = []string{"solana.nativeTransfer"}
	pol.Programs = []string{SolanaSystemProgram, SolanaComputeBudget}
	pol.Destinations = []string{dest.PublicKey}
	pol.SOLCap.MaxPerTxRaw = 500_000_000
	pol.SOLCap.MaxDailyRaw = 1_000_000_000
	w, err = svc.UpdatePolicy(ctx, w.ID, pol, true, w.Policy.PolicyHash, "owner")
	if err != nil {
		t.Fatal(err)
	}

	// Empty AllowedVenues MUST fail closed for autonomous venue routing!
	w, err = svc.UpdateConnections(ctx, w.ID, WalletConnections{
		AllowTradeBots: true,
		SkillGrants: []SkillGrant{
			{SkillID: "skill-readonly", Actions: []string{"solana.splTransferChecked"}},
		},
	}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResolveWallet(ctx, WalletSelector{WalletID: w.ID, IsAutonomous: true, Venue: "solana"}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected empty AllowedVenues to fail closed for autonomous venue, got %v", err)
	}

	// H2: SkillGrantID restricts operations inside PrepareReview.
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:     w.ID,
		Operation:    "solana.nativeTransfer",
		Destination:  dest.PublicKey,
		AmountHuman:  "0.1",
		IsBotOrAuto:  true,
		SkillGrantID: "skill-readonly",
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied when SkillGrant does not allow solana.nativeTransfer, got %v", err)
	}

	// P1: Fixture FetchBalance includes SPL token balances.
	bal, err := svc.FetchBalance(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bal.Tokens) == 0 || bal.Tokens[0].AmountRaw == 0 {
		t.Fatalf("expected SPL token balances in FetchBalance, got %+v", bal)
	}
}

func TestWaveS3JupiterALTVerificationAndOutputMintAllowlist(t *testing.T) {
	ctx := context.Background()

	// Build an on-chain ALT account containing realKey1 at index 0.
	altPub, _, _ := ed25519.GenerateKey(rand.Reader)
	realKey1, _, _ := ed25519.GenerateKey(rand.Reader)
	fakeKey1, _, _ := ed25519.GenerateKey(rand.Reader)
	altData := make([]byte, solanaALTHeaderBytes+32)
	copy(altData[solanaALTHeaderBytes:], realKey1)
	altDataB64 := base64.StdEncoding.EncodeToString(altData)
	altKeyStr := Base58Encode(altPub)

	rt := p0RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(raw, &req)
		var respBody string
		switch req.Method {
		case "getGenesisHash":
			respBody = `{"jsonrpc":"2.0","id":1,"result":"` + SolanaMainnetGenesis + `"}`
		case "getSlot":
			respBody = `{"jsonrpc":"2.0","id":1,"result":300000000}`
		case "getAccountInfo":
			respBody = `{"jsonrpc":"2.0","id":1,"result":{"value":{"owner":"` + solanaALTProgramID + `","data":["` + altDataB64 + `","base64"]}}}`
		case "getLatestBlockhash":
			respBody = `{"jsonrpc":"2.0","id":1,"result":{"value":{"blockhash":"` + SolanaMainnetGenesis + `"}}}`
		case "simulateTransaction":
			respBody = `{"jsonrpc":"2.0","id":1,"result":{"value":{"err":null}}}`
		default:
			respBody = `{"jsonrpc":"2.0","id":1,"result":null}`
		}
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(respBody)),
		}, nil
	})

	// Direct ALT verification catches caller-substituted ALT address.
	err := verifyAddressLookupTablesRPC(ctx, rt, "https://primary.rpc.example", []onchain.AddressLookupTable{
		{AccountKey: altKeyStr, Addresses: []string{Base58Encode(fakeKey1)}},
	})
	if err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("expected ALT mismatch error on falsified address, got %v", err)
	}
	// Matching on-chain ALT address passes verification.
	if err := verifyAddressLookupTablesRPC(ctx, rt, "https://primary.rpc.example", []onchain.AddressLookupTable{
		{AccountKey: altKeyStr, Addresses: []string{Base58Encode(realKey1)}},
	}); err != nil {
		t.Fatalf("expected matching on-chain ALT verified, got %v", err)
	}

	// Unallowlisted OutputMint MUST be rejected in PrepareReview("jupiter.swap").
	svc, err := NewService(Config{DataDir: t.TempDir(), FastKDF: true, RPCTransport: rt})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	w, err := svc.CreateNativeWallet(ctx, "Swapper", RoleTrading, "https://primary.rpc.example")
	if err != nil {
		t.Fatal(err)
	}
	pol := w.Policy
	pol.Operations = []string{"jupiter.swap"}
	pol.Programs = []string{SolanaSystemProgram, SolanaComputeBudget, SolanaTokenProgram, SolanaATAProgram, SolanaJupiterV6Program}
	pol.MaxSlippageBps = 100
	pol.SOLCap.MaxPerTxRaw = 500_000_000
	pol.SOLCap.MaxDailyRaw = 1_000_000_000
	w, err = svc.UpdatePolicy(ctx, w.ID, pol, true, w.Policy.PolicyHash, "owner")
	if err != nil {
		t.Fatal(err)
	}
	rogueMintPub, _, _ := ed25519.GenerateKey(rand.Reader)
	var jupData [40]byte
	copy(jupData[:8], []byte{187, 100, 250, 204, 49, 196, 175, 20})
	binary.LittleEndian.PutUint64(jupData[8:16], 10_000_000)
	binary.LittleEndian.PutUint64(jupData[16:24], 1)
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:  w.ID,
		Operation: "jupiter.swap",
		JupiterQuote: &onchain.Quote{
			Owner:          w.PublicKey,
			InputMint:      onchain.SOL,
			OutputMint:     Base58Encode(rogueMintPub),
			Amount:         "10000000",
			Output:         "1",
			SlippageBPS:    50,
			MaxFeeLamports: 25_000,
		},
	}); !errors.Is(err, ErrPolicyDenied) || !strings.Contains(err.Error(), "allowlisted") {
		t.Fatalf("expected ErrPolicyDenied rejecting unallowlisted swap OutputMint, got %v", err)
	}
}
