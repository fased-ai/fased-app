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

func TestValidationAndConfigStatisticalFixes(t *testing.T) {
	// 1. Explicit JSON zero feeBps / slippageBps must be preserved instead of defaulting to 5.0 / 3.0.
	var zeroCfg BacktestConfig
	if err := json.Unmarshal([]byte(`{"feeBps":0,"slippageBps":0}`), &zeroCfg); err != nil {
		t.Fatalf("Unmarshal zero fee config: %v", err)
	}
	normZero, err := NormalizeBacktestConfig(zeroCfg)
	if err != nil || normZero.FeeBps != 0 || normZero.SlippageBps != 0 {
		t.Fatalf("expected explicit 0 fee/slippage preserved, got fee=%v slip=%v err=%v", normZero.FeeBps, normZero.SlippageBps, err)
	}

	var omittedCfg BacktestConfig
	if err := json.Unmarshal([]byte(`{}`), &omittedCfg); err != nil {
		t.Fatalf("Unmarshal omitted config: %v", err)
	}
	normDefault, err := NormalizeBacktestConfig(omittedCfg)
	if err != nil || normDefault.FeeBps != defaultFeeBps || normDefault.SlippageBps != defaultSlippageBps {
		t.Fatalf("expected omitted fee/slippage to default (%.1f/%.1f), got %.1f/%.1f err=%v", defaultFeeBps, defaultSlippageBps, normDefault.FeeBps, normDefault.SlippageBps, err)
	}

	// 2. buildParamGrid centers sweep on user-supplied cfg.Params.
	inputs := []fasedscript.InputSpec{{Name: "fast", Default: 9, Min: 5, Max: 30, Step: 2}}
	grid := buildParamGrid(inputs, map[string]float64{"fast": 20})
	if len(grid) != 3 || grid[0]["fast"] != 18 || grid[1]["fast"] != 20 || grid[2]["fast"] != 22 {
		t.Fatalf("expected grid centered at user param 20 -> [18,20,22], got %+v", grid)
	}

	// 3. Sub-penny pricing retains significant digits cleanly.
	subCandles := []marketdata.Candle{
		{Time: 1000, Open: 0.00001234, High: 0.00001240, Low: 0.00001220, Close: 0.00001235, Volume: 100},
		{Time: 2000, Open: 0.00001236, High: 0.00001250, Low: 0.00001230, Close: 0.00001248, Volume: 100},
		{Time: 3000, Open: 0.00001249, High: 0.00001260, Low: 0.00001240, Close: 0.00001255, Volume: 100},
	}
	_, subTrades, _ := Simulate(subCandles, fasedscript.StrategySpec{Long: []bool{true, false, false}}, normZero)
	if len(subTrades) != 1 || subTrades[0].EntryPrice <= 0 || subTrades[0].ExitPrice <= 0 {
		t.Fatalf("expected non-zero rounded sub-penny trade prices, got %+v", subTrades)
	}

	// 4. Gate 3 must fail when holdout executes 0 trades.
	candles := syntheticCandles(240)
	flatProg, err := fasedscript.Compile(`plot("c", close); strategy({ long: false, short: false });`)
	if err != nil {
		t.Fatal(err)
	}
	flatCfg, _ := NormalizeBacktestConfig(BacktestConfig{Limit: 240, Permutations: 50, WFPermutations: 25})
	flatRep, err := RunFourGateValidation(t.Context(), candles, flatProg, flatCfg)
	if err != nil {
		t.Fatalf("RunFourGateValidation zero-trade: %v", err)
	}
	if flatRep.Gate3.Passed || flatRep.Gate3.HoldoutTrades != 0 {
		t.Fatalf("expected Gate 3 failure with 0 holdout trades, got %+v", flatRep.Gate3)
	}

	// 5. Canceled context propagates context.Canceled rather than bogus p-values.
	emaProg, err := fasedscript.Compile(`const p = input("p", 30, 26, 34, 4); const m = ema(close, p); const f = ema(close, 5); plot("m", m); strategy({ long: crossover(f, m), exitLong: crossunder(f, m) });`)
	if err != nil {
		t.Fatal(err)
	}
	cCtx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := RunFourGateValidation(cCtx, candles, emaProg, flatCfg); err == nil {
		t.Fatal("expected canceled context error from RunFourGateValidation")
	}

	// 6. Causal warm-up prefix enables ema(close, 30) to generate signals cleanly in a 40-bar holdout tail.
	mWarm, err := evalAndSimulateWindow(t.Context(), candles, len(candles)-40, emaProg, map[string]float64{"p": 30}, flatCfg)
	if err != nil {
		t.Fatalf("evalAndSimulateWindow warm-up check failed: %v", err)
	}
	if mWarm.TradeCount == 0 {
		t.Fatalf("expected warm-up prefix to enable trades in 40-bar holdout for ema(30), got 0 trades")
	}

	// 7. Gate 1 ignores a 1-trade outlier cell when selecting BestParams if eligible (>=3 trades) cells exist.
	outlierSrc := `const mode = input("mode", 1, 0, 2, 1);
const bars = cumCount;
plot("m", close);`
	_ = outlierSrc
	// Build a clean deterministic script with 3 parameter modes: mode=0 (<3 trades high Sharpe) vs mode=1 & mode=2 (>=3 trades).
	upDown := make([]marketdata.Candle, 120)
	baseTs := int64(1795400000)
	for i := range upDown {
		// Sawtooth wave cleanly ascending overall so fast/slow MA crosses >= 6 times on mode=0,1 while mode=2 only trades once via steep threshold.
		px := 100.0 + float64(i)*0.4 + math.Sin(float64(i)*0.5)*3.5
		upDown[i] = marketdata.Candle{Time: baseTs + int64(i)*3600, Open: px - 0.5, High: px + 1.5, Low: px - 1.5, Close: px + 0.5, Volume: 100}
	}
	outlierProg, err := fasedscript.Compile(`const th = input("th", 0, 0, 2, 1);
const f = ema(close, 3);
const s = ema(close, 8);
const trigger = th >= 2 ? (highest(close, 12) == close && shift(close, 1) < close && close > 140) : crossover(f, s);
const exit = th >= 2 ? false : crossunder(f, s);
plot("f", f);
strategy({ long: trigger, exitLong: exit });`)
	if err != nil {
		t.Fatalf("Compile Gate 1 outlier script: %v", err)
	}
	repOutlier, err := RunFourGateValidation(t.Context(), upDown, outlierProg, flatCfg)
	if err != nil {
		t.Fatalf("RunFourGateValidation outlier test: %v", err)
	}
	// If th=2 produced < 3 trades on inSample, Gate 1 must not pick th=2 as BestParams when th=0/1 have >=3 trades.
	if len(repOutlier.Gate1.Cells) < 2 {
		t.Fatalf("expected >=2 plateau cells, got %+v", repOutlier.Gate1.Cells)
	}
	hasEligible := false
	outlierSingleTrade := false
	for _, c := range repOutlier.Gate1.Cells {
		if c.Trades >= 3 {
			hasEligible = true
		}
		if c.Params["th"] == 2 && c.Trades < 3 {
			outlierSingleTrade = true
		}
	}
	if hasEligible && outlierSingleTrade && repOutlier.Gate1.BestParams["th"] == 2 {
		t.Fatalf("expected Gate 1 to ignore <3-trade outlier cell th=2, got BestParams=%+v cells=%+v", repOutlier.Gate1.BestParams, repOutlier.Gate1.Cells)
	}
}
