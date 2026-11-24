package worldintel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func httpResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}

func TestValidateSourceEndpointSSRF(t *testing.T) {
	t.Parallel()

	bad := []SourceInput{
		{ID: "bad-priv", Name: "Priv", Kind: KindRSS, Endpoint: "https://10.0.0.1/feed.xml", Category: CategoryMacro, Region: RegionNA},
		{ID: "bad-cgnat", Name: "CGNAT", Kind: KindRSS, Endpoint: "https://100.64.1.2/feed.xml", Category: CategoryMacro, Region: RegionNA},
		{ID: "bad-loop", Name: "Loop", Kind: KindRSS, Endpoint: "http://127.0.0.1:8080/feed.xml", Category: CategoryMacro, Region: RegionNA, AllowLocalhost: true},
		{ID: "bad-cred", Name: "Cred", Kind: KindRSS, Endpoint: "https://user:pass@example.com/feed.xml", Category: CategoryMacro, Region: RegionNA},
		{ID: "bad-http-sidecar", Name: "RemoteHTTP", Kind: KindSidecar, Endpoint: "http://example.com/api/events", Category: CategorySidecar, Region: RegionGlobal, AllowLocalhost: true},
		{ID: "bad-local-off", Name: "LocalOff", Kind: KindSidecar, Endpoint: "http://127.0.0.1:3000/api/events", Category: CategorySidecar, Region: RegionGlobal, AllowLocalhost: false},
	}
	for _, in := range bad {
		if _, err := ValidateSourceInput(in); err == nil {
			t.Fatalf("expected SSRF/validation error for %+v", in)
		}
	}

	good := []SourceInput{
		{ID: "good-rss", Name: "Good", Kind: KindRSS, Endpoint: "https://example.org/rss.xml?tag=macro", Category: CategoryMacro, Region: RegionEU},
		{ID: "good-sidecar", Name: "Sidecar", Kind: KindSidecar, Endpoint: "http://127.0.0.1:3000/api/events", Category: CategorySidecar, Region: RegionGlobal, AllowLocalhost: true},
	}
	for _, in := range good {
		if _, err := ValidateSourceInput(in); err != nil {
			t.Fatalf("unexpected error for %+v: %v", in, err)
		}
	}
}

