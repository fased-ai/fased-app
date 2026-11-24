package quantlab

import (
	"encoding/json"
	"testing"

	"github.com/fased-ai/fased-app/internal/fasedscript"
)

func FuzzQuantLabInputs(f *testing.F) {
	f.Add(`{"venue":"hyperliquid","symbol":"BTC","interval":"1h","limit":120,"feeBps":5,"slippageBps":3,"permutations":50,"wfPermutations":25,"holdoutPct":0.2}`, uint64(42))
	f.Add(`{"venue":"bad","limit":99999}`, uint64(0))

	candles := syntheticCandles(60)
	strat := fasedscript.StrategySpec{
		Long:          []bool{false, true, false, false, false, true},
		ExitLong:      []bool{false, false, false, true, false, false},
		StopLossPct:   2.5,
		TakeProfitPct: 5.0,
	}

	f.Fuzz(func(t *testing.T, cfgJSON string, seed uint64) {
		var rawCfg BacktestConfig
		if json.Unmarshal([]byte(cfgJSON), &rawCfg) == nil {
			_, _ = NormalizeBacktestConfig(rawCfg)
		}
		permed := PermuteCandles(candles, seed)
		if len(permed) != len(candles) {
			t.Fatalf("PermuteCandles length mismatch: %d vs %d", len(permed), len(candles))
		}
		m, _, _ := Simulate(permed, strat, BacktestConfig{
			Interval:       "1h",
			InitialCapital: 10000,
			FeeBps:         5,
			SlippageBps:    3,
		})
		if _, err := json.Marshal(m); err != nil {
			t.Fatalf("metrics JSON marshal failed: %v", err)
		}
	})
}
