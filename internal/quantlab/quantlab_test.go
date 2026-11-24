package quantlab

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/fasedscript"
	"github.com/fased-ai/fased-app/internal/marketdata"
)

func syntheticCandles(n int) []marketdata.Candle {
	out := make([]marketdata.Candle, n)
	price := 50000.0
	baseTs := int64(1795400000)
	for i := 0; i < n; i++ {
		wave := math.Sin(float64(i)/7.0)*180.0 + 24.0
		open := price
		closePrice := price + wave
		if closePrice < 100 {
			closePrice = 100
		}
		high := math.Max(open, closePrice) + 95.0
		low := math.Max(10, math.Min(open, closePrice)-90.0)
		vol := 150.0 + float64(i%11)*35.0
		out[i] = marketdata.Candle{
			Time:   baseTs + int64(i)*3600,
			Open:   open,
			High:   high,
			Low:    low,
			Close:  closePrice,
			Volume: vol,
		}
		price = closePrice
	}
	return out
}

func TestNextBarOpenCausalSimulationAndRiskStops(t *testing.T) {
	// Verify signal at bar 1 enters at bar 2 Open (NOT bar 1 Close), and SL triggers before TP conservatively.
	candles := []marketdata.Candle{
		{Time: 1000, Open: 100, High: 101, Low: 99, Close: 100, Volume: 10},
		{Time: 2000, Open: 100, High: 102, Low: 98, Close: 101, Volume: 10},
		{Time: 3000, Open: 110, High: 120, Low: 105, Close: 112, Volume: 10}, // bar 2 Open=110 entry
		{Time: 4000, Open: 112, High: 130, Low: 95, Close: 128, Volume: 10},  // both SL (low 95) and TP (high 130) hit -> SL wins!
		{Time: 5000, Open: 128, High: 129, Low: 127, Close: 128, Volume: 10},
	}
	strat := fasedscript.StrategySpec{
		Long:          []bool{false, true, false, false, false},
		StopLossPct:   5.0,
		TakeProfitPct: 10.0,
	}
	cfg := BacktestConfig{
		Interval:       "1h",
		InitialCapital: 10000,
		FeeBps:         0,
		SlippageBps:    0,
	}
	_, trades, _ := Simulate(candles, strat, cfg)
	if len(trades) != 1 {
		t.Fatalf("expected 1 completed trade, got %d", len(trades))
	}
	if trades[0].EntryPrice != 110 || trades[0].EntryTime != 3000 {
		t.Fatalf("expected next-bar open entry at 110 t=3000, got %+v", trades[0])
	}
	if trades[0].ExitReason != "stop_loss" {
		t.Fatalf("expected conservative stop_loss before take_profit, got %s", trades[0].ExitReason)
	}
}

