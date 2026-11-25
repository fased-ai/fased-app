package equities

import (
	"encoding/json"
	"testing"
)

func FuzzEquitiesAndOCCParsers(f *testing.F) {
	f.Add("SPY   261218C00600000", `{"venue":"alpaca","symbol":"SPY","side":"BUY","orderType":"LIMIT","quantity":1,"limitPrice":590,"environment":"paper"}`)
	f.Add("NVDA261218P00145500", `{"venue":"ibkr","accountId":"DU123456","symbol":"NVDA","side":"SELL","orderType":"MARKET","quantity":2,"environment":"paper"}`)
	f.Add("BAD", `{"venue":"alpaca","symbol":"AAPL","side":"BUY","orderType":"MARKET","quantity":1,"environment":"live"}`)

	f.Fuzz(func(t *testing.T, rawOCC, rawOrder string) {
		_, _ = ParseOCCSymbol(rawOCC)
		var req OrderRequest
		if json.Unmarshal([]byte(rawOrder), &req) == nil {
			_, _ = ValidateOrderRequest(&req)
		}
		_ = ValidateVenueOrigin(VenueIBKR, "paper", rawOCC, rawOrder)
	})
}
