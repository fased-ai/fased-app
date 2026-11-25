package equities

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
