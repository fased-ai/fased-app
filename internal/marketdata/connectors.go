package marketdata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

func parseFloatStrict(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		if isFiniteNonNeg(v) {
			return v, true
		}
	case json.Number:
		f, err := v.Float64()
		if err == nil && isFiniteNonNeg(f) {
			return f, true
		}
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err == nil && isFiniteNonNeg(f) {
			return f, true
		}
	}
	return 0, false
}

func parseSignedFloatStrict(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			return v, true
		}
	case json.Number:
		f, err := v.Float64()
		if err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f, true
		}
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f, true
		}
	}
	return 0, false
}

func normalizeCoinBase(symbol string) string {
	s := strings.ToUpper(strings.TrimSpace(symbol))
	for _, suf := range []string{"-PERP", "-USD", "/USDT", "/USD", "USDT", "USDC"} {
		if strings.HasSuffix(s, suf) && len(s) > len(suf) {
			s = strings.TrimSuffix(s, suf)
			break
		}
	}
	return s
}

func venueQuerySymbol(venue, symbol string) string {
	s := strings.TrimSpace(symbol)
	switch venue {
	case VenueHyperliquid, VenueLighter:
		return normalizeCoinBase(s)
	case VenueBinance, VenueBybit:
		up := strings.ToUpper(s)
		up = strings.ReplaceAll(up, "/", "")
		up = strings.ReplaceAll(up, "-PERP", "USDT")
		if !strings.HasSuffix(up, "USDT") && !strings.HasSuffix(up, "USDC") && !strings.HasSuffix(up, "USD") {
			up += "USDT"
		}
		return up
	case VenueKraken:
		up := strings.ToUpper(strings.ReplaceAll(s, "/", ""))
		if up == "BTC" || up == "BTCUSD" {
			return "XBTUSD"
		}
		return up
	case VenueOKX:
		base := normalizeCoinBase(s)
		return base + "-USDT-SWAP"
	case VenueCoinbase:
		base := normalizeCoinBase(s)
		return base + "-USD"
	case VenueDeribit:
		base := normalizeCoinBase(s)
		return base + "-PERPETUAL"
	case VenueAlpaca:
		return strings.ToUpper(s)
	default:
		return s
	}
}

// ParseHyperliquidCandles parses Hyperliquid info candleSnapshot JSON arrays.
func ParseHyperliquidCandles(raw []byte) ([]Candle, error) {
	var rows []struct {
		T int64   `json:"t"`
		O string  `json:"o"`
		H string  `json:"h"`
		L string  `json:"l"`
		C string  `json:"c"`
		V string  `json:"v"`
		N int64   `json:"n"`
		X float64 `json:"-"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("decode hyperliquid candles: %w", err)
	}
	out := make([]Candle, 0, len(rows))
	for _, r := range rows {
		o, okO := parseFloatStrict(r.O)
		h, okH := parseFloatStrict(r.H)
		l, okL := parseFloatStrict(r.L)
		c, okC := parseFloatStrict(r.C)
		vol, okV := parseFloatStrict(r.V)
		if !okO || !okH || !okL || !okC || !okV || o <= 0 || h <= 0 || l <= 0 || c <= 0 {
			continue
		}
		ts := r.T
		if ts > 1e12 {
			ts /= 1000
		}
		out = append(out, Candle{
			Time:   ts,
			Open:   o,
			High:   h,
			Low:    l,
			Close:  c,
			Volume: vol,
			Trades: r.N,
		})
	}
	return sanitizeCandles(out)
}

// ParseHyperliquidBook parses Hyperliquid l2Book JSON responses.
func ParseHyperliquidBook(raw []byte, venue, symbol string) (OrderBookSnapshot, error) {
	var resp struct {
		Time   int64 `json:"time"`
		Levels [][]struct {
			Px string `json:"px"`
			Sz string `json:"sz"`
		} `json:"levels"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return OrderBookSnapshot{}, fmt.Errorf("decode hyperliquid l2Book: %w", err)
	}
	if len(resp.Levels) < 2 {
		return OrderBookSnapshot{}, errors.New("hyperliquid l2Book missing bid/ask levels")
	}
	bids := parseBookLevels(resp.Levels[0], true)
	asks := parseBookLevels(resp.Levels[1], false)
	return buildOrderBookSnapshot(venue, symbol, resp.Time, bids, asks, VenueHyperliquid), nil
}

