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

func TestStoreChartDrawingsCRUDAndLimits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dir := t.TempDir()
	nowTime := time.Date(2026, 11, 25, 18, 0, 0, 0, time.UTC)
	store, err := NewStore(dir, func() time.Time { return nowTime })
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// 1. Save horizontal line, trendline, and fib drawings.
	hline, err := store.SaveDrawing(ctx, ChartDrawing{
		ID:     "draw-hline-btc",
		Venue:  VenueHyperliquid,
		Symbol: "BTC",
		Kind:   DrawingKindHLine,
		P1:     DrawingPoint{Time: 1764000000, Price: 95000},
		Label:  "Breakout level",
	})
	if err != nil {
		t.Fatalf("SaveDrawing hline failed: %v", err)
	}
	if hline.CreatedAt != nowTime.Unix() || hline.UpdatedAt != nowTime.Unix() {
		t.Fatalf("unexpected timestamps on hline: %+v", hline)
	}

	trend, err := store.SaveDrawing(ctx, ChartDrawing{
		ID:       "draw-trend-btc",
		Venue:    VenueHyperliquid,
		Symbol:   "BTC",
		Interval: "1h",
		Kind:     DrawingKindTrendline,
		P1:       DrawingPoint{Time: 1000, Price: 90000},
		P2:       &DrawingPoint{Time: 2000, Price: 92000},
	})
	if err != nil {
		t.Fatalf("SaveDrawing trendline failed: %v", err)
	}
	if lvl, ok := EvaluateDrawingPrice(trend, 3000); !ok || lvl != 94000 {
		t.Fatalf("EvaluateDrawingPrice trendline = (%v, %v), want (94000, true)", lvl, ok)
	}
	// Vertical trendline avoids division by zero.
	vert := ChartDrawing{
		ID:   "draw-vert",
		Kind: DrawingKindTrendline,
		P1:   DrawingPoint{Time: 1000, Price: 90000},
		P2:   &DrawingPoint{Time: 1000, Price: 91500},
	}
	if lvl, ok := EvaluateDrawingPrice(vert, 2500); !ok || lvl != 91500 {
		t.Fatalf("EvaluateDrawingPrice vertical trendline = (%v, %v), want (91500, true)", lvl, ok)
	}

	fib, err := store.SaveDrawing(ctx, ChartDrawing{
		ID:       "draw-fib-eth",
		Venue:    VenueHyperliquid,
		Symbol:   "ETH",
		Interval: "15m",
		Kind:     DrawingKindFib,
		P1:       DrawingPoint{Time: 1000, Price: 3000},
		P2:       &DrawingPoint{Time: 2000, Price: 4000},
	})
	if err != nil {
		t.Fatalf("SaveDrawing fib failed: %v", err)
	}
	// Fib 0.618 retracement from 4000 high down to 3000 low = 3382.
	if lvl, ok := EvaluateDrawingPrice(fib, 2500); !ok || lvl < 3381.9 || lvl > 3382.1 {
		t.Fatalf("EvaluateDrawingPrice fib = (%v, %v), want (~3382, true)", lvl, ok)
	}

	// Update preserves createdAt while bumping updatedAt.
	nowTime = nowTime.Add(5 * time.Minute)
	updated, err := store.SaveDrawing(ctx, ChartDrawing{
		ID:     "draw-hline-btc",
		Venue:  VenueHyperliquid,
		Symbol: "BTC",
		Kind:   DrawingKindHLine,
		P1:     DrawingPoint{Time: 1764000000, Price: 95500},
		Label:  "Adjusted breakout",
	})
	if err != nil {
		t.Fatalf("SaveDrawing update failed: %v", err)
	}
	if updated.CreatedAt != hline.CreatedAt || updated.UpdatedAt <= hline.UpdatedAt {
		t.Fatalf("update did not preserve createdAt/bump updatedAt: old=%+v new=%+v", hline, updated)
	}

	// Filter by venue + symbol + interval includes symbol-wide (empty interval) drawings.
	listBTC1h, err := store.ListDrawings(ctx, VenueHyperliquid, "BTC", "1h")
	if err != nil || len(listBTC1h) != 2 {
		t.Fatalf("ListDrawings BTC 1h = (%d, %v), want 2", len(listBTC1h), err)
	}
	gotFib, found, err := store.GetDrawing(ctx, "draw-fib-eth")
	if err != nil || !found || gotFib.Kind != DrawingKindFib {
		t.Fatalf("GetDrawing fib = (%+v, %v, %v)", gotFib, found, err)
	}

	// Reject malformed inputs.
	for _, bad := range []ChartDrawing{
		{ID: "../bad", Venue: VenueHyperliquid, Symbol: "BTC", Kind: DrawingKindHLine, P1: DrawingPoint{Time: 1, Price: 1}},
		{ID: "bad-kind", Venue: VenueHyperliquid, Symbol: "BTC", Kind: "triangle", P1: DrawingPoint{Time: 1, Price: 1}},
		{ID: "bad-price", Venue: VenueHyperliquid, Symbol: "BTC", Kind: DrawingKindHLine, P1: DrawingPoint{Time: 1, Price: 0}},
		{ID: "missing-p2", Venue: VenueHyperliquid, Symbol: "BTC", Kind: DrawingKindTrendline, P1: DrawingPoint{Time: 1, Price: 100}},
	} {
		if _, err := store.SaveDrawing(ctx, bad); err == nil {
			t.Fatalf("expected validation error for drawing %+v", bad)
		}
	}

	// Delete drawing.
	if deleted, err := store.DeleteDrawing(ctx, "draw-fib-eth"); err != nil || !deleted {
		t.Fatalf("DeleteDrawing failed: deleted=%v err=%v", deleted, err)
	}
	if _, found, err := store.GetDrawing(ctx, "draw-fib-eth"); err != nil || found {
		t.Fatalf("expected deleted drawing absent: found=%v err=%v", found, err)
	}

	// Enforce per-symbol limit (maxDrawingsPerSymbol = 64). Already 2 saved for hyperliquid:BTC.
	for i := 2; i < maxDrawingsPerSymbol; i++ {
		if _, err := store.SaveDrawing(ctx, ChartDrawing{
			ID:     "draw-cap-" + string(rune('a'+i/26)) + string(rune('a'+i%26)),
			Venue:  VenueHyperliquid,
			Symbol: "BTC",
			Kind:   DrawingKindHLine,
			P1:     DrawingPoint{Time: int64(1000 + i), Price: float64(90000 + i)},
		}); err != nil {
			t.Fatalf("cap setup save %d failed: %v", i, err)
		}
	}
	if _, err := store.SaveDrawing(ctx, ChartDrawing{
		ID:     "draw-cap-overflow",
		Venue:  VenueHyperliquid,
		Symbol: "BTC",
		Kind:   DrawingKindHLine,
		P1:     DrawingPoint{Time: 2000, Price: 99999},
	}); err == nil {
		t.Fatal("expected per-symbol drawings limit error")
	}
}

