package polymarket

import (
	"encoding/json"
	"testing"
)

func FuzzPolymarketOrderAndDecimalCodec(f *testing.F) {
	f.Add("0.55", "100.25", "BUY", "1234567890", `{"v":"1.25"}`)
	f.Add("0.0001", "999999999999", "SELL", "token-1", `12345`)
	f.Add("-1", "0", "", "", `null`)

	f.Fuzz(func(t *testing.T, price, shares, side, tokenID, rawJSON string) {
		_, _ = parseFixedDecimals(price, 6)
		_, _ = parseFixedDecimals(shares, 6)
		_, _ = BuildOrderIntent(OrderDraft{
			TokenID: tokenID,
			Side:    side,
			Price:   price,
			Shares:  shares,
		}, 1_700_000_000)
		var ft FlexText
		_ = json.Unmarshal([]byte(rawJSON), &ft)
	})
}
