package wenlv1x

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/fased-ai/fased-app/internal/custody"
	"github.com/fased-ai/fased-app/internal/onchain"
)

func TestPresetsCurveReversalAndEngineLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// 1. All 6 presets sum to 10000 bps and expand cleanly to 80 normalized bars.
	presets := BuiltInPresets()
	if len(presets) != 6 {
		t.Fatalf("expected 6 presets, got %d", len(presets))
	}
	for _, p := range presets {
		if len(p.Curve) != CurveBarCount {
			t.Fatalf("preset %s curve len=%d want 80", p.ID, len(p.Curve))
		}
		var sum float64
		for _, v := range p.Curve {
			sum += v
		}
		if math.Abs(sum-1.0) > 1e-6 {
			t.Fatalf("preset %s curve sum=%f want 1.0", p.ID, sum)
		}
		var bps int
		for _, w := range p.Buckets {
			bps += int(w)
		}
		if bps != 10000 {
			t.Fatalf("preset %s buckets sum=%d want 10000", p.ID, bps)
		}
	}

	// 2. Hybrid AI trend-reversal abort check.
	round := RoundTelemetry{
		Buckets: []RoundBucket{
			{Index: 0, Code: "A", EVMultiple: 0.9},
			{Index: 1, Code: "B", EVMultiple: 1.05},
			{Index: 2, Code: "C", EVMultiple: 1.25},
			{Index: 3, Code: "D", EVMultiple: 1.1},
			{Index: 4, Code: "E", EVMultiple: 0.8},
		},
	}
	decPause := EvaluateTrendDecision("trend-up", true, -18, 12, round, 0.02)
	if decPause.Action != "pause" || !decPause.SignalTriggered {
		t.Fatalf("expected reversal pause on -18bps against trend-up, got %+v", decPause)
	}
	decGo := EvaluateTrendDecision("wide", true, 14, 10, round, 0.02)
	if decGo.Action != "commit" || decGo.SelectedPreset != "trend-up" {
		t.Fatalf("expected commit with trend-up on +14bps, got %+v", decGo)
	}

	// 3. Engine + Custody RoleMining Devnet execution vs Mainnet hard gate.
	custSvc, err := custody.NewService(custody.Config{DataDir: t.TempDir(), FixtureMode: true, FastKDF: true})
	if err != nil {
		t.Fatal(err)
	}
	defer custSvc.Close()

	mineW, err := custSvc.CreateNativeWallet(ctx, "Devnet Miner", custody.RoleMining, "https://api.devnet.solana.com")
	if err != nil {
		t.Fatal(err)
	}
	mineW, err = custSvc.UpdatePolicy(ctx, mineW.ID, custody.WalletPolicy{
		Role:              custody.RoleMining,
		ApprovalMode:      custody.ApprovalBoundedAuto,
		AutoApproveMaxUSD: 25.0,
		Operations:        custody.AllMiningOperations(),
		Programs:          []string{custody.SolanaSystemProgram, custody.SolanaComputeBudget, custody.SolanaTokenProgram, custody.SolanaATAProgram, onchain.LV1XDevnetProgramID},
		SOLCap: custody.AssetCap{
			Asset:       "solana:native",
			Symbol:      "SOL",
			Decimals:    9,
			MaxPerTxRaw: 50_000_000,
			MaxDailyRaw: 200_000_000,
		},
		Mining: &custody.MiningPolicy{
			Cluster:               "devnet",
			ProgramID:             onchain.LV1XDevnetProgramID,
			LedgerID:              onchain.LV1XDevnetLedgerID,
			ExpectedELFSHA256:     onchain.LV1XDevnetELFSHA256,
			MaxSOLPerRoundLamport: 50_000_000,
			MaxDailySOLLamports:   200_000_000,
			AllowedStrategies:     []string{"wide", "center", "trend-up"},
			AutoClaim:             true,
		},
	}, true, mineW.Policy.PolicyHash, "owner")
	if err != nil {
		t.Fatal(err)
	}

	eng, err := NewEngine(Config{DataDir: t.TempDir(), Custody: custSvc, FixtureMode: true})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	st, err := eng.Status(ctx, "devnet")
	if err != nil || st.MainnetEnabled || len(st.Streak) != 24 || len(st.Presets) != 6 {
		t.Fatalf("unexpected status: mainnet=%v streak=%d presets=%d err=%v", st.MainnetEnabled, len(st.Streak), len(st.Presets), err)
	}

	// Mainnet commit MUST be blocked.
	if _, _, err := eng.SubmitCommit(ctx, CommitRequest{
		WalletID:   mineW.ID,
		Cluster:    "mainnet-beta",
		AmountSOL:  0.02,
		StrategyID: "wide",
	}); !errors.Is(err, custody.ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied on mainnet commit, got %v", err)
	}

	// Devnet autonomous commit + claim + session lifecycle succeed.
	rev, led, err := eng.SubmitCommit(ctx, CommitRequest{
		WalletID:   mineW.ID,
		Cluster:    "devnet",
		AmountSOL:  0.02,
		StrategyID: "center",
		Autonomous: true,
		TaskID:     "tsk-mine-auto",
	})
	if err != nil || led == nil || led.State != custody.LedgerConfirmed || rev.Operation != custody.MiningOpCommit {
		t.Fatalf("Devnet auto commit failed: rev=%+v led=%+v err=%v", rev, led, err)
	}

	_, claimLed, err := eng.SubmitClaim(ctx, ClaimRequest{
		WalletID:   mineW.ID,
		RoundSlot:  st.Round.RoundSlot,
		Autonomous: true,
	})
	if err != nil || claimLed == nil || claimLed.State != custody.LedgerConfirmed {
		t.Fatalf("Devnet auto claim failed: led=%+v err=%v", claimLed, err)
	}

	sess, err := eng.CreateSession(ctx, SessionRecord{
		WalletID:       mineW.ID,
		WalletHandle:   mineW.Handle,
		Cluster:        "devnet",
		StrategyID:     "center",
		Mode:           "auto",
		PerRoundSOL:    0.02,
		AutoClaim:      true,
		StopOnReversal: true,
	})
	if err != nil || sess.Status != "active" {
		t.Fatalf("CreateSession failed: %+v err=%v", sess, err)
	}
	stopped, err := eng.SetSessionStatus(ctx, sess.ID, "stopped")
	if err != nil || stopped.Status != "stopped" {
		t.Fatalf("SetSessionStatus failed: %+v err=%v", stopped, err)
	}
	topped, err := eng.DepositSession(ctx, sess.ID, 0.25)
	if err != nil || topped.DepositSOL < 0.25 {
		t.Fatalf("DepositSession failed: %+v err=%v", topped, err)
	}

	// 4. Candles, ProtocolStats, Leaderboard, Portfolio, StakingOverview, SavedStrategies + SimulateStrategy.
	candles, err := eng.GetCandles(ctx, "10m", 60)
	if err != nil || len(candles.Candles) != 60 || candles.Interval != "10m" {
		t.Fatalf("GetCandles failed: len=%d err=%v", len(candles.Candles), err)
	}
	pStats, err := eng.ProtocolStats(ctx, "devnet")
	if err != nil || pStats.ProgramID != onchain.LV1XDevnetProgramID {
		t.Fatalf("ProtocolStats failed: %+v err=%v", pStats, err)
	}
	lb, err := eng.Leaderboard(ctx, "devnet", "24h")
	if err != nil || len(lb.Rounds) == 0 {
		t.Fatalf("Leaderboard failed: rounds=%d err=%v", len(lb.Rounds), err)
	}
	pf, err := eng.Portfolio(ctx, "devnet", mineW.ID)
	if err != nil || pf.WalletPublicKey != mineW.PublicKey || len(pf.ClaimItems) == 0 {
		t.Fatalf("Portfolio failed: pub=%s claims=%d err=%v", pf.WalletPublicKey, len(pf.ClaimItems), err)
	}
	stk, err := eng.StakingOverview(ctx, "devnet", mineW.ID)
	if err != nil || stk.MinStakeSat != 10 {
		t.Fatalf("StakingOverview failed: %+v err=%v", stk, err)
	}

	// Reveal + Claim SAT/BTC + Stake/Activate/ClaimStaking/Unstake + ClaimAll.
	saltHex := "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	if _, _, err := eng.SubmitReveal(ctx, RevealRequest{WalletID: mineW.ID, RoundSlot: st.Round.RoundSlot, Selection: 2, SaltHex: saltHex}); err != nil {
		t.Fatalf("SubmitReveal failed: %v", err)
	}
	if _, _, err := eng.SubmitClaim(ctx, ClaimRequest{WalletID: mineW.ID, Asset: "sat", RoundSlot: st.Round.RoundSlot, CreateATA: true}); err != nil {
		t.Fatalf("SubmitClaim(sat) failed: %v", err)
	}
	if _, _, err := eng.SubmitClaim(ctx, ClaimRequest{WalletID: mineW.ID, Asset: "btc", RoundSlot: st.Round.RoundSlot, CreateATA: true}); err != nil {
		t.Fatalf("SubmitClaim(btc) failed: %v", err)
	}
	if _, _, err := eng.SubmitStake(ctx, StakeActionRequest{WalletID: mineW.ID, AmountSAT: 25}); err != nil {
		t.Fatalf("SubmitStake failed: %v", err)
	}
	if _, _, err := eng.SubmitActivateStake(ctx, StakeActionRequest{WalletID: mineW.ID}); err != nil {
		t.Fatalf("SubmitActivateStake failed: %v", err)
	}
	if _, _, err := eng.SubmitClaimStaking(ctx, StakeActionRequest{WalletID: mineW.ID, CreateATA: true}); err != nil {
		t.Fatalf("SubmitClaimStaking failed: %v", err)
	}
	if _, _, err := eng.SubmitUnstake(ctx, StakeActionRequest{WalletID: mineW.ID, AmountSAT: 10, CreateATA: true}); err != nil {
		t.Fatalf("SubmitUnstake failed: %v", err)
	}

	saved, err := eng.SaveStrategy(ctx, SavedStrategy{
		Name:        "My Center Edge",
		Description: "Custom 80-bar test",
		Mode:        "neutral",
		Buckets:     [5]uint16{1000, 2000, 4000, 2000, 1000},
	})
	if err != nil || saved.ID == "" {
		t.Fatalf("SaveStrategy failed: %+v err=%v", saved, err)
	}
	listS, err := eng.ListSavedStrategies(ctx)
	if err != nil || len(listS) != 1 {
		t.Fatalf("ListSavedStrategies failed: %d err=%v", len(listS), err)
	}
	sim, err := eng.SimulateStrategy(ctx, SimulateRequest{BucketsBps: saved.Buckets, PerRoundSOL: 0.05, Limit: 24})
	if err != nil || sim.Rounds != 24 || sim.TotalCommitted <= 0 {
		t.Fatalf("SimulateStrategy failed: %+v err=%v", sim, err)
	}
	if err := eng.DeleteStrategy(ctx, saved.ID); err != nil {
		t.Fatalf("DeleteStrategy failed: %v", err)
	}
}
