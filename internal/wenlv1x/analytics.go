package wenlv1x

import (
	"context"
	"math"
	"strings"

	"github.com/fased-ai/fased-app/internal/onchain"
)

func phaseLabel(p uint8) string {
	switch p {
	case 0:
		return "Bootstrap"
	case 1:
		return "Graduated"
	case 2:
		return "Draining"
	case 3:
		return "Wind-down"
	default:
		return "Bootstrap"
	}
}

func (e *Engine) resolveWalletPublicKey(ctx context.Context, walletID string) string {
	clean := strings.TrimSpace(walletID)
	if clean == "" || e.custody == nil {
		return ""
	}
	if w, err := e.custody.GetWallet(ctx, clean); err == nil {
		return w.PublicKey
	}
	return ""
}

// ProtocolStats aggregates global Lean V1 supply, BTC treasury, staking, and bootstrap/graduation state.
func (e *Engine) ProtocolStats(ctx context.Context, cluster string) (ProtocolStats, error) {
	c := normalizeCluster(cluster)
	nowSec := e.now().UTC().Unix()
	snap := e.readSnapshot(ctx, c, "")
	prog := snap.ProgramID
	ledger := snap.LedgerID
	rtAddr, _ := onchain.LV1XRuntimePDA(prog, ledger)
	mintAddr := nonEmptyStr(snap.Mint, func() string { m, _ := onchain.LV1XMintPDA(prog, ledger); return m }())
	stAddr, _ := onchain.LV1XStakingStatePDA(prog, ledger)
	trAddr, _ := onchain.LV1XTreasuryStatePDA(prog, ledger)

	out := ProtocolStats{
		Cluster:            c,
		ProgramID:          prog,
		LedgerID:           ledger,
		RuntimeAddress:     rtAddr,
		MintAddress:        mintAddr,
		BTCMintAddress:     nonEmptyStr(snap.BTCMint, onchain.LV1XDevnetBTCMint),
		StakingAddress:     stAddr,
		TreasuryAddress:    trAddr,
		Phase:              0,
		PhaseName:          phaseLabel(0),
		MaxSupplySat:       float64(onchain.LV1XLifetimeSatAtoms) / 1e9,
		LPSolMin:           float64(onchain.LV1XDefaultLPSolMin) / 1e9,
		LPSatMin:           float64(onchain.LV1XDefaultLPSatMin) / 1e9,
		BoostMultiplier:    uint8(onchain.LV1XDefaultBoostMult),
		BoostBudgetSat:     uint32(onchain.LV1XDefaultBoostBudget),
		TGEBps:             onchain.LV1XDefaultTGEBps,
		VestingDurationSec: onchain.LV1XBootstrapVestingSec,
		RoundIntervalSec:   onchain.LV1XRoundIntervalSec,
		FeeSplit:           [4]int{72, 13, 10, 5},
		Source:             snap.Source,
		ObservedAt:         nowSec,
	}

	if snap.Runtime != nil {
		rt := snap.Runtime
		out.Phase = rt.Phase
		out.PhaseName = phaseLabel(rt.Phase)
		out.Paused = rt.Paused
		out.BootstrapWinddown = rt.BootstrapWinddown
		if rt.BTCMint != "" {
			out.BTCMintAddress = rt.BTCMint
		}
		out.PoolAddress = rt.Pool
		out.MintedSat = float64(rt.Issuance.Awarded) / 1e9
		out.BoostAwardedSat = float64(rt.Issuance.BoostAwarded) / 1e9
		out.LPSol = float64(rt.LPSol) / 1e9
		out.LPSolMin = float64(rt.LPSolMin) / 1e9
		out.LPSat = float64(rt.LPSat) / 1e9
		out.LPSatMin = float64(rt.LPSatMin) / 1e9
		out.GraduatedAt = rt.GraduatedAt
		out.TGEBps = rt.TGEBps
		out.VestingDurationSec = rt.VestingDuration
		out.BoostBudgetSat = rt.BoostBudgetSat
		out.BoostMultiplier = rt.BoostMultiplier
	} else if e.fixture {
		out.MintedSat = 148_250.0
		out.BoostAwardedSat = 18_420.0
		out.LPSol = 18.45
		out.LPSat = 6_240.0
	}

	if snap.Treasury != nil {
		out.BTCTreasurySat = snap.Treasury.TotalBtcBought
		out.LPBurnedSOL = float64(snap.Treasury.TotalLpBurned) / 1e9
		out.TreasuryBtcSolPending = float64(snap.Treasury.BtcSolPending) / 1e9
		out.TreasuryLpSolPending = float64(snap.Treasury.LpSolPending) / 1e9
	} else if e.fixture {
		out.BTCTreasurySat = 125_000
	}

	if snap.Staking != nil {
		out.TotalStakedSat = float64(snap.Staking.TotalActiveStake+snap.Staking.TotalPendingStake) / 1e9
	} else if e.fixture {
		out.TotalStakedSat = 42_500.0
	}
	if out.MintedSat > 0 {
		out.StakedPct = math.Round((out.TotalStakedSat/out.MintedSat)*10000) / 100.0
	}

	streak, _ := e.ListStreak(ctx, 48)
	var vol float64
	for _, s := range streak {
		vol += s.PoolSOL
	}
	out.Volume24hSOL = math.Round(vol*1000) / 1000
	return out, nil
}

