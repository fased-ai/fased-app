package marketdata

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"net/http"
	"strings"
	"time"
)

// Config parameterizes a marketdata Service.
type Config struct {
	DataDir       string
	FixtureMode   bool
	Now           func() time.Time
	HTTPTransport http.RoundTripper // Tests only.
}

// Service unifies multi-venue market data connectors, deterministic chart transforms,
// monthly SQLite candle persistence, watchlists, and in-process SSE pub/sub.
type Service struct {
	store       *Store
	bus         *Bus
	client      *upstreamClient
	fixtureMode bool
	now         func() time.Time
}

// NewService opens the marketdata partition store, initializes the pub/sub bus,
// and seeds a default Core Desk watchlist if none exist.
func NewService(cfg Config) (*Service, error) {
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	store, err := NewStore(cfg.DataDir, nowFn)
	if err != nil {
		return nil, err
	}
	svc := &Service{
		store:       store,
		bus:         NewBus(),
		client:      newUpstreamClient(cfg.HTTPTransport),
		fixtureMode: cfg.FixtureMode,
		now:         nowFn,
	}
	if err := svc.seedDefaultWatchlists(context.Background()); err != nil {
		_ = store.Close()
		return nil, err
	}
	return svc, nil
}

func (s *Service) seedDefaultWatchlists(ctx context.Context) error {
	existing, err := s.store.ListWatchlists(ctx)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}
	_, err = s.store.SaveWatchlist(ctx, Watchlist{
		ID:   "core-desk",
		Name: "Core Desk",
		Items: []WatchlistItem{
			{Venue: VenueHyperliquid, Symbol: "BTC", Label: "BTC Perp", Interval: "1h", Style: StyleStandard},
			{Venue: VenueHyperliquid, Symbol: "ETH", Label: "ETH Perp", Interval: "1h", Style: StyleStandard},
			{Venue: VenueHyperliquid, Symbol: "SOL", Label: "SOL Perp", Interval: "15m", Style: StyleHeikinAshi},
			{Venue: VenueBinance, Symbol: "BTCUSDT", Label: "BTC Spot", Interval: "1h", Style: StyleStandard},
		},
	})
	return err
}

// Close releases the pub/sub bus and closes all SQLite partitions cleanly.
func (s *Service) Close() error {
	s.bus.Close()
	return s.store.Close()
}

// Subscribe attaches a subscriber channel to the pub/sub bus.
func (s *Service) Subscribe(filter Filter) (<-chan Event, func()) {
	return s.bus.Subscribe(filter)
}

// SubscribeMulti attaches a single subscriber channel matching any of filters.
func (s *Service) SubscribeMulti(filters []Filter) (<-chan Event, func()) {
	return s.bus.SubscribeMulti(filters)
}

// Publish emits an event onto the in-process marketdata bus.
func (s *Service) Publish(evt Event) {
	if evt.Timestamp == 0 {
		evt.Timestamp = s.now().UTC().UnixMilli()
	}
	s.bus.Publish(evt)
}

// PruneOldPartitions deletes monthly SQLite candle files older than maxAgeDays.
func (s *Service) PruneOldPartitions(maxAgeDays int) (int, error) {
	return s.store.PruneOldPartitions(maxAgeDays)
}

// ListInstruments lists known tracked symbols across venues or for a specific venue.
func (s *Service) ListInstruments(ctx context.Context, venueFilter string) ([]Instrument, error) {
	nowSec := s.now().UTC().Unix()
	all := defaultInstrumentsCatalog(nowSec)
	if strings.TrimSpace(venueFilter) == "" {
		return all, nil
	}
	v, err := NormalizeVenue(venueFilter)
	if err != nil {
		return nil, err
	}
	filtered := make([]Instrument, 0, len(all))
	for _, inst := range all {
		if inst.Venue == v {
			filtered = append(filtered, inst)
		}
	}
	return filtered, nil
}

