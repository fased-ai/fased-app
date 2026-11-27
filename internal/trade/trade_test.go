package trade

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/marketdata"
)

func makeTestCandles(base float64, closes []float64) []marketdata.Candle {
	out := make([]marketdata.Candle, 0, len(closes))
	start := int64(1764000000)
	prev := base
	for i, c := range closes {
		hi := maxFloat(prev, c) + 2
		lo := minFloat(prev, c) - 2
		out = append(out, marketdata.Candle{
			Time:   start + int64(i)*3600,
			Open:   prev,
			High:   hi,
			Low:    lo,
			Close:  c,
			Volume: 100,
		})
		prev = c
	}
	return out
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func TestUnifiedTradeEngineStrategiesAndRisk(t *testing.T) {
	now := time.Date(2026, 11, 25, 22, 0, 0, 0, time.UTC)
	candles := makeTestCandles(100, []float64{
		100, 101, 102, 101, 103, 104, 102, 105, 106, 107, 106, 108, 109, 110, 112,
	})
	svc, err := NewService(Config{
		DataDir: t.TempDir(),
		Now:     func() time.Time { return now },
		MarketReader: func(_ context.Context, _, _, _ string, _ int) ([]marketdata.Candle, error) {
			return candles, nil
		},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	// 1. Market fill immediately.
	mkt, err := svc.SubmitOrder(t.Context(), OrderRequest{
		Venue:    marketdata.VenueHyperliquid,
		Symbol:   "BTC",
		Mode:     ModeManual,
		Strategy: StrategyMarket,
		Side:     SideBuy,
		Quantity: 1,
	})
	if err != nil || mkt.Status != StatusFilled || !mkt.Simulated || mkt.AvgFillPrice != 112 {
		t.Fatalf("market order unexpected: %+v err=%v", mkt, err)
	}

	// 2. Bracket SL/TP hit + DailyLossUSD accumulation.
	br, err := svc.SubmitOrder(t.Context(), OrderRequest{
		Venue:       marketdata.VenueHyperliquid,
		Symbol:      "BTC",
		Mode:        ModeSemiAuto,
		Strategy:    StrategyBracket,
		Side:        SideBuy,
		Quantity:    10,
		StopPrice:   105,
		TargetPrice: 120,
	})
	if err != nil || br.Status != StatusActive {
		t.Fatalf("bracket submit failed: %+v err=%v", br, err)
	}
	// Simulate price drop hitting stop loss (105).
	candles = append(candles, marketdata.Candle{Time: 1764060000, Open: 112, High: 113, Low: 104, Close: 105, Volume: 200})
	sw1, err := svc.EvaluateActive(t.Context())
	if err != nil || sw1.Triggered != 1 {
		t.Fatalf("bracket SL sweep failed: %+v err=%v", sw1, err)
	}
	snap1, _ := svc.GetSnapshot(t.Context())
	if snap1.Policy.DailyLossUSD != 70 { // (112 - 105) * 10 = 70
		t.Fatalf("expected DailyLossUSD=70, got %.2f", snap1.Policy.DailyLossUSD)
	}

	// 3. Trailing ATR ratchet upward then breach.
	candles = makeTestCandles(200, []float64{
		200, 201, 202, 201, 202, 203, 202, 204, 203, 205, 204, 206, 205, 207, 210,
	})
	tr, err := svc.SubmitOrder(t.Context(), OrderRequest{
		Venue:         marketdata.VenueHyperliquid,
		Symbol:        "SOL",
		Mode:          ModeBot,
		Strategy:      StrategyTrailingATR,
		Side:          SideBuy,
		Quantity:      2,
		ATRMultiplier: 1.5,
	})
	if err != nil || tr.TrailingStopPrice <= 0 {
		t.Fatalf("trailing_atr submit failed: %+v err=%v", tr, err)
	}
	initialStop := tr.TrailingStopPrice
	// Price rallies to 220 -> stop must ratchet strictly upward.
	candles = append(candles, marketdata.Candle{Time: 1764070000, Open: 210, High: 221, Low: 209, Close: 220, Volume: 150})
	_, _ = svc.EvaluateActive(t.Context())
	trAfterUp, _ := svc.store.getPlan(t.Context(), tr.ID)
	if trAfterUp.TrailingStopPrice <= initialStop {
		t.Fatalf("expected trailing stop ratchet above %.4f, got %.4f", initialStop, trAfterUp.TrailingStopPrice)
	}
	// Price dumps below trailing stop -> triggers exit.
	candles = append(candles, marketdata.Candle{Time: 1764073600, Open: 220, High: 220, Low: trAfterUp.TrailingStopPrice - 1, Close: trAfterUp.TrailingStopPrice - 1, Volume: 300})
	swTrail, _ := svc.EvaluateActive(t.Context())
	if swTrail.Triggered != 1 {
		t.Fatalf("expected trailing_atr trigger on breach, got %+v", swTrail)
	}

	// 4. DCA slices + completion after advancing clock, plus Grid & Risk cap rejection.
	dca, err := svc.SubmitOrder(t.Context(), OrderRequest{
		Venue:           marketdata.VenueBinance,
		Symbol:          "BTCUSDT",
		Mode:            ModeBot,
		Strategy:        StrategyDCA,
		Side:            SideBuy,
		Quantity:        3,
		Slices:          3,
		IntervalSeconds: 60,
	})
	if err != nil || dca.SlicesFilled != 1 {
		t.Fatalf("dca submit failed: %+v err=%v", dca, err)
	}
	now = now.Add(65 * time.Second)
	_, _ = svc.EvaluateActive(t.Context())
	now = now.Add(65 * time.Second)
	swDCA, _ := svc.EvaluateActive(t.Context())
	if swDCA.Completed != 1 {
		t.Fatalf("expected dca completion on slice 3, got %+v", swDCA)
	}

	grid, err := svc.SubmitOrder(t.Context(), OrderRequest{
		Venue:      marketdata.VenueHyperliquid,
		Symbol:     "SOL",
		Mode:       ModeBot,
		Strategy:   StrategyGrid,
		Side:       SideBuy,
		Quantity:   5,
		GridLower:  190,
		GridUpper:  240,
		GridLevels: 4,
	})
	if err != nil || grid.Status != StatusActive {
		t.Fatalf("grid submit failed: %+v err=%v", grid, err)
	}
	cancelled, err := svc.CancelPlan(t.Context(), grid.ID)
	if err != nil || cancelled.Status != StatusCancelled {
		t.Fatalf("CancelPlan failed: %+v err=%v", cancelled, err)
	}

	// Risk notional limit rejection.
	if _, err := svc.SubmitOrder(t.Context(), OrderRequest{
		Venue:      marketdata.VenueHyperliquid,
		Symbol:     "BTC",
		Strategy:   StrategyLimit,
		Side:       SideBuy,
		Quantity:   10,
		EntryPrice: 50000,
	}); !errors.Is(err, ErrRiskRejected) {
		t.Fatalf("expected ErrRiskRejected on huge notional, got %v", err)
	}
}

func TestGlobalKillSwitchHaltsActivePlansAndBlocksOrders(t *testing.T) {
	svc, err := NewService(Config{
		DataDir: t.TempDir(),
		MarketReader: func(_ context.Context, _, _, _ string, _ int) ([]marketdata.Candle, error) {
			return makeTestCandles(100, []float64{100, 101}), nil
		},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	twap, err := svc.SubmitOrder(t.Context(), OrderRequest{
		Venue:    marketdata.VenueHyperliquid,
		Symbol:   "ETH",
		Mode:     ModeAIAgent,
		Strategy: StrategyTWAP,
		Side:     SideBuy,
		Quantity: 4,
		Slices:   4,
	})
	if err != nil {
		t.Fatalf("SubmitOrder TWAP: %v", err)
	}
	ks, err := svc.SetKillSwitch(t.Context(), true, "emergency flatten", "owner")
	if err != nil || !ks.Active {
		t.Fatalf("SetKillSwitch failed: %+v err=%v", ks, err)
	}
	got, _ := svc.store.getPlan(t.Context(), twap.ID)
	if got.Status != StatusKilled {
		t.Fatalf("expected active plan marked killed, got %+v", got)
	}
	if _, err := svc.SubmitOrder(t.Context(), OrderRequest{
		Venue:    marketdata.VenueHyperliquid,
		Symbol:   "ETH",
		Strategy: StrategyMarket,
		Side:     SideBuy,
		Quantity: 1,
	}); !errors.Is(err, ErrKillSwitchActive) {
		t.Fatalf("expected ErrKillSwitchActive, got %v", err)
	}
}

func TestLiveExecutionRoutingAndPersistentBots(t *testing.T) {
	candles := makeTestCandles(100, []float64{100, 102, 105, 108})
	// 1. Live order without LiveExecutor refuses explicitly (no fake fill).
	noLiveSvc, err := NewService(Config{
		DataDir: t.TempDir(),
		MarketReader: func(_ context.Context, _, _, _ string, _ int) ([]marketdata.Candle, error) {
			return candles, nil
		},
	})
	if err != nil {
		t.Fatalf("NewService(noLive): %v", err)
	}
	t.Cleanup(func() { _ = noLiveSvc.Close() })
	if _, err := noLiveSvc.SubmitOrder(t.Context(), OrderRequest{
		Venue:         marketdata.VenueHyperliquid,
		Symbol:        "BTC",
		Side:          SideBuy,
		Quantity:      0.5,
		AccountID:     "plugin:hyperliquid",
		ExecutionMode: ExecutionModeLive,
	}); err == nil {
		t.Fatalf("expected error when live router is unconfigured")
	}

	// 2. Live order with LiveExecutor + FasedScript bot evaluation & kill pause.
	evalCalls := 0
	svc, err := NewService(Config{
		DataDir: t.TempDir(),
		MarketReader: func(_ context.Context, _, _, _ string, _ int) ([]marketdata.Candle, error) {
			return candles, nil
		},
		LiveExecutor: func(_ context.Context, req OrderRequest, refPrice float64) (LiveExecReceipt, error) {
			return LiveExecReceipt{
				VenueOrderID: "hl-ord-42",
				FilledPrice:  refPrice,
				Immediate:    true,
			}, nil
		},
		ScriptEvaluator: func(_ context.Context, bot TradeBot, _ []marketdata.Candle) (string, string, string, error) {
			evalCalls++
			return "enter_long", SideBuy, "fasedscript trigger " + bot.ScriptID, nil
		},
	})
	if err != nil {
		t.Fatalf("NewService(live): %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	livePlan, err := svc.SubmitOrder(t.Context(), OrderRequest{
		Venue:         marketdata.VenueHyperliquid,
		Symbol:        "BTC",
		Side:          SideBuy,
		Quantity:      0.25,
		AccountID:     "plugin:hyperliquid",
		ExecutionMode: ExecutionModeLive,
	})
	if err != nil || livePlan.Simulated || livePlan.VenueOrderID != "hl-ord-42" || livePlan.ExecutionMode != ExecutionModeLive {
		t.Fatalf("unexpected live plan: %+v err=%v", livePlan, err)
	}

	bot, err := svc.CreateBot(t.Context(), CreateBotRequest{
		Name:           "Trend Runner",
		Kind:           BotKindFasedScript,
		AccountID:      "paper",
		Venue:          marketdata.VenueHyperliquid,
		Symbol:         "SOL",
		Interval:       "1h",
		ScriptID:       "ema-cross",
		MaxNotionalUSD: 200,
	})
	if err != nil || bot.Status != BotStatusPaused {
		t.Fatalf("CreateBot failed: %+v err=%v", bot, err)
	}
	started, err := svc.SetBotStatus(t.Context(), bot.ID, "start")
	if err != nil || started.Status != BotStatusRunning {
		t.Fatalf("SetBotStatus(start) failed: %+v err=%v", started, err)
	}
	sum1, err := svc.EvaluateBots(t.Context())
	if err != nil || sum1.Evaluated != 1 || sum1.Submitted != 1 {
		t.Fatalf("EvaluateBots first bar failed: %+v err=%v", sum1, err)
	}
	// Re-evaluating the same bar must not duplicate the order.
	sum2, err := svc.EvaluateBots(t.Context())
	if err != nil || sum2.Submitted != 0 {
		t.Fatalf("EvaluateBots same-bar dedupe failed: %+v err=%v", sum2, err)
	}
	if evalCalls != 1 {
		t.Fatalf("expected 1 eval call after bar dedupe, got %d", evalCalls)
	}
	snap, _ := svc.GetSnapshot(t.Context())
	foundBotOrder := false
	for _, p := range snap.Plans {
		if p.BotID == bot.ID && p.Source == SourceBot && p.Mode == ModeBot {
			foundBotOrder = true
		}
	}
	if !foundBotOrder {
		t.Fatalf("expected bot order plan in trade snapshot, got %+v", snap.Plans)
	}

	// Kill switch must immediately pause running bots on activation and keep EvaluateBots halted.
	_, _ = svc.SetKillSwitch(t.Context(), true, "risk stop", "owner")
	botsAfterKill, err := svc.ListBots(t.Context())
	if err != nil || len(botsAfterKill) != 1 || botsAfterKill[0].Status != BotStatusPaused {
		t.Fatalf("expected kill switch to immediately pause running bot, got %+v err=%v", botsAfterKill, err)
	}
	sumKill, err := svc.EvaluateBots(t.Context())
	if err != nil || !sumKill.Halted {
		t.Fatalf("expected EvaluateBots to report halted under kill switch, got %+v err=%v", sumKill, err)
	}
}
