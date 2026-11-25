package marketdata

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// resolveLighterMarketID maps a Lighter symbol (e.g. ETH, BTC) to its numeric market_id
// via cached GET /api/v1/orderBooks metadata.
func (c *upstreamClient) resolveLighterMarketID(ctx context.Context, symbol string) (int64, error) {
	cleaned := strings.ToUpper(strings.TrimSpace(symbol))
	if cleaned == "" {
		return 0, fmt.Errorf("lighter symbol required")
	}
	if n, err := strconv.ParseInt(cleaned, 10, 64); err == nil && n >= 0 {
		return n, nil
	}
	cacheKey := "lighter:" + cleaned
	if c.poolCache != nil {
		if hit, ok := c.poolCache.get(cacheKey); ok {
			if n, err := strconv.ParseInt(hit, 10, 64); err == nil {
				return n, nil
			}
		}
	}
	lookup := func() (any, error) {
		if c.poolCache != nil {
			if hit, ok := c.poolCache.get(cacheKey); ok {
				return hit, nil
			}
		}
		raw, err := c.get(ctx, "https://mainnet.zklighter.elliot.ai/api/v1/orderBooks")
		if err != nil {
			return "", err
		}
		var doc struct {
			OrderBooks []struct {
				Symbol   string `json:"symbol"`
				MarketID int64  `json:"market_id"`
			} `json:"order_books"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return "", fmt.Errorf("decode lighter orderBooks: %w", err)
		}
		var matched string
		for _, ob := range doc.OrderBooks {
			up := strings.ToUpper(strings.TrimSpace(ob.Symbol))
			if up == "" {
				continue
			}
			idStr := strconv.FormatInt(ob.MarketID, 10)
			if c.poolCache != nil {
				c.poolCache.set("lighter:"+up, idStr, 15*time.Minute)
			}
			if up == cleaned {
				matched = idStr
			}
		}
		if matched == "" {
			return "", fmt.Errorf("unknown lighter market symbol %q", symbol)
		}
		return matched, nil
	}
	var val string
	if c.poolFlight != nil {
		v, err := c.poolFlight.Do(cacheKey, lookup)
		if err != nil {
			return 0, err
		}
		val, _ = v.(string)
	} else {
		v, err := lookup()
		if err != nil {
			return 0, err
		}
		val, _ = v.(string)
	}
	id, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid lighter market id for %q", symbol)
	}
	return id, nil
}

// ParseLighterCandles normalizes GET /api/v1/candles responses into marketdata Candle slices.
func ParseLighterCandles(raw []byte) ([]Candle, error) {
	var doc struct {
		C []struct {
			T int64   `json:"t"`
			O float64 `json:"o"`
			H float64 `json:"h"`
			L float64 `json:"l"`
			C float64 `json:"c"`
			V float64 `json:"v"`
		} `json:"c"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode lighter candles: %w", err)
	}
	out := make([]Candle, 0, len(doc.C))
	for _, r := range doc.C {
		if !isFiniteNonNeg(r.O) || !isFiniteNonNeg(r.H) || !isFiniteNonNeg(r.L) || !isFiniteNonNeg(r.C) || !isFiniteNonNeg(r.V) ||
			r.O <= 0 || r.H <= 0 || r.L <= 0 || r.C <= 0 {
			continue
		}
		tsSec := r.T
		if tsSec > 1e12 {
			tsSec /= 1000
		}
		out = append(out, Candle{
			Time:   tsSec,
			Open:   r.O,
			High:   r.H,
			Low:    r.L,
			Close:  r.C,
			Volume: r.V,
		})
	}
	return sanitizeCandles(out)
}

type lighterBookEntry struct {
	Price               string `json:"price"`
	RemainingBaseAmount string `json:"remaining_base_amount"`
	InitialBaseAmount   string `json:"initial_base_amount"`
}

func aggregateLighterLevels(rows []lighterBookEntry, descending bool) []OrderBookLevel {
	byPrice := make(map[float64]float64, len(rows))
	for _, row := range rows {
		px, okP := parseFloatStrict(row.Price)
		szStr := row.RemainingBaseAmount
		if szStr == "" {
			szStr = row.InitialBaseAmount
		}
		sz, okS := parseFloatStrict(szStr)
		if !okP || !okS || px <= 0 || sz <= 0 {
			continue
		}
		byPrice[px] += sz
	}
	out := make([]OrderBookLevel, 0, len(byPrice))
	for px, sz := range byPrice {
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

// ParseLighterOrderBook aggregates GET /api/v1/orderBookOrders orders into level-2 snapshot depth.
func ParseLighterOrderBook(raw []byte, venue, symbol string, nowMs int64) (OrderBookSnapshot, error) {
	var doc struct {
		Bids []lighterBookEntry `json:"bids"`
		Asks []lighterBookEntry `json:"asks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return OrderBookSnapshot{}, fmt.Errorf("decode lighter orderBookOrders: %w", err)
	}
	bids := aggregateLighterLevels(doc.Bids, true)
	asks := aggregateLighterLevels(doc.Asks, false)
	return buildOrderBookSnapshot(venue, symbol, nowMs, bids, asks, VenueLighter), nil
}

// ParseLighterTrades parses GET /api/v1/recentTrades payloads into TradeTick slices.
func ParseLighterTrades(raw []byte) ([]TradeTick, error) {
	var doc struct {
		Trades []struct {
			TradeID    int64  `json:"trade_id"`
			Price      string `json:"price"`
			Size       string `json:"size"`
			Timestamp  int64  `json:"timestamp"`
			IsMakerAsk bool   `json:"is_maker_ask"`
		} `json:"trades"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode lighter recentTrades: %w", err)
	}
	out := make([]TradeTick, 0, len(doc.Trades))
	for _, r := range doc.Trades {
		px, okP := parseFloatStrict(r.Price)
		sz, okS := parseFloatStrict(r.Size)
		if !okP || !okS || px <= 0 || sz <= 0 {
			continue
		}
		side := "sell"
		if r.IsMakerAsk {
			side = "buy"
		}
		out = append(out, TradeTick{
			ID:    strconv.FormatInt(r.TradeID, 10),
			Time:  r.Timestamp,
			Price: px,
			Size:  sz,
			Side:  side,
		})
	}
	return out, nil
}