// GetCandles returns normalized OHLCV or transformed candles, backed by monthly SQLite
// partitions and offline synthetic FixtureMode when configured.
func (s *Service) GetCandles(ctx context.Context, venue, symbol, interval string, opts TransformOptions, limit int) (CandleSeries, error) {
	v, err := NormalizeVenue(venue)
	if err != nil {
		return CandleSeries{}, err
	}
	sym, err := NormalizeSymbol(symbol)
	if err != nil {
		return CandleSeries{}, err
	}
	iv, err := NormalizeInterval(interval)
	if err != nil {
		return CandleSeries{}, err
	}
	style, err := NormalizeStyle(opts.Style)
	if err != nil {
		return CandleSeries{}, err
	}
	opts.Style = style
	if limit <= 0 || limit > maxCandlesLimit {
		limit = 200
	}

	stepSec, _ := IntervalSeconds(iv)
	nowSec := s.now().UTC().Unix()
	endSec := (nowSec / stepSec) * stepSec
	startSec := endSec - int64(limit)*stepSec

	var base []Candle
	source := "cache"
	if s.fixtureMode {
		base = generateSyntheticCandles(v, sym, stepSec, startSec, limit)
		_ = s.store.UpsertCandles(ctx, v, sym, iv, base)
		source = "fixture"
	} else {
		fetched, fetchErr := s.client.fetchCandles(ctx, v, sym, iv, startSec, nowSec, limit)
		if fetchErr == nil && len(fetched) > 0 {
			base = fetched
			_ = s.store.UpsertCandles(ctx, v, sym, iv, base)
			source = v
		} else {
			cached, cacheErr := s.store.QueryCandles(ctx, v, sym, iv, startSec, nowSec, limit)
			if cacheErr == nil && len(cached) > 0 {
				base = cached
			} else if fetchErr != nil {
				return CandleSeries{}, fetchErr
			}
		}
	}
	if limit > 0 && len(base) > limit {
		base = base[len(base)-limit:]
	}

	if (style == StyleTick || style == StyleVolume) && s.fixtureMode {
		ticks := generateSyntheticTicks(v, sym, endSec*1000, 180)
		agg, aggErr := AggregateTicksToCandles(ticks, opts)
		if aggErr != nil {
			return CandleSeries{}, aggErr
		}
		series := CandleSeries{
			Venue:     v,
			Symbol:    sym,
			Interval:  iv,
			Style:     style,
			Candles:   agg,
			Source:    source,
			Fixture:   s.fixtureMode,
			UpdatedAt: nowSec,
		}
		s.Publish(Event{Type: "candle", Venue: v, Symbol: sym, Interval: iv, Style: style, Payload: series})
		return series, nil
	}

	transformed, err := TransformCandles(base, opts)
	if err != nil {
		return CandleSeries{}, err
	}
	series := CandleSeries{
		Venue:     v,
		Symbol:    sym,
		Interval:  iv,
		Style:     style,
		Candles:   transformed,
		Source:    source,
		Fixture:   s.fixtureMode,
		UpdatedAt: nowSec,
	}
	if len(transformed) > 0 {
		s.Publish(Event{Type: "candle", Venue: v, Symbol: sym, Interval: iv, Style: style, Payload: transformed[len(transformed)-1]})
	}
	return series, nil
}

// GetOrderBook returns top-of-book L2 depth for a symbol.
func (s *Service) GetOrderBook(ctx context.Context, venue, symbol string, limit int) (OrderBookSnapshot, error) {
	v, err := NormalizeVenue(venue)
	if err != nil {
		return OrderBookSnapshot{}, err
	}
	sym, err := NormalizeSymbol(symbol)
	if err != nil {
		return OrderBookSnapshot{}, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	nowMs := s.now().UTC().UnixMilli()
	if s.fixtureMode {
		book := generateSyntheticOrderBook(v, sym, nowMs, limit)
		s.Publish(Event{Type: "orderbook", Venue: v, Symbol: sym, Payload: book})
		return book, nil
	}
	book, err := s.client.fetchOrderBook(ctx, v, sym, limit, nowMs)
	if err != nil {
		return OrderBookSnapshot{}, err
	}
	s.Publish(Event{Type: "orderbook", Venue: v, Symbol: sym, Payload: book})
	return book, nil
}

// GetTrades returns recent trades for a venue and symbol.
func (s *Service) GetTrades(ctx context.Context, venue, symbol string, limit int) ([]TradeTick, error) {
	v, err := NormalizeVenue(venue)
	if err != nil {
		return nil, err
	}
	sym, err := NormalizeSymbol(symbol)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 60
	}
	nowMs := s.now().UTC().UnixMilli()
	if s.fixtureMode {
		return generateSyntheticTicks(v, sym, nowMs, limit), nil
	}
	return s.client.fetchTrades(ctx, v, sym, limit)
}

