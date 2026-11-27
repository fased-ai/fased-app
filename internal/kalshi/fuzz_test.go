package kalshi

import "testing"

func FuzzKalshiOrderAndErrorCodec(f *testing.F) {
	f.Add("KXBTC-26DEC31-T100000", "bid", "10", "0.42", `{"error":{"code":"invalid","message":"bad order"}}`)
	f.Add("../bad", "ask", "0", "1.01", `not-json`)

	f.Fuzz(func(t *testing.T, ticker, side, count, price, errRaw string) {
		_ = validTicker(ticker)
		_, _ = parseScaledDecimal(price, 4)
		_, _ = BuildOrder(OrderDraft{
			Ticker: ticker,
			Side:   side,
			Count:  count,
			Price:  price,
		})
		_, _ = parseKalshiError([]byte(errRaw))
	})
}
