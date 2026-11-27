package wenlv1x

import (
	"context"
	"math"
	"strings"

	"github.com/fased-ai/fased-app/internal/onchain"
)

func normalizeCandleInterval(raw string) (string, int64) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "10s":
		return "10s", 10
	case "1m":
		return "1m", 60
	case "5m":
		return "5m", 300
	case "1h":
		return "1h", 3600
	case "1d":
		return "1d", 86400
	default:
		return "10m", onchain.LV1XRoundIntervalSec
	}
}

// GetCandles returns BTC/USD candles synced to 10m LV1X round slots, using Fased's
// marketdata provider when available and falling back cleanly to deterministic candles.
func (e *Engine) GetCandles(ctx context.Context, rawInterval string, limit int) (CandleResponse, error) {
	interval, stepSec := normalizeCandleInterval(rawInterval)
	if limit <= 0 || limit > 480 {
		limit = 120
	}

	var bars []CandleBar
	source := "simulation"
	if e.candleProvider != nil && !e.fixture {
		if live, src, err := e.candleProvider(ctx, interval, limit); err == nil && len(live) > 0 {
			bars = live
			if src != "" {
				source = src
			} else {
				source = "marketdata"
			}
		}
	}

	if len(bars) == 0 {
		bars = e.deterministicCandles(ctx, stepSec, limit)
	} else {
		for i := range bars {
			bars[i].RoundSlot = uint64(bars[i].Time / onchain.LV1XRoundIntervalSec)
		}
	}

	return CandleResponse{
		Interval: interval,
		Symbol:   "BTC/USD",
		Source:   source,
		Candles:  bars,
	}, nil
}

func (e *Engine) deterministicCandles(ctx context.Context, stepSec int64, limit int) []CandleBar {
	nowSec := e.now().UTC().Unix()
	alignedEnd := (nowSec / stepSec) * stepSec
	streak, _ := e.ListStreak(ctx, 48)
	streakBySlot := make(map[uint64]StreakEntry, len(streak))
	for _, s := range streak {
		streakBySlot[s.RoundSlot] = s
	}

	out := make([]CandleBar, 0, limit)
	price := 94250.0
	for i := limit - 1; i >= 0; i-- {
		ts := alignedEnd - int64(i)*stepSec
		slot := uint64(ts / onchain.LV1XRoundIntervalSec)
		wave := math.Sin(float64(ts)/900.0)*42.0 + math.Cos(float64(ts)/310.0)*18.0
		open := math.Round((price+wave)*100) / 100
		delta := math.Sin(float64(ts+stepSec)/420.0)*36.0 + math.Cos(float64(i)*0.7)*14.0
		var win *uint8
		if st, ok := streakBySlot[slot]; ok {
			delta = open * (float64(st.MoveBps) / 10000.0)
			r := st.WinningRange
			win = &r
		}
		closeP := math.Round((open+delta)*100) / 100
		spread := math.Max(12.0, math.Abs(delta)*0.45+16.0)
		high := math.Round((math.Max(open, closeP)+spread)*100) / 100
		low := math.Round((math.Min(open, closeP)-spread*0.85)*100) / 100
		moveBps := 0
		if open > 0 {
			moveBps = int(math.Round(((closeP - open) / open) * 10000.0))
		}
		vol := math.Round((12.5+math.Abs(delta)*0.45)*100) / 100
		out = append(out, CandleBar{
			Time:         ts,
			Open:         open,
			High:         high,
			Low:          low,
			Close:        closeP,
			Volume:       vol,
			RoundSlot:    slot,
			MoveBps:      moveBps,
			WinningRange: win,
		})
		price = closeP
	}
	return out
}
