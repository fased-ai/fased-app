package fasedscript

import (
	"encoding/json"
	"testing"
)

func FuzzFasedScriptCompileAndEval(f *testing.F) {
	for _, tpl := range Templates() {
		f.Add(tpl.Source)
	}
	f.Add(`const a: number = 10; const sig = crossover(ema(close, 5), ema(close, 15)); strategy({ long: sig, exitLong: crossunder(ema(close, 5), ema(close, 15)) });`)
	f.Add(`shift(close, -1);`)
	f.Add(`while (true) {}`)

	candles := syntheticTrendCandles(40)

	f.Fuzz(func(t *testing.T, src string) {
		prog, err := Compile(src)
		if err != nil {
			return
		}
		res, err := prog.Eval(candles, nil)
		if err != nil {
			return
		}
		if _, err := json.Marshal(res); err != nil {
			t.Fatalf("EvalResult JSON marshal failure on valid Eval output: %v", err)
		}
	})
}