func TestParseCSVOHLCVAndCustomFeeds(t *testing.T) {
	csv := "timestamp,open,high,low,close,volume\n" +
		"2026-11-20,100,110,95,108,500\n" +
		"2026-11-21 12:00:00,108,115,106,112,750\n" +
		"1795266000000,112,120,110,118,900\n"
	candles, err := ParseCSVOHLCV(csv)
	if err != nil {
		t.Fatalf("ParseCSVOHLCV: %v", err)
	}
	if len(candles) != 3 || candles[2].Close != 118 || candles[2].Time != 1795266000 {
		t.Fatalf("unexpected parsed candles: %+v", candles)
	}
	if _, err := ParseCSVOHLCV("bad,row,1,2,3"); err == nil {
		t.Fatal("expected malformed CSV timestamp rejection")
	}
	if _, err := ParseCSVOHLCV("1795262400,100,90,95,98,1"); err == nil {
		t.Fatal("expected high < open OHLC rejection")
	}

	svc, err := NewService(Config{DataDir: t.TempDir(), FixtureMode: true})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	// Empty custom feed returns empty slice even in FixtureMode.
	emptySeries, err := svc.GetCandles(t.Context(), VenueCustom, "MYFEED", "1h", TransformOptions{Style: StyleStandard}, 50)
	if err != nil || len(emptySeries.Candles) != 0 {
		t.Fatalf("expected empty custom series, got %+v err=%v", emptySeries, err)
	}

	sum, err := svc.IngestCustomCandles(t.Context(), IngestCandlesRequest{
		Symbol:   "MYFEED",
		Interval: "1d",
		Format:   "csv",
		CSV:      csv,
	})
	if err != nil || sum.BarCount != 3 {
		t.Fatalf("IngestCustomCandles failed: %+v err=%v", sum, err)
	}
	series, err := svc.GetCandles(t.Context(), VenueCustom, "MYFEED", "1d", TransformOptions{Style: StyleHeikinAshi}, 50)
	if err != nil || len(series.Candles) != 3 || series.Source != "custom" {
		t.Fatalf("GetCandles custom HEIKIN failed: %+v err=%v", series, err)
	}
	feeds, err := svc.ListCustomFeeds(t.Context())
	if err != nil || len(feeds) != 1 || feeds[0].Symbol != "MYFEED" {
		t.Fatalf("ListCustomFeeds failed: %+v err=%v", feeds, err)
	}
	insts, err := svc.ListInstruments(t.Context(), VenueCustom)
	if err != nil || len(insts) != 1 || insts[0].Symbol != "MYFEED" {
		t.Fatalf("ListInstruments(custom) failed: %+v err=%v", insts, err)
	}
}
