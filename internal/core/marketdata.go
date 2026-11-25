package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/marketdata"
)

func (s *Store) loadedMarketDataService() *marketdata.Service {
	if s == nil {
		return nil
	}
	s.marketDataMu.Lock()
	defer s.marketDataMu.Unlock()
	return s.marketData
}

func (s *Store) marketDataService() (*marketdata.Service, error) {
	if s == nil {
		return nil, errors.New("workspace store unavailable")
	}
	s.marketDataMu.Lock()
	defer s.marketDataMu.Unlock()
	if s.marketData != nil {
		return s.marketData, nil
	}
	svc, err := marketdata.NewService(marketdata.Config{
		DataDir:       s.dir,
		FixtureMode:   s.providerFixture,
		Now:           s.now,
		HTTPTransport: s.marketDataTransport,
	})
	if err != nil {
		return nil, err
	}
	s.marketData = svc
	return svc, nil
}

func queryPositiveInt(raw string, def, maxVal int) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return def, nil
	}
	v, err := strconv.Atoi(trimmed)
	if err != nil || v <= 0 || v > maxVal {
		return 0, fmt.Errorf("parameter must be an integer between 1 and %d", maxVal)
	}
	return v, nil
}

func queryOptionalFloat(raw string) (float64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	v, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || v < 0 {
		return 0, errors.New("numeric parameter must be non-negative")
	}
	return v, nil
}

func (h *HTTPHandler) marketDataAPI(w http.ResponseWriter, r *http.Request, token string) {
	if !h.allowRate(r, "marketdata", 180) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	svc, err := h.store.marketDataService()
	if err != nil {
		writeError(w, 500, "market data service unavailable")
		return
	}

	rel := strings.TrimPrefix(r.URL.Path, "/api/v1/market/")
	q := r.URL.Query()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	switch {
	case rel == "instruments":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		items, err := svc.ListInstruments(ctx, q.Get("venue"))
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"instruments": items, "count": len(items)})

	case rel == "candles":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		limit, err := queryPositiveInt(q.Get("limit"), 200, 2000)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		brickSize, err := queryOptionalFloat(q.Get("brickSize"))
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		rangeSize, err := queryOptionalFloat(q.Get("rangeSize"))
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		reversal, err := queryOptionalFloat(q.Get("reversal"))
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		boxSize, err := queryOptionalFloat(q.Get("boxSize"))
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		lineBreakCount, err := queryPositiveInt(q.Get("lineBreakCount"), 3, 20)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		tickCount, err := queryPositiveInt(q.Get("tickCount"), 50, 5000)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		volThresh, err := queryOptionalFloat(q.Get("volumeThreshold"))
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		series, err := svc.GetCandles(ctx, q.Get("venue"), q.Get("symbol"), q.Get("interval"), marketdata.TransformOptions{
			Style:           q.Get("style"),
			BrickSize:       brickSize,
			RangeSize:       rangeSize,
			Reversal:        reversal,
			BoxSize:         boxSize,
			LineBreakCount:  lineBreakCount,
			TickCount:       tickCount,
			VolumeThreshold: volThresh,
		}, limit)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, series)

	case rel == "orderbook":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		limit, err := queryPositiveInt(q.Get("limit"), 20, 100)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		book, err := svc.GetOrderBook(ctx, q.Get("venue"), q.Get("symbol"), limit)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, book)

	case rel == "trades":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		limit, err := queryPositiveInt(q.Get("limit"), 60, 200)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		trades, err := svc.GetTrades(ctx, q.Get("venue"), q.Get("symbol"), limit)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{
			"venue":  strings.ToLower(strings.TrimSpace(q.Get("venue"))),
			"symbol": strings.TrimSpace(q.Get("symbol")),
			"trades": trades,
			"count":  len(trades),
		})

	case rel == "footprint":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		limit, err := queryPositiveInt(q.Get("limit"), 48, 200)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		stepSize, err := queryOptionalFloat(q.Get("stepSize"))
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		fp, err := svc.GetFootprint(ctx, q.Get("venue"), q.Get("symbol"), q.Get("interval"), stepSize, limit)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, fp)

	case rel == "macro":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		limit, err := queryPositiveInt(q.Get("limit"), 120, 500)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		pts, err := svc.GetMacroSeries(ctx, q.Get("seriesId"), limit)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"seriesId": strings.ToUpper(strings.TrimSpace(q.Get("seriesId"))), "points": pts, "count": len(pts)})

	case rel == "sec":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		limit, err := queryPositiveInt(q.Get("limit"), 20, 100)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		filings, err := svc.GetSECFilings(ctx, q.Get("cik"), limit)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"cik": strings.TrimSpace(q.Get("cik")), "filings": filings, "count": len(filings)})

	case rel == "cohorts":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		snap, err := svc.GetCohorts(ctx, q.Get("window"))
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, snap)

	case rel == "watchlists":
		if r.Method == http.MethodGet {
			items, err := svc.ListWatchlists(ctx)
			if err != nil {
				writeError(w, 500, "watchlists unavailable")
				return
			}
			writeJSON(w, 200, map[string]any{"watchlists": items, "count": len(items)})
			return
		}
		if r.Method != http.MethodPut {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var wl marketdata.Watchlist
		if !decodeJSON(w, r, &wl) {
			return
		}
		saved, err := svc.SaveWatchlist(ctx, wl)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, saved)

	case strings.HasPrefix(rel, "watchlists/"):
		if r.Method != http.MethodDelete {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		id := strings.TrimPrefix(rel, "watchlists/")
		deleted, err := svc.DeleteWatchlist(ctx, id)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if !deleted {
			writeError(w, 404, "watchlist not found")
			return
		}
		writeJSON(w, 200, map[string]any{"deleted": true, "id": id})

	case rel == "stream":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		h.marketDataStreamSSE(w, r, svc)

	default:
		writeError(w, 404, "unknown market operation")
	}
}