func parseBookLevels(raw []struct {
	Px string `json:"px"`
	Sz string `json:"sz"`
}, descending bool) []OrderBookLevel {
	out := make([]OrderBookLevel, 0, len(raw))
	var running float64
	for _, lv := range raw {
		px, okP := parseFloatStrict(lv.Px)
		sz, okS := parseFloatStrict(lv.Sz)
		if !okP || !okS || px <= 0 {
			continue
		}
		out = append(out, OrderBookLevel{Price: px, Size: sz})
	}
	sort.Slice(out, func(i, j int) bool {
		if descending {
			return out[i].Price > out[j].Price
		}
		return out[i].Price < out[j].Price
	})
	for i := range out {
		running += out[i].Size
		out[i].Total = running
	}
	return out
}

func buildOrderBookSnapshot(venue, symbol string, ts int64, bids, asks []OrderBookLevel, source string) OrderBookSnapshot {
	var spread, mid float64
	if len(bids) > 0 && len(asks) > 0 {
		spread = asks[0].Price - bids[0].Price
		mid = (asks[0].Price + bids[0].Price) / 2.0
	}
	return OrderBookSnapshot{
		Venue:    venue,
		Symbol:   symbol,
		Time:     ts,
		Bids:     bids,
		Asks:     asks,
		Spread:   spread,
		MidPrice: mid,
		Source:   source,
	}
}

type leaderboardRowPayload struct {
	EthAddress         string          `json:"ethAddress"`
	DisplayName        string          `json:"displayName"`
	AccountValue       string          `json:"accountValue"`
	WindowPerformances [][]interface{} `json:"windowPerformances"`
}

func cohortEntryFromRow(row leaderboardRowPayload, rank int) (CohortEntry, bool) {
	if strings.TrimSpace(row.EthAddress) == "" {
		return CohortEntry{}, false
	}
	acctVal, _ := parseFloatStrict(row.AccountValue)
	entry := CohortEntry{
		Address:      row.EthAddress,
		DisplayName:  strings.TrimSpace(row.DisplayName),
		AccountValue: acctVal,
		Rank:         rank,
	}
	for _, wp := range row.WindowPerformances {
		if len(wp) < 2 {
			continue
		}
		key, _ := wp[0].(string)
		statsMap, _ := wp[1].(map[string]any)
		if statsMap == nil {
			continue
		}
		roi, _ := parseSignedFloatStrict(statsMap["roi"])
		switch key {
		case "day":
			entry.DayROI = roi
		case "week":
			entry.WeekROI = roi
		case "month":
			entry.MonthROI = roi
		case "allTime":
			entry.AllTimeROI = roi
		}
	}
	return entry, true
}

func cohortROIForWindow(entry CohortEntry, window string) float64 {
	switch window {
	case "day":
		return entry.DayROI
	case "month":
		return entry.MonthROI
	case "allTime":
		return entry.AllTimeROI
	default:
		return entry.WeekROI
	}
}

func finalizeCohortSnapshot(entries []CohortEntry, window string, updatedAt int64, source string, fixture bool) CohortSnapshot {
	sort.SliceStable(entries, func(i, j int) bool {
		ri := cohortROIForWindow(entries[i], window)
		rj := cohortROIForWindow(entries[j], window)
		if ri != rj {
			return ri > rj
		}
		return entries[i].AccountValue > entries[j].AccountValue
	})
	for i := range entries {
		entries[i].Rank = i + 1
	}
	return CohortSnapshot{
		Venue:     VenueHyperliquid,
		Window:    window,
		UpdatedAt: updatedAt,
		Entries:   entries,
		Source:    source,
		Fixture:   fixture,
	}
}

