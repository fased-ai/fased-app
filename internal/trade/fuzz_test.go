package trade

import (
	"encoding/json"
	"math"
	"testing"
)

func FuzzNormalizeOrderRequest(f *testing.F) {
	f.Add([]byte(`{"venue":"hyperliquid","symbol":"BTC","strategy":"bracket","side":"buy","quantity":0.25,"entryPrice":95000,"stopPrice":92000,"targetPrice":99000}`))
	f.Add([]byte(`{"venue":"binance","symbol":"BTCUSDT","strategy":"grid","side":"buy","quantity":1,"gridLower":80000,"gridUpper":100000,"gridLevels":6}`))
	f.Add([]byte(`{"venue":"solana","symbol":"SOL","strategy":"trailing_atr","side":"sell","quantity":5,"atrMultiplier":2.5}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var req OrderRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return
		}
		norm, err := NormalizeOrderRequest(req)
		if err != nil {
			return
		}
		if math.IsNaN(norm.Quantity) || math.IsInf(norm.Quantity, 0) || norm.Quantity <= 0 {
			t.Fatalf("invalid normalized quantity: %+v", norm)
		}
	})
}
