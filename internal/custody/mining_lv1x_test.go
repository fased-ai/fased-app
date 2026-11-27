package custody

import (
	"context"
	"errors"
	"testing"

	"github.com/fased-ai/fased-app/internal/onchain"
)

func TestRoleMiningLV1XPolicyCapsAndApprovalModes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, err := NewService(Config{DataDir: t.TempDir(), FixtureMode: true, FastKDF: true})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	// Create dedicated RoleMining wallet on Devnet.
	mineWallet, err := svc.CreateNativeWallet(ctx, "Devnet Miner", RoleMining, "https://api.devnet.solana.com")
	if err != nil {
		t.Fatal(err)
	}
	// Configure bounded_auto with Devnet LV1X policy (0.05 SOL/round, 0.15 SOL/day).
	miningPol := WalletPolicy{
		Role:              RoleMining,
		ApprovalMode:      ApprovalBoundedAuto,
		AutoApproveMaxUSD: 25.0,
		Operations:        AllMiningOperations(),
		Programs:          []string{SolanaSystemProgram, SolanaComputeBudget, SolanaTokenProgram, SolanaATAProgram, onchain.LV1XDevnetProgramID},
		SOLCap: AssetCap{
			Asset:       "solana:native",
			Symbol:      "SOL",
			Decimals:    9,
			MaxPerTxRaw: 60_000_000,
			MaxDailyRaw: 150_000_000,
		},
		Mining: &MiningPolicy{
			Cluster:               "devnet",
			ProgramID:             onchain.LV1XDevnetProgramID,
			LedgerID:              onchain.LV1XDevnetLedgerID,
			ExpectedELFSHA256:     onchain.LV1XDevnetELFSHA256,
			MaxSOLPerRoundLamport: 50_000_000,
			MaxDailySOLLamports:   150_000_000,
			AllowedStrategies:     []string{"wide", "center"},
			AutoClaim:             true,
		},
	}
	mineWallet, err = svc.UpdatePolicy(ctx, mineWallet.ID, miningPol, true, mineWallet.Policy.PolicyHash, "owner")
	if err != nil {
		t.Fatalf("UpdatePolicy RoleMining: %v", err)
	}

	// Mainnet LV1X policy update MUST be hard-rejected while LV1XMainnetProgramID is empty.
	badMainnet := mineWallet.Policy
	badMainnet.Mining = &MiningPolicy{
		Cluster:               "mainnet-beta",
		ProgramID:             onchain.LV1XDevnetProgramID,
		LedgerID:              onchain.LV1XDevnetLedgerID,
		ExpectedELFSHA256:     onchain.LV1XDevnetELFSHA256,
		MaxSOLPerRoundLamport: 20_000_000,
		AllowMainnet:          true,
	}
	if _, err := svc.UpdatePolicy(ctx, mineWallet.ID, badMainnet, true, mineWallet.Policy.PolicyHash, "owner"); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied for Mainnet LV1X policy, got %v", err)
	}

	// Generic trading bots CANNOT use RoleMining wallets even with Auto policy.
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    mineWallet.ID,
		Operation:   "solana.nativeTransfer",
		Destination: mineWallet.PublicKey,
		AmountRaw:   10_000_000,
		IsBotOrAuto: true,
	}); !errors.Is(err, ErrRoleDenied) {
		t.Fatalf("expected ErrRoleDenied for generic bot on RoleMining wallet, got %v", err)
	}

	// RoleVault CANNOT execute mining operations.
	vaultW, err := svc.CreateNativeWallet(ctx, "Vault", RoleVault, OfficialMainnetRPC)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:  vaultW.ID,
		Operation: MiningOpCommit,
		MiningLV1X: &MiningLV1XRequest{
			RoundSlot:      101,
			ShardID:        0,
			Selection:      2,
			AmountLamports: 20_000_000,
		},
	}); err == nil {
		t.Fatal("expected RoleVault mining denial")
	}

	// Unallowed strategy rejected.
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:  mineWallet.ID,
		Operation: MiningOpCommit,
		MiningLV1X: &MiningLV1XRequest{
			RoundSlot:      101,
			ShardID:        0,
			Selection:      2,
			AmountLamports: 20_000_000,
			StrategyID:     "trend_up",
		},
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied for unallowed strategy, got %v", err)
	}

	// Valid bounded_auto commit executes cleanly within per-round cap (30M <= 50M).
	rev1, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    mineWallet.ID,
		Operation:   MiningOpCommit,
		IsBotOrAuto: true,
		MiningLV1X: &MiningLV1XRequest{
			RoundSlot:      101,
			ShardID:        0,
			Selection:      2,
			AmountLamports: 30_000_000,
			StrategyID:     "wide",
		},
	})
	if err != nil {
		t.Fatalf("PrepareReview mining.lv1x.commit failed: %v", err)
	}
	if !allowMiningAutonomousExecution(mineWallet, rev1, "task:tsk-mine-1") {
		t.Fatal("expected allowMiningAutonomousExecution=true for RoleMining bounded_auto")
	}
	led1, err := svc.ExecuteNativeReview(ctx, mineWallet.ID, rev1.ID, "task:tsk-mine-1")
	if err != nil || led1.State != LedgerConfirmed {
		t.Fatalf("ExecuteNativeReview failed: state=%s err=%v", led1.State, err)
	}

	// Second commit in SAME round exceeding 50M total (30M + 25M = 55M > 50M) MUST be denied.
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:  mineWallet.ID,
		Operation: MiningOpCommit,
		MiningLV1X: &MiningLV1XRequest{
			RoundSlot:      101,
			ShardID:        0,
			Selection:      1,
			AmountLamports: 25_000_000,
			StrategyID:     "center",
		},
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected per-round cap rejection (55M > 50M), got %v", err)
	}

	// Semi-Auto permits unattended AutoClaim, but refuses unattended Commit.
	semiPol := mineWallet.Policy
	semiPol.ApprovalMode = ApprovalSemiAuto
	semiWallet, err := svc.UpdatePolicy(ctx, mineWallet.ID, semiPol, false, mineWallet.Policy.PolicyHash, "owner")
	if err != nil {
		t.Fatalf("tighten to Semi-Auto: %v", err)
	}
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    semiWallet.ID,
		Operation:   MiningOpCommit,
		IsBotOrAuto: true,
		MiningLV1X: &MiningLV1XRequest{
			RoundSlot:      102,
			ShardID:        0,
			Selection:      2,
			AmountLamports: 20_000_000,
		},
	}); !errors.Is(err, ErrRoleDenied) {
		t.Fatalf("expected ErrRoleDenied for Semi-Auto unattended commit, got %v", err)
	}
	claimRev, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    semiWallet.ID,
		Operation:   MiningOpClaim,
		IsBotOrAuto: true,
		MiningLV1X: &MiningLV1XRequest{
			RoundSlot:   101,
			ShardID:     0,
			Page:        0,
			EntriesMask: 0x01,
		},
	})
	if err != nil {
		t.Fatalf("expected Semi-Auto unattended AutoClaim allowed, got %v", err)
	}
	if !allowMiningAutonomousExecution(semiWallet, claimRev, "mining-engine") {
		t.Fatal("expected allowMiningAutonomousExecution=true for Semi-Auto AutoClaim")
	}

	// Reveal, ClaimSAT, ClaimBTC, Stake/Activate/ClaimStaking/Unstake, and Session Deposit.
	saltHex := "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	for _, tc := range []struct {
		name string
		op   string
		req  MiningLV1XRequest
		auto bool
	}{
		{"reveal", MiningOpReveal, MiningLV1XRequest{RoundSlot: 101, ShardID: 0, Page: 0, EntryRow: 0, Selection: 2, SaltHex: saltHex}, true},
		{"claim_sat", MiningOpClaimSat, MiningLV1XRequest{RoundSlot: 101, ShardID: 0, Page: 0, EntryRow: 0, CreateATA: true}, true},
		{"claim_btc", MiningOpClaimBtc, MiningLV1XRequest{RoundSlot: 101, ShardID: 0, Page: 0, EntryRow: 0, BtcMint: onchain.LV1XDevnetBTCMint, CreateATA: true}, true},
		{"claim_staking", MiningOpClaimStaking, MiningLV1XRequest{BtcMint: onchain.LV1XDevnetBTCMint, CreateATA: true}, true},
		{"stake", MiningOpStake, MiningLV1XRequest{SatAmountAtoms: onchain.LV1XMinStakeAtoms}, false},
		{"activate", MiningOpActivateStake, MiningLV1XRequest{}, false},
		{"unstake", MiningOpUnstake, MiningLV1XRequest{SatAmountAtoms: onchain.LV1XMinStakeAtoms, CreateATA: true}, false},
		{"sess_deposit", MiningOpSession, MiningLV1XRequest{SessionAction: "deposit", SessionRef: 7, AmountLamports: 15_000_000}, false},
	} {
		rev, pErr := svc.PrepareReview(ctx, PrepareReviewRequest{
			WalletID:    semiWallet.ID,
			Operation:   tc.op,
			IsBotOrAuto: tc.auto,
			MiningLV1X:  &tc.req,
		})
		if pErr != nil {
			t.Fatalf("%s PrepareReview failed: %v", tc.name, pErr)
		}
		actor := "owner"
		if tc.auto {
			actor = "mining-engine"
		}
		led, eErr := svc.ExecuteNativeReview(ctx, semiWallet.ID, rev.ID, actor)
		if eErr != nil || led.State != LedgerConfirmed {
			t.Fatalf("%s ExecuteNativeReview failed: state=%s err=%v", tc.name, led.State, eErr)
		}
	}

	// Autonomous Stake on Semi-Auto MUST be rejected.
	if _, err := svc.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    semiWallet.ID,
		Operation:   MiningOpStake,
		IsBotOrAuto: true,
		MiningLV1X:  &MiningLV1XRequest{SatAmountAtoms: onchain.LV1XMinStakeAtoms},
	}); !errors.Is(err, ErrRoleDenied) {
		t.Fatalf("expected ErrRoleDenied for autonomous stake, got %v", err)
	}
}
