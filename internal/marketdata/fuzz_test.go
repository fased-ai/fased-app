package marketdata

import (
	"encoding/json"
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
		_, _ = ParseLighterCandles(data)
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

		var d ChartDrawing
		if err := json.Unmarshal(data, &d); err == nil {
			if clean, err := ValidateChartDrawing(d); err == nil {
				if lvl, ok := EvaluateDrawingPrice(clean, clean.P1.Time+3600); ok {
					if math.IsNaN(lvl) || math.IsInf(lvl, 0) || lvl <= 0 {
						t.Fatalf("invalid evaluated drawing level: %v", lvl)
					}
				}
			}
		}

		if parsed, err := ParseCSVOHLCV(string(data)); err == nil {
			for _, c := range parsed {
				if math.IsNaN(c.Close) || math.IsInf(c.Close, 0) || c.Close <= 0 {
					t.Fatalf("invalid CSV candle: %+v", c)
				}
			}
		}
	})
}

func FuzzParseStage5Candles(f *testing.F) {
	f.Add([]byte(`{"code":"0","msg":"","data":[["1795600000000","61000","61200","60900","61100","120"]]}`))
	f.Add([]byte(`[[1795600000,3100,3180,3120,3165,420.5]]`))
	f.Add([]byte(`{"result":{"status":"ok","ticks":[1795600000000],"open":[61000],"high":[61200],"low":[60900],"close":[61100],"volume":[12.3]}}`))
	f.Add([]byte(`{"bars":[{"t":"2026-11-25T15:00:00Z","o":597,"h":599,"l":596,"c":598,"v":120000,"n":10}]}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		for _, fn := range []func([]byte) ([]Candle, error){
			ParseOKXCandles,
			ParseCoinbaseCandles,
			ParseDeribitCandles,
			ParseAlpacaBars,
		} {
			candles, err := fn(data)
			if err != nil {
				continue
			}
			for _, c := range candles {
				if math.IsNaN(c.Close) || math.IsInf(c.Close, 0) || c.Close <= 0 {
					t.Fatalf("invalid Stage 5 candle: %+v", c)
				}
			}
		}
	})
}