// ParseHyperliquidLeaderboardReader streams a Hyperliquid leaderboard JSON response
// and stops once 50 valid ranked entries have been collected.
func ParseHyperliquidLeaderboardReader(r io.Reader, window string, updatedAt int64) (CohortSnapshot, error) {
	if norm, err := NormalizeCohortWindow(window); err == nil {
		window = norm
	} else {
		window = "week"
	}
	dec := json.NewDecoder(r)
	tok, err := dec.Token()
	if err != nil {
		return CohortSnapshot{}, fmt.Errorf("decode hyperliquid leaderboard: %w", err)
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return CohortSnapshot{}, fmt.Errorf("decode hyperliquid leaderboard: expected object")
	}

	entries := make([]CohortEntry, 0, 50)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return CohortSnapshot{}, fmt.Errorf("decode hyperliquid leaderboard: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return CohortSnapshot{}, fmt.Errorf("decode hyperliquid leaderboard: expected field name")
		}
		if key != "leaderboardRows" {
			var discard json.RawMessage
			if err := dec.Decode(&discard); err != nil {
				return CohortSnapshot{}, fmt.Errorf("decode hyperliquid leaderboard: %w", err)
			}
			continue
		}

		arrTok, err := dec.Token()
		if err != nil {
			return CohortSnapshot{}, fmt.Errorf("decode hyperliquid leaderboard: %w", err)
		}
		if arrTok == nil {
			continue
		}
		arrDelim, ok := arrTok.(json.Delim)
		if !ok || arrDelim != '[' {
			return CohortSnapshot{}, fmt.Errorf("decode hyperliquid leaderboard: expected leaderboardRows array")
		}
		rowIndex := 0
		for dec.More() {
			var row leaderboardRowPayload
			if err := dec.Decode(&row); err != nil {
				return CohortSnapshot{}, fmt.Errorf("decode hyperliquid leaderboard: %w", err)
			}
			rowIndex++
			if entry, keep := cohortEntryFromRow(row, rowIndex); keep {
				entries = append(entries, entry)
				if len(entries) >= 50 {
					return finalizeCohortSnapshot(entries, window, updatedAt, "hyperliquid-leaderboard", false), nil
				}
			}
		}
		endArr, err := dec.Token()
		if err != nil {
			return CohortSnapshot{}, fmt.Errorf("decode hyperliquid leaderboard: %w", err)
		}
		if d, ok := endArr.(json.Delim); !ok || d != ']' {
			return CohortSnapshot{}, fmt.Errorf("decode hyperliquid leaderboard: expected end of leaderboardRows")
		}
	}
	endObj, err := dec.Token()
	if err != nil {
		return CohortSnapshot{}, fmt.Errorf("decode hyperliquid leaderboard: %w", err)
	}
	if d, ok := endObj.(json.Delim); !ok || d != '}' {
		return CohortSnapshot{}, fmt.Errorf("decode hyperliquid leaderboard: expected closing object")
	}

	return finalizeCohortSnapshot(entries, window, updatedAt, "hyperliquid-leaderboard", false), nil
}

// ParseHyperliquidLeaderboard extracts ranked cohorts for a given window ("day", "week", "month", "allTime").
func ParseHyperliquidLeaderboard(raw []byte, window string, updatedAt int64) (CohortSnapshot, error) {
	return ParseHyperliquidLeaderboardReader(bytes.NewReader(raw), window, updatedAt)
}

// ParseBinanceKlines parses Binance REST /api/v3/klines nested arrays.
func ParseBinanceKlines(raw []byte) ([]Candle, error) {
	var rows [][]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("decode binance klines: %w", err)
	}
	out := make([]Candle, 0, len(rows))
	for _, r := range rows {
		if len(r) < 6 {
			continue
		}
		tsMs, okT := parseFloatStrict(r[0])
		o, okO := parseFloatStrict(r[1])
		h, okH := parseFloatStrict(r[2])
		l, okL := parseFloatStrict(r[3])
		c, okC := parseFloatStrict(r[4])
		vol, okV := parseFloatStrict(r[5])
		if !okT || !okO || !okH || !okL || !okC || !okV || o <= 0 || h <= 0 || l <= 0 || c <= 0 {
			continue
		}
		var trades int64
		if len(r) > 8 {
			if tr, ok := parseFloatStrict(r[8]); ok {
				trades = int64(tr)
			}
		}
		var buyVol float64
		if len(r) > 9 {
			buyVol, _ = parseFloatStrict(r[9])
		}
		sellVol := vol - buyVol
		if sellVol < 0 {
			sellVol = 0
		}
		out = append(out, Candle{
			Time:       int64(tsMs) / 1000,
			Open:       o,
			High:       h,
			Low:        l,
			Close:      c,
			Volume:     vol,
			BuyVolume:  buyVol,
			SellVolume: sellVol,
			Trades:     trades,
		})
	}
	return sanitizeCandles(out)
}

