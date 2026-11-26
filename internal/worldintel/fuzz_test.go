package worldintel

import (
	"math"
	"testing"
)

func FuzzWorldIntelParsers(f *testing.F) {
	f.Add([]byte(`{"articles":[{"url":"https://example.org/a","title":"Fed holds rates in Washington","seendate":"20261124T000000Z"}]}`))
	f.Add([]byte(`{"features":[{"id":"eq1","properties":{"mag":5.1,"place":"Tokyo, Japan","title":"M 5.1 Tokyo"},"geometry":{"coordinates":[139.7,35.6]}}]}`))
	f.Add([]byte(`[{"id":"m1","slug":"fed","question":"Will Fed cut rates?","outcomePrices":"[\"0.55\",\"0.45\"]","volume24hr":1000}]`))
	f.Add([]byte(`{"markets":[{"ticker":"KX1","title":"Will CPI cool?","yes_bid":45,"yes_ask":55,"volume_24h":500}]}`))
	f.Add([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><item><title>ECB note</title><link>https://ecb.europa.eu/a</link><pubDate>Tue, 24 Nov 2026 00:00:00 GMT</pubDate></item></channel></rss>`))
	f.Add([]byte(`https://127.0.0.1:8080/feed.xml`))

	src := DefaultSources()[0]
	now := int64(1760000000)

	f.Fuzz(func(t *testing.T, data []byte) {
		evs, err := ParseGDELTJSON(data, src, now)
		if err == nil {
			for _, ev := range evs {
				if math.IsNaN(ev.Lat) || math.IsInf(ev.Lat, 0) || math.IsNaN(ev.Lon) || math.IsInf(ev.Lon, 0) {
					t.Fatalf("non-finite event coords: %+v", ev)
				}
			}
		}
		_, _ = ParseUSGSGeoJSON(data, src, now)
		preds, pErr := ParsePolymarketJSON(data, now)
		if pErr == nil {
			for _, p := range preds {
				if math.IsNaN(p.YesProb) || p.YesProb < 0 || p.YesProb > 1 {
					t.Fatalf("invalid YesProb: %+v", p)
				}
			}
		}
		_, _ = ParseKalshiJSON(data, now)
		_, _ = ParseRSSOrAtom(data, src, now)
		_, _ = ParseWorldBankJSON(data, src, now)
		_, _ = ParseConflictJSON(data, src, now)
		_, _ = ParseSECForm4Feed(data, src, now)
		_, _ = ParseWorldMonitorSidecarJSON(data, src, now)

		if len(data) <= 1024 {
			raw := string(data)
			_, _, _, _ = InferLocation(raw, RegionGlobal)
			_ = ExtractTickers(raw)
			_, _ = ValidateSourceEndpoint(raw, KindRSS, false)
			_, _ = ValidateSourceEndpoint(raw, KindSidecar, true)
		}
	})
}