func parseStreamSubsParam(raw string) ([]marketdata.Filter, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	parts := strings.Split(trimmed, ",")
	if len(parts) > 16 {
		return nil, errors.New("subs cannot exceed 16 streams")
	}
	filters := make([]marketdata.Filter, 0, len(parts))
	for _, entry := range parts {
		fields := strings.Split(strings.TrimSpace(entry), ":")
		if len(fields) < 2 || len(fields) > 4 {
			return nil, fmt.Errorf("invalid subscription tuple %q", entry)
		}
		v, err := marketdata.NormalizeVenue(fields[0])
		if err != nil {
			return nil, err
		}
		sym, err := marketdata.NormalizeSymbol(fields[1])
		if err != nil {
			return nil, err
		}
		iv := ""
		if len(fields) >= 3 && strings.TrimSpace(fields[2]) != "" {
			iv, err = marketdata.NormalizeInterval(fields[2])
			if err != nil {
				return nil, err
			}
		}
		st := ""
		if len(fields) == 4 && strings.TrimSpace(fields[3]) != "" {
			st, err = marketdata.NormalizeStyle(fields[3])
			if err != nil {
				return nil, err
			}
		}
		filters = append(filters, marketdata.Filter{
			Venue:    v,
			Symbol:   sym,
			Interval: iv,
			Style:    st,
		})
	}
	return filters, nil
}