// ParseBinanceDepth parses Binance REST /api/v3/depth snapshots.
func ParseBinanceDepth(raw []byte, venue, symbol string, nowMs int64) (OrderBookSnapshot, error) {
	var resp struct {
		Bids [][]string `json:"bids"`
		Asks [][]string `json:"asks"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return OrderBookSnapshot{}, fmt.Errorf("decode binance depth: %w", err)
	}
	bids := parseStringPairs(resp.Bids, true)
	asks := parseStringPairs(resp.Asks, false)
	return buildOrderBookSnapshot(venue, symbol, nowMs, bids, asks, VenueBinance), nil
}

func parseStringPairs(pairs [][]string, descending bool) []OrderBookLevel {
	out := make([]OrderBookLevel, 0, len(pairs))
	for _, p := range pairs {
		if len(p) < 2 {
			continue
		}
		px, okP := parseFloatStrict(p[0])
		sz, okS := parseFloatStrict(p[1])
		if !okP || !okS || px <= 0 {
			continue
		}
		out = append(out, OrderBookLevel{Price: px, Size: sz})
	}
	sort.Slice(out, func(i, j int) bool {
		if descending {
			return out[i].Price > out[j].Price
		}
		return out[i].Price < out[j].Price
	})
	var running float64
	for i := range out {
		running += out[i].Size
		out[i].Total = running
	}
	return out
}

// ParseBybitKlines parses Bybit v5 /v5/market/kline JSON payloads.
func ParseBybitKlines(raw []byte) ([]Candle, error) {
	var resp struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List [][]string `json:"list"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode bybit kline: %w", err)
	}
	if resp.RetCode != 0 {
		return nil, fmt.Errorf("bybit API error %d: %s", resp.RetCode, strings.TrimSpace(resp.RetMsg))
	}
	out := make([]Candle, 0, len(resp.Result.List))
	for _, r := range resp.Result.List {
		if len(r) < 6 {
			continue
		}
		tsMs, okT := parseFloatStrict(r[0])
		o, okO := parseFloatStrict(r[1])
		h, okH := parseFloatStrict(r[2])
		l, okL := parseFloatStrict(r[3])
		c, okC := parseFloatStrict(r[4])
		vol, okV := parseFloatStrict(r[5])
		if !okT || !okO || !okH || !okL || !okC || !okV || o <= 0 || h <= 0 || l <= 0 || c <= 0 {
			continue
		}
		out = append(out, Candle{
			Time:   int64(tsMs) / 1000,
			Open:   o,
			High:   h,
			Low:    l,
			Close:  c,
			Volume: vol,
		})
	}
	return sanitizeCandles(out)
}

// ParseBybitOrderBook parses Bybit v5 /v5/market/orderbook JSON payloads.
func ParseBybitOrderBook(raw []byte, venue, symbol string) (OrderBookSnapshot, error) {
	var resp struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			Bids [][]string `json:"b"`
			Asks [][]string `json:"a"`
			Ts   int64      `json:"ts"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return OrderBookSnapshot{}, fmt.Errorf("decode bybit orderbook: %w", err)
	}
	if resp.RetCode != 0 {
		return OrderBookSnapshot{}, fmt.Errorf("bybit API error %d: %s", resp.RetCode, strings.TrimSpace(resp.RetMsg))
	}
	bids := parseStringPairs(resp.Result.Bids, true)
	asks := parseStringPairs(resp.Result.Asks, false)
	return buildOrderBookSnapshot(venue, symbol, resp.Result.Ts, bids, asks, VenueBybit), nil
}

// ParseKrakenOHLC parses Kraken /0/public/OHLC responses.
func ParseKrakenOHLC(raw []byte) ([]Candle, error) {
	var resp struct {
		Error  []string                   `json:"error"`
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode kraken ohlc: %w", err)
	}
	if len(resp.Error) > 0 {
		return nil, fmt.Errorf("kraken API error: %s", strings.Join(resp.Error, "; "))
	}
	var rows [][]any
	for k, v := range resp.Result {
		if k == "last" {
			continue
		}
		if err := json.Unmarshal(v, &rows); err == nil {
			break
		}
	}
	out := make([]Candle, 0, len(rows))
	for _, r := range rows {
		if len(r) < 7 {
			continue
		}
		ts, okT := parseFloatStrict(r[0])
		o, okO := parseFloatStrict(r[1])
		h, okH := parseFloatStrict(r[2])
		l, okL := parseFloatStrict(r[3])
		c, okC := parseFloatStrict(r[4])
		vol, okV := parseFloatStrict(r[6])
		if !okT || !okO || !okH || !okL || !okC || !okV || o <= 0 || h <= 0 || l <= 0 || c <= 0 {
			continue
		}
		var trades int64
		if len(r) > 7 {
			if tr, ok := parseFloatStrict(r[7]); ok {
				trades = int64(tr)
			}
		}
		out = append(out, Candle{
			Time:   int64(ts),
			Open:   o,
			High:   h,
			Low:    l,
			Close:  c,
			Volume: vol,
			Trades: trades,
		})
	}
	return sanitizeCandles(out)
}

// ParseGeckoTerminalOHLCV parses GeckoTerminal v2 OHLCV arrays for Solana pools.
func ParseGeckoTerminalOHLCV(raw []byte) ([]Candle, error) {
	var resp struct {
		Data struct {
			Attributes struct {
				OHLCVList [][]float64 `json:"ohlcv_list"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode geckoterminal ohlcv: %w", err)
	}
	out := make([]Candle, 0, len(resp.Data.Attributes.OHLCVList))
	for _, r := range resp.Data.Attributes.OHLCVList {
		if len(r) < 6 {
			continue
		}
		if !isFinitePositive(r[1]) || !isFinitePositive(r[2]) || !isFinitePositive(r[3]) || !isFinitePositive(r[4]) || !isFiniteNonNeg(r[5]) {
			continue
		}
		out = append(out, Candle{
			Time:   int64(r[0]),
			Open:   r[1],
			High:   r[2],
			Low:    r[3],
			Close:  r[4],
			Volume: r[5],
		})
	}
	return sanitizeCandles(out)
}

