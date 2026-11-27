package lighter

import "testing"

func FuzzLighterOrderAndDecimalCodec(f *testing.F) {
	f.Add("100.5", "0.25", "BUY", "LIMIT", int64(1))
	f.Add("0", "999999999999999999999", "SELL", "MARKET", int64(-1))

	f.Fuzz(func(t *testing.T, price, size, side, orderType string, marketID int64) {
		_, _ = parseScaledUint(price, 6)
		_, _ = parseScaledUint(size, 6)
		_, _ = BuildCreateOrderIntent(OrderDraft{
			Symbol:       "SOL-USD",
			Side:         side,
			OrderType:    orderType,
			Price:        price,
			Size:         size,
			AccountIndex: 1,
			APIKeyIndex:  0,
			Nonce:        1,
		}, OrderBookMeta{
			MarketID:               marketID,
			Symbol:                 "SOL-USD",
			SupportedPriceDecimals: 2,
			SupportedSizeDecimals:  4,
			MinBaseAmount:          "0.01",
			MinQuoteAmount:         "1",
		}, 1_700_000_000)
	})
}
