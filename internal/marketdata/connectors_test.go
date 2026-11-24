package marketdata

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestSSRFAndUpstreamSecurityGuards(t *testing.T) {
	blockedIPs := []string{
		"127.0.0.1",
		"10.0.0.1",
		"172.16.0.10",
		"192.168.1.5",
		"100.64.0.1",
		"100.118.152.85", // Tailscale CGNAT must be rejected.
		"169.254.169.254",
		"::1",
		"fe80::1",
	}
	for _, raw := range blockedIPs {
		if ip := net.ParseIP(raw); isPublicUnicastIP(ip) {
			t.Fatalf("expected non-public IP %s to be rejected", raw)
		}
	}
	if !isPublicUnicastIP(net.ParseIP("1.1.1.1")) {
		t.Fatal("expected 1.1.1.1 to be accepted")
	}

	if _, err := validateUpstreamURL("http://api.binance.com/api/v3/klines"); err == nil {
		t.Fatal("expected non-HTTPS scheme rejection")
	}
	if _, err := validateUpstreamURL("https://evil.example.com/steal"); err == nil {
		t.Fatal("expected non-allowlisted host rejection")
	}
	if _, err := validateUpstreamURL("https://api.hyperliquid.xyz/info"); err != nil {
		t.Fatalf("unexpected allowlist error: %v", err)
	}

	// Verify redirects are blocked and SEC User-Agent is set.
	c := newUpstreamClient(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() == "data.sec.gov" {
			if ua := r.Header.Get("User-Agent"); ua != secEDGARUserAgent {
				t.Fatalf("expected SEC user-agent %q, got %q", secEDGARUserAgent, ua)
			}
			return jsonResponse(302, ""), nil
		}
		return jsonResponse(200, string(bytes.Repeat([]byte("a"), maxUpstreamBodyBytes+50))), nil
	}))
	if _, err := c.get(context.Background(), "https://data.sec.gov/submissions/CIK0000320193.json"); err == nil {
		t.Fatal("expected HTTP 302 redirect rejection")
	}
	if _, err := c.get(context.Background(), "https://api.binance.com/api/v3/klines"); err == nil {
		t.Fatal("expected >2 MiB body cap rejection")
	}
}

