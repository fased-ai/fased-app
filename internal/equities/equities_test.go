package equities

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func TestEquitiesCompileTimeLockAndOCCParser(t *testing.T) {
	t.Parallel()
	if EquitiesActivationEnabled {
		t.Fatal("EquitiesActivationEnabled must remain false")
	}

	opt, err := ParseOCCSymbol("SPY   261218C00600000")
	if err != nil {
		t.Fatalf("ParseOCCSymbol padded failed: %v", err)
	}
	if opt.Underlying != "SPY" || opt.Expiration != "2026-12-18" || opt.Right != "C" || opt.Strike != 600.0 || opt.Canonical != "SPY   261218C00600000" {
		t.Fatalf("unexpected OCCOption: %+v", opt)
	}

	compact, err := ParseOCCSymbol("nvda261218p00145500")
	if err != nil {
		t.Fatalf("ParseOCCSymbol compact failed: %v", err)
	}
	if compact.Canonical != "NVDA  261218P00145500" || compact.Strike != 145.5 {
		t.Fatalf("unexpected compact OCCOption: %+v", compact)
	}

	if _, err := ParseOCCSymbol("BAD-OCC-SYMBOL"); err == nil {
		t.Fatal("expected invalid OCC error")
	}

	// Alpaca live & IBKR non-DU live accounts must fail closed.
	if err := ValidateVenueOrigin(VenueAlpaca, "live", AlpacaLiveOrigin, ""); !errors.Is(err, ErrExecutionDisabled) {
		t.Fatalf("expected ErrExecutionDisabled for alpaca live, got %v", err)
	}
	if err := ValidateVenueOrigin(VenueAlpaca, "paper", AlpacaLiveOrigin, ""); !errors.Is(err, ErrExecutionDisabled) {
		t.Fatalf("expected ErrExecutionDisabled for alpaca non-paper origin, got %v", err)
	}
	if err := ValidateVenueOrigin(VenueIBKR, "paper", IBKRLocalOrigin, "U1234567"); err == nil {
		t.Fatal("expected non-DU IBKR account to fail closed")
	}
	if err := ValidateVenueOrigin(VenueIBKR, "paper", "https://10.0.0.5:5000", "DU1234567"); err == nil {
		t.Fatal("expected non-loopback IBKR gateway host to fail closed")
	}
	if err := ValidateVenueOrigin(VenueIBKR, "paper", IBKRLocalOrigin, "DU1234567"); err != nil {
		t.Fatalf("expected IBKR DU* localhost paper check to pass: %v", err)
	}
}

