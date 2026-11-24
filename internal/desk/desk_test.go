package desk

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/marketdata"
	"github.com/fased-ai/fased-app/internal/quantlab"
	"github.com/fased-ai/fased-app/internal/worldintel"
)

func sampleCandles(nowSec int64, count int, startPx, step float64) []marketdata.Candle {
	out := make([]marketdata.Candle, 0, count)
	px := startPx
	for i := 0; i < count; i++ {
		ts := nowSec - int64(count-i)*3600
		open := px
		var closePx float64
		if i%3 == 1 {
			closePx = px - step*0.8
		} else {
			closePx = px + step*0.95
		}
		hi := open
		if closePx > hi {
			hi = closePx
		}
		lo := open
		if closePx < lo {
			lo = closePx
		}
		out = append(out, marketdata.Candle{
			Time:   ts,
			Open:   open,
			High:   hi + step*0.5,
			Low:    lo - step*0.5,
			Close:  closePx,
			Volume: 1500,
		})
		px = closePx
	}
	return out
}

func newTestDesk(t *testing.T, clock *time.Time, externalKill *bool) *Service {
	t.Helper()
	if clock == nil {
		fixed := time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC)
		clock = &fixed
	}
	if externalKill == nil {
		ext := false
		externalKill = &ext
	}
	svc, err := NewService(Config{
		DataDir:     t.TempDir(),
		FixtureMode: true,
		Now:         func() time.Time { return *clock },
		MarketReader: func(_ context.Context, _, _ string, _ string, limit int) ([]marketdata.Candle, error) {
			all := sampleCandles(clock.Unix(), 240, 100.0, 1.2)
			if limit > 0 && limit < len(all) {
				return all[len(all)-limit:], nil
			}
			return all, nil
		},
		IntelEvents: func(_ context.Context, _ worldintel.EventFilter) ([]worldintel.IntelEvent, error) {
			return []worldintel.IntelEvent{
				{
					ID:          "ev1",
					SourceName:  "FED",
					Title:       "Healthy institutional volume",
					URL:         "https://example.com/news",
					PublishedAt: clock.Add(-15 * time.Minute).UnixMilli(),
					Urgency:     0.4,
					Sentiment:   "positive",
					Untrusted:   true,
				},
			}, nil
		},
		LabRunsReader: func(_ context.Context, _ int) ([]quantlab.ValidationRunSummary, error) {
			return []quantlab.ValidationRunSummary{
				{
					ID:             "run_ok",
					ScriptID:       "ema-rsi-trend",
					Symbol:         "BTC",
					Grade:          "A",
					GatesPassed:    4,
					PromotionReady: true,
					Sharpe:         2.0,
				},
			}, nil
		},
		ExternalKillActive: func(_ context.Context) bool {
			return *externalKill
		},
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

func TestHardSeatRBACAndPaperLock(t *testing.T) {
	nonExecSeats := []SeatID{SeatChief, SeatScout, SeatChartist, SeatQuant, SeatSkeptic, SeatRisk, SeatJournal}
	for _, s := range nonExecSeats {
		if err := AssertSeatToolAllowed(s, "place_paper_order"); !errors.Is(err, ErrSeatPermissionDenied) {
			t.Fatalf("expected non-executor seat %s blocked from place_paper_order, got %v", s, err)
		}
		if err := AssertSeatToolAllowed(s, "close_paper_position"); !errors.Is(err, ErrSeatPermissionDenied) {
			t.Fatalf("expected non-executor seat %s blocked from close_paper_position, got %v", s, err)
		}
	}
	if err := AssertSeatToolAllowed(SeatExecutor, "place_paper_order"); err != nil {
		t.Fatalf("expected executor permitted place_paper_order, got %v", err)
	}
	for _, p := range DefaultSeatPolicies() {
		if p.CanModifyLimits {
			t.Fatalf("seat %s unexpectedly had CanModifyLimits=true", p.ID)
		}
		if err := AssertSeatToolAllowed(p.ID, "modify_risk_limits"); !errors.Is(err, ErrSeatPermissionDenied) {
			t.Fatalf("expected %s blocked from modify_risk_limits, got %v", p.ID, err)
		}
		if err := AssertSeatToolAllowed(p.ID, "execute_live_order"); !errors.Is(err, ErrSeatPermissionDenied) {
			t.Fatalf("expected %s blocked from execute_live_order", p.ID)
		}
	}

	svc := newTestDesk(t, nil, nil)
	live := DefaultRiskLimits()
	live.Mode = "live"
	if _, err := svc.UpdateLimits(t.Context(), live); !errors.Is(err, ErrLiveExecutionDisabled) {
		t.Fatalf("expected ErrLiveExecutionDisabled on UpdateLimits live mode, got %v", err)
	}
	if _, err := svc.RunCycle(t.Context(), CycleRequest{Mode: "live"}); !errors.Is(err, ErrLiveExecutionDisabled) {
		t.Fatalf("expected ErrLiveExecutionDisabled on RunCycle live mode, got %v", err)
	}
}

func TestShadowCycleAndPaperApprovalLifecycle(t *testing.T) {
	svc := newTestDesk(t, nil, nil)

	// 1) Shadow cycle must record ticket & journal entry without touching paper cash or opening positions.
	shadowRes, err := svc.RunCycle(t.Context(), CycleRequest{Venue: "hyperliquid", Symbol: "BTC", Interval: "1h", Mode: ModeShadow})
	if err != nil {
		t.Fatalf("shadow RunCycle failed: %v", err)
	}
	if shadowRes.FinalStatus != "SHADOW" || len(shadowRes.Artifacts) != 8 {
		t.Fatalf("unexpected shadow cycle result: %+v", shadowRes)
	}
	snap, err := svc.GetSnapshot(t.Context())
	if err != nil {
		t.Fatalf("GetSnapshot failed: %v", err)
	}
	if snap.Account.CashUSD != 100000 || len(snap.Positions) != 0 || snap.Decay.ShadowCount != 1 {
		t.Fatalf("shadow cycle mutated paper state unexpectedly: %+v", snap)
	}

	// 2) Paper cycle with RequirePMTap=true queues PENDING_PM -> ApproveTicket -> ClosePosition.
	paperRes, err := svc.RunCycle(t.Context(), CycleRequest{Venue: "hyperliquid", Symbol: "BTC", Interval: "1h", Mode: ModePaper})
	if err != nil {
		t.Fatalf("paper RunCycle failed: %v", err)
	}
	if paperRes.FinalStatus != "PENDING_PM" || paperRes.Ticket == nil {
		t.Fatalf("expected PENDING_PM ticket, got %+v", paperRes)
	}
	pos, err := svc.ApproveTicket(t.Context(), paperRes.Ticket.ID, "owner")
	if err != nil {
		t.Fatalf("ApproveTicket failed: %v", err)
	}
	if pos.Status != "OPEN" || pos.EntryFeeUSD <= 0 {
		t.Fatalf("unexpected open position: %+v", pos)
	}
	closed, err := svc.ClosePosition(t.Context(), pos.ID, pos.EntryPrice*1.03, "TAKE_TARGET")
	if err != nil {
		t.Fatalf("ClosePosition failed: %v", err)
	}
	if closed.Status != "CLOSED" || closed.RealizedPnL <= 0 || closed.PnLR <= 0 {
		t.Fatalf("expected profitable closed position, got %+v", closed)
	}
	journal, err := svc.ListJournal(t.Context(), "BTC", "CLOSED_WIN", 10)
	if err != nil || len(journal) != 1 {
		t.Fatalf("expected CLOSED_WIN journal entry, got %d err=%v", len(journal), err)
	}

	// 3) RejectTicket records REJECTED_BY_PM cleanly.
	paperRes2, err := svc.RunCycle(t.Context(), CycleRequest{Venue: "hyperliquid", Symbol: "ETH", Interval: "1h", Mode: ModePaper})
	if err != nil || paperRes2.Ticket == nil {
		t.Fatalf("second paper cycle failed: %v", err)
	}
	rej, err := svc.RejectTicket(t.Context(), paperRes2.Ticket.ID, "too volatile", "owner")
	if err != nil || rej.Status != "REJECTED_BY_PM" {
		t.Fatalf("RejectTicket failed: %+v err=%v", rej, err)
	}
}

func TestSkepticKillCooldownBenchAndKillSwitch(t *testing.T) {
	clock := time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC)
	extKill := false
	toxic := true
	svc, err := NewService(Config{
		DataDir:     t.TempDir(),
		FixtureMode: true,
		Now:         func() time.Time { return clock },
		MarketReader: func(_ context.Context, _, _ string, _ string, limit int) ([]marketdata.Candle, error) {
			all := sampleCandles(clock.Unix(), 240, 100.0, 1.2)
			if limit > 0 && limit < len(all) {
				return all[len(all)-limit:], nil
			}
			return all, nil
		},
		IntelEvents: func(_ context.Context, _ worldintel.EventFilter) ([]worldintel.IntelEvent, error) {
			if !toxic {
				return nil, nil
			}
			return []worldintel.IntelEvent{
				{
					ID:          "tox1",
					SourceName:  "Alert",
					Title:       "Exchange exploit rumor",
					URL:         "",
					PublishedAt: 0,
					Urgency:     0.95,
					Sentiment:   "negative",
					Untrusted:   true,
				},
			}, nil
		},
		ExternalKillActive: func(_ context.Context) bool { return extKill },
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	// 1) Toxic unsourced catalyst triggers Skeptic KILL.
	killedRes, err := svc.RunCycle(t.Context(), CycleRequest{Symbol: "BTC", Mode: ModePaper})
	if err != nil {
		t.Fatalf("RunCycle failed: %v", err)
	}
	if killedRes.FinalStatus != "KILLED_BY_SKEPTIC" {
		t.Fatalf("expected KILLED_BY_SKEPTIC, got %s", killedRes.FinalStatus)
	}
	toxic = false

	// 2) 3 consecutive losses trigger automatic cooldown bench + block subsequent cycle.
	limits := DefaultRiskLimits()
	limits.Mode = ModePaper
	limits.RequirePMTap = false
	limits.MaxDailyLossPct = 20.0
	if _, err := svc.UpdateLimits(t.Context(), limits); err != nil {
		t.Fatalf("UpdateLimits failed: %v", err)
	}
	for i := 0; i < 3; i++ {
		res, cErr := svc.RunCycle(t.Context(), CycleRequest{Symbol: "SOL", Mode: ModePaper})
		if cErr != nil || res.Position == nil {
			t.Fatalf("cycle %d failed: %+v err=%v", i, res, cErr)
		}
		if _, err := svc.ClosePosition(t.Context(), res.Position.ID, res.Position.StopPrice, "STOP_LOSS"); err != nil {
			t.Fatalf("ClosePosition loss %d failed: %v", i, err)
		}
	}
	benchedRes, err := svc.RunCycle(t.Context(), CycleRequest{Symbol: "SOL", Mode: ModePaper})
	if err != nil {
		t.Fatalf("benched cycle returned error: %v", err)
	}
	if benchedRes.FinalStatus != "KILLED_BY_SKEPTIC" {
		t.Fatalf("expected SOL benched & killed, got %s", benchedRes.FinalStatus)
	}

	// 3) External kill switch halts RunCycle immediately even when local kill switch is off.
	extKill = true
	if _, err := svc.RunCycle(t.Context(), CycleRequest{Symbol: "BTC", Mode: ModePaper}); !errors.Is(err, ErrKillSwitchActive) {
		t.Fatalf("expected ErrKillSwitchActive when external kill is active, got %v", err)
	}
	extKill = false

	// 4) reset_paper clears bench/losses, then Unoverridable Desk Kill Switch cancels pending tickets, flattens open positions, and requires CONFIRM to unkill.
	if _, err := svc.SetControl(t.Context(), "reset_paper", "", "clear bench for kill switch check", "owner"); err != nil {
		t.Fatalf("SetControl reset_paper failed: %v", err)
	}
	limits.RequirePMTap = true
	_, _ = svc.UpdateLimits(t.Context(), limits)
	openRes, err := svc.RunCycle(t.Context(), CycleRequest{Symbol: "BTC", Mode: ModePaper})
	if err != nil || openRes.Ticket == nil {
		t.Fatalf("open cycle failed: %+v err=%v", openRes, err)
	}
	pendingRes, err := svc.RunCycle(t.Context(), CycleRequest{Symbol: "ETH", Mode: ModePaper})
	if err != nil || pendingRes.Ticket == nil {
		t.Fatalf("pending cycle failed: %+v err=%v", pendingRes, err)
	}
	pos, err := svc.ApproveTicket(t.Context(), openRes.Ticket.ID, "owner")
	if err != nil || pos.Status != "OPEN" {
		t.Fatalf("ApproveTicket failed: %+v err=%v", pos, err)
	}
	if _, err := svc.SetControl(t.Context(), "kill", "", "emergency stop", "owner"); err != nil {
		t.Fatalf("SetControl kill failed: %v", err)
	}
	snap, err := svc.GetSnapshot(t.Context())
	if err != nil {
		t.Fatalf("GetSnapshot failed: %v", err)
	}
	if !snap.Control.KillSwitchActive || snap.Account.OpenPositions != 0 || snap.Account.PendingTickets != 0 {
		t.Fatalf("kill switch did not cancel pending tickets & flatten positions: %+v", snap)
	}
	if _, err := svc.SetControl(t.Context(), "unkill", "NOPE", "try bypass", "owner"); err == nil {
		t.Fatal("expected unkill without CONFIRM to fail")
	}
	if _, err := svc.SetControl(t.Context(), "unkill", "CONFIRM", "verified safe", "owner"); err != nil {
		t.Fatalf("expected unkill with CONFIRM to succeed, got %v", err)
	}
}

func TestDecayReportTriggersAfter20LosingTrades(t *testing.T) {
	clock := time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC)
	svc := newTestDesk(t, &clock, nil)
	limits := DefaultRiskLimits()
	limits.Mode = ModePaper
	limits.RequirePMTap = false
	limits.MaxConsecutiveLosses = 12
	limits.MaxDailyLossPct = 20.0
	if _, err := svc.UpdateLimits(t.Context(), limits); err != nil {
		t.Fatalf("UpdateLimits failed: %v", err)
	}
	for i := 0; i < 20; i++ {
		if i%10 == 0 && i > 0 {
			clock = clock.Add(25 * time.Hour)
		}
		res, err := svc.RunCycle(t.Context(), CycleRequest{
			Symbol:   fmt.Sprintf("SYM%d", i%3),
			Mode:     ModePaper,
			ScriptID: "ema-rsi-trend",
		})
		if err != nil || res.Position == nil {
			t.Fatalf("cycle %d failed: %+v err=%v", i, res, err)
		}
		// Include one tiny green close at i==10 to reset the consecutive-loss streak cleanly
		// while still leaving average realized R far below 50% of backtest expectancy.
		exitPx := res.Position.StopPrice
		reason := "STOP_LOSS"
		if i == 10 {
			exitPx = res.Position.EntryPrice * 1.002
			reason = "SMALL_SCALP"
		}
		if _, err := svc.ClosePosition(t.Context(), res.Position.ID, exitPx, reason); err != nil {
			t.Fatalf("ClosePosition %d failed: %v", i, err)
		}
		if res2, _ := svc.store.getControl(t.Context()); res2.KillSwitchActive {
			_, _ = svc.SetControl(t.Context(), "unkill", "CONFIRM", "test continue", "owner")
		}
	}
	rep, err := svc.GetDecayReport(t.Context())
	if err != nil {
		t.Fatalf("GetDecayReport failed: %v", err)
	}
	if rep.ClosedTrades < 20 || !rep.Decaying || rep.Status != "DECAYING" {
		t.Fatalf("expected DECAYING report after 20 losing trades, got %+v", rep)
	}
}

func TestStage3DeskSafetyConcurrencyAndScripts(t *testing.T) {
	clock := time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC)
	scripts := map[string]string{
		"flat-script": `plot("c", close); strategy({ long: false, short: false });`,
		"short-only":  `plot("c", close); strategy({ short: close > 0, stopLossPct: 2.0, takeProfitPct: 6.0, sizePct: 5 });`,
	}
	var bearishToxic bool
	var candlesMu sync.Mutex
	customCandles := sampleCandles(clock.Unix(), 240, 100.0, 1.2)
	ready4Gate := false

	svc, err := NewService(Config{
		DataDir:     t.TempDir(),
		FixtureMode: true,
		Now:         func() time.Time { return clock },
		MarketReader: func(_ context.Context, _, _ string, _ string, limit int) ([]marketdata.Candle, error) {
			candlesMu.Lock()
			defer candlesMu.Unlock()
			if limit > 0 && limit < len(customCandles) {
				cp := make([]marketdata.Candle, limit)
				copy(cp, customCandles[len(customCandles)-limit:])
				return cp, nil
			}
			cp := make([]marketdata.Candle, len(customCandles))
			copy(cp, customCandles)
			return cp, nil
		},
		ScriptReader: func(_ context.Context, scriptID string) (quantlab.ScriptRecord, bool, error) {
			src, ok := scripts[scriptID]
			if !ok {
				return quantlab.ScriptRecord{}, false, nil
			}
			return quantlab.ScriptRecord{ID: scriptID, Name: scriptID, Source: src}, true, nil
		},
		IntelEvents: func(_ context.Context, _ worldintel.EventFilter) ([]worldintel.IntelEvent, error) {
			if !bearishToxic {
				return nil, nil
			}
			return []worldintel.IntelEvent{
				{
					ID:          "bear1",
					SourceName:  "SEC",
					Title:       "Major custodian insolvency filing",
					URL:         "https://example.com/sec",
					PublishedAt: clock.Add(-10 * time.Minute).UnixMilli(),
					Urgency:     0.90,
					Sentiment:   "bearish",
					Untrusted:   true,
				},
			}, nil
		},
		LabRunsReader: func(_ context.Context, _ int) ([]quantlab.ValidationRunSummary, error) {
			return []quantlab.ValidationRunSummary{
				{
					ID:             "run_gate",
					ScriptID:       "short-only",
					Symbol:         "BTC",
					PromotionReady: ready4Gate,
					GatesPassed:    4,
					Grade:          "PASS_4_GATE",
					Sharpe:         2.0,
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	// 1. ScriptReader with no signals returns NO_SIGNAL and no ticket.
	noSig, err := svc.RunCycle(t.Context(), CycleRequest{Symbol: "BTC", ScriptID: "flat-script", Mode: ModePaper})
	if err != nil || noSig.FinalStatus != "NO_SIGNAL" || noSig.Ticket != nil {
		t.Fatalf("expected NO_SIGNAL with nil ticket, got %+v err=%v", noSig, err)
	}

	// 2. ScriptReader short-only strategy drives Side/Stop/Target/SizePct even on rising candles.
	shortSig, err := svc.RunCycle(t.Context(), CycleRequest{Symbol: "BTC", ScriptID: "short-only", Mode: ModePaper})
	if err != nil || shortSig.Ticket == nil || shortSig.Ticket.Side != "short" {
		t.Fatalf("expected short ticket from short-only script, got %+v err=%v", shortSig, err)
	}
	if shortSig.Ticket.NotionalUSD > 100000*0.05+1 {
		t.Fatalf("expected sizePct=5%% cap on notional ($5000 max), got %.2f", shortSig.Ticket.NotionalUSD)
	}
	if _, err := svc.RejectTicket(t.Context(), shortSig.Ticket.ID, "cleanup", "owner"); err != nil {
		t.Fatalf("RejectTicket failed: %v", err)
	}

	// 3. Toxic "bearish" catalyst with primary source triggers Skeptic kill.
	bearishToxic = true
	toxRes, err := svc.RunCycle(t.Context(), CycleRequest{Symbol: "BTC", ScriptID: "short-only", Mode: ModePaper})
	if err != nil || toxRes.FinalStatus != "KILLED_BY_SKEPTIC" {
		t.Fatalf("expected KILLED_BY_SKEPTIC on bearish high-urgency catalyst, got %+v err=%v", toxRes, err)
	}
	bearishToxic = false

	// 4. ApproveTicket checks RequireFourGatePass and MaxSlippageBps, and recomputes sizing;
	// concurrent ApproveTicket (5 goroutines) opens at most once; concurrent ClosePosition (5 goroutines) credits once.
	slipRes, err := svc.RunCycle(t.Context(), CycleRequest{Symbol: "BTC", ScriptID: "short-only", Mode: ModePaper})
	if err != nil || slipRes.Ticket == nil {
		t.Fatalf("slipRes cycle: %+v err=%v", slipRes, err)
	}
	lim := DefaultRiskLimits()
	lim.Mode = ModePaper
	lim.RequirePMTap = true
	lim.RequireFourGatePass = true
	if _, err := svc.UpdateLimits(t.Context(), lim); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApproveTicket(t.Context(), slipRes.Ticket.ID, "owner"); !errors.Is(err, ErrRiskRejected) {
		t.Fatalf("expected ErrRiskRejected when RequireFourGatePass=true and 4-Gate not passed, got %v", err)
	}

	lim.RequireFourGatePass = false
	if _, err := svc.UpdateLimits(t.Context(), lim); err != nil {
		t.Fatal(err)
	}
	slipRes2, err := svc.RunCycle(t.Context(), CycleRequest{Symbol: "BTC", ScriptID: "short-only", Mode: ModePaper})
	if err != nil || slipRes2.Ticket == nil {
		t.Fatal(err)
	}
	candlesMu.Lock()
	customCandles[len(customCandles)-1].Close *= 1.10 // +10% drift > 25 bps
	candlesMu.Unlock()
	if _, err := svc.ApproveTicket(t.Context(), slipRes2.Ticket.ID, "owner"); !errors.Is(err, ErrRiskRejected) {
		t.Fatalf("expected MAX_SLIPPAGE_EXCEEDED ErrRiskRejected, got %v", err)
	}

	candlesMu.Lock()
	customCandles = sampleCandles(clock.Unix(), 240, 100.0, 1.2)
	candlesMu.Unlock()

	concRes, err := svc.RunCycle(t.Context(), CycleRequest{Symbol: "BTC", ScriptID: "short-only", Mode: ModePaper})
	if err != nil || concRes.Ticket == nil {
		t.Fatal(err)
	}
	var appOK atomic.Int32
	var openedPos PaperPosition
	var posMu sync.Mutex
	var appWG sync.WaitGroup
	for g := 0; g < 5; g++ {
		appWG.Add(1)
		go func() {
			defer appWG.Done()
			pos, aErr := svc.ApproveTicket(t.Context(), concRes.Ticket.ID, "owner")
			if aErr == nil {
				appOK.Add(1)
				posMu.Lock()
				openedPos = pos
				posMu.Unlock()
			}
		}()
	}
	appWG.Wait()
	if appOK.Load() != 1 || openedPos.ID == "" {
		t.Fatalf("expected exactly 1 concurrent ApproveTicket success, got %d", appOK.Load())
	}

	var closeOK atomic.Int32
	var closeWG sync.WaitGroup
	for g := 0; g < 5; g++ {
		closeWG.Add(1)
		go func() {
			defer closeWG.Done()
			if _, cErr := svc.ClosePosition(t.Context(), openedPos.ID, openedPos.EntryPrice, "CONC_TEST"); cErr == nil {
				closeOK.Add(1)
			}
		}()
	}
	closeWG.Wait()
	if closeOK.Load() != 1 {
		t.Fatalf("expected exactly 1 concurrent ClosePosition success, got %d", closeOK.Load())
	}

	// 5. After MaxConsecutiveLosses bench expires (+25h), desk resumes trading cleanly without deadlock.
	if _, err := svc.SetControl(t.Context(), "reset_paper", "", "clear conc loss before 3-loss test", "owner"); err != nil {
		t.Fatal(err)
	}
	lim.RequirePMTap = false
	lim.MaxConsecutiveLosses = 3
	lim.CooldownHours = 24
	lim.MaxDailyLossPct = 20.0
	if _, err := svc.UpdateLimits(t.Context(), lim); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		cRes, cErr := svc.RunCycle(t.Context(), CycleRequest{Symbol: "SOL", ScriptID: "short-only", Mode: ModePaper})
		if cErr != nil || cRes.Position == nil {
			t.Fatalf("loss cycle %d failed: %+v err=%v", i, cRes, cErr)
		}
		if _, err := svc.ClosePosition(t.Context(), cRes.Position.ID, cRes.Position.StopPrice, "STOP_LOSS"); err != nil {
			t.Fatal(err)
		}
	}
	benchedSOL, err := svc.RunCycle(t.Context(), CycleRequest{Symbol: "SOL", ScriptID: "short-only", Mode: ModePaper})
	if err != nil || benchedSOL.FinalStatus != "KILLED_BY_SKEPTIC" {
		t.Fatalf("expected SOL killed during 24h cooldown bench, got %+v err=%v", benchedSOL, err)
	}
	clock = clock.Add(25 * time.Hour)
	candlesMu.Lock()
	customCandles = sampleCandles(clock.Unix(), 240, 100.0, 1.2)
	candlesMu.Unlock()
	resumedRes, err := svc.RunCycle(t.Context(), CycleRequest{Symbol: "SOL", ScriptID: "short-only", Mode: ModePaper})
	if err != nil || resumedRes.Position == nil {
		t.Fatalf("expected desk to resume after cooldown expiry without ConsecutiveLosses deadlock, got %+v err=%v", resumedRes, err)
	}
	_, _ = svc.ClosePosition(t.Context(), resumedRes.Position.ID, resumedRes.Position.EntryPrice, "CLEANUP")

	// 6. reset_paper resets GetDecayReport().ClosedTrades to 0.
	if _, err := svc.SetControl(t.Context(), "reset_paper", "", "reset decay", "owner"); err != nil {
		t.Fatal(err)
	}
	decAfterReset, err := svc.GetDecayReport(t.Context())
	if err != nil || decAfterReset.ClosedTrades != 0 {
		t.Fatalf("expected ClosedTrades==0 after reset_paper, got %+v err=%v", decAfterReset, err)
	}

	// 7. Daily-loss auto-kill flattens remaining open positions.
	lim.MaxDailyLossPct = 2.0
	lim.MaxOpenPositions = 5
	if _, err := svc.UpdateLimits(t.Context(), lim); err != nil {
		t.Fatal(err)
	}
	rBTC, err := svc.RunCycle(t.Context(), CycleRequest{Symbol: "BTC", ScriptID: "short-only", Mode: ModePaper})
	if err != nil || rBTC.Position == nil {
		t.Fatal(err)
	}
	rETH, err := svc.RunCycle(t.Context(), CycleRequest{Symbol: "ETH", ScriptID: "short-only", Mode: ModePaper})
	if err != nil || rETH.Position == nil {
		t.Fatal(err)
	}
	// Close BTC at a huge loss so daily loss >= 2% -> auto-kill must flatten open ETH position too!
	if _, err := svc.ClosePosition(t.Context(), rBTC.Position.ID, rBTC.Position.EntryPrice*2.0, "HUGE_LOSS"); err != nil {
		t.Fatal(err)
	}
	snapKill, err := svc.GetSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !snapKill.Control.KillSwitchActive || snapKill.Account.OpenPositions != 0 {
		t.Fatalf("expected daily-loss auto-kill to flatten open ETH position, got ctrl=%+v open=%d", snapKill.Control, snapKill.Account.OpenPositions)
	}
}

func TestStage3DeskMarkPositionsAndSubPenny(t *testing.T) {
	clock := time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	candles := sampleCandles(clock.Unix(), 240, 0.00001234, 0.00000015)

	svc, err := NewService(Config{
		DataDir:     t.TempDir(),
		FixtureMode: true,
		Now:         func() time.Time { return clock },
		MarketReader: func(_ context.Context, _, _, _ string, limit int) ([]marketdata.Candle, error) {
			mu.Lock()
			defer mu.Unlock()
			if limit > 0 && limit < len(candles) {
				cp := make([]marketdata.Candle, limit)
				copy(cp, candles[len(candles)-limit:])
				return cp, nil
			}
			cp := make([]marketdata.Candle, len(candles))
			copy(cp, candles)
			return cp, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	lim := DefaultRiskLimits()
	lim.Mode = ModePaper
	lim.RequirePMTap = false
	if _, err := svc.UpdateLimits(t.Context(), lim); err != nil {
		t.Fatal(err)
	}

	// Sub-penny cycle succeeds without collapsing StopPrice/TargetPrice to 0.
	res, err := svc.RunCycle(t.Context(), CycleRequest{Symbol: "BONK", Mode: ModePaper})
	if err != nil || res.Position == nil {
		t.Fatalf("expected sub-penny cycle to open position, got %+v err=%v", res, err)
	}
	pos := res.Position
	if pos.EntryPrice <= 0 || pos.StopPrice <= 0 || pos.TargetPrice <= 0 || math.IsNaN(pos.StopPrice) {
		t.Fatalf("sub-penny prices invalid: %+v", pos)
	}

	subCh, unsub := svc.Subscribe()
	defer unsub()

	// Pre-entry bar wick below StopPrice must NOT close the position; post-entry spike to TargetPrice MUST close and publish position_closed.
	openedSec := pos.OpenedAt / 1000
	mu.Lock()
	candles = []marketdata.Candle{
		{Time: openedSec, Open: pos.EntryPrice, High: pos.EntryPrice, Low: pos.StopPrice * 0.5, Close: pos.EntryPrice, Volume: 100},
		{Time: openedSec + 60, Open: pos.EntryPrice, High: pos.TargetPrice * 1.05, Low: pos.EntryPrice * 0.9999, Close: pos.TargetPrice * 1.02, Volume: 100},
	}
	mu.Unlock()

	clock = clock.Add(2 * time.Minute)
	marked, err := svc.MarkPositions(t.Context())
	if err != nil || len(marked) != 1 || marked[0].Status != "CLOSED" || marked[0].ExitReason != "TAKE_PROFIT" {
		t.Fatalf("expected MarkPositions to skip pre-entry wick and close on post-entry TAKE_PROFIT, got %+v err=%v", marked, err)
	}

	select {
	case evt := <-subCh:
		if evt.Type != "position_closed" {
			t.Fatalf("expected position_closed SSE event, got %+v", evt)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for position_closed SSE event from MarkPositions")
	}
}
