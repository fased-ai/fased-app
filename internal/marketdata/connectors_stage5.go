package marketdata

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func okxBarParam(iv string) string {
	switch iv {
	case "1m", "5m", "15m":
		return iv
	case "1h":
		return "1H"
	case "4h":
		return "4H"
	case "1d":
		return "1D"
	default:
		return "1H"
	}
}

func coinbaseGranularityParam(iv string) int {
	switch iv {
	case "1m":
		return 60
	case "5m":
		return 300
	case "15m":
		return 900
	case "1h":
		return 3600
	case "4h":
		return 21600 // Coinbase supports 60, 300, 900, 3600, 21600, 86400.
	case "1d":
		return 86400
	default:
		return 3600
	}
}

func deribitResolutionParam(iv string) string {
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
		return "1D"
	default:
		return "60"
	}
}

func alpacaTimeframeParam(iv string) string {
	switch iv {
	case "1m":
		return "1Min"
	case "5m":
		return "5Min"
	case "15m":
		return "15Min"
	case "1h":
		return "1Hour"
	case "4h":
		return "4Hour"
	case "1d":
		return "1Day"
	default:
		return "1Hour"
	}
}

func resolveAlpacaCreds() (string, string) {
	keyID := strings.TrimSpace(os.Getenv("FASED_ALPACA_API_KEY"))
	if keyID == "" {
		keyID = strings.TrimSpace(os.Getenv("APCA_API_KEY_ID"))
	}
	secret := strings.TrimSpace(os.Getenv("FASED_ALPACA_API_SECRET"))
	if secret == "" {
		secret = strings.TrimSpace(os.Getenv("APCA_API_SECRET_KEY"))
	}
	return keyID, secret
}

// ParseOKXCandles parses OKX v5 /api/v5/market/candles JSON responses.
func ParseOKXCandles(raw []byte) ([]Candle, error) {
	var resp struct {
		Code string     `json:"code"`
		Msg  string     `json:"msg"`
		Data [][]string `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode okx candles: %w", err)
	}
	if strings.TrimSpace(resp.Code) != "0" {
		return nil, fmt.Errorf("okx API error %s: %s", resp.Code, strings.TrimSpace(resp.Msg))
	}
	out := make([]Candle, 0, len(resp.Data))
	for _, r := range resp.Data {
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
		tsSec := int64(tsMs)
		if tsSec >= 1_000_000_000_000 {
			tsSec /= 1000
		}
		out = append(out, Candle{
			Time:   tsSec,
			Open:   o,
			High:   h,
			Low:    l,
			Close:  c,
			Volume: vol,
		})
	}
	return sanitizeCandles(out)
}

// ParseOKXOrderBook parses OKX v5 /api/v5/market/books L2 snapshots.
func ParseOKXOrderBook(raw []byte, venue, symbol string, nowMs int64) (OrderBookSnapshot, error) {
	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			Bids [][]string `json:"bids"`
			Asks [][]string `json:"asks"`
			Ts   string     `json:"ts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return OrderBookSnapshot{}, fmt.Errorf("decode okx orderbook: %w", err)
	}
	if strings.TrimSpace(resp.Code) != "0" {
		return OrderBookSnapshot{}, fmt.Errorf("okx API error %s: %s", resp.Code, strings.TrimSpace(resp.Msg))
	}
	if len(resp.Data) == 0 {
		return OrderBookSnapshot{}, fmt.Errorf("okx orderbook returned empty data")
	}
	snap := resp.Data[0]
	ts := nowMs
	if parsedTs, ok := parseFloatStrict(snap.Ts); ok && parsedTs > 0 {
		ts = int64(parsedTs)
	}
	bids := parseStringPairs(snap.Bids, true)
	asks := parseStringPairs(snap.Asks, false)
	return buildOrderBookSnapshot(venue, symbol, ts, bids, asks, VenueOKX), nil
}

