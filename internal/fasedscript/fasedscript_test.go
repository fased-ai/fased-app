package fasedscript

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/fased-ai/fased-app/internal/marketdata"
)

func syntheticTrendCandles(n int) []marketdata.Candle {
	out := make([]marketdata.Candle, n)
	price := 100.0
	baseTs := int64(1795400000)
	for i := 0; i < n; i++ {
		cycle := float64((i%16)-7) * 0.65
		drift := 0.25 + cycle
		open := price
		closePrice := price + drift
		high := mathMax(open, closePrice) + 1.2
		low := mathMin(open, closePrice) - 1.1
		vol := 100.0 + float64(i%7)*25.0
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

func mathMax(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func mathMin(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func TestTemplatesCompileEvalAndJSONSafe(t *testing.T) {
	candles := syntheticTrendCandles(160)
	for _, tpl := range Templates() {
		prog, err := Compile(tpl.Source)
		if err != nil {
			t.Fatalf("Compile(%s): %v", tpl.ID, err)
		}
		res, err := prog.Eval(candles, nil)
		if err != nil {
			t.Fatalf("Eval(%s): %v", tpl.ID, err)
		}
		if res.BarCount != len(candles) || len(res.Plots) == 0 {
			t.Fatalf("unexpected EvalResult for %s: bars=%d plots=%d", tpl.ID, res.BarCount, len(res.Plots))
		}
		// Must marshal cleanly without NaN errors.
		encoded, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("json.Marshal(%s): %v", tpl.ID, err)
		}
		if !strings.Contains(string(encoded), "null") {
			t.Logf("template %s produced no leading null warmup values", tpl.ID)
		}
	}
}

func TestSandboxAndLookAheadGuards(t *testing.T) {
	candles := syntheticTrendCandles(50)

	// Reject forbidden keywords at compile time.
	for _, bad := range []string{
		`import fs from "fs";`,
		`while (true) {}`,
		`for (let i = 0; i < 10; i = i + 1) {}`,
		`const x = eval("1+1");`,
		`const x = fetch("https://evil.example");`,
	} {
		if _, err := Compile(bad); err == nil {
			t.Fatalf("expected sandbox compile rejection for %q", bad)
		}
	}

	// Reject negative shift lag (look-ahead bias).
	prog, err := Compile(`const future = shift(close, -1); plot("f", future);`)
	if err != nil {
		t.Fatalf("Compile shift test: %v", err)
	}
	if _, err := prog.Eval(candles, nil); err == nil || !strings.Contains(err.Error(), "look-ahead") {
		t.Fatalf("expected look-ahead rejection on shift(close, -1), got %v", err)
	}

	// Reject oversized script.
	huge := strings.Repeat("const a = 1;\n", 4096)
	if _, err := Compile(huge); err == nil {
		t.Fatal("expected size/AST bound rejection on huge script")
	}

	// Reject deeply nested expression.
	nested := strings.Repeat("(", 64) + "1" + strings.Repeat(")", 64)
	if _, err := Compile(nested); err == nil {
		t.Fatal("expected nesting depth rejection")
	}
}

func TestConcurrentProgramEvalAndOverrides(t *testing.T) {
	candles := syntheticTrendCandles(120)
	tpl := Templates()[0]
	prog, err := Compile(tpl.Source)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(fastVal float64) {
			defer wg.Done()
			res, err := prog.Eval(candles, map[string]float64{"fast": fastVal})
			if err != nil {
				t.Errorf("concurrent Eval error: %v", err)
				return
			}
			if res.Params["fast"] != fastVal {
				t.Errorf("expected param fast=%v, got %v", fastVal, res.Params["fast"])
			}
		}(float64(5 + g*2))
	}
	wg.Wait()
}

func TestVMEdgeCasesSeriesIfNullUnaryAndUTCVWAP(t *testing.T) {
	candles := syntheticTrendCandles(40)

	// 1. Reject bar series inside `if (...)` (causes look-ahead bias), while scalar `if` works.
	ifProg, err := Compile(`const m = ema(close, 5); var a = close; if (close > m) { a = m; } plot("a", a);`)
	if err != nil {
		t.Fatalf("Compile series if test: %v", err)
	}
	if _, err := ifProg.Eval(candles, nil); err == nil || !strings.Contains(err.Error(), "look-ahead") {
		t.Fatalf("expected look-ahead error on series `if`, got %v", err)
	}

	scalarIfProg, err := Compile(`const f = input("fast", 8, 2, 20, 1); var p = ema(close, 5); if (f > 5) { p = ema(close, 9); } plot("p", p);`)
	if err != nil {
		t.Fatalf("Compile scalar if test: %v", err)
	}
	if res, err := scalarIfProg.Eval(candles, nil); err != nil || len(res.Plots) != 1 {
		t.Fatalf("expected scalar if to evaluate cleanly, got err=%v", err)
	}

	// 2. Null equality works cleanly without crashing.
	nullProg, err := Compile(`const a = null; if (!(a == null) || (a != null) || (a == 1)) { plot("fail", close); } plot("ok", close);`)
	if err != nil {
		t.Fatalf("Compile null equality test: %v", err)
	}
	if res, err := nullProg.Eval(candles, nil); err != nil || len(res.Plots) != 1 || res.Plots[0].Name != "ok" {
		t.Fatalf("unexpected null equality behavior: %+v err=%v", res.Plots, err)
	}

	// 3. Deep unary chain respects MaxRecursionDepth.
	deepUnary := strings.Repeat("!", 64) + "true"
	if _, err := Compile(deepUnary); err == nil || !strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("expected nesting depth error on deep unary chain, got %v", err)
	}

	// 4. VWAP resets cumulative session sums at UTC midnight boundaries.
	day1Midnight := int64(86400 * 20000)
	sessionCandles := []marketdata.Candle{
		{Time: day1Midnight + 3600, Open: 100, High: 100, Low: 100, Close: 100, Volume: 1000},
		{Time: day1Midnight + 7200, Open: 100, High: 100, Low: 100, Close: 100, Volume: 1000},
		{Time: day1Midnight + 86400 + 3600, Open: 200, High: 200, Low: 200, Close: 200, Volume: 10},
	}
	vw := VWAP(sessionCandles)
	if len(vw) != 3 || vw[2] != 200 {
		t.Fatalf("expected VWAP to reset on UTC midnight boundary to 200.0, got %+v", vw)
	}
}