func TestAllConnectorsAndParsers(t *testing.T) {
	t.Parallel()
	now := int64(1760000000)

	gdeltRaw := []byte(`{"articles":[{"url":"https://news.example.org/oil","title":"Urgent Brent crude surge near Strait of Hormuz","seendate":"20261124T010000Z","domain":"news.example.org","sourcecountry":"Oman"}]}`)
	gdeltEvents, err := ParseGDELTJSON(gdeltRaw, DefaultSources()[0], now)
	if err != nil || len(gdeltEvents) != 1 || gdeltEvents[0].Region != RegionMENA || !gdeltEvents[0].Untrusted {
		t.Fatalf("unexpected GDELT parse result: len=%d err=%v ev=%+v", len(gdeltEvents), err, gdeltEvents)
	}

	usgsRaw := []byte(`{"features":[{"id":"us7000abc","properties":{"mag":5.4,"place":"50 km E of Tokyo, Japan","title":"M 5.4 - Tokyo, Japan","time":1759999000000,"url":"https://earthquake.usgs.gov/earthquakes/eventpage/us7000abc"},"geometry":{"coordinates":[140.2,35.6,12.0]}}]}`)
	usgsEvents, err := ParseUSGSGeoJSON(usgsRaw, DefaultSources()[1], now)
	if err != nil || len(usgsEvents) != 1 || usgsEvents[0].Lat != 35.6 || usgsEvents[0].Category != CategorySeismic {
		t.Fatalf("unexpected USGS parse result: %+v err=%v", usgsEvents, err)
	}

	polyRaw := []byte(`[{"id":"p1","slug":"fed-cut-2027","question":"Will Fed cut rates 25bps in Washington?","outcomePrices":"[\"0.64\",\"0.36\"]","volume24hr":150000,"liquidity":"45000","oneDayPriceChange":0.05}]`)
	polyMkts, err := ParsePolymarketJSON(polyRaw, now)
	if err != nil || len(polyMkts) != 1 || polyMkts[0].YesProb != 0.64 || polyMkts[0].Venue != VenuePolymarket {
		t.Fatalf("unexpected Polymarket parse result: %+v err=%v", polyMkts, err)
	}

	kalshiRaw := []byte(`{"markets":[{"ticker":"KXFED-CUT","title":"Will Fed cut rates 25bps in Washington?","yes_bid":49,"yes_ask":51,"volume_24h":90000,"liquidity":20000}]}`)
	kalshiMkts, err := ParseKalshiJSON(kalshiRaw, now)
	if err != nil || len(kalshiMkts) != 1 || kalshiMkts[0].YesProb != 0.5 || kalshiMkts[0].Venue != VenueKalshi {
		t.Fatalf("unexpected Kalshi parse result: %+v err=%v", kalshiMkts, err)
	}

	arbs := FindPredictionArbitrage(append(polyMkts, kalshiMkts...))
	if len(arbs) != 1 || arbs[0].SpreadPct != 14 {
		t.Fatalf("expected 14%% arbitrage spread, got %+v", arbs)
	}

	rssRaw := []byte(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>Fed</title><item><title>Federal Reserve updates Bitcoin and Treasury collateral note</title><link>https://www.federalreserve.gov/press/2026.htm?src=rss</link><pubDate>Tue, 24 Nov 2026 01:00:00 GMT</pubDate></item></channel></rss>`)
	rssEvents, err := ParseRSSOrAtom(rssRaw, DefaultSources()[4], now)
	if err != nil || len(rssEvents) != 1 || len(rssEvents[0].Tickers) == 0 {
		t.Fatalf("unexpected RSS parse result: %+v err=%v", rssEvents, err)
	}

	// DTD/XXE directives must strictly fail.
	xxeRaw := []byte(`<?xml version="1.0"?><!DOCTYPE foo [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><rss version="2.0"><channel><item><title>&xxe;</title></item></channel></rss>`)
	if _, err := ParseRSSOrAtom(xxeRaw, DefaultSources()[4], now); err == nil {
		t.Fatalf("expected DTD/XXE XML rejection")
	}

	sidecarRaw := []byte(`{"events":[{"id":"sc1","title":"Beijing semiconductor tariff alert","summary":"NVDA impact","url":"https://example.com/sc1","region":"apac","lat":39.9,"lon":116.4,"publishedAt":1759990000,"tickers":["$SOL"]}]}`)
	sidecarEvents, err := ParseWorldMonitorSidecarJSON(sidecarRaw, DefaultSources()[6], now)
	if err != nil || len(sidecarEvents) != 1 || len(sidecarEvents[0].Tickers) != 2 {
		t.Fatalf("unexpected sidecar parse result: %+v err=%v", sidecarEvents, err)
	}
}

func TestServiceFixtureModeAndMockSyncAndBus(t *testing.T) {
	t.Parallel()
	fixedNow := time.Unix(1760000000, 0).UTC()

	// 1. FixtureMode: zero outbound HTTP calls, rich map + arbitrage + sources CRUD.
	called := false
	svc, err := NewService(Config{
		DataDir:     t.TempDir(),
		FixtureMode: true,
		Now:         func() time.Time { return fixedNow },
		HTTPTransport: rtFunc(func(r *http.Request) (*http.Response, error) {
			called = true
			return httpResp(500, "{}"), nil
		}),
	})
	if err != nil {
		t.Fatalf("NewService fixture: %v", err)
	}
	defer func() { _ = svc.Close() }()

	summary, err := svc.GetMapSummary(context.Background(), "", 0, 0)
	if err != nil || len(summary.Events) < 5 || len(summary.Clusters) == 0 {
		t.Fatalf("unexpected map summary: len=%d clusters=%d err=%v", len(summary.Events), len(summary.Clusters), err)
	}
	preds, err := svc.ListPredictions(context.Background(), PredictionFilter{})
	if err != nil || len(preds.Markets) < 3 || len(preds.Arbitrage) < 1 {
		t.Fatalf("unexpected predictions/arbitrage: %+v err=%v", preds, err)
	}
	if called {
		t.Fatalf("FixtureMode must not dial network")
	}

	// Custom source save -> delete -> 404; built-in delete refused.
	if _, err := svc.DeleteSource(context.Background(), "gdelt-global"); err == nil {
		t.Fatalf("expected error deleting built-in source")
	}
	_, err = svc.SaveSource(context.Background(), SourceInput{
		ID:       "custom-feed",
		Name:     "Custom Feed",
		Kind:     KindRSS,
		Endpoint: "https://feed.intel.invalid/rss.xml",
		Category: CategoryCrypto,
		Region:   RegionGlobal,
	})
	if err != nil {
		t.Fatalf("SaveSource custom: %v", err)
	}
	syncRes, err := svc.Sync(context.Background(), "custom-feed")
	if err != nil || syncRes.SyncedSources != 1 || syncRes.EventCount != 1 {
		t.Fatalf("Sync custom-feed unexpected: %+v err=%v", syncRes, err)
	}
	deleted, err := svc.DeleteSource(context.Background(), "custom-feed")
	if err != nil || !deleted {
		t.Fatalf("DeleteSource custom failed: deleted=%v err=%v", deleted, err)
	}
	if _, err := svc.Sync(context.Background(), "missing-source"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist for missing source sync, got %v", err)
	}

	// 2. Non-fixture mocked sync + Bus pub/sub.
	liveSvc, err := NewService(Config{
		DataDir:     t.TempDir(),
		FixtureMode: false,
		Now:         func() time.Time { return fixedNow },
		HTTPTransport: rtFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(r.URL.Host, "polymarket.com"):
				return httpResp(200, `[{"id":"p-live","slug":"btc-q4","question":"Will BTC break $200k?","outcomePrices":"[\"0.55\",\"0.45\"]","volume24hr":300000}]`), nil
			case strings.Contains(r.URL.Host, "gdeltproject.org"):
				return httpResp(200, `{"articles":[{"url":"https://example.com/btc-live","title":"Tokyo BTC ETF inflow milestone","seendate":"20261124T010000Z","domain":"example.com"}]}`), nil
			default:
				return httpResp(200, `{}`), nil
			}
		}),
	})
	if err != nil {
		t.Fatalf("NewService live mock: %v", err)
	}
	defer func() { _ = liveSvc.Close() }()

	ch, unsub := liveSvc.Subscribe(StreamFilter{Region: RegionAPAC})
	defer unsub()

	liveSync, err := liveSvc.Sync(context.Background(), "gdelt-global")
	if err != nil || liveSync.EventCount != 1 {
		t.Fatalf("live mock gdelt sync failed: %+v err=%v", liveSync, err)
	}
	select {
	case msg := <-ch:
		if msg.Event == nil || !msg.Event.Untrusted || msg.Event.Region != RegionAPAC {
			t.Fatalf("unexpected bus event: %+v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for filtered WorldIntel bus event")
	}
}