func bybitIntervalParam(iv string) string {
	switch iv {
	case "1m":
		return "1"
	case "5m":
		return "5"
	case "15m":
		return "15"
	case "1h":
		return "60"
	case "4h":
		return "240"
	case "1d":
		return "D"
	default:
		return "60"
	}
}

func krakenIntervalParam(iv string) int {
	switch iv {
	case "1m":
		return 1
	case "5m":
		return 5
	case "15m":
		return 15
	case "1h":
		return 60
	case "4h":
		return 240
	case "1d":
		return 1440
	default:
		return 60
	}
}

func geckoTimeframeParams(iv string) (tf string, agg int) {
	switch iv {
	case "1m":
		return "minute", 1
	case "5m":
		return "minute", 5
	case "15m":
		return "minute", 15
	case "1h":
		return "hour", 1
	case "4h":
		return "hour", 4
	case "1d":
		return "day", 1
	default:
		return "hour", 1
	}
}

func (c *upstreamClient) getBinance(ctx context.Context, pathAndQuery string) ([]byte, error) {
	// Prefer Binance's official unauthenticated public market-data mirror first so
	// US-hosted deployments avoid api.binance.com HTTP 451 geo-blocks.
	raw, err := c.get(ctx, "https://data-api.binance.vision"+pathAndQuery)
	if err == nil || ctx.Err() != nil {
		return raw, err
	}
	fallbackRaw, fallbackErr := c.get(ctx, "https://api.binance.com"+pathAndQuery)
	if fallbackErr == nil {
		return fallbackRaw, nil
	}
	return nil, err
}

