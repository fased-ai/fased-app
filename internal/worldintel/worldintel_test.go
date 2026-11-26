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

	// Verify Kalshi 1-cent probability parsing, sidecar ms scaling, InferLocation word boundaries, and sync_complete event.
	kalshiOneCent, err := ParseKalshiJSON([]byte(`{"markets":[{"ticker":"KX-1C","title":"Tail risk","yes_bid":1,"yes_ask":1,"last_price":1}]}`), 1760000000)
	if err != nil || len(kalshiOneCent) != 1 || kalshiOneCent[0].YesProb != 0.01 {
		t.Fatalf("expected Kalshi 1c -> 0.01 prob, got %+v err=%v", kalshiOneCent, err)
	}
	sidecarMs, err := ParseWorldMonitorSidecarJSON([]byte(`{"events":[{"id":"ms1","title":"Indiana factory update and aspirant policy","publishedAt":1759990000000}]}`), DefaultSources()[6], 1760000000)
	if err != nil || len(sidecarMs) != 1 || sidecarMs[0].PublishedAt != 1759990000 {
		t.Fatalf("expected sidecar ms scaling to 1759990000, got %+v err=%v", sidecarMs, err)
	}
	if reg, _, _, _ := InferLocation("Indiana per sec", RegionNA); reg != RegionNA {
		t.Fatalf("InferLocation false substring match on Indiana/sec -> %q, want %q", reg, RegionNA)
	}
	if reg, _, _, _ := InferLocation("SEC announces enforcement action", RegionGlobal); reg != RegionNA {
		t.Fatalf("InferLocation missed SEC word match -> %q, want %q", reg, RegionNA)
	}

	syncCh, unsubAll := liveSvc.Subscribe(StreamFilter{})
	defer unsubAll()
	if _, err := liveSvc.Sync(context.Background(), "gdelt-global"); err != nil {
		t.Fatalf("Sync #2: %v", err)
	}
	var seenComplete bool
	timeout := time.After(time.Second)
	for !seenComplete {
		select {
		case evt := <-syncCh:
			if evt.Type == "sync_complete" {
				seenComplete = true
			}
		case <-timeout:
			t.Fatal("timed out waiting for sync_complete bus event")
		}
	}
}

func TestWorldBankConflictAndSECForm4ParsersAndFreshnessSLO(t *testing.T) {
	t.Parallel()
	now := int64(1760000000)

	wbRaw := []byte(`[{"page":1},[{"indicator":{"id":"FP.CPI.TOTL.ZG","value":"Inflation CPI"},"country":{"id":"US","value":"United States"},"countryiso3code":"USA","date":"2026","value":2.75}]]`)
	wbSrc := DefaultSources()[7]
	wbEvents, err := ParseWorldBankJSON(wbRaw, wbSrc, now)
	if err != nil || len(wbEvents) != 1 || wbEvents[0].Category != CategoryMacro || !wbEvents[0].Untrusted {
		t.Fatalf("ParseWorldBankJSON failed: %+v err=%v", wbEvents, err)
	}

	ucdpRaw := []byte(`{"Result":[{"id":1,"country":"Ukraine","region":"Europe","side_a":"SideA","side_b":"SideB","date_start":"2026-11-20","best":12,"latitude":48.5,"longitude":37.5,"where_description":"Donetsk"}]}`)
	acledRaw := []byte(`{"data":[{"event_id_cnty":"SYR1","event_date":"2026-11-21","event_type":"Armed clash","country":"Syria","region":"Middle East","location":"Aleppo","notes":"Shelling near highway","fatalities":"3","latitude":"36.2","longitude":"37.16"}]}`)
	confSrc := DefaultSources()[8]
	ucdpEvents, err := ParseConflictJSON(ucdpRaw, confSrc, now)
	if err != nil || len(ucdpEvents) != 1 || ucdpEvents[0].Urgency < 0.85 {
		t.Fatalf("ParseConflictJSON(UCDP) failed: %+v err=%v", ucdpEvents, err)
	}
	acledEvents, err := ParseConflictJSON(acledRaw, confSrc, now)
	if err != nil || len(acledEvents) != 1 || acledEvents[0].Category != CategoryConflict {
		t.Fatalf("ParseConflictJSON(ACLED) failed: %+v err=%v", acledEvents, err)
	}

	form4Raw := []byte(`<?xml version="1.0"?><ownershipDocument><issuer><issuerName>Coinbase Global</issuerName><issuerTradingSymbol>COIN</issuerTradingSymbol></issuer><reportingOwner><reportingOwnerId><rptOwnerName>Armstrong Brian</rptOwnerName></reportingOwnerId></reportingOwner><nonDerivativeTable><nonDerivativeTransaction><transactionDate><value>2026-11-20</value></transactionDate><transactionCoding><transactionCode>S</transactionCode></transactionCoding><transactionAmounts><transactionShares><value>2500</value></transactionShares><transactionPricePerShare><value>310</value></transactionPricePerShare></transactionAmounts></nonDerivativeTransaction></nonDerivativeTable></ownershipDocument>`)
	f4Src := DefaultSources()[9]
	f4Events, err := ParseSECForm4Feed(form4Raw, f4Src, now)
	if err != nil || len(f4Events) != 1 || len(f4Events[0].Tickers) == 0 || f4Events[0].Tickers[0] != "COIN" {
		t.Fatalf("ParseSECForm4Feed failed: %+v err=%v", f4Events, err)
	}

	cur := time.Unix(now, 0)
	svc, err := NewService(Config{DataDir: t.TempDir(), FixtureMode: true, Now: func() time.Time { return cur }})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer svc.Close()

	if _, err := svc.Sync(context.Background(), "worldbank-gdp"); err != nil {
		t.Fatalf("Sync worldbank-gdp: %v", err)
	}
	list1, err := svc.ListSources(context.Background())
	if err != nil {
		t.Fatalf("ListSources: %v", err)
	}
	var gotFresh, gotDisabled bool
	for _, item := range list1 {
		if item.ID == "worldbank-gdp" && item.FreshnessStatus == "fresh" {
			gotFresh = true
		}
		if item.ID == "sec-form4-insiders" && item.FreshnessStatus == "disabled" {
			gotDisabled = true
		}
	}
	if !gotFresh || !gotDisabled {
		t.Fatalf("expected fresh worldbank-gdp and disabled sec-form4-insiders, got %+v", list1)
	}
	cur = cur.Add(2 * time.Hour)
	list2, _ := svc.ListSources(context.Background())
	for _, item := range list2 {
		if item.ID == "worldbank-gdp" && item.FreshnessStatus != "stale" {
			t.Fatalf("expected stale SLO after 2h, got %q", item.FreshnessStatus)
		}
	}
}