// GetFootprint computes orderflow footprint bars, delta, CVD, VPOC, VAH, and VAL.
func (s *Service) GetFootprint(ctx context.Context, venue, symbol, interval string, stepSize float64, limit int) (FootprintSeries, error) {
	if limit <= 0 || limit > 200 {
		limit = 48
	}
	candles, err := s.GetCandles(ctx, venue, symbol, interval, TransformOptions{Style: StyleStandard}, limit)
	if err != nil {
		return FootprintSeries{}, err
	}
	ticks, _ := s.GetTrades(ctx, venue, symbol, 150)
	if stepSize <= 0 && len(candles.Candles) > 0 {
		stepSize = autoPriceStep(candles.Candles, 4.0)
	}
	bars, err := ComputeFootprintAndProfile(candles.Candles, ticks, stepSize)
	if err != nil {
		return FootprintSeries{}, err
	}
	return FootprintSeries{
		Venue:     candles.Venue,
		Symbol:    candles.Symbol,
		Interval:  candles.Interval,
		StepSize:  stepSize,
		Bars:      bars,
		Source:    candles.Source,
		Fixture:   s.fixtureMode,
		UpdatedAt: s.now().UTC().Unix(),
	}, nil
}

// GetMacroSeries returns FRED observations, cached in meta.sqlite.
func (s *Service) GetMacroSeries(ctx context.Context, seriesID string, limit int) ([]MacroPoint, error) {
	id, err := NormalizeSeriesID(seriesID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 120
	}
	nowSec := s.now().UTC().Unix()
	var pts []MacroPoint
	if s.fixtureMode {
		pts = generateSyntheticMacro(id, nowSec)
		_ = s.store.SaveMacroCache(ctx, id, pts)
	} else {
		fetched, fetchErr := s.client.fetchFREDSeries(ctx, id, nowSec)
		if fetchErr == nil && len(fetched) > 0 {
			pts = fetched
			_ = s.store.SaveMacroCache(ctx, id, pts)
		} else {
			cached, _, ok, cacheErr := s.store.LoadMacroCache(ctx, id)
			if cacheErr == nil && ok {
				pts = cached
			} else if fetchErr != nil {
				return nil, fetchErr
			}
		}
	}
	if len(pts) > limit {
		pts = pts[len(pts)-limit:]
	}
	return pts, nil
}

// GetSECFilings fetches recent EDGAR filings for a company CIK.
func (s *Service) GetSECFilings(ctx context.Context, cik string, limit int) ([]SECFiling, error) {
	padded, err := NormalizeCIK(cik)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if s.fixtureMode {
		return generateSyntheticSECFilings(padded, limit), nil
	}
	return s.client.fetchSECFilings(ctx, padded, limit)
}

// GetCohorts returns ranked trader cohorts for a venue and window.
func (s *Service) GetCohorts(ctx context.Context, window string) (CohortSnapshot, error) {
	w := strings.TrimSpace(window)
	if w == "" {
		w = "week"
	}
	nowSec := s.now().UTC().Unix()
	if s.fixtureMode {
		return generateSyntheticCohorts(w, nowSec), nil
	}
	return s.client.fetchCohorts(ctx, w, nowSec)
}

// ListWatchlists lists saved watchlists.
func (s *Service) ListWatchlists(ctx context.Context) ([]Watchlist, error) {
	return s.store.ListWatchlists(ctx)
}

// SaveWatchlist creates or updates a watchlist and notifies bus listeners.
func (s *Service) SaveWatchlist(ctx context.Context, w Watchlist) (Watchlist, error) {
	saved, err := s.store.SaveWatchlist(ctx, w)
	if err != nil {
		return Watchlist{}, err
	}
	s.Publish(Event{Type: "watchlist", Payload: saved})
	return saved, nil
}

// DeleteWatchlist deletes a watchlist by ID and notifies bus listeners.
func (s *Service) DeleteWatchlist(ctx context.Context, id string) (bool, error) {
	deleted, err := s.store.DeleteWatchlist(ctx, id)
	if err != nil {
		return false, err
	}
	if deleted {
		s.Publish(Event{Type: "watchlist", Payload: map[string]any{"deletedId": id}})
	}
	return deleted, nil
}