// ParseOKXTrades parses OKX v5 /api/v5/market/trades responses.
func ParseOKXTrades(raw []byte) ([]TradeTick, error) {
	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			TradeID string `json:"tradeId"`
			Px      string `json:"px"`
			Sz      string `json:"sz"`
			Side    string `json:"side"`
			Ts      string `json:"ts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode okx trades: %w", err)
	}
	if strings.TrimSpace(resp.Code) != "0" {
		return nil, fmt.Errorf("okx API error %s: %s", resp.Code, strings.TrimSpace(resp.Msg))
	}
	out := make([]TradeTick, 0, len(resp.Data))
	for _, r := range resp.Data {
		px, okP := parseFloatStrict(r.Px)
		sz, okS := parseFloatStrict(r.Sz)
		tsMs, okT := parseFloatStrict(r.Ts)
		if !okP || !okS || !okT || px <= 0 || sz <= 0 {
			continue
		}
		side := "sell"
		if strings.EqualFold(r.Side, "buy") {
			side = "buy"
		}
		out = append(out, TradeTick{
			ID:    strings.TrimSpace(r.TradeID),
			Time:  int64(tsMs),
			Price: px,
			Size:  sz,
			Side:  side,
		})
	}
	return out, nil
}

// ParseCoinbaseCandles parses Coinbase Exchange /products/{id}/candles arrays:
// [timestamp_sec, low, high, open, close, volume].
func ParseCoinbaseCandles(raw []byte) ([]Candle, error) {
	var rows [][]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("decode coinbase candles: %w", err)
	}
	out := make([]Candle, 0, len(rows))
	for _, r := range rows {
		if len(r) < 6 {
			continue
		}
		ts, okT := parseFloatStrict(r[0])
		l, okL := parseFloatStrict(r[1])
		h, okH := parseFloatStrict(r[2])
		o, okO := parseFloatStrict(r[3])
		c, okC := parseFloatStrict(r[4])
		vol, okV := parseFloatStrict(r[5])
		if !okT || !okO || !okH || !okL || !okC || !okV || o <= 0 || h <= 0 || l <= 0 || c <= 0 {
			continue
		}
		tsSec := int64(ts)
		if tsSec >= 1_000_000_000_000 {
			tsSec /= 1000
		}
		out = append(out, Candle{
			Time:   tsSec,
			Open:   o,
			High:   h,
			Low:    l,
			Close:  c,
			Volume: vol,
		})
	}
	return sanitizeCandles(out)
}

func parseAnyPairs(pairs [][]any) [][]string {
	out := make([][]string, 0, len(pairs))
	for _, row := range pairs {
		if len(row) < 2 {
			continue
		}
		px, okP := parseFloatStrict(row[0])
		sz, okS := parseFloatStrict(row[1])
		if !okP || !okS || px <= 0 {
			continue
		}
		out = append(out, []string{
			strconv.FormatFloat(px, 'f', -1, 64),
			strconv.FormatFloat(sz, 'f', -1, 64),
		})
	}
	return out
}

// ParseCoinbaseOrderBook parses Coinbase Exchange /products/{id}/book?level=2 JSON payloads.
func ParseCoinbaseOrderBook(raw []byte, venue, symbol string, nowMs int64) (OrderBookSnapshot, error) {
	var resp struct {
		Bids [][]any `json:"bids"`
		Asks [][]any `json:"asks"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return OrderBookSnapshot{}, fmt.Errorf("decode coinbase orderbook: %w", err)
	}
	bids := parseStringPairs(parseAnyPairs(resp.Bids), true)
	asks := parseStringPairs(parseAnyPairs(resp.Asks), false)
	return buildOrderBookSnapshot(venue, symbol, nowMs, bids, asks, VenueCoinbase), nil
}

// ParseCoinbaseTrades parses Coinbase Exchange /products/{id}/trades JSON arrays.
func ParseCoinbaseTrades(raw []byte) ([]TradeTick, error) {
	var rows []struct {
		TradeID int64  `json:"trade_id"`
		Time    string `json:"time"`
		Price   string `json:"price"`
		Size    string `json:"size"`
		Side    string `json:"side"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("decode coinbase trades: %w", err)
	}
	out := make([]TradeTick, 0, len(rows))
	for _, r := range rows {
		px, okP := parseFloatStrict(r.Price)
		sz, okS := parseFloatStrict(r.Size)
		if !okP || !okS || px <= 0 || sz <= 0 {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(r.Time))
		if err != nil {
			continue
		}
		side := "sell"
		if strings.EqualFold(r.Side, "buy") {
			side = "buy"
		}
		out = append(out, TradeTick{
			ID:    strconv.FormatInt(r.TradeID, 10),
			Time:  ts.UTC().UnixMilli(),
			Price: px,
			Size:  sz,
			Side:  side,
		})
	}
	return out, nil
}

// ParseDeribitCandles parses Deribit /api/v2/public/get_tradingview_chart_data payloads.
func ParseDeribitCandles(raw []byte) ([]Candle, error) {
	var resp struct {
		Result struct {
			Status string    `json:"status"`
			Ticks  []int64   `json:"ticks"`
			Open   []float64 `json:"open"`
			High   []float64 `json:"high"`
			Low    []float64 `json:"low"`
			Close  []float64 `json:"close"`
			Volume []float64 `json:"volume"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode deribit candles: %w", err)
	}
	n := len(resp.Result.Ticks)
	if len(resp.Result.Open) < n {
		n = len(resp.Result.Open)
	}
	if len(resp.Result.High) < n {
		n = len(resp.Result.High)
	}
	if len(resp.Result.Low) < n {
		n = len(resp.Result.Low)
	}
	if len(resp.Result.Close) < n {
		n = len(resp.Result.Close)
	}
	if len(resp.Result.Volume) < n {
		n = len(resp.Result.Volume)
	}
	out := make([]Candle, 0, n)
	for i := 0; i < n; i++ {
		o, h, l, c, vol := resp.Result.Open[i], resp.Result.High[i], resp.Result.Low[i], resp.Result.Close[i], resp.Result.Volume[i]
		if !isFinitePositive(o) || !isFinitePositive(h) || !isFinitePositive(l) || !isFinitePositive(c) || !isFiniteNonNeg(vol) {
			continue
		}
		tsSec := resp.Result.Ticks[i]
		if tsSec >= 1_000_000_000_000 {
			tsSec /= 1000
		}
		out = append(out, Candle{
			Time:   tsSec,
			Open:   o,
			High:   h,
			Low:    l,
			Close:  c,
			Volume: vol,
		})
	}
	return sanitizeCandles(out)
}

