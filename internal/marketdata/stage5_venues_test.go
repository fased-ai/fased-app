package marketdata

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type stage5RoundTripFunc func(*http.Request) (*http.Response, error)

func (fn stage5RoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func stage5JSONResponse(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Request:    req,
	}
}

func TestStage5VenuesFixtureData(t *testing.T) {
	t.Parallel()
	now := time.Unix(1795600000, 0)
	svc, err := NewService(Config{
		DataDir:     t.TempDir(),
		FixtureMode: true,
		Now:         func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	defer svc.Close()

	for _, tc := range []struct {
		venue  string
		symbol string
	}{
		{VenueOKX, "BTC"},
		{VenueCoinbase, "ETH"},
		{VenueDeribit, "BTC-PERPETUAL"},
		{VenueAlpaca, "SPY"},
	} {
		series, err := svc.GetCandles(context.Background(), tc.venue, tc.symbol, "1h", TransformOptions{Style: StyleStandard}, 12)
		if err != nil || len(series.Candles) != 12 {
			t.Fatalf("GetCandles(%s, %s) failed: len=%d err=%v", tc.venue, tc.symbol, len(series.Candles), err)
		}
		book, err := svc.GetOrderBook(context.Background(), tc.venue, tc.symbol, 5)
		if err != nil || len(book.Bids) == 0 || len(book.Asks) == 0 {
			t.Fatalf("GetOrderBook(%s, %s) failed: err=%v", tc.venue, tc.symbol, err)
		}
	}
}

func TestStage5PublicVenueParsersAndTransport(t *testing.T) {
	t.Setenv("APCA_API_KEY_ID", "PKTESTKEY123")
	t.Setenv("APCA_API_SECRET_KEY", "test-secret-xyz")

	now := time.Unix(1795600000, 0)
	rt := stage5RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		u := req.URL.String()
		switch {
		case strings.Contains(u, "www.okx.com/api/v5/market/candles"):
			return stage5JSONResponse(req, `{"code":"0","msg":"","data":[["1795596400000","61000","61200","60900","61100","120.5"],["1795600000000","61100","61500","61050","61400","95.2"]]}`), nil
		case strings.Contains(u, "www.okx.com/api/v5/market/books"):
			return stage5JSONResponse(req, `{"code":"0","msg":"","data":[{"bids":[["61390","1.5","0","1"]],"asks":[["61410","2.0","0","1"]],"ts":"1795600000000"}]}`), nil
		case strings.Contains(u, "www.okx.com/api/v5/market/trades"):
			return stage5JSONResponse(req, `{"code":"0","msg":"","data":[{"tradeId":"okx-1","px":"61400","sz":"0.4","side":"buy","ts":"1795600000000"}]}`), nil
		case strings.Contains(u, "api.exchange.coinbase.com/products/") && strings.Contains(u, "/candles"):
			// Coinbase tuple: [time, low, high, open, close, volume]
			return stage5JSONResponse(req, `[[1795600000,3100,3180,3120,3165,420.5],[1795596400,3080,3130,3090,3120,300.1]]`), nil
		case strings.Contains(u, "api.exchange.coinbase.com/products/") && strings.Contains(u, "/book"):
			return stage5JSONResponse(req, `{"bids":[["3164","5.2",2]],"asks":[["3166","4.1",1]]}`), nil
		case strings.Contains(u, "api.exchange.coinbase.com/products/") && strings.Contains(u, "/trades"):
			return stage5JSONResponse(req, `[{"trade_id":42,"time":"2026-11-25T12:00:00Z","price":"3165","size":"0.8","side":"sell"}]`), nil
		case strings.Contains(u, "www.deribit.com/api/v2/public/get_tradingview_chart_data"):
			return stage5JSONResponse(req, `{"result":{"status":"ok","ticks":[1795596400000,1795600000000],"open":[61000,61200],"high":[61300,61500],"low":[60900,61100],"close":[61200,61450],"volume":[12.3,18.4]}}`), nil
		case strings.Contains(u, "www.deribit.com/api/v2/public/get_order_book"):
			return stage5JSONResponse(req, `{"result":{"timestamp":1795600000000,"bids":[[61440,3.0]],"asks":[[61460,2.5]]}}`), nil
		case strings.Contains(u, "www.deribit.com/api/v2/public/get_last_trades_by_instrument"):
			return stage5JSONResponse(req, `{"result":{"trades":[{"trade_id":"db-1","timestamp":1795600000000,"price":61450,"amount":0.5,"direction":"buy"}]}}`), nil
		case strings.Contains(u, "data.alpaca.markets/v2/stocks/SPY/bars"):
			if req.Header.Get("APCA-API-KEY-ID") != "PKTESTKEY123" || req.Header.Get("APCA-API-SECRET-KEY") != "test-secret-xyz" {
				t.Fatalf("missing expected Alpaca headers: %v", req.Header)
			}
			return stage5JSONResponse(req, `{"bars":[{"t":"2026-11-25T15:00:00Z","o":597.0,"h":599.2,"l":596.5,"c":598.4,"v":120000,"n":450}]}`), nil
		default:
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(bytes.NewBufferString("{}")),
				Request:    req,
			}, nil
		}
	})

	svc, err := NewService(Config{
		DataDir:       t.TempDir(),
		FixtureMode:   false,
		Now:           func() time.Time { return now },
		HTTPTransport: rt,
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	defer svc.Close()

	for _, tc := range []struct {
		venue  string
		symbol string
		wantN  int
		wantC  float64
	}{
		{VenueOKX, "BTC", 2, 61400},
		{VenueCoinbase, "ETH", 2, 3165},
		{VenueDeribit, "BTC-PERPETUAL", 2, 61450},
		{VenueAlpaca, "SPY", 1, 598.4},
	} {
		series, err := svc.GetCandles(context.Background(), tc.venue, tc.symbol, "1h", TransformOptions{Style: StyleStandard}, 10)
		if err != nil || len(series.Candles) != tc.wantN || series.Fixture {
			t.Fatalf("GetCandles(%s, %s) failed: %+v err=%v", tc.venue, tc.symbol, series, err)
		}
		gotC := series.Candles[len(series.Candles)-1].Close
		if gotC != tc.wantC {
			t.Fatalf("GetCandles(%s) close=%v, want %v", tc.venue, gotC, tc.wantC)
		}
		if tc.venue != VenueAlpaca {
			book, err := svc.GetOrderBook(context.Background(), tc.venue, tc.symbol, 5)
			if err != nil || len(book.Bids) == 0 || len(book.Asks) == 0 || book.Fixture {
				t.Fatalf("GetOrderBook(%s) failed: %+v err=%v", tc.venue, book, err)
			}
			trades, err := svc.GetTrades(context.Background(), tc.venue, tc.symbol, 5)
			if err != nil || len(trades) == 0 {
				t.Fatalf("GetTrades(%s) failed: %+v err=%v", tc.venue, trades, err)
			}
		}
	}
}
