package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func wsURLFromHTTP(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func startMockWSServer(t *testing.T, handler func(ctx context.Context, conn *websocket.Conn)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		handler(r.Context(), conn)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFeedHubMockExchangeWebSocketsAndLazyLifecycle(t *testing.T) {
	nowMs := int64(1760003600000)

	var hlSubs []string
	var hlMu sync.Mutex
	hlSrv := startMockWSServer(t, func(ctx context.Context, conn *websocket.Conn) {
		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var msg struct {
				Method       string `json:"method"`
				Subscription struct {
					Type string `json:"type"`
					Coin string `json:"coin"`
				} `json:"subscription"`
			}
			if json.Unmarshal(raw, &msg) != nil || msg.Method != "subscribe" {
				continue
			}
			hlMu.Lock()
			hlSubs = append(hlSubs, msg.Subscription.Type+":"+msg.Subscription.Coin)
			hlMu.Unlock()
			if msg.Subscription.Type == "trades" {
				payload, _ := json.Marshal(map[string]any{
					"channel": "trades",
					"data": []map[string]any{
						{"coin": msg.Subscription.Coin, "side": "B", "px": "94525.5", "sz": "0.5", "time": nowMs + 100, "tid": 901},
						{"coin": msg.Subscription.Coin, "side": "A", "px": "94530.0", "sz": "0.2", "time": nowMs + 200, "tid": 902},
					},
				})
				_ = conn.Write(ctx, websocket.MessageText, payload)
			} else if msg.Subscription.Type == "l2Book" {
				payload, _ := json.Marshal(map[string]any{
					"channel": "l2Book",
					"data": map[string]any{
						"coin": msg.Subscription.Coin,
						"time": nowMs + 150,
						"levels": [][]map[string]string{
							{{"px": "94520", "sz": "2.0"}},
							{{"px": "94535", "sz": "1.5"}},
						},
					},
				})
				_ = conn.Write(ctx, websocket.MessageText, payload)
			}
		}
	})

	binSrv := startMockWSServer(t, func(ctx context.Context, conn *websocket.Conn) {
		_, _, err := conn.Read(ctx)
		if err != nil {
			return
		}
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"stream":"btcusdt@trade","data":{"e":"trade","s":"BTCUSDT","t":71,"p":"94510.25","q":"0.4","T":1760003600100,"m":false}}`))
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"stream":"btcusdt@depth20@100ms","data":{"bids":[["94500","1.0"]],"asks":[["94515","2.0"]]}}`))
		<-ctx.Done()
	})

	bybitSrv := startMockWSServer(t, func(ctx context.Context, conn *websocket.Conn) {
		_, _, err := conn.Read(ctx)
		if err != nil {
			return
		}
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"topic":"orderbook.50.BTCUSDT","type":"snapshot","ts":1760003600100,"data":{"s":"BTCUSDT","b":[["94490","3.0"],["94480","1.0"]],"a":[["94510","2.0"],["94520","4.0"]]}}`))
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"topic":"orderbook.50.BTCUSDT","type":"delta","ts":1760003600200,"data":{"s":"BTCUSDT","b":[["94490","0"]],"a":[["94525","1.5"]]}}`))
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"topic":"publicTrade.BTCUSDT","type":"snapshot","data":[{"i":"by-1","T":1760003600300,"p":"94505","v":"0.75","S":"Buy"}]}`))
		<-ctx.Done()
	})

	krakenSrv := startMockWSServer(t, func(ctx context.Context, conn *websocket.Conn) {
		for i := 0; i < 2; i++ {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"channel":"book","type":"snapshot","data":[{"symbol":"BTC/USD","bids":[{"price":94400,"qty":1.2},{"price":94390,"qty":2.0}],"asks":[{"price":94410,"qty":1.5}],"timestamp":"2025-10-09T12:00:00Z"}]}`))
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"channel":"book","type":"update","data":[{"symbol":"BTC/USD","bids":[{"price":94400,"qty":0}],"asks":[{"price":94415,"qty":0.8}],"timestamp":"2025-10-09T12:00:01Z"}]}`))
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"channel":"trade","type":"update","data":[{"symbol":"BTC/USD","side":"buy","qty":0.25,"price":94405,"trade_id":88,"timestamp":"2025-10-09T12:00:02Z"}]}`))
		<-ctx.Done()
	})

	// Seed HTTP responses for candle backfill test while WS provides live ticks.
	mockHTTP := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Hostname() {
		case "api.hyperliquid.xyz":
			return jsonResponse(200, `[{"t":1760003600000,"o":"94500","h":"94520","l":"94490","c":"94510","v":"0.6","n":3}]`), nil
		default:
			return jsonResponse(404, "not found"), nil
		}
	})

	svc, err := NewService(Config{
		DataDir:       t.TempDir(),
		FixtureMode:   false,
		Now:           func() time.Time { return time.UnixMilli(nowMs).UTC() },
		HTTPTransport: mockHTTP,
		WSEndpoints: map[string]string{
			VenueHyperliquid: wsURLFromHTTP(hlSrv),
			VenueBinance:     wsURLFromHTTP(binSrv),
			VenueBybit:       wsURLFromHTTP(bybitSrv),
			VenueKraken:      wsURLFromHTTP(krakenSrv),
		},
		FeedIdleGrace: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	defer func() { _ = svc.Close() }()

	// Must remain 100% lazy at cold start (0 active WS workers).
	if n := svc.ActiveFeedConnections(); n != 0 {
		t.Fatalf("expected 0 active WS connections before AcquireFeed, got %d", n)
	}

	busCh, cancelBus := svc.Subscribe(Filter{Type: "trade"})
	defer cancelBus()

	relHLBTC := svc.AcquireFeed(VenueHyperliquid, "BTC")
	relHLETH := svc.AcquireFeed(VenueHyperliquid, "ETH")
	relBIN := svc.AcquireFeed(VenueBinance, "BTCUSDT")
	relBYB := svc.AcquireFeed(VenueBybit, "BTCUSDT")
	relKRK := svc.AcquireFeed(VenueKraken, "XBTUSD")

	if n := svc.ActiveFeedConnections(); n != 4 {
		t.Fatalf("expected 4 venue WS connections (BTC+ETH shared on hyperliquid), got %d", n)
	}

	// Wait until all 4 venues report trades into FeedCache.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		tHL, _, okHL := svc.cache.RecentTrades(VenueHyperliquid, "BTC", 10)
		tBIN, _, okBIN := svc.cache.RecentTrades(VenueBinance, "BTCUSDT", 10)
		tBYB, _, okBYB := svc.cache.RecentTrades(VenueBybit, "BTCUSDT", 10)
		tKRK, _, okKRK := svc.cache.RecentTrades(VenueKraken, "XBTUSD", 10)
		bBYB, _, hasBookBYB := svc.cache.GetOrderBook(VenueBybit, "BTCUSDT", 10)
		bKRK, _, hasBookKRK := svc.cache.GetOrderBook(VenueKraken, "XBTUSD", 10)
		if okHL && okBIN && okBYB && okKRK && len(tHL) >= 2 && len(tBIN) >= 1 && len(tBYB) >= 1 && len(tKRK) >= 1 && hasBookBYB && len(bBYB.Bids) == 1 && len(bBYB.Asks) == 3 && hasBookKRK && len(bKRK.Bids) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Verify Bus received trade event.
	select {
	case <-busCh:
	case <-time.After(time.Second):
		t.Fatal("expected trade event published on Bus from WS adapter")
	}

	// Verify Bybit delta deleted bid 94490 cleanly (leaving 94480).
	bybitBook, err := svc.GetOrderBook(context.Background(), VenueBybit, "BTCUSDT", 10)
	if err != nil || len(bybitBook.Bids) != 1 || bybitBook.Bids[0].Price != 94480 {
		t.Fatalf("expected Bybit L2 delta deletion to leave bid 94480, got %+v err=%v", bybitBook, err)
	}

	// Verify Kraken delta deleted bid 94400 cleanly (leaving 94390).
	krkBook, _, okKrk := svc.cache.GetOrderBook(VenueKraken, "XBTUSD", 10)
	if !okKrk || len(krkBook.Bids) != 1 || krkBook.Bids[0].Price != 94390 || len(krkBook.Asks) != 2 {
		t.Fatalf("expected Kraken L2 delta deletion + add, got %+v", krkBook)
	}

	// Verify Hyperliquid live WS trades enrich GetFootprint as SourceKind="ticks".
	fp, err := svc.GetFootprint(context.Background(), VenueHyperliquid, "BTC", "1m", 0, 5)
	if err != nil || len(fp.Bars) == 0 {
		t.Fatalf("GetFootprint failed: %+v err=%v", fp, err)
	}
	lastFP := fp.Bars[len(fp.Bars)-1]
	if lastFP.SourceKind != "ticks" || lastFP.Delta <= 0 {
		t.Fatalf("expected live WS ticks footprint with SourceKind=ticks, got %+v", lastFP)
	}

	// Release all feeds and verify idle wind-down closes all workers cleanly.
	relHLBTC()
	relHLETH()
	relBIN()
	relBYB()
	relKRK()

	stopDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(stopDeadline) && svc.ActiveFeedConnections() > 0 {
		time.Sleep(15 * time.Millisecond)
	}
	if n := svc.ActiveFeedConnections(); n != 0 {
		t.Fatalf("expected 0 active WS connections after idle grace, got %d", n)
	}

	// FixtureMode must make zero WS dials even if AcquireFeed is invoked.
	var fixDials atomic.Int32
	fixWSSrv := startMockWSServer(t, func(ctx context.Context, conn *websocket.Conn) {
		fixDials.Add(1)
	})
	fixSvc, err := NewService(Config{
		DataDir:     t.TempDir(),
		FixtureMode: true,
		WSEndpoints: map[string]string{VenueHyperliquid: wsURLFromHTTP(fixWSSrv)},
	})
	if err != nil {
		t.Fatalf("NewService(FixtureMode) failed: %v", err)
	}
	defer func() { _ = fixSvc.Close() }()
	relFix := fixSvc.AcquireFeed(VenueHyperliquid, "BTC")
	defer relFix()
	time.Sleep(30 * time.Millisecond)
	if fixDials.Load() != 0 || fixSvc.ActiveFeedConnections() != 0 {
		t.Fatalf("expected zero WS dials in FixtureMode, got dials=%d conns=%d", fixDials.Load(), fixSvc.ActiveFeedConnections())
	}
}

func TestSingleFlightCoalescingAndSolanaPoolCache(t *testing.T) {
	nowSec := int64(1760003600)
	var mu sync.Mutex
	curTime := time.Unix(nowSec, 0).UTC()
	nowFn := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return curTime
	}

	var hlCandleCalls atomic.Int32
	var dexCalls atomic.Int32
	var geckoCalls atomic.Int32

	barrier := make(chan struct{})
	svc, err := NewService(Config{
		DataDir:     t.TempDir(),
		FixtureMode: false,
		Now:         nowFn,
		HTTPTransport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Hostname() {
			case "api.hyperliquid.xyz":
				if hlCandleCalls.Add(1) == 1 {
					<-barrier
				}
				return jsonResponse(200, `[{"t":1760000000000,"o":"94000","h":"94100","l":"93900","c":"94050","v":"12.5","n":40}]`), nil
			case "api.dexscreener.com":
				dexCalls.Add(1)
				return jsonResponse(200, `{"pairs":[{"chainId":"solana","pairAddress":"PoolAddr12345678901234567890123456"}]}`), nil
			case "api.geckoterminal.com":
				geckoCalls.Add(1)
				return jsonResponse(200, `{"data":{"attributes":{"ohlcv_list":[[1760000000,210.0,215.0,209.0,214.0,1500.0]]}}}`), nil
			default:
				return nil, errors.New("unexpected host: " + r.URL.Hostname())
			}
		}),
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	defer func() { _ = svc.Close() }()

	// 10 concurrent identical candle fetches must coalesce into 1 upstream request.
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cs, err := svc.GetCandles(context.Background(), VenueHyperliquid, "BTC", "1h", TransformOptions{Style: StyleStandard}, 50)
			if err != nil {
				errs <- err
				return
			}
			if len(cs.Candles) == 0 {
				errs <- errors.New("empty candles")
			}
		}()
	}
	time.Sleep(25 * time.Millisecond)
	close(barrier)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("concurrent GetCandles failed: %v", e)
	}
	if n := hlCandleCalls.Load(); n != 1 {
		t.Fatalf("expected 1 coalesced Hyperliquid candle request across 10 callers, got %d", n)
	}

	// 5 sequential Solana candle calls across advancing 3s steps (past 2s candle TTL)
	// must hit DexScreener only once thanks to 15m Solana pool cache.
	mint := "So11111111111111111111111111111111111111112"
	for i := 0; i < 5; i++ {
		if _, err := svc.GetCandles(context.Background(), VenueSolana, mint, "1h", TransformOptions{Style: StyleStandard}, 20); err != nil {
			t.Fatalf("Solana GetCandles step %d failed: %v", i, err)
		}
		mu.Lock()
		curTime = curTime.Add(3 * time.Second)
		mu.Unlock()
	}
	if d := dexCalls.Load(); d != 1 {
		t.Fatalf("expected 1 DexScreener lookup across 5 sequential Solana requests, got %d", d)
	}
	if g := geckoCalls.Load(); g != 5 {
		t.Fatalf("expected 5 GeckoTerminal OHLCV fetches after candle TTL expiry, got %d", g)
	}
}

