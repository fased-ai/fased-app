package cex

import (
	"encoding/json"
	"testing"
)

func FuzzCEXOrderAndPermissionParsers(f *testing.F) {
	f.Add(VenueBinance, `{"venue":"binance","symbol":"BTCUSDT","side":"BUY","orderType":"LIMIT","quantity":0.1,"price":50000,"environment":"paper"}`, "spot_trade")
	f.Add(VenueKraken, `{"venue":"kraken","symbol":"XBTUSD","side":"SELL","orderType":"MARKET","quantity":1,"environment":"mainnet"}`, "withdraw")
	f.Add(VenueDeribit, `{"venue":"deribit","symbol":"BTC-PERPETUAL","side":"BUY","orderType":"LIMIT","quantity":10,"price":60000,"environment":"testnet"}`, "derivatives_trade")

	f.Fuzz(func(t *testing.T, venue, rawOrder, perm string) {
		var req OrderRequest
		if json.Unmarshal([]byte(rawOrder), &req) == nil {
			_ = ValidateOrderRequest(&req)
		}
		_, _ = VerifyTradeOnlyPermissions(venue, []string{perm})
		_ = ValidateTestnetURL(venue, rawOrder)
	})
}
