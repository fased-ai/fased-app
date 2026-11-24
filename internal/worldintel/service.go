package worldintel

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Config parameterizes the clean-room WorldIntel Service.
type Config struct {
	DataDir       string
	FixtureMode   bool
	Now           func() time.Time
	HTTPTransport http.RoundTripper // Tests only.
	Scorer        Scorer            // Optional TypeSafe Jev scorer; falls back deterministically.
}

// Service manages clean-room global news/geo/seismic/prediction feeds, scoring,
// cluster correlation, and live SSE broadcast without vendoring AGPL code.
type Service struct {
	store         *store
	bus           *Bus
	fixtureMode   bool
	now           func() time.Time
	httpTransport http.RoundTripper
	scorer        Scorer
	seedMu        sync.Mutex
	seeded        bool
}

// NewService opens isolated <dataDir>/worldintel/intel.sqlite storage.
func NewService(cfg Config) (*Service, error) {
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	st, err := openStore(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	return &Service{
		store:         st,
		bus:           NewBus(),
		fixtureMode:   cfg.FixtureMode,
		now:           nowFn,
		httpTransport: cfg.HTTPTransport,
		scorer:        cfg.Scorer,
	}, nil
}

// Close closes the pub/sub bus and SQLite handles cleanly.
func (s *Service) Close() error {
	if s.bus != nil {
		s.bus.Close()
	}
	if s.store != nil {
		return s.store.close()
	}
	return nil
}

// Subscribe listens for filtered live WorldIntel updates.
func (s *Service) Subscribe(filter StreamFilter) (<-chan StreamEvent, func()) {
	return s.bus.Subscribe(filter)
}

func (s *Service) ensureFixtureSeed(ctx context.Context) error {
	if !s.fixtureMode {
		return nil
	}
	s.seedMu.Lock()
	defer s.seedMu.Unlock()
	if s.seeded {
		return nil
	}
	s.seeded = true
	if s.store.countEvents(ctx) > 0 && s.store.countPredictions(ctx) > 0 {
		return nil
	}

	nowSec := s.now().UTC().Unix()
	rawEvents := []IntelEvent{
		{
			ID:          "fix-na-fed",
			SourceID:    "fed-press",
			SourceName:  "Federal Reserve Press",
			Category:    CategoryMacro,
			Title:       "Federal Reserve signals cautious Treasury liquidity adjustment in Washington",
			Summary:     "FOMC liquidity release affects USD yields and BTC risk appetite",
			URL:         "https://www.federalreserve.gov/newsevents/pressreleases.htm",
			PublishedAt: nowSec - 900,
			IngestedAt:  nowSec,
			Verified:    true,
		},
		{
			ID:          "fix-na-etf",
			SourceID:    "gdelt-global",
			SourceName:  "GDELT Global Macro & Geo",
			Category:    CategoryCrypto,
			Title:       "New York Bitcoin and Solana spot inflow surge lifts crypto desk volumes",
			Summary:     "BTC and SOL spot flows climb alongside lower Treasury yields",
			URL:         "https://example.org/intel/ny-btc-sol",
			PublishedAt: nowSec - 1500,
			IngestedAt:  nowSec,
		},
		{
			ID:          "fix-eu-ecb",
			SourceID:    "ecb-press",
			SourceName:  "ECB Press Releases",
			Category:    CategoryMacro,
			Title:       "ECB Frankfurt updates Eurozone bank liquidity facility terms",
			Summary:     "Eurozone sovereign spreads tighten ahead of policy review",
			URL:         "https://www.ecb.europa.eu/press/html/index.en.html",
			PublishedAt: nowSec - 2400,
			IngestedAt:  nowSec,
			Verified:    true,
		},
		{
			ID:          "fix-mena-oil",
			SourceID:    "gdelt-global",
			SourceName:  "GDELT Global Macro & Geo",
			Category:    CategoryEnergy,
			Title:       "Urgent Strait of Hormuz tanker advisory pushes Brent crude and Gold higher",
			Summary:     "OIL and XAU bid rises sharply on Middle East shipping risk",
			URL:         "https://example.org/intel/hormuz-oil",
			PublishedAt: nowSec - 600,
			IngestedAt:  nowSec,
		},
		{
			ID:          "fix-apac-semi",
			SourceID:    "gdelt-global",
			SourceName:  "GDELT Global Macro & Geo",
			Category:    CategoryGeopolitics,
			Title:       "Taipei and Tokyo semiconductor export talks spur NVDA volatility",
			Summary:     "APAC semiconductor supply update impacts QQQ and NVDA",
			URL:         "https://example.org/intel/apac-semi",
			PublishedAt: nowSec - 3200,
			IngestedAt:  nowSec,
		},
		{
			ID:          "fix-seismic-jp",
			SourceID:    "usgs-quakes",
			SourceName:  "USGS M4.5+ Seismic",
			Category:    CategorySeismic,
			Title:       "M 5.8 - 62 km E of Tokyo, Japan offshore tremor monitored",
			Summary:     "M5.8 Tokyo coastal tremor with normal infrastructure operations",
			URL:         "https://earthquake.usgs.gov/earthquakes/map/",
			PublishedAt: nowSec - 4200,
			IngestedAt:  nowSec,
			Verified:    true,
		},
	}

	scored := make([]IntelEvent, 0, len(rawEvents))
	for _, ev := range rawEvents {
		reg, cc, lat, lon := InferLocation(ev.Title+" "+ev.Summary, RegionGlobal)
		ev.Region = reg
		ev.CountryCode = cc
		ev.Lat = lat
		ev.Lon = lon
		ev.Tickers = ExtractTickers(ev.Title + " " + ev.Summary)
		scored = append(scored, ScoreEvent(ctx, s.scorer, ev))
	}
	if err := s.store.upsertEvents(ctx, scored); err != nil {
		return err
	}

	preds := []PredictionMarket{
		{
			ID:        "poly-fed-cut-25",
			Venue:     VenuePolymarket,
			Slug:      "fed-cut-25bps-q3",
			Question:  "Will Fed cut interest rates by 25bps before Q3?",
			Category:  CategoryMacro,
			YesProb:   0.62,
			NoProb:    0.38,
			Volume24h: 1840000,
			Liquidity: 420000,
			Change24h: 0.04,
			URL:       "https://polymarket.com/event/fed-cut-25bps-q3",
			EndDate:   "2027-09-30",
			Tickers:   []string{"DXY", "TLT"},
			Region:    RegionNA,
			Lat:       38.8921,
			Lon:       -77.0459,
			UpdatedAt: nowSec,
		},
		{
			ID:        "kalshi-fed-cut-25",
			Venue:     VenueKalshi,
			Slug:      "KXFED-25BPS-Q3",
			Question:  "Will Fed cut interest rates by 25bps before Q3?",
			Category:  CategoryMacro,
			YesProb:   0.48,
			NoProb:    0.52,
			Volume24h: 920000,
			Liquidity: 265000,
			Change24h: -0.02,
			URL:       "https://kalshi.com/markets/kxfed-25bps-q3",
			EndDate:   "2027-09-30",
			Tickers:   []string{"DXY", "TLT"},
			Region:    RegionNA,
			Lat:       38.8921,
			Lon:       -77.0459,
			UpdatedAt: nowSec,
		},
		{
			ID:        "poly-btc-150k",
			Venue:     VenuePolymarket,
			Slug:      "bitcoin-above-150k",
			Question:  "Will Bitcoin reach $150k before year end?",
			Category:  CategoryCrypto,
			YesProb:   0.41,
			NoProb:    0.59,
			Volume24h: 2410000,
			Liquidity: 610000,
			Change24h: 0.06,
			URL:       "https://polymarket.com/event/bitcoin-above-150k",
			EndDate:   "2027-12-31",
			Tickers:   []string{"BTC"},
			Region:    RegionGlobal,
			Lat:       40.7128,
			Lon:       -74.0060,
			UpdatedAt: nowSec,
		},
	}
	return s.store.upsertPredictions(ctx, preds)
}

// ListSources returns built-in and custom WorldIntel sources.
func (s *Service) ListSources(ctx context.Context) ([]SourceConfig, error) {
	return s.store.listSources(ctx)
}

// SaveSource validates and persists a WorldIntel feed or local sidecar configuration.
func (s *Service) SaveSource(ctx context.Context, in SourceInput) (SourceConfig, error) {
	return s.store.saveSource(ctx, in)
}

// DeleteSource deletes a custom non-built-in source configuration.
func (s *Service) DeleteSource(ctx context.Context, id string) (bool, error) {
	return s.store.deleteSource(ctx, id)
}

// ListEvents queries scored global events matching optional filters.
func (s *Service) ListEvents(ctx context.Context, filter EventFilter) ([]IntelEvent, error) {
	if err := s.ensureFixtureSeed(ctx); err != nil {
		return nil, err
	}
	return s.store.listEvents(ctx, filter)
}

// GetMapSummary returns geo-pinned events, multi-source clusters, and regional counts.
func (s *Service) GetMapSummary(ctx context.Context, category string, minUrgency float64, hours int) (MapSummary, error) {
	if err := s.ensureFixtureSeed(ctx); err != nil {
		return MapSummary{}, err
	}
	var since int64
	if hours > 0 {
		if hours > 720 {
			hours = 720
		}
		since = s.now().UTC().Unix() - int64(hours)*3600
	}
	events, err := s.store.listEvents(ctx, EventFilter{
		Category:   category,
		MinUrgency: minUrgency,
		Since:      since,
		Limit:      250,
	})
	if err != nil {
		return MapSummary{}, err
	}
	regionCounts := map[string]int{}
	for _, ev := range events {
		regionCounts[ev.Region]++
	}
	return MapSummary{
		Events:       events,
		Clusters:     BuildClusters(events),
		RegionCounts: regionCounts,
		UpdatedAt:    s.now().UTC().Unix(),
	}, nil
}

// ListPredictions queries active prediction markets and computes Polymarket vs Kalshi spreads.
func (s *Service) ListPredictions(ctx context.Context, filter PredictionFilter) (PredictionList, error) {
	if err := s.ensureFixtureSeed(ctx); err != nil {
		return PredictionList{}, err
	}
	markets, err := s.store.listPredictions(ctx, filter)
	if err != nil {
		return PredictionList{}, err
	}
	allForArb := markets
	if strings.TrimSpace(filter.Venue) != "" {
		if all, aErr := s.store.listPredictions(ctx, PredictionFilter{Category: filter.Category, Query: filter.Query, Limit: 250}); aErr == nil {
			allForArb = all
		}
	}
	return PredictionList{
		Markets:   markets,
		Arbitrage: FindPredictionArbitrage(allForArb),
		Count:     len(markets),
	}, nil
}

// Sync fetches, parses, enriches, scores, persists, and broadcasts updates for one or all enabled sources.
func (s *Service) Sync(ctx context.Context, sourceID string) (SyncResult, error) {
	nowSec := s.now().UTC().Unix()
	var targets []SourceConfig

	trimmedID := strings.TrimSpace(sourceID)
	if trimmedID != "" {
		src, err := s.store.getSource(ctx, trimmedID)
		if err != nil {
			return SyncResult{}, err
		}
		if !src.Enabled {
			return SyncResult{}, fmt.Errorf("intel source %q is disabled", src.ID)
		}
		targets = []SourceConfig{src}
	} else {
		all, err := s.store.listSources(ctx)
		if err != nil {
			return SyncResult{}, err
		}
		for _, src := range all {
			if src.Enabled {
				targets = append(targets, src)
			}
		}
	}

	if err := s.ensureFixtureSeed(ctx); err != nil {
		return SyncResult{}, err
	}

	res := SyncResult{
		SyncedAt: nowSec,
	}
	var errs map[string]string

	for _, src := range targets {
		if s.fixtureMode && (s.httpTransport == nil || isFixtureEndpoint(src.Endpoint)) {
			evCount, predCount, fErr := s.syncFixtureSource(ctx, src, nowSec)
			if fErr != nil {
				if errs == nil {
					errs = map[string]string{}
				}
				errs[src.ID] = fErr.Error()
				_ = s.store.markSourceSync(ctx, src.ID, nowSec, fErr.Error())
				continue
			}
			res.SyncedSources++
			res.EventCount += evCount
			res.PredictionCount += predCount
			_ = s.store.markSourceSync(ctx, src.ID, nowSec, "")
			continue
		}

		raw, fErr := fetchSourceBytes(ctx, s.httpTransport, src)
		if fErr != nil {
			if errs == nil {
				errs = map[string]string{}
			}
			errs[src.ID] = fErr.Error()
			_ = s.store.markSourceSync(ctx, src.ID, nowSec, fErr.Error())
			continue
		}

		evCount, predCount, pErr := s.ingestPayload(ctx, src, raw, nowSec)
		if pErr != nil {
			if errs == nil {
				errs = map[string]string{}
			}
			errs[src.ID] = pErr.Error()
			_ = s.store.markSourceSync(ctx, src.ID, nowSec, pErr.Error())
			continue
		}
		res.SyncedSources++
		res.EventCount += evCount
		res.PredictionCount += predCount
		_ = s.store.markSourceSync(ctx, src.ID, nowSec, "")
	}

	if len(errs) > 0 {
		res.Errors = errs
	}
	return res, nil
}

func (s *Service) syncFixtureSource(ctx context.Context, src SourceConfig, nowSec int64) (int, int, error) {
	switch src.Kind {
	case KindPolymarket, KindKalshi:
		preds, err := s.store.listPredictions(ctx, PredictionFilter{Venue: src.Kind, Limit: 20})
		if err != nil {
			return 0, 0, err
		}
		for i := range preds {
			copyPred := preds[i]
			s.bus.Publish(StreamEvent{Type: "prediction", Timestamp: nowSec, Prediction: &copyPred})
		}
		return 0, len(preds), nil
	default:
		ev := IntelEvent{
			ID:          makeEventID(src.ID, src.Endpoint, "fixture-sync"),
			SourceID:    src.ID,
			SourceName:  src.Name,
			Category:    src.Category,
			Title:       fmt.Sprintf("%s monitored update in global markets", src.Name),
			Summary:     fmt.Sprintf("Fixture sync snapshot for %s (%s)", src.ID, src.Kind),
			URL:         ValidateEventURL(src.Endpoint),
			PublishedAt: nowSec,
			IngestedAt:  nowSec,
			Verified:    src.BuiltIn,
		}
		reg, cc, lat, lon := InferLocation(ev.Title, src.Region)
		ev.Region = reg
		ev.CountryCode = cc
		ev.Lat = lat
		ev.Lon = lon
		ev.Tickers = ExtractTickers(ev.Title)
		ev = ScoreEvent(ctx, s.scorer, ev)
		if err := s.store.upsertEvents(ctx, []IntelEvent{ev}); err != nil {
			return 0, 0, err
		}
		s.bus.Publish(StreamEvent{Type: "event", Timestamp: nowSec, Event: &ev})
		return 1, 0, nil
	}
}

func (s *Service) ingestPayload(ctx context.Context, src SourceConfig, raw []byte, nowSec int64) (int, int, error) {
	switch src.Kind {
	case KindPolymarket:
		preds, err := ParsePolymarketJSON(raw, nowSec)
		if err != nil {
			return 0, 0, err
		}
		if err := s.store.upsertPredictions(ctx, preds); err != nil {
			return 0, 0, err
		}
		for i := range preds {
			copyPred := preds[i]
			s.bus.Publish(StreamEvent{Type: "prediction", Timestamp: nowSec, Prediction: &copyPred})
		}
		return 0, len(preds), nil

	case KindKalshi:
		preds, err := ParseKalshiJSON(raw, nowSec)
		if err != nil {
			return 0, 0, err
		}
		if err := s.store.upsertPredictions(ctx, preds); err != nil {
			return 0, 0, err
		}
		for i := range preds {
			copyPred := preds[i]
			s.bus.Publish(StreamEvent{Type: "prediction", Timestamp: nowSec, Prediction: &copyPred})
		}
		return 0, len(preds), nil

	case KindGDELT:
		events, err := ParseGDELTJSON(raw, src, nowSec)
		if err != nil {
			return 0, 0, err
		}
		return s.scoreAndStoreEvents(ctx, events, src.BuiltIn, nowSec)

	case KindUSGS:
		events, err := ParseUSGSGeoJSON(raw, src, nowSec)
		if err != nil {
			return 0, 0, err
		}
		return s.scoreAndStoreEvents(ctx, events, true, nowSec)

	case KindRSS:
		events, err := ParseRSSOrAtom(raw, src, nowSec)
		if err != nil {
			return 0, 0, err
		}
		return s.scoreAndStoreEvents(ctx, events, src.BuiltIn, nowSec)

	case KindSidecar:
		events, err := ParseWorldMonitorSidecarJSON(raw, src, nowSec)
		if err != nil {
			return 0, 0, err
		}
		return s.scoreAndStoreEvents(ctx, events, false, nowSec)

	default:
		return 0, 0, fmt.Errorf("unsupported source kind %q", src.Kind)
	}
}

func (s *Service) scoreAndStoreEvents(ctx context.Context, events []IntelEvent, verified bool, nowSec int64) (int, int, error) {
	if len(events) == 0 {
		return 0, 0, nil
	}
	scored := make([]IntelEvent, 0, len(events))
	for _, ev := range events {
		ev.Verified = verified
		scored = append(scored, ScoreEvent(ctx, s.scorer, ev))
	}
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].PublishedAt > scored[j].PublishedAt
	})
	if err := s.store.upsertEvents(ctx, scored); err != nil {
		return 0, 0, err
	}
	for i := range scored {
		copyEv := scored[i]
		s.bus.Publish(StreamEvent{Type: "event", Timestamp: nowSec, Event: &copyEv})
	}
	return len(scored), 0, nil
}