func TestServiceScriptCRUDValidationReproducibilityAndPersistence(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC)
	candles := syntheticCandles(320)

	svc, err := NewService(Config{
		DataDir:     dir,
		FixtureMode: true,
		Now:         func() time.Time { return now },
		MarketReader: func(_ context.Context, _, _, _ string, limit int) ([]marketdata.Candle, error) {
			if limit < len(candles) {
				return candles[len(candles)-limit:], nil
			}
			return candles, nil
		},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	scripts, err := svc.ListScripts(t.Context())
	if err != nil || len(scripts) != 4 {
		t.Fatalf("expected 4 seeded built-in scripts, got %d err=%v", len(scripts), err)
	}

	// Refuse overwriting or deleting built-in template.
	if _, err := svc.SaveScript(t.Context(), ScriptInput{
		ID:     "ema-rsi-trend",
		Name:   "Hijack",
		Source: `plot("x", close);`,
	}); err == nil {
		t.Fatal("expected built-in overwrite refusal")
	}
	if _, err := svc.DeleteScript(t.Context(), "ema-rsi-trend"); err == nil {
		t.Fatal("expected built-in deletion refusal")
	}

	// Save custom script cleanly.
	customSrc := `const fast = input("fast", 6, 4, 10, 2);
const slow = input("slow", 14, 12, 20, 4);
const ef = ema(close, fast);
const es = ema(close, slow);
plot("Fast", ef, "accent", "overlay");
plot("Slow", es, "muted", "overlay");
strategy({ long: crossover(ef, es), exitLong: crossunder(ef, es), stopLossPct: 3, sizePct: 50 });`

	saved, err := svc.SaveScript(t.Context(), ScriptInput{
		ID:          "custom-ema-test",
		Name:        "Custom EMA Test",
		Category:    "trend",
		Description: "Clean custom script",
		Source:      customSrc,
	})
	if err != nil || len(saved.Inputs) != 2 {
		t.Fatalf("SaveScript: %+v err=%v", saved, err)
	}

	// EvaluateIndicator returns aligned JSON-marshalable plots.
	evalRes, err := svc.EvaluateIndicator(t.Context(), EvalIndicatorRequest{
		ScriptID: "custom-ema-test",
		Limit:    120,
	})
	if err != nil || evalRes.BarCount != 120 || len(evalRes.Plots) != 2 {
		t.Fatalf("EvaluateIndicator failed: %+v err=%v", evalRes, err)
	}
	if _, err := json.Marshal(evalRes); err != nil {
		t.Fatalf("EvaluateIndicator JSON marshal: %v", err)
	}

	// RunBacktest works deterministically.
	btRes, err := svc.RunBacktest(t.Context(), BacktestRequest{
		ScriptID: "custom-ema-test",
		Config:   BacktestConfig{Limit: 320},
	})
	if err != nil || btRes.Metrics.TradeCount == 0 || len(btRes.Equity) == 0 {
		t.Fatalf("RunBacktest failed: metrics=%+v err=%v", btRes.Metrics, err)
	}

	// Run full 4-Gate validation (1,000 bar perms + 200 WF perms) twice with same seed -> bit-identical stats & fast runtime.
	start := time.Now()
	run1, err := svc.RunValidation(t.Context(), ValidateRequest{
		ScriptID: "custom-ema-test",
		Config:   BacktestConfig{Limit: 320, Seed: 777, Permutations: 1000, WFPermutations: 200},
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("RunValidation #1: %v", err)
	}
	t.Logf("4-Gate full validation (1000 perms + 200 WF perms) took %s", elapsed)
	if run1.Report.ExecutionPolicy != "paper_shadow_only" || run1.Report.Gate2.Iterations != 1000 || run1.Report.Gate4.Iterations != 200 || len(run1.Report.Gate3.Folds) == 0 {
		t.Fatalf("unexpected 4-Gate report shape: %+v", run1.Report)
	}

	run2, err := svc.RunValidation(t.Context(), ValidateRequest{
		ScriptID: "custom-ema-test",
		Config:   BacktestConfig{Limit: 320, Seed: 777, Permutations: 1000, WFPermutations: 200},
	})
	if err != nil {
		t.Fatalf("RunValidation #2: %v", err)
	}
	if run1.Report.Gate2.PValue != run2.Report.Gate2.PValue || run1.Report.Gate4.PValue != run2.Report.Gate4.PValue {
		t.Fatalf("expected bit-identical seeded permutation p-values: run1=(%.4f,%.4f) run2=(%.4f,%.4f)",
			run1.Report.Gate2.PValue, run1.Report.Gate4.PValue, run2.Report.Gate2.PValue, run2.Report.Gate4.PValue)
	}

	// Verify SQLite persistence across Close/Reopen.
	_ = svc.Close()
	reopened, err := NewService(Config{DataDir: dir, FixtureMode: true, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("reopen NewService: %v", err)
	}
	defer reopened.Close()

	runs, err := reopened.ListRuns(t.Context(), 10)
	if err != nil || len(runs) != 2 {
		t.Fatalf("expected 2 persisted validation runs after reopen, got %d err=%v", len(runs), err)
	}
	gotRun, found, err := reopened.GetRun(t.Context(), run1.ID)
	if err != nil || !found || gotRun.Report.Gate2.Iterations != 1000 {
		t.Fatalf("GetRun after reopen failed: found=%v err=%v", found, err)
	}

	deleted, err := reopened.DeleteScript(t.Context(), "custom-ema-test")
	if err != nil || !deleted {
		t.Fatalf("DeleteScript failed: deleted=%v err=%v", deleted, err)
	}
}