func TestLiveTickAndVolumeStylesAndCohortWindowSorting(t *testing.T) {
	nowSec := int64(1760003600)
	svc, err := NewService(Config{
		DataDir:     t.TempDir(),
		FixtureMode: false,
		Now:         func() time.Time { return time.Unix(nowSec, 0).UTC() },
		HTTPTransport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Hostname() {
			case "data-api.binance.vision":
				if r.URL.Path == "/api/v3/klines" {
					return jsonResponse(200, `[[1760000000000,"100","105","99","104","50",1760003599999,"0",20,"30"]]`), nil
				}
				if r.URL.Path == "/api/v3/trades" {
					return jsonResponse(200, `[
						{"id":1,"price":"100.0","qty":"4.0","time":1760000001000,"isBuyerMaker":false},
						{"id":2,"price":"101.0","qty":"6.0","time":1760000002000,"isBuyerMaker":false},
						{"id":3,"price":"102.0","qty":"5.0","time":1760000003000,"isBuyerMaker":true},
						{"id":4,"price":"103.0","qty":"5.0","time":1760000004000,"isBuyerMaker":false}
					]`), nil
				}
			case "stats-data.hyperliquid.xyz":
				return jsonResponse(200, `{"leaderboardRows":[
					{"ethAddress":"0x1111111111111111111111111111111111111111","displayName":"week-king","accountValue":"100000","windowPerformances":[["day",{"roi":"0.01"}],["week",{"roi":"0.55"}],["month",{"roi":"0.20"}]]},
					{"ethAddress":"0x2222222222222222222222222222222222222222","displayName":"day-sprinter","accountValue":"100000","windowPerformances":[["day",{"roi":"0.35"}],["week",{"roi":"0.10"}],["month",{"roi":"0.05"}]]}
				]}`), nil
			}
			return jsonResponse(404, "not found"), nil
		}),
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	defer func() { _ = svc.Close() }()

	tickSeries, err := svc.GetCandles(context.Background(), VenueBinance, "BTCUSDT", "1m", TransformOptions{Style: StyleTick, TickCount: 2}, 10)
	if err != nil {
		t.Fatalf("live StyleTick failed: %v", err)
	}
	if len(tickSeries.Candles) != 2 || tickSeries.Candles[0].Trades != 2 || tickSeries.Candles[1].Close != 103.0 {
		t.Fatalf("expected 2 tick bars from live REST trades, got %+v", tickSeries.Candles)
	}

	volSeries, err := svc.GetCandles(context.Background(), VenueBinance, "BTCUSDT", "1m", TransformOptions{Style: StyleVolume, VolumeThreshold: 10.0}, 10)
	if err != nil {
		t.Fatalf("live StyleVolume failed: %v", err)
	}
	if len(volSeries.Candles) != 2 || volSeries.Candles[0].Volume != 10.0 || volSeries.Candles[1].Volume != 10.0 {
		t.Fatalf("expected 2 volume bars (10.0 each) from live trades, got %+v", volSeries.Candles)
	}

	dayCohorts, err := svc.GetCohorts(context.Background(), "day")
	if err != nil || len(dayCohorts.Entries) != 2 || dayCohorts.Entries[0].DisplayName != "day-sprinter" || dayCohorts.Entries[0].Rank != 1 {
		t.Fatalf("expected day window sorted with day-sprinter #1, got %+v err=%v", dayCohorts, err)
	}
	weekCohorts, err := svc.GetCohorts(context.Background(), "week")
	if err != nil || len(weekCohorts.Entries) != 2 || weekCohorts.Entries[0].DisplayName != "week-king" || weekCohorts.Entries[0].Rank != 1 {
		t.Fatalf("expected week window sorted with week-king #1, got %+v err=%v", weekCohorts, err)
	}
	if _, err := svc.GetCohorts(context.Background(), "decade"); err == nil {
		t.Fatal("expected invalid cohort window rejection")
	}
}