func (h *HTTPHandler) marketDataStreamSSE(w http.ResponseWriter, r *http.Request, svc *marketdata.Service) {
	q := r.URL.Query()
	venue := strings.ToLower(strings.TrimSpace(q.Get("venue")))
	symbol := strings.TrimSpace(q.Get("symbol"))
	interval := strings.ToLower(strings.TrimSpace(q.Get("interval")))
	style := strings.ToLower(strings.TrimSpace(q.Get("style")))
	noSnapshot := q.Get("noSnapshot") == "1" || strings.EqualFold(q.Get("noSnapshot"), "true")
	subsParam := strings.TrimSpace(q.Get("subs"))

	filters, err := parseStreamSubsParam(subsParam)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if len(filters) == 0 {
		if venue != "" {
			var err error
			venue, err = marketdata.NormalizeVenue(venue)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
		}
		if symbol != "" {
			var err error
			symbol, err = marketdata.NormalizeSymbol(symbol)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
		}
		if interval != "" {
			var err error
			interval, err = marketdata.NormalizeInterval(interval)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
		}
		if style != "" {
			var err error
			style, err = marketdata.NormalizeStyle(style)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
		}
		filters = []marketdata.Filter{{
			Venue:    venue,
			Symbol:   symbol,
			Interval: interval,
			Style:    style,
		}}
	}

	var initial *marketdata.CandleSeries
	if len(filters) == 1 && subsParam == "" && !noSnapshot && venue != "" && symbol != "" {
		snapCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		series, err := svc.GetCandles(snapCtx, venue, symbol, interval, marketdata.TransformOptions{Style: style}, 100)
		cancel()
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		initial = &series
	}

	busFilters := make([]marketdata.Filter, 0, len(filters))
	derivedStyles := make(map[string][]string)
	wantStandard := make(map[string]bool)
	styleKeyFor := func(v, s, iv string) string {
		return strings.ToLower(strings.TrimSpace(v)) + "|" + strings.ToUpper(strings.TrimSpace(s)) + "|" + strings.ToLower(strings.TrimSpace(iv))
	}
	for _, f := range filters {
		k := styleKeyFor(f.Venue, f.Symbol, f.Interval)
		if f.Style != "" && f.Style != marketdata.StyleStandard && f.Style != marketdata.StyleTick && f.Style != marketdata.StyleVolume {
			list := derivedStyles[k]
			exists := false
			for _, item := range list {
				if item == f.Style {
					exists = true
					break
				}
			}
			if !exists {
				derivedStyles[k] = append(list, f.Style)
			}
			f.Style = marketdata.StyleStandard
		} else if f.Style == marketdata.StyleStandard || f.Style == "" {
			wantStandard[k] = true
		}
		busFilters = append(busFilters, f)
	}

	subCh, unsubscribe := svc.SubscribeMulti(busFilters)
	defer unsubscribe()

	var releaseFeeds []func()
	acquired := make(map[string]bool, len(filters))
	for _, f := range filters {
		if f.Venue == "" || f.Symbol == "" {
			continue
		}
		key := f.Venue + ":" + strings.ToUpper(f.Symbol)
		if acquired[key] {
			continue
		}
		acquired[key] = true
		releaseFeeds = append(releaseFeeds, svc.AcquireFeed(f.Venue, f.Symbol))
	}
	defer func() {
		for _, release := range releaseFeeds {
			release()
		}
	}()

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	writeSSE := func(eventName string, payload any) bool {
		raw, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, raw); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}

	if initial != nil {
		if !writeSSE("snapshot", initial) {
			return
		}
	} else {
		if !writeSSE("ready", map[string]any{"status": "connected", "venue": venue, "symbol": symbol}) {
			return
		}
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if !writeSSE("heartbeat", map[string]any{"timestamp": time.Now().UTC().UnixMilli()}) {
				return
			}
		case evt, ok := <-subCh:
			if !ok {
				return
			}
			evtName := evt.Type
			if evtName == "" {
				evtName = "update"
			}
			if evt.Type == "candle" && evt.Style == marketdata.StyleStandard {
				ek := styleKeyFor(evt.Venue, evt.Symbol, evt.Interval)
				styles := derivedStyles[ek]
				if len(derivedStyles) == 0 || len(styles) == 0 || wantStandard[ek] {
					if !writeSSE(evtName, evt) {
						return
					}
				}
				for _, reqStyle := range styles {
					cached, has := svc.CachedCandles(evt.Venue, evt.Symbol, evt.Interval, 160)
					if !has || len(cached) == 0 {
						continue
					}
					transformed, tErr := marketdata.TransformCandles(cached, marketdata.TransformOptions{Style: reqStyle})
					if tErr != nil || len(transformed) == 0 {
						continue
					}
					derivedEvt := evt
					derivedEvt.Style = reqStyle
					derivedEvt.Payload = transformed[len(transformed)-1]
					if !writeSSE(evtName, derivedEvt) {
						return
					}
				}
				continue
			}
			if !writeSSE(evtName, evt) {
				return
			}
		}
	}
}