// Leaderboard returns protocol & miner rankings across 24h | 7d | 30d | all.
func (e *Engine) Leaderboard(ctx context.Context, cluster, rawWindow string) (LeaderboardResponse, error) {
	c := normalizeCluster(cluster)
	window := strings.ToLower(strings.TrimSpace(rawWindow))
	if window != "24h" && window != "7d" && window != "30d" && window != "all" {
		window = "24h"
	}
	nowSec := e.now().UTC().Unix()
	limit := 24
	if window == "7d" || window == "30d" || window == "all" {
		limit = 48
	}
	streak, err := e.ListStreak(ctx, limit)
	if err != nil {
		return LeaderboardResponse{}, err
	}

	rounds := make([]LeaderboardRound, 0, len(streak))
	var vol, satTotal float64
	for i := len(streak) - 1; i >= 0; i-- {
		s := streak[i]
		w := s.WinningRange
		vol += s.PoolSOL
		msat := s.MinerSat
		if msat <= 0 && e.fixture {
			msat = 115.0 + float64(i%5)*12.5
		}
		satTotal += msat
		shares := [5]float64{0.2, 0.2, 0.2, 0.2, 0.2}
		rounds = append(rounds, LeaderboardRound{
			RoundSlot:    s.RoundSlot,
			Phase:        "settled",
			WinningRange: &w,
			Code:         s.Code,
			MoveBps:      s.MoveBps,
			PoolSOL:      s.PoolSOL,
			MinerCount:   0,
			MinerSat:     msat,
			CrowdShares:  shares,
			SettledAt:    s.SettledAt,
		})
	}

	// Build miner leaderboard combining real Custody RoleMining wallets with real claim activity.
	miners := make([]LeaderboardMiner, 0, 8)
	var latestSlot uint64
	if len(rounds) > 0 {
		latestSlot = rounds[0].RoundSlot
	}
	if e.custody != nil {
		if wallets, wErr := e.custody.ListWallets(ctx); wErr == nil {
			for _, w := range wallets {
				if w.Role != "mining" {
					continue
				}
				var committed, returned, sat, btc float64
				var cnt, wins int
				rows, qErr := e.db.QueryContext(ctx, `SELECT kind, COALESCE(amount_sol,0), COALESCE(amount_sat,0), COALESCE(amount_btc,0), COALESCE(round_slot,0) FROM lv1x_entries WHERE wallet_id = ?`, w.ID)
				if qErr == nil {
					for rows.Next() {
						var kind string
						var aSol, aSat, aBtc float64
						var sl uint64
						if rows.Scan(&kind, &aSol, &aSat, &aBtc, &sl) == nil {
							switch kind {
							case "commit":
								committed += aSol
								cnt++
								if sl > latestSlot {
									latestSlot = sl
								}
							case "claim_sol":
								returned += aSol
							case "claim_sat":
								sat += aSat
								if aSat > 0 {
									wins++
								}
							case "claim_btc":
								btc += aBtc
							}
						}
					}
					_ = rows.Close()
				}
				if cnt > 0 || len(miners) == 0 {
					status := "active"
					if wins > 0 {
						status = "winner"
					}
					miners = append(miners, LeaderboardMiner{
						Address:       w.PublicKey,
						ShortAddress:  shortB58(w.PublicKey),
						Status:        status,
						Rounds:        cnt,
						Wins:          wins,
						CommittedSOL:  math.Round(committed*1e4) / 1e4,
						ReturnedSOL:   math.Round(returned*1e4) / 1e4,
						NetSOL:        math.Round((returned-committed)*1e4) / 1e4,
						EarnedSAT:     math.Round(sat*100) / 100,
						EarnedBTC:     math.Round(btc*1e8) / 1e8,
						LastRoundSlot: latestSlot,
					})
				}
			}
		}
	}

	minersCnt := len(miners)
	if e.fixture {
		minersCnt = max(18, len(miners))
	}
	return LeaderboardResponse{
		Cluster:    c,
		Window:     window,
		VolumeSOL:  math.Round(vol*1000) / 1000,
		RoundsCnt:  len(rounds),
		MinersCnt:  minersCnt,
		SatAwarded: math.Round(satTotal*100) / 100,
		Miners:     miners,
		Rounds:     rounds,
		Source:     "onchain+ledger",
		ObservedAt: nowSec,
	}, nil
}