// ParseDeribitOrderBook parses Deribit /api/v2/public/get_order_book payloads.
func ParseDeribitOrderBook(raw []byte, venue, symbol string, nowMs int64) (OrderBookSnapshot, error) {
	var resp struct {
		Result struct {
			Timestamp int64   `json:"timestamp"`
			Bids      [][]any `json:"bids"`
			Asks      [][]any `json:"asks"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return OrderBookSnapshot{}, fmt.Errorf("decode deribit orderbook: %w", err)
	}
	ts := nowMs
	if resp.Result.Timestamp > 0 {
		ts = resp.Result.Timestamp
	}
	bids := parseStringPairs(parseAnyPairs(resp.Result.Bids), true)
	asks := parseStringPairs(parseAnyPairs(resp.Result.Asks), false)
	return buildOrderBookSnapshot(venue, symbol, ts, bids, asks, VenueDeribit), nil
}

// ParseDeribitTrades parses Deribit /api/v2/public/get_last_trades_by_instrument payloads.
func ParseDeribitTrades(raw []byte) ([]TradeTick, error) {
	var resp struct {
		Result struct {
			Trades []struct {
				TradeID   string  `json:"trade_id"`
				Timestamp int64   `json:"timestamp"`
				Price     float64 `json:"price"`
				Amount    float64 `json:"amount"`
				Direction string  `json:"direction"`
			} `json:"trades"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode deribit trades: %w", err)
	}
	out := make([]TradeTick, 0, len(resp.Result.Trades))
	for _, r := range resp.Result.Trades {
		if !isFinitePositive(r.Price) || !isFinitePositive(r.Amount) || r.Timestamp <= 0 {
			continue
		}
		side := "sell"
		if strings.EqualFold(r.Direction, "buy") {
			side = "buy"
		}
		out = append(out, TradeTick{
			ID:    strings.TrimSpace(r.TradeID),
			Time:  r.Timestamp,
			Price: r.Price,
			Size:  r.Amount,
			Side:  side,
		})
	}
	return out, nil
}

// ParseAlpacaBars parses Alpaca Market Data v2 /v2/stocks/{symbol}/bars responses.
func ParseAlpacaBars(raw []byte) ([]Candle, error) {
	var resp struct {
		Bars []struct {
			T string  `json:"t"`
			O float64 `json:"o"`
			H float64 `json:"h"`
			L float64 `json:"l"`
			C float64 `json:"c"`
			V float64 `json:"v"`
			N int64   `json:"n"`
		} `json:"bars"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode alpaca bars: %w", err)
	}
	out := make([]Candle, 0, len(resp.Bars))
	for _, r := range resp.Bars {
		if !isFinitePositive(r.O) || !isFinitePositive(r.H) || !isFinitePositive(r.L) || !isFinitePositive(r.C) || !isFiniteNonNeg(r.V) {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(r.T))
		if err != nil {
			continue
		}
		out = append(out, Candle{
			Time:   ts.UTC().Unix(),
			Open:   r.O,
			High:   r.H,
			Low:    r.L,
			Close:  r.C,
			Volume: r.V,
			Trades: r.N,
		})
	}
	return sanitizeCandles(out)
}
