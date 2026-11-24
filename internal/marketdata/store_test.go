package marketdata

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStorePartitioningPruneAndWatchlists(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dir := t.TempDir()
	nowTime := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	store, err := NewStore(dir, func() time.Time { return nowTime })
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Verify isolation inside <dir>/marketdata/ and no core.sqlite touches.
	if _, err := os.Stat(filepath.Join(dir, "core.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("core.sqlite should not exist in empty test dir")
	}

	augTs := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC).Unix()
	novTs := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC).Unix()
	candles := []Candle{
		{Time: augTs, Open: 90, High: 95, Low: 88, Close: 94, Volume: 120},
		{Time: novTs, Open: 100, High: 105, Low: 99, Close: 104, Volume: 220},
	}
	if err := store.UpsertCandles(ctx, VenueHyperliquid, "BTC", "1h", candles); err != nil {
		t.Fatalf("UpsertCandles failed: %v", err)
	}
	// Idempotent upsert overwrite.
	candles[1].Close = 106
	if err := store.UpsertCandles(ctx, VenueHyperliquid, "BTC", "1h", candles); err != nil {
		t.Fatalf("UpsertCandles second write failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, marketDataDirName, "candles_2026_08.sqlite")); err != nil {
		t.Fatalf("expected 2026_08 partition: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, marketDataDirName, "candles_2026_11.sqlite")); err != nil {
		t.Fatalf("expected 2026_11 partition: %v", err)
	}

	got, err := store.QueryCandles(ctx, VenueHyperliquid, "BTC", "1h", augTs-10, novTs+10, 50)
	if err != nil {
		t.Fatalf("QueryCandles failed: %v", err)
	}
	if len(got) != 2 || got[1].Close != 106 {
		t.Fatalf("unexpected queried candles: %+v", got)
	}

	pruned, err := store.PruneOldPartitions(45)
	if err != nil {
		t.Fatalf("PruneOldPartitions failed: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("expected 1 pruned partition (2026_08), got %d", pruned)
	}
	if _, err := os.Stat(filepath.Join(dir, marketDataDirName, "candles_2026_08.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("expected 2026_08 partition removed")
	}

	// Watchlist CRUD.
	saved, err := store.SaveWatchlist(ctx, Watchlist{
		ID:   "perps-main",
		Name: "Perps Main",
		Items: []WatchlistItem{
			{Venue: VenueHyperliquid, Symbol: "ETH", Interval: "15m", Style: StyleHeikinAshi},
		},
	})
	if err != nil {
		t.Fatalf("SaveWatchlist failed: %v", err)
	}
	if saved.ID != "perps-main" || len(saved.Items) != 1 {
		t.Fatalf("unexpected saved watchlist: %+v", saved)
	}
	lists, err := store.ListWatchlists(ctx)
	if err != nil || len(lists) != 1 {
		t.Fatalf("ListWatchlists failed: len=%d err=%v", len(lists), err)
	}
	if deleted, err := store.DeleteWatchlist(ctx, "perps-main"); err != nil || !deleted {
		t.Fatalf("DeleteWatchlist failed: deleted=%v err=%v", deleted, err)
	}
	if _, err := store.SaveWatchlist(ctx, Watchlist{ID: "../bad", Name: "Invalid"}); err == nil {
		t.Fatal("expected invalid watchlist id error")
	}
}