// Portfolio aggregates wallet SOL/SAT/BTC balances, claimable breakdown, vesting progress, history & sessions.
func (e *Engine) Portfolio(ctx context.Context, cluster, walletID string) (PortfolioResponse, error) {
	c := normalizeCluster(cluster)
	nowSec := e.now().UTC().Unix()
	snap := e.readSnapshot(ctx, c, walletID)
	pub := e.resolveWalletPublicKey(ctx, walletID)

	pStats, _ := e.ProtocolStats(ctx, c)
	sessions, _ := e.ListSessions(ctx)
	history, _ := e.ListEntries(ctx, walletID, 50)
	claimItems, _ := e.ListClaimableItemsForCluster(ctx, c, walletID)

	var claimSOL, claimSAT, vestedSAT, claimBTC float64
	for i := range claimItems {
		it := &claimItems[i]
		vestedAtoms := onchain.LV1XVestedBootstrapSat(it.SatAtoms, pStats.Phase, pStats.GraduatedAt, nowSec, pStats.VestingDurationSec, pStats.TGEBps)
		if pStats.Phase == 0 && pStats.GraduatedAt == 0 {
			// Prior to bootstrap graduation show full claimable estimate gated until TGE unless already claimable.
			vestedAtoms = it.SatAtoms
		}
		it.VestedAtoms = vestedAtoms
		if !it.SolClaimed {
			claimSOL += float64(it.SolLamports) / 1e9
		}
		claimSAT += float64(it.SatAtoms) / 1e9
		vestedSAT += float64(vestedAtoms) / 1e9
		if !it.BtcClaimed {
			claimBTC += float64(it.BtcAtoms) / 1e8
		}
	}

	vestPct := 100.0
	var vestEnd int64
	if pStats.Phase == 1 && pStats.GraduatedAt > 0 && pStats.VestingDurationSec > 0 {
		vestEnd = pStats.GraduatedAt + int64(pStats.VestingDurationSec)
		elapsed := max(int64(0), nowSec-pStats.GraduatedAt)
		vestPct = math.Min(100.0, (float64(elapsed)/float64(pStats.VestingDurationSec))*100.0)
	}

	return PortfolioResponse{
		Cluster:            c,
		WalletID:           walletID,
		WalletPublicKey:    pub,
		SolBalanceSOL:      math.Round(snap.WalletSol*1e6) / 1e6,
		SatBalance:         math.Round(snap.WalletSat*1e6) / 1e6,
		BtcBalance:         math.Round(snap.WalletBtc*1e8) / 1e8,
		ClaimableSOL:       math.Round(claimSOL*1e6) / 1e6,
		ClaimableSAT:       math.Round(claimSAT*1e4) / 1e4,
		VestedSAT:          math.Round(vestedSAT*1e4) / 1e4,
		ClaimableBTC:       math.Round(claimBTC*1e8) / 1e8,
		VestingProgressPct: math.Round(vestPct*10) / 10,
		VestingEndsAt:      vestEnd,
		ClaimItems:         claimItems,
		History:            history,
		Sessions:           sessions,
		Source:             snap.Source,
		ObservedAt:         nowSec,
	}, nil
}

