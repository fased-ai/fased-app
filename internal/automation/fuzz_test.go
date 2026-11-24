package automation

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/fased-ai/fased-app/internal/marketdata"
)

func FuzzAutomationInputs(f *testing.F) {
	f.Add(`{"id":"tr-1","name":"BTC Alert","kind":"price","condition":{"venue":"hyperliquid","symbol":"BTC","interval":"1h","comparator":"above","threshold":90000},"action":{"type":"notify","severity":"warn"}}`, "cross_above", 100.0, 99.0, 100.0)
	f.Add(`{"id":"tr-2","name":"Cron","kind":"cron","condition":{"cronExpr":"*/5 * * * *"},"action":{"type":"jev_triage"}}`, "pct_change_gte", 110.0, 100.0, 5.0)
	f.Add(`bad-json`, "invalid", math.NaN(), math.Inf(1), 0.0)

	f.Fuzz(func(t *testing.T, triggerJSON, comparator string, curr, prev, thresh float64) {
		var in TriggerInput
		if json.Unmarshal([]byte(triggerJSON), &in) == nil {
			_, _ = NormalizeTriggerInput(in)
		}
		_, _ = ValidateWebhookEndpoint(triggerJSON)
		_ = CompareMetric(comparator, curr, prev, thresh)

		candles := make([]marketdata.Candle, 0, 22)
		base := math.Abs(curr)
		if math.IsNaN(base) || math.IsInf(base, 0) || base == 0 {
			base = 100
		}
		for i := 0; i < 22; i++ {
			c := base + float64(i)
			candles = append(candles, marketdata.Candle{
				Time:   int64(1700000000 + i*60),
				Open:   c,
				High:   c + 2,
				Low:    c - 1,
				Close:  c + 0.5,
				Volume: 50 + float64(i),
			})
		}
		_, _, _ = ComputeRSI14(candles)
		_, _, _ = ComputeEMACross(candles, 9, 21)
		_, _, _ = ComputeVWAPDelta(candles)
		_, _, _ = ComputeVolumeSpike(candles, 10)
		_, _, _ = ComputeCVDDelta(candles, 10)
	})
}