func TestConnectorsAndMockedLiveFetch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mockTransport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Hostname() {
		case "api.hyperliquid.xyz":
			raw, _ := io.ReadAll(r.Body)
			if strings.Contains(string(raw), "l2Book") {
				return jsonResponse(200, `{"time":1760000000000,"levels":[[{"px":"94000","sz":"1.2"}],[{"px":"94010","sz":"0.8"}]]}`), nil
			}
			if strings.Contains(string(raw), "recentTrades") {
				return jsonResponse(200, `[{"px":"94005","sz":"0.25","side":"B","time":1760000000100,"tid":11}]`), nil
			}
			return jsonResponse(200, `[{"t":1760000000000,"o":"94000","h":"94100","l":"93900","c":"94050","v":"12.5","n":40}]`), nil
		case "stats-data.hyperliquid.xyz":
			return jsonResponse(200, `{"leaderboardRows":[{"ethAddress":"0xabc","displayName":"top1","accountValue":"500000","windowPerformances":[["day",{"roi":"-0.035"}],["week",{"roi":"0.12"}]]}]}`), nil
		case "data-api.binance.vision", "api.binance.com":
			return jsonResponse(200, `[[1760000000000,"94000","94200","93950","94150","25.4",1760003599999,"0",100,"14.0"]]`), nil
		case "api.bybit.com":
			return jsonResponse(200, `{"retCode":0,"result":{"list":[["1760000000000","94010","94150","93980","94100","18.2"]]}}`), nil
		case "api.kraken.com":
			return jsonResponse(200, `{"error":[],"result":{"XXBTZUSD":[[1760000000,"94000","94100","93900","94020","94010","5.1",19]]}}`), nil
		case "api.dexscreener.com":
			return jsonResponse(200, `{"pairs":[{"chainId":"solana","pairAddress":"PoolAddr12345678901234567890123456"}]}`), nil
		case "api.geckoterminal.com":
			return jsonResponse(200, `{"data":{"attributes":{"ohlcv_list":[[1760000000,210.0,215.0,209.0,214.0,1500.0]]}}}`), nil
		case "fred.stlouisfed.org":
			return jsonResponse(200, "DATE,CPIAUCSL\n2026-09-01,.\n2026-10-01,314.25\n2026-11-01,-0.15\n2026-11-02,NaN\n2026-11-03,Inf\n"), nil
		case "data.sec.gov":
			return jsonResponse(200, `{"cik":"320193","name":"Apple Inc.","filings":{"recent":{"accessionNumber":["0000320193-26-000100"],"filingDate":["2026-11-01"],"form":["10-K"],"primaryDocument":["aapl.htm"],"primaryDocDescription":["Annual"]}}}`), nil
		default:
			return nil, errors.New("unexpected upstream host: " + r.URL.Hostname())
		}
	})

	svc, err := NewService(Config{
		DataDir:       t.TempDir(),
		FixtureMode:   false,
		Now:           func() time.Time { return time.Unix(1760003600, 0).UTC() },
		HTTPTransport: mockTransport,
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	defer func() { _ = svc.Close() }()

	for _, venue := range []string{VenueHyperliquid, VenueBinance, VenueBybit, VenueKraken, VenueSolana} {
		sym := "BTC"
		if venue == VenueSolana {
			sym = "SOL"
		}
		res, err := svc.GetCandles(ctx, venue, sym, "1h", TransformOptions{Style: StyleStandard}, 10)
		if err != nil || len(res.Candles) == 0 {
			t.Fatalf("GetCandles(%s) failed: len=%d err=%v", venue, len(res.Candles), err)
		}
	}

	book, err := svc.GetOrderBook(ctx, VenueHyperliquid, "BTC", 10)
	if err != nil || book.Spread != 10 {
		t.Fatalf("GetOrderBook failed: %+v err=%v", book, err)
	}
	trades, err := svc.GetTrades(ctx, VenueHyperliquid, "BTC", 10)
	if err != nil || len(trades) != 1 || trades[0].Side != "buy" {
		t.Fatalf("GetTrades failed: %+v err=%v", trades, err)
	}
	cohorts, err := svc.GetCohorts(ctx, "week")
	if err != nil || len(cohorts.Entries) != 1 || cohorts.Entries[0].WeekROI != 0.12 || cohorts.Entries[0].DayROI != -0.035 {
		t.Fatalf("GetCohorts failed: %+v err=%v", cohorts, err)
	}
	macro, err := svc.GetMacroSeries(ctx, "CPIAUCSL", 10)
	if err != nil || len(macro) != 2 || macro[1].Value != -0.15 {
		t.Fatalf("GetMacroSeries failed: %+v err=%v", macro, err)
	}
	filings, err := svc.GetSECFilings(ctx, "320193", 10)
	if err != nil || len(filings) != 1 || filings[0].Form != "10-K" {
		t.Fatalf("GetSECFilings failed: %+v err=%v", filings, err)
	}
}

func TestFixtureModeZeroNetworkRequests(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var networkCalls atomic.Int32
	svc, err := NewService(Config{
		DataDir:     t.TempDir(),
		FixtureMode: true,
		Now:         func() time.Time { return time.Unix(1760003600, 0).UTC() },
		HTTPTransport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			networkCalls.Add(1)
			return nil, errors.New("network forbidden in FixtureMode")
		}),
	})
	if err != nil {
		t.Fatalf("NewService(FixtureMode) failed: %v", err)
	}
	defer func() { _ = svc.Close() }()

	if inst, err := svc.ListInstruments(ctx, ""); err != nil || len(inst) == 0 {
		t.Fatalf("ListInstruments failed: %v", err)
	}
	if cs, err := svc.GetCandles(ctx, VenueHyperliquid, "BTC", "1h", TransformOptions{Style: StyleRenko}, 30); err != nil || !cs.Fixture || len(cs.Candles) == 0 {
		t.Fatalf("Fixture GetCandles failed: %+v err=%v", cs, err)
	}
	if _, err := svc.GetFootprint(ctx, VenueHyperliquid, "BTC", "1h", 0, 12); err != nil {
		t.Fatalf("Fixture GetFootprint failed: %v", err)
	}
	if _, err := svc.GetOrderBook(ctx, VenueHyperliquid, "BTC", 10); err != nil {
		t.Fatalf("Fixture GetOrderBook failed: %v", err)
	}
	if _, err := svc.GetMacroSeries(ctx, "CPIAUCSL", 10); err != nil {
		t.Fatalf("Fixture GetMacroSeries failed: %v", err)
	}
	if _, err := svc.GetSECFilings(ctx, "320193", 10); err != nil {
		t.Fatalf("Fixture GetSECFilings failed: %v", err)
	}
	if _, err := svc.GetCohorts(ctx, "week"); err != nil {
		t.Fatalf("Fixture GetCohorts failed: %v", err)
	}
	if n := networkCalls.Load(); n != 0 {
		t.Fatalf("FixtureMode issued %d HTTP requests, expected 0", n)
	}
}

