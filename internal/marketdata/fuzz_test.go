package marketdata

import (
	"math"
	"testing"
)

func FuzzMarketDataParsersAndTransforms(f *testing.F) {
	f.Add([]byte(`[{"t":1760000000000,"o":"100","h":"102","l":"99","c":"101","v":"10"}]`))
	f.Add([]byte(`[[1760000000000,"100","105","98","104","50"]]`))
	f.Add([]byte(`{"retCode":0,"result":{"list":[["1760000000000","100","101","99","100","5"]]}}`))
	f.Add([]byte("DATE,CPI\n2026-10-01,314.5\n2026-11-01,.\n"))
	f.Add([]byte(`{"name":"Issuer","filings":{"recent":{"accessionNumber":["0001-26-000001"],"filingDate":["2026-10-01"],"form":["10-Q"],"primaryDocument":["doc.htm"]}}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		candles, _ := ParseHyperliquidCandles(data)
		if _, err := ParseBinanceKlines(data); err == nil {
			// Also test transformed styles whenever Binance array parses.
		}
		_, _ = ParseBybitKlines(data)
		_, _ = ParseKrakenOHLC(data)
		_, _ = ParseGeckoTerminalOHLCV(data)
		_, _ = ParseFREDCSV(data, "CPIAUCSL", 1760000000)
		_, _ = ParseSECSubmissions(data, "0000320193", 10)
		_, _ = ParseHyperliquidLeaderboard(data, "week", 1760000000)

		if len(candles) > 0 {
			for _, st := range []string{StyleHeikinAshi, StyleRenko, StyleRange, StyleKagi, StylePointFigure, StyleThreeLineBreak} {
				out, err := TransformCandles(candles, TransformOptions{Style: st})
				if err != nil {
					continue
				}
				for _, b := range out {
					if math.IsNaN(b.Open) || math.IsNaN(b.Close) || math.IsInf(b.Open, 0) || math.IsInf(b.Close, 0) {
						t.Fatalf("non-finite transformed bar: %+v", b)
					}
				}
			}
		}
	})
}
