package marketdata

import (
	"context"
	"testing"
	"time"
)

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