// StakingOverview aggregates LV1X StakingState and the user's StakePosition.
func (e *Engine) StakingOverview(ctx context.Context, cluster, walletID string) (StakingOverviewResponse, error) {
	c := normalizeCluster(cluster)
	nowSec := e.now().UTC().Unix()
	snap := e.readSnapshot(ctx, c, walletID)
	prog := snap.ProgramID
	ledger := snap.LedgerID
	stAddr, _ := onchain.LV1XStakingStatePDA(prog, ledger)
	pub := e.resolveWalletPublicKey(ctx, walletID)
	var posAddr string
	if pub != "" {
		posAddr, _ = onchain.LV1XStakePositionPDA(prog, ledger, pub)
	}

	out := StakingOverviewResponse{
		Cluster:          c,
		StakingAddress:   stAddr,
		PositionAddress:  posAddr,
		MinStakeSat:      float64(onchain.LV1XMinStakeAtoms) / 1e9,
		WalletSatBalance: snap.WalletSat,
		WalletBtcBalance: snap.WalletBtc,
		Source:           snap.Source,
		ObservedAt:       nowSec,
	}
	if snap.Runtime != nil {
		out.Phase = snap.Runtime.Phase
		out.Paused = snap.Runtime.Paused
	}
	if snap.Staking != nil {
		out.TotalActiveSat = float64(snap.Staking.TotalActiveStake) / 1e9
		out.TotalPendingSat = float64(snap.Staking.TotalPendingStake) / 1e9
		out.TotalBtcDistributed = float64(snap.Staking.TotalBtcDistributed) / 1e8
		out.StreamRemainingBtc = float64(snap.Staking.StreamRemainingBtc) / 1e8
		out.StreamEndTs = snap.Staking.StreamEndTs
	} else if e.fixture {
		out.TotalActiveSat = 38_400.0
		out.TotalPendingSat = 4_100.0
		out.TotalBtcDistributed = 0.0842
		out.StreamRemainingBtc = 0.0045
		out.StreamEndTs = nowSec + 14400
	}
	if snap.StakePos != nil {
		out.UserActiveSat = float64(snap.StakePos.ActiveStake) / 1e9
		out.UserPendingSat = float64(snap.StakePos.PendingStake) / 1e9
		out.PendingMatureAt = snap.StakePos.PendingMatureAt
		out.CanActivate = snap.StakePos.PendingStake > 0 && nowSec >= snap.StakePos.PendingMatureAt
		out.ClaimableBtc = float64(snap.StakePos.UnclaimedBtc) / 1e8
	}
	if out.TotalActiveSat > 0 && out.UserActiveSat > 0 {
		out.SharePct = math.Round((out.UserActiveSat/out.TotalActiveSat)*10000) / 100.0
	}
	return out, nil
}

func shortB58(addr string) string {
	if len(addr) <= 10 {
		return addr
	}
	return addr[:4] + "…" + addr[len(addr)-4:]
}