func (c *upstreamClient) fetchCandles(ctx context.Context, venue, symbol, interval string, startSec, endSec int64, limit int) ([]Candle, error) {
	if limit <= 0 || limit > maxCandlesLimit {
		limit = 200
	}
	qSym := venueQuerySymbol(venue, symbol)
	switch venue {
	case VenueHyperliquid:
		body, err := json.Marshal(map[string]any{
			"type": "candleSnapshot",
			"req": map[string]any{
				"coin":      qSym,
				"interval":  interval,
				"startTime": startSec * 1000,
				"endTime":   endSec * 1000,
			},
		})
		if err != nil {
			return nil, err
		}
		raw, err := c.postJSON(ctx, "https://api.hyperliquid.xyz/info", body)
		if err != nil {
			return nil, err
		}
		return ParseHyperliquidCandles(raw)

	case VenueBinance:
		u := fmt.Sprintf("/api/v3/klines?symbol=%s&interval=%s&limit=%d",
			url.QueryEscape(qSym), url.QueryEscape(interval), minInt(limit, 1000))
		raw, err := c.getBinance(ctx, u)
		if err != nil {
			return nil, err
		}
		return ParseBinanceKlines(raw)

	case VenueBybit:
		u := fmt.Sprintf("https://api.bybit.com/v5/market/kline?category=linear&symbol=%s&interval=%s&limit=%d",
			url.QueryEscape(qSym), url.QueryEscape(bybitIntervalParam(interval)), minInt(limit, 1000))
		raw, err := c.get(ctx, u)
		if err != nil {
			return nil, err
		}
		return ParseBybitKlines(raw)

	case VenueKraken:
		u := fmt.Sprintf("https://api.kraken.com/0/public/OHLC?pair=%s&interval=%d",
			url.QueryEscape(qSym), krakenIntervalParam(interval))
		raw, err := c.get(ctx, u)
		if err != nil {
			return nil, err
		}
		return ParseKrakenOHLC(raw)

	case VenueSolana:
		poolAddr, err := c.resolveSolanaPool(ctx, symbol)
		if err != nil {
			return nil, err
		}
		tf, agg := geckoTimeframeParams(interval)
		u := fmt.Sprintf("https://api.geckoterminal.com/api/v2/networks/solana/pools/%s/ohlcv/%s?aggregate=%d&limit=%d",
			url.PathEscape(poolAddr), url.PathEscape(tf), agg, minInt(limit, 1000))
		raw, err := c.get(ctx, u)
		if err != nil {
			return nil, err
		}
		return ParseGeckoTerminalOHLCV(raw)

	case VenueLighter:
		marketID, err := c.resolveLighterMarketID(ctx, qSym)
		if err != nil {
			return nil, err
		}
		if endSec <= 0 {
			endSec = time.Now().Unix()
		}
		if startSec <= 0 || startSec >= endSec {
			startSec = endSec - int64(limit)*validIntervals[interval]
		}
		u := fmt.Sprintf("https://mainnet.zklighter.elliot.ai/api/v1/candles?market_id=%d&resolution=%s&start_timestamp=%d&end_timestamp=%d&count_back=%d",
			marketID, url.QueryEscape(interval), startSec*1000, endSec*1000, minInt(limit, 1000))
		raw, err := c.get(ctx, u)
		if err != nil {
			return nil, err
		}
		return ParseLighterCandles(raw)

	default:
		return nil, fmt.Errorf("venue %q does not provide OHLCV candles", venue)
	}
}

func (c *upstreamClient) resolveSolanaPool(ctx context.Context, symbolOrPool string) (string, error) {
	cleaned := strings.TrimSpace(symbolOrPool)
	cacheKey := "sol:" + cleaned
	if c.poolCache != nil {
		if hit, ok := c.poolCache.get(cacheKey); ok {
			return hit, nil
		}
	}
	lookup := func() (any, error) {
		if c.poolCache != nil {
			if hit, ok := c.poolCache.get(cacheKey); ok {
				return hit, nil
			}
		}
		if len(cleaned) >= 32 && len(cleaned) <= 44 && !strings.ContainsAny(cleaned, "/-:") {
			// Look up DexScreener pairs if this is a token mint rather than a pool address.
			u := fmt.Sprintf("https://api.dexscreener.com/latest/dex/tokens/%s", url.PathEscape(cleaned))
			raw, err := c.get(ctx, u)
			if err == nil {
				var ds struct {
					Pairs []struct {
						ChainID     string `json:"chainId"`
						PairAddress string `json:"pairAddress"`
					} `json:"pairs"`
				}
				if jsonErr := json.Unmarshal(raw, &ds); jsonErr == nil {
					for _, p := range ds.Pairs {
						if strings.EqualFold(p.ChainID, "solana") && p.PairAddress != "" {
							if c.poolCache != nil {
								c.poolCache.set(cacheKey, p.PairAddress, 15*time.Minute)
							}
							return p.PairAddress, nil
						}
					}
				}
			}
			if c.poolCache != nil {
				c.poolCache.set(cacheKey, cleaned, 15*time.Minute)
			}
			return cleaned, nil
		}
		u := fmt.Sprintf("https://api.dexscreener.com/latest/dex/search?q=%s", url.QueryEscape(cleaned))
		raw, err := c.get(ctx, u)
		if err != nil {
			return "", err
		}
		var ds struct {
			Pairs []struct {
				ChainID     string `json:"chainId"`
				PairAddress string `json:"pairAddress"`
			} `json:"pairs"`
		}
		if err := json.Unmarshal(raw, &ds); err != nil {
			return "", err
		}
		for _, p := range ds.Pairs {
			if strings.EqualFold(p.ChainID, "solana") && p.PairAddress != "" {
				if c.poolCache != nil {
					c.poolCache.set(cacheKey, p.PairAddress, 15*time.Minute)
				}
				return p.PairAddress, nil
			}
		}
		return "", fmt.Errorf("no solana pool resolved for %q", symbolOrPool)
	}
	if c.poolFlight != nil {
		v, err := c.poolFlight.Do(cacheKey, lookup)
		if err != nil {
			return "", err
		}
		pool, _ := v.(string)
		return pool, nil
	}
	v, err := lookup()
	if err != nil {
		return "", err
	}
	pool, _ := v.(string)
	return pool, nil
}