func defaultInstrumentsCatalog(nowSec int64) []Instrument {
	return []Instrument{
		{Venue: VenueHyperliquid, Symbol: "BTC", Base: "BTC", Quote: "USDC", AssetClass: "crypto_perp", TickSize: 1.0, LotSize: 0.001, MaxLeverage: 40, UpdatedAt: nowSec},
		{Venue: VenueHyperliquid, Symbol: "ETH", Base: "ETH", Quote: "USDC", AssetClass: "crypto_perp", TickSize: 0.1, LotSize: 0.01, MaxLeverage: 25, UpdatedAt: nowSec},
		{Venue: VenueHyperliquid, Symbol: "SOL", Base: "SOL", Quote: "USDC", AssetClass: "crypto_perp", TickSize: 0.01, LotSize: 0.1, MaxLeverage: 20, UpdatedAt: nowSec},
		{Venue: VenueBinance, Symbol: "BTCUSDT", Base: "BTC", Quote: "USDT", AssetClass: "crypto_spot", TickSize: 0.01, LotSize: 0.0001, UpdatedAt: nowSec},
		{Venue: VenueBinance, Symbol: "ETHUSDT", Base: "ETH", Quote: "USDT", AssetClass: "crypto_spot", TickSize: 0.01, LotSize: 0.001, UpdatedAt: nowSec},
		{Venue: VenueBybit, Symbol: "BTCUSDT", Base: "BTC", Quote: "USDT", AssetClass: "crypto_perp", TickSize: 0.1, LotSize: 0.001, MaxLeverage: 50, UpdatedAt: nowSec},
		{Venue: VenueKraken, Symbol: "XBTUSD", Base: "BTC", Quote: "USD", AssetClass: "crypto_spot", TickSize: 0.1, LotSize: 0.0001, UpdatedAt: nowSec},
		{Venue: VenueSolana, Symbol: "So11111111111111111111111111111111111111112", Base: "SOL", Quote: "USDC", AssetClass: "solana_dex", TickSize: 0.01, LotSize: 0.01, UpdatedAt: nowSec},
		{Venue: VenueFRED, Symbol: "CPIAUCSL", Base: "CPIAUCSL", Quote: "INDEX", AssetClass: "macro", UpdatedAt: nowSec},
		{Venue: VenueSEC, Symbol: "0000320193", Base: "AAPL", Quote: "USD", AssetClass: "sec_filing", UpdatedAt: nowSec},
	}
}

func seedPriceForSymbol(venue, symbol string) (float64, uint64) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(venue + ":" + symbol))
	digest := h.Sum64()
	base := normalizeCoinBase(symbol)
	switch base {
	case "BTC", "XBT":
		return 94500.0, digest
	case "ETH":
		return 3350.0, digest
	case "SOL", "SO11111111111111111111111111111111111111112":
		return 218.0, digest
	default:
		return 100.0 + float64(digest%500), digest
	}
}

func generateSyntheticCandles(venue, symbol string, stepSec, startSec int64, count int) []Candle {
	basePrice, seed := seedPriceForSymbol(venue, symbol)
	out := make([]Candle, 0, count)
	price := basePrice
	for i := 0; i < count; i++ {
		ts := startSec + int64(i)*stepSec
		phase := float64(i)*0.21 + float64(seed%97)*0.03
		deltaPct := math.Sin(phase)*0.0065 + math.Cos(phase*0.5)*0.0025
		open := price
		closePrice := math.Max(open*(1+deltaPct), basePrice*0.25)
		wick := math.Abs(open) * (0.003 + float64((i+int(seed%7))%5)*0.001)
		high := math.Max(open, closePrice) + wick
		low := math.Max(math.Min(open, closePrice)-wick, 1e-6)
		vol := 120.0 + float64((i*17+int(seed%53))%300)*1.75
		buyRatio := 0.45
		if closePrice >= open {
			buyRatio = 0.62
		}
		out = append(out, Candle{
			Time:       ts,
			Open:       round2(open),
			High:       round2(high),
			Low:        round2(low),
			Close:      round2(closePrice),
			Volume:     round2(vol),
			BuyVolume:  round2(vol * buyRatio),
			SellVolume: round2(vol * (1 - buyRatio)),
			Trades:     int64(45 + (i*11)%140),
		})
		price = closePrice
	}
	return out
}