func TestBinanceUsesPublicVisionMirrorAndFallsBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Verify GetCandles, GetOrderBook, and GetTrades hit data-api.binance.vision first.
	var hosts []string
	var comCalls atomic.Int32
	primaryTransport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hosts = append(hosts, r.URL.Hostname())
		if r.URL.Hostname() == "api.binance.com" {
			comCalls.Add(1)
			return jsonResponse(451, "restricted location"), nil
		}
		if r.URL.Hostname() != "data-api.binance.vision" {
			return nil, errors.New("unexpected host: " + r.URL.Hostname())
		}
		switch r.URL.Path {
		case "/api/v3/klines":
			return jsonResponse(200, `[[1760000000000,"94000","94200","93950","94150","25.4",1760003599999,"0",100,"14.0"]]`), nil
		case "/api/v3/depth":
			return jsonResponse(200, `{"bids":[["94000","1.5"]],"asks":[["94010","1.0"]]}`), nil
		case "/api/v3/trades":
			return jsonResponse(200, `[{"id":7,"price":"94005","qty":"0.25","time":1760000000100,"isBuyerMaker":false}]`), nil
		default:
			return jsonResponse(404, "not found"), nil
		}
	})

	svc, err := NewService(Config{
		DataDir:       t.TempDir(),
		FixtureMode:   false,
		Now:           func() time.Time { return time.Unix(1760003600, 0).UTC() },
		HTTPTransport: primaryTransport,
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	defer func() { _ = svc.Close() }()

	if cs, err := svc.GetCandles(ctx, VenueBinance, "BTCUSDT", "1h", TransformOptions{Style: StyleStandard}, 10); err != nil || len(cs.Candles) == 0 {
		t.Fatalf("GetCandles failed: len=%d err=%v", len(cs.Candles), err)
	}
	if book, err := svc.GetOrderBook(ctx, VenueBinance, "BTCUSDT", 10); err != nil || book.Spread != 10 {
		t.Fatalf("GetOrderBook failed: %+v err=%v", book, err)
	}
	if trades, err := svc.GetTrades(ctx, VenueBinance, "BTCUSDT", 10); err != nil || len(trades) != 1 || trades[0].Price != 94005 {
		t.Fatalf("GetTrades failed: %+v err=%v", trades, err)
	}
	if comCalls.Load() != 0 {
		t.Fatalf("expected 0 api.binance.com calls when data-api.binance.vision succeeds, got %d", comCalls.Load())
	}
	if len(hosts) < 3 {
		t.Fatalf("expected >=3 vision requests, got %v", hosts)
	}

	// 2. Verify graceful fallback to api.binance.com when data-api.binance.vision returns HTTP 503.
	var fallbackHosts []string
	fallbackTransport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		fallbackHosts = append(fallbackHosts, r.URL.Hostname())
		if r.URL.Hostname() == "data-api.binance.vision" {
			return jsonResponse(503, "temporary unavailable"), nil
		}
		if r.URL.Hostname() == "api.binance.com" && r.URL.Path == "/api/v3/klines" {
			return jsonResponse(200, `[[1760000000000,"94000","94200","93950","94150","25.4",1760003599999,"0",100,"14.0"]]`), nil
		}
		return jsonResponse(404, "not found"), nil
	})
	fallbackSvc, err := NewService(Config{
		DataDir:       t.TempDir(),
		FixtureMode:   false,
		Now:           func() time.Time { return time.Unix(1760003600, 0).UTC() },
		HTTPTransport: fallbackTransport,
	})
	if err != nil {
		t.Fatalf("NewService(fallback) failed: %v", err)
	}
	defer func() { _ = fallbackSvc.Close() }()

	if cs, err := fallbackSvc.GetCandles(ctx, VenueBinance, "BTCUSDT", "1h", TransformOptions{Style: StyleStandard}, 10); err != nil || len(cs.Candles) == 0 {
		t.Fatalf("fallback GetCandles failed: len=%d err=%v", len(cs.Candles), err)
	}
	if len(fallbackHosts) != 2 || fallbackHosts[0] != "data-api.binance.vision" || fallbackHosts[1] != "api.binance.com" {
		t.Fatalf("expected [data-api.binance.vision api.binance.com], got %v", fallbackHosts)
	}
}