func (c *upstreamClient) fetchOrderBook(ctx context.Context, venue, symbol string, limit int, nowMs int64) (OrderBookSnapshot, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	qSym := venueQuerySymbol(venue, symbol)
	switch venue {
	case VenueHyperliquid:
		body, err := json.Marshal(map[string]any{"type": "l2Book", "coin": qSym})
		if err != nil {
			return OrderBookSnapshot{}, err
		}
		raw, err := c.postJSON(ctx, "https://api.hyperliquid.xyz/info", body)
		if err != nil {
			return OrderBookSnapshot{}, err
		}
		return ParseHyperliquidBook(raw, venue, symbol)
	case VenueBinance:
		u := fmt.Sprintf("/api/v3/depth?symbol=%s&limit=%d", url.QueryEscape(qSym), minInt(limit, 100))
		raw, err := c.getBinance(ctx, u)
		if err != nil {
			return OrderBookSnapshot{}, err
		}
		return ParseBinanceDepth(raw, venue, symbol, nowMs)
	case VenueBybit:
		u := fmt.Sprintf("https://api.bybit.com/v5/market/orderbook?category=linear&symbol=%s&limit=%d", url.QueryEscape(qSym), minInt(limit, 50))
		raw, err := c.get(ctx, u)
		if err != nil {
			return OrderBookSnapshot{}, err
		}
		return ParseBybitOrderBook(raw, venue, symbol)
	case VenueLighter:
		marketID, err := c.resolveLighterMarketID(ctx, qSym)
		if err != nil {
			return OrderBookSnapshot{}, err
		}
		u := fmt.Sprintf("https://mainnet.zklighter.elliot.ai/api/v1/orderBookOrders?market_id=%d&limit=%d", marketID, minInt(limit, 100))
		raw, err := c.get(ctx, u)
		if err != nil {
			return OrderBookSnapshot{}, err
		}
		return ParseLighterOrderBook(raw, venue, symbol, nowMs)
	default:
		return OrderBookSnapshot{}, fmt.Errorf("orderbook not supported on venue %q", venue)
	}
}