func toolGetMarketCandles(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var args struct {
		Venue    string `json:"venue"`
		Symbol   string `json:"symbol"`
		Interval string `json:"interval"`
		Style    string `json:"style"`
		Limit    int    `json:"limit"`
	}
	if err := decodeToolArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.Venue) == "" {
		args.Venue = marketdata.VenueHyperliquid
	}
	if strings.TrimSpace(args.Symbol) == "" {
		return nil, errors.New("get_market_candles requires symbol")
	}
	svc, err := s.marketDataService()
	if err != nil {
		return nil, err
	}
	return svc.GetCandles(ctx, args.Venue, args.Symbol, args.Interval, marketdata.TransformOptions{Style: args.Style}, args.Limit)
}

func toolGetOrderBook(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var args struct {
		Venue  string `json:"venue"`
		Symbol string `json:"symbol"`
		Limit  int    `json:"limit"`
	}
	if err := decodeToolArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.Venue) == "" {
		args.Venue = marketdata.VenueHyperliquid
	}
	if strings.TrimSpace(args.Symbol) == "" {
		return nil, errors.New("get_orderbook requires symbol")
	}
	svc, err := s.marketDataService()
	if err != nil {
		return nil, err
	}
	return svc.GetOrderBook(ctx, args.Venue, args.Symbol, args.Limit)
}

func toolGetFootprint(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var args struct {
		Venue    string  `json:"venue"`
		Symbol   string  `json:"symbol"`
		Interval string  `json:"interval"`
		StepSize float64 `json:"stepSize"`
		Limit    int     `json:"limit"`
	}
	if err := decodeToolArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.Venue) == "" {
		args.Venue = marketdata.VenueHyperliquid
	}
	if strings.TrimSpace(args.Symbol) == "" {
		return nil, errors.New("get_footprint requires symbol")
	}
	svc, err := s.marketDataService()
	if err != nil {
		return nil, err
	}
	return svc.GetFootprint(ctx, args.Venue, args.Symbol, args.Interval, args.StepSize, args.Limit)
}

func toolGetMacroOrSEC(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var args struct {
		Kind     string `json:"kind"`
		SeriesID string `json:"seriesId"`
		CIK      string `json:"cik"`
		Window   string `json:"window"`
		Limit    int    `json:"limit"`
	}
	if err := decodeToolArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	svc, err := s.marketDataService()
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(args.Kind)) {
	case "macro", "fred", "":
		if strings.TrimSpace(args.SeriesID) == "" && strings.TrimSpace(args.CIK) != "" {
			filings, err := svc.GetSECFilings(ctx, args.CIK, args.Limit)
			if err != nil {
				return nil, err
			}
			return map[string]any{"kind": "sec", "cik": args.CIK, "filings": filings, "count": len(filings)}, nil
		}
		pts, err := svc.GetMacroSeries(ctx, args.SeriesID, args.Limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"kind": "macro", "seriesId": strings.ToUpper(strings.TrimSpace(args.SeriesID)), "points": pts, "count": len(pts)}, nil
	case "sec", "edgar":
		filings, err := svc.GetSECFilings(ctx, args.CIK, args.Limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"kind": "sec", "cik": args.CIK, "filings": filings, "count": len(filings)}, nil
	case "cohorts", "leaderboard":
		return svc.GetCohorts(ctx, args.Window)
	default:
		return nil, fmt.Errorf("unsupported kind %q (expected macro, sec, or cohorts)", args.Kind)
	}
}
