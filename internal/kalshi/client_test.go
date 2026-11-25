package kalshi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/signer"
)

type mockControlledTransport struct {
	fn func(*http.Request) (*http.Response, error)
}

func (m mockControlledTransport) ControlledFixture() bool { return true }
func (m mockControlledTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return m.fn(r)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestSSRFPrivateIPDenied(t *testing.T) {
	t.Parallel()
	_, err := dialPublicAddress(
		t.Context(),
		"tcp",
		"demo-api.kalshi.co:443",
		func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("10.0.0.1")}, nil
		},
		func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("dial invoked for RFC1918 address")
			return nil, nil
		},
	)
	if err == nil {
		t.Fatal("expected private IP denial")
	}
}

func TestBuildOrderExactFixedPointRisk(t *testing.T) {
	t.Parallel()
	// bid 10 @ $0.5600 => $5.60 risk = 5_600_000 microUSD
	bidOrd, err := BuildOrder(OrderDraft{
		Ticker: "KXFED-26DEC-T400",
		Side:   "bid",
		Count:  "10",
		Price:  "0.56",
	})
	if err != nil {
		t.Fatalf("BuildOrder bid failed: %v", err)
	}
	if bidOrd.EstimatedMicroUSD != 5_600_000 || bidOrd.CountFormatted != "10.00" || bidOrd.PriceFormatted != "0.5600" {
		t.Fatalf("unexpected bid order: %+v", bidOrd)
	}

	// ask 10 @ $0.6000 => max loss $4.00 = 4_000_000 microUSD
	askOrd, err := BuildOrder(OrderDraft{
		Ticker: "KXFED-26DEC-T400",
		Side:   "ask",
		Count:  "10",
		Price:  "0.6000",
	})
	if err != nil {
		t.Fatalf("BuildOrder ask failed: %v", err)
	}
	if askOrd.EstimatedMicroUSD != 4_000_000 {
		t.Fatalf("unexpected ask risk: %+v", askOrd)
	}

	// Out-of-range prices rejected.
	for _, badPx := range []string{"0.0050", "1.0000", "0", "-0.5"} {
		if _, err := BuildOrder(OrderDraft{Ticker: "KXFED-26DEC", Side: "bid", Count: "1", Price: badPx}); err == nil {
			t.Fatalf("expected invalid price rejection for %q", badPx)
		}
	}
}

func TestProdLockedCancelQueryExclusionAndControlledFlow(t *testing.T) {
	t.Parallel()
	prodClient, err := NewControlledClient("prod", mockControlledTransport{fn: func(*http.Request) (*http.Response, error) {
		t.Fatal("prod transport must never be dialed while locked")
		return nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = prodClient.SubmitOrder(t.Context(), OrderDraft{Ticker: "KXBTC", Side: "bid", Count: "1", Price: "0.50"}, func(context.Context, signer.KalshiSignIntent) (*signer.KalshiSignedRequest, error) {
		return nil, nil
	})
	if !errors.Is(err, ErrExecutionDisabled) {
		t.Fatalf("expected ErrExecutionDisabled for prod submit, got %v", err)
	}

	var signedCancelPath string
	var gotAuthHeader string
	c, err := NewControlledClient("demo", mockControlledTransport{fn: func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/trade-api/v2/markets/") && strings.HasSuffix(r.URL.Path, "/orderbook"):
			return jsonResponse(200, `{"orderbook_fp":{"yes_dollars":[["0.5600","25.00"]],"no_dollars":[["0.4400","30.00"]]}}`), nil
		case r.Method == http.MethodPost && r.URL.Path == "/trade-api/v2/portfolio/events/orders":
			gotAuthHeader = r.Header.Get("KALSHI-ACCESS-SIGNATURE")
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["price"] != "0.5600" || body["count"] != "5.00" {
				return jsonResponse(400, `{"code":"bad_request","message":"bad format"}`), nil
			}
			return jsonResponse(201, `{"order":{"order_id":"k-ord-999","client_order_id":"c-1","status":"resting"}}`), nil
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/trade-api/v2/portfolio/events/orders/"):
			if r.URL.RawQuery != "market_ticker=KXBTC-26" {
				return jsonResponse(400, `{"message":"missing market_ticker"}`), nil
			}
			return jsonResponse(200, `{"order":{"order_id":"k-ord-999","status":"canceled"}}`), nil
		default:
			return jsonResponse(404, `{"code":"not_found"}`), nil
		}
	}})
	if err != nil {
		t.Fatal(err)
	}

	book, err := c.FetchOrderBook(t.Context(), "KXBTC-26")
	if err != nil || book.BestYesBid != "0.5600" || book.YesCents != 1 {
		t.Fatalf("FetchOrderBook unexpected: %+v err=%v", book, err)
	}

	signFn := func(_ context.Context, intent signer.KalshiSignIntent) (*signer.KalshiSignedRequest, error) {
		if intent.Method == http.MethodDelete {
			signedCancelPath = intent.Path
		}
		return &signer.KalshiSignedRequest{
			Environment:     "demo",
			KeyID:           "demo-key-1",
			TimestampMs:     intent.TimestampMs,
			Method:          intent.Method,
			Path:            intent.Path,
			SignatureBase64: "c2lnLW9r",
		}, nil
	}
	sub, err := c.SubmitOrder(t.Context(), OrderDraft{Ticker: "KXBTC-26", Side: "bid", Count: "5", Price: "0.56"}, signFn)
	if err != nil || !sub.Submitted || sub.OrderID != "k-ord-999" || gotAuthHeader != "c2lnLW9r" {
		t.Fatalf("SubmitOrder unexpected: sub=%+v err=%v sig=%q", sub, err, gotAuthHeader)
	}
	can, err := c.CancelOrder(t.Context(), "k-ord-999", "KXBTC-26", signFn)
	if err != nil || !can.Submitted {
		t.Fatalf("CancelOrder unexpected: %+v err=%v", can, err)
	}
	if signedCancelPath != "/trade-api/v2/portfolio/events/orders/k-ord-999" || strings.Contains(signedCancelPath, "?") {
		t.Fatalf("signed cancel path must exclude query parameters, got %q", signedCancelPath)
	}
}
