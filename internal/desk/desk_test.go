package desk

import (
	"context"
	"errors"
	"fmt"
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
			if limit < 60 {
				limit = 60
			}
			return sampleCandles(clock.Unix(), limit, 100.0, 1.2), nil
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
			if limit < 60 {
				limit = 60
			}
			return sampleCandles(clock.Unix(), limit, 100.0, 1.2), nil
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