func TestEquitiesServicePaperOrdersAndOptionsChain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Unix(1795600000, 0)
	svc, err := NewService(Config{
		DataDir:     dir,
		FixtureMode: true,
		Now:         func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	defer svc.Close()

	q, err := svc.GetQuote(context.Background(), "spy")
	if err != nil || q.Symbol != "SPY" || q.Last <= 0 {
		t.Fatalf("GetQuote failed: %+v err=%v", q, err)
	}

	chain, err := svc.FetchOptionsChain(context.Background(), "NVDA")
	if err != nil || len(chain.Contracts) != 10 || !chain.Fixture {
		t.Fatalf("FetchOptionsChain failed: %+v err=%v", chain, err)
	}

	// Live equities order blocked.
	if _, err := svc.SubmitOrder(context.Background(), OrderRequest{
		Venue:       VenueAlpaca,
		Symbol:      "AAPL",
		Side:        "BUY",
		OrderType:   "LIMIT",
		Quantity:    5,
		LimitPrice:  230,
		Environment: "live",
	}); !errors.Is(err, ErrExecutionDisabled) {
		t.Fatalf("expected ErrExecutionDisabled for live alpaca order, got %v", err)
	}

	rec, err := svc.SubmitOrder(context.Background(), OrderRequest{
		Venue:       VenueIBKR,
		AccountID:   "du9876543",
		Symbol:      chain.Contracts[0].OCCSymbol,
		Side:        "BUY",
		OrderType:   "LIMIT",
		Quantity:    1,
		LimitPrice:  12.50,
		Environment: "paper",
	})
	if err != nil {
		t.Fatalf("SubmitOrder IBKR paper OCC option failed: %v", err)
	}
	if !rec.IsOption || !rec.Simulated || rec.AccountID != "DU9876543" {
		t.Fatalf("unexpected OrderReceipt: %+v", rec)
	}

	st, err := svc.Status(context.Background())
	if err != nil || !st.LiveLocked || st.ActivationEnabled || len(st.RecentOrders) != 1 {
		t.Fatalf("unexpected StatusView: %+v err=%v", st, err)
	}
}

func TestEquitiesMainnetCappedPolicyAndCeiling(t *testing.T) {
	svc, err := NewService(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	defer svc.Close()

	if err := svc.ConfigureMainnetCapped(250_000_001); err == nil {
		t.Fatal("expected >$250 max order to fail")
	}
	if err := svc.ConfigureMainnetCapped(100_000_000); err != nil {
		t.Fatalf("ConfigureMainnetCapped($100) failed: %v", err)
	}
	st, err := svc.Status(context.Background())
	if err != nil || st.LiveLocked || !st.CappedMode || st.MaxOrderMicroUSD != 100_000_000 {
		t.Fatalf("unexpected capped status: %+v err=%v", st, err)
	}
	if _, err := svc.SubmitOrder(context.Background(), OrderRequest{
		Venue:       VenueAlpaca,
		Symbol:      "SPY",
		Side:        "BUY",
		OrderType:   "LIMIT",
		Quantity:    1,
		LimitPrice:  590, // $590 > $100 cap
		Environment: "live",
	}); err == nil {
		t.Fatal("expected over-cap live equity order to fail")
	}
	rec, err := svc.SubmitOrder(context.Background(), OrderRequest{
		Venue:       VenueAlpaca,
		Symbol:      "AAPL",
		Side:        "BUY",
		OrderType:   "LIMIT",
		Quantity:    0.2,
		LimitPrice:  250, // $50 <= $100 cap
		Environment: "live",
	})
	if err != nil || rec.Status != "mainnet_capped_routed" {
		t.Fatalf("expected mainnet_capped_routed, got %+v err=%v", rec, err)
	}
}

func TestEquitiesLiveAlpacaSnapshotQuoteAndParser(t *testing.T) {
	t.Parallel()
	now := time.Unix(1795600000, 0).UTC()
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://data.alpaca.markets/v2/stocks/NVDA/snapshot?feed=iex" {
			t.Fatalf("unexpected URL: %s", r.URL.String())
		}
		if r.Header.Get("APCA-API-KEY-ID") != "key-1" || r.Header.Get("APCA-API-SECRET-KEY") != "sec-1" {
			t.Fatalf("missing alpaca auth headers: %v", r.Header)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body: io.NopCloser(bytes.NewBufferString(`{
				"latestTrade":{"p":143.25,"t":"2026-11-25T15:30:00Z"},
				"latestQuote":{"bp":143.20,"ap":143.30,"t":"2026-11-25T15:30:00Z"},
				"dailyBar":{"v":2450000}
			}`)),
			Request: r,
		}, nil
	})
	svc, err := NewService(Config{
		DataDir:         t.TempDir(),
		FixtureMode:     false,
		Now:             func() time.Time { return now },
		HTTPTransport:   rt,
		AlpacaKeyID:     "key-1",
		AlpacaSecretKey: "sec-1",
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	defer svc.Close()

	q, err := svc.GetQuote(context.Background(), "nvda")
	if err != nil {
		t.Fatalf("GetQuote failed: %v", err)
	}
	if q.Symbol != "NVDA" || q.Source != "alpaca-iex" || q.Bid != 143.20 || q.Ask != 143.30 || q.Last != 143.25 || q.Volume != 2450000 {
		t.Fatalf("unexpected live Alpaca snapshot quote: %+v", q)
	}
	if _, err := ParseAlpacaSnapshotQuote("NVDA", []byte(`{"latestTrade":{"p":0}}`), now.Unix()); err == nil {
		t.Fatal("expected error for zero/missing snapshot price")
	}
}