func (c *upstreamClient) fetchTrades(ctx context.Context, venue, symbol string, limit int) ([]TradeTick, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	qSym := venueQuerySymbol(venue, symbol)
	switch venue {
	case VenueHyperliquid:
		body, err := json.Marshal(map[string]any{"type": "recentTrades", "coin": qSym})
		if err != nil {
			return nil, err
		}
		raw, err := c.postJSON(ctx, "https://api.hyperliquid.xyz/info", body)
		if err != nil {
			return nil, err
		}
		var rows []struct {
			Px   string `json:"px"`
			Sz   string `json:"sz"`
			Side string `json:"side"`
			Time int64  `json:"time"`
			Tid  int64  `json:"tid"`
		}
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, err
		}
		out := make([]TradeTick, 0, len(rows))
		for _, r := range rows {
			px, okP := parseFloatStrict(r.Px)
			sz, okS := parseFloatStrict(r.Sz)
			if !okP || !okS || px <= 0 {
				continue
			}
			side := "sell"
			if strings.EqualFold(r.Side, "B") || strings.EqualFold(r.Side, "buy") {
				side = "buy"
			}
			out = append(out, TradeTick{
				ID:    strconv.FormatInt(r.Tid, 10),
				Time:  r.Time,
				Price: px,
				Size:  sz,
				Side:  side,
			})
		}
		return out, nil
	case VenueBinance:
		u := fmt.Sprintf("/api/v3/trades?symbol=%s&limit=%d", url.QueryEscape(qSym), minInt(limit, 100))
		raw, err := c.getBinance(ctx, u)
		if err != nil {
			return nil, err
		}
		var rows []struct {
			ID           int64  `json:"id"`
			Price        string `json:"price"`
			Qty          string `json:"qty"`
			Time         int64  `json:"time"`
			IsBuyerMaker bool   `json:"isBuyerMaker"`
		}
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, err
		}
		out := make([]TradeTick, 0, len(rows))
		for _, r := range rows {
			px, okP := parseFloatStrict(r.Price)
			sz, okS := parseFloatStrict(r.Qty)
			if !okP || !okS || px <= 0 {
				continue
			}
			side := "buy"
			if r.IsBuyerMaker {
				side = "sell"
			}
			out = append(out, TradeTick{
				ID:    strconv.FormatInt(r.ID, 10),
				Time:  r.Time,
				Price: px,
				Size:  sz,
				Side:  side,
			})
		}
		return out, nil
	case VenueBybit:
		u := fmt.Sprintf("https://api.bybit.com/v5/market/recent-trade?category=linear&symbol=%s&limit=%d", url.QueryEscape(qSym), minInt(limit, 60))
		raw, err := c.get(ctx, u)
		if err != nil {
			return nil, err
		}
		var resp struct {
			RetCode int    `json:"retCode"`
			RetMsg  string `json:"retMsg"`
			Result  struct {
				List []struct {
					ExecID string `json:"execId"`
					Price  string `json:"price"`
					Size   string `json:"size"`
					Side   string `json:"side"`
					Time   string `json:"time"`
				} `json:"list"`
			} `json:"result"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, err
		}
		if resp.RetCode != 0 {
			return nil, fmt.Errorf("bybit API error %d: %s", resp.RetCode, strings.TrimSpace(resp.RetMsg))
		}
		out := make([]TradeTick, 0, len(resp.Result.List))
		for _, r := range resp.Result.List {
			px, okP := parseFloatStrict(r.Price)
			sz, okS := parseFloatStrict(r.Size)
			tsMs, okT := parseFloatStrict(r.Time)
			if !okP || !okS || !okT || px <= 0 {
				continue
			}
			side := "sell"
			if strings.EqualFold(r.Side, "Buy") {
				side = "buy"
			}
			out = append(out, TradeTick{
				ID:    r.ExecID,
				Time:  int64(tsMs),
				Price: px,
				Size:  sz,
				Side:  side,
			})
		}
		return out, nil
	case VenueKraken:
		u := fmt.Sprintf("https://api.kraken.com/0/public/Trades?pair=%s&count=%d", url.QueryEscape(qSym), minInt(limit, 100))
		raw, err := c.get(ctx, u)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Error  []string                   `json:"error"`
			Result map[string]json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, err
		}
		if len(resp.Error) > 0 {
			return nil, fmt.Errorf("kraken API error: %s", strings.Join(resp.Error, "; "))
		}
		var rows [][]any
		for k, v := range resp.Result {
			if k == "last" {
				continue
			}
			if json.Unmarshal(v, &rows) == nil {
				break
			}
		}
		out := make([]TradeTick, 0, len(rows))
		for i, r := range rows {
			if len(r) < 4 {
				continue
			}
			px, okP := parseFloatStrict(r[0])
			sz, okS := parseFloatStrict(r[1])
			tsSec, okT := parseFloatStrict(r[2])
			if !okP || !okS || !okT || px <= 0 {
				continue
			}
			side := "sell"
			if s, _ := r[3].(string); strings.EqualFold(s, "b") {
				side = "buy"
			}
			tsMs := int64(tsSec * 1000)
			out = append(out, TradeTick{
				ID:    fmt.Sprintf("krk-%d-%d", tsMs, i),
				Time:  tsMs,
				Price: px,
				Size:  sz,
				Side:  side,
			})
		}
		return out, nil
	case VenueLighter:
		marketID, err := c.resolveLighterMarketID(ctx, qSym)
		if err != nil {
			return nil, err
		}
		u := fmt.Sprintf("https://mainnet.zklighter.elliot.ai/api/v1/recentTrades?market_id=%d&limit=%d", marketID, minInt(limit, 100))
		raw, err := c.get(ctx, u)
		if err != nil {
			return nil, err
		}
		return ParseLighterTrades(raw)
	default:
		return []TradeTick{}, nil
	}
}

func (c *upstreamClient) fetchCohorts(ctx context.Context, window string, nowSec int64) (CohortSnapshot, error) {
	var snap CohortSnapshot
	if err := c.streamGet(ctx, "https://stats-data.hyperliquid.xyz/Mainnet/leaderboard", func(r io.Reader) error {
		var parseErr error
		snap, parseErr = ParseHyperliquidLeaderboardReader(r, window, nowSec)
		return parseErr
	}); err != nil {
		return CohortSnapshot{}, err
	}
	return snap, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