func generateSyntheticOrderBook(venue, symbol string, nowMs int64, levels int) OrderBookSnapshot {
	mid, _ := seedPriceForSymbol(venue, symbol)
	step := math.Max(mid*0.0005, 0.01)
	bids := make([]OrderBookLevel, 0, levels)
	asks := make([]OrderBookLevel, 0, levels)
	var bidRun, askRun float64
	for i := 0; i < levels; i++ {
		offset := float64(i+1) * step
		sz := 1.5 + float64((i*3)%11)*0.4
		bidRun += sz
		askRun += sz * 1.05
		bids = append(bids, OrderBookLevel{Price: round2(mid - offset), Size: round2(sz), Total: round2(bidRun)})
		asks = append(asks, OrderBookLevel{Price: round2(mid + offset), Size: round2(sz * 1.05), Total: round2(askRun)})
	}
	snap := buildOrderBookSnapshot(venue, symbol, nowMs, bids, asks, "fixture")
	snap.Fixture = true
	return snap
}

func generateSyntheticTicks(venue, symbol string, endMs int64, count int) []TradeTick {
	mid, seed := seedPriceForSymbol(venue, symbol)
	out := make([]TradeTick, 0, count)
	for i := 0; i < count; i++ {
		ts := endMs - int64(count-1-i)*1500
		offset := math.Sin(float64(i)*0.4+float64(seed%19)) * mid * 0.0025
		side := "buy"
		if (i+int(seed))%3 == 0 {
			side = "sell"
		}
		out = append(out, TradeTick{
			ID:    fmt.Sprintf("fix-%d", ts),
			Time:  ts,
			Price: round2(math.Max(mid+offset, 1e-4)),
			Size:  round2(0.15 + float64((i*7)%19)*0.12),
			Side:  side,
		})
	}
	return out
}

func generateSyntheticMacro(seriesID string, nowSec int64) []MacroPoint {
	base := 310.5
	if seriesID == "UNRATE" {
		base = 4.1
	} else if seriesID == "DFF" {
		base = 4.5
	}
	out := make([]MacroPoint, 0, 12)
	endMonth := time.Unix(nowSec, 0).UTC()
	for i := 11; i >= 0; i-- {
		d := endMonth.AddDate(0, -i, 0).Format("2006-01-01")
		out = append(out, MacroPoint{
			SeriesID:  seriesID,
			Date:      d,
			Value:     round2(base + float64(11-i)*0.15),
			Source:    "fixture",
			UpdatedAt: nowSec,
		})
	}
	return out
}

func generateSyntheticSECFilings(cik string, limit int) []SECFiling {
	forms := []string{"10-Q", "8-K", "10-K", "4"}
	n := minInt(limit, 8)
	out := make([]SECFiling, 0, n)
	for i := 0; i < n; i++ {
		form := forms[i%len(forms)]
		acc := fmt.Sprintf("0000320193-26-%06d", 1000+i)
		out = append(out, SECFiling{
			CIK:             cik,
			Company:         "Fixture Issuer Corp",
			Form:            form,
			FilingDate:      fmt.Sprintf("2026-%02d-15", 11-(i%10)),
			AccessionNumber: acc,
			PrimaryDocument: "filing.htm",
			URL:             fmt.Sprintf("https://www.sec.gov/Archives/edgar/data/%s/%s/filing.htm", strings.TrimLeft(cik, "0"), strings.ReplaceAll(acc, "-", "")),
			Description:     form + " periodic report",
		})
	}
	return out
}

func generateSyntheticCohorts(window string, nowSec int64) CohortSnapshot {
	return CohortSnapshot{
		Venue:     VenueHyperliquid,
		Window:    window,
		UpdatedAt: nowSec,
		Source:    "fixture",
		Fixture:   true,
		Entries: []CohortEntry{
			{Address: "0x1111111111111111111111111111111111111111", DisplayName: "desk-alpha", AccountValue: 2450000, DayROI: 0.042, WeekROI: 0.148, MonthROI: 0.312, AllTimeROI: 1.84, Rank: 1},
			{Address: "0x2222222222222222222222222222222222222222", DisplayName: "delta-neutral-1", AccountValue: 1820000, DayROI: 0.019, WeekROI: 0.084, MonthROI: 0.205, AllTimeROI: 1.12, Rank: 2},
			{Address: "0x3333333333333333333333333333333333333333", DisplayName: "sol-momentum", AccountValue: 940000, DayROI: 0.031, WeekROI: 0.112, MonthROI: 0.275, AllTimeROI: 0.96, Rank: 3},
		},
	}
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
