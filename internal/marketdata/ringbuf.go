package marketdata

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxRingTrades = 4096
)

type symbolBuffer struct {
	mu               sync.RWMutex
	venue            string
	canonSymbol      string
	trades           []TradeTick
	lastTradeWriteMs int64
	book             *OrderBookSnapshot
	bookUpdatedMs    int64
	candles          map[string][]Candle
	candlesUpdatedMs map[string]int64
}

// FeedCache stores sharded per-symbol ring buffers for live trades, L2 books,
// and real-time rolled OHLCV bars in memory for sub-millisecond reads.
type FeedCache struct {
	mu     sync.RWMutex
	shards map[string]*symbolBuffer
	now    func() time.Time
}

func newFeedCache(nowFn func() time.Time) *FeedCache {
	if nowFn == nil {
		nowFn = time.Now
	}
	return &FeedCache{
		shards: make(map[string]*symbolBuffer),
		now:    nowFn,
	}
}

func canonicalCacheKey(venue, symbol string) string {
	v := strings.ToLower(strings.TrimSpace(venue))
	return v + ":" + strings.ToUpper(venueQuerySymbol(v, symbol))
}

func (fc *FeedCache) shard(venue, symbol string, create bool) *symbolBuffer {
	key := canonicalCacheKey(venue, symbol)
	fc.mu.RLock()
	sb, ok := fc.shards[key]
	fc.mu.RUnlock()
	if ok || !create {
		return sb
	}
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if sb, ok = fc.shards[key]; ok {
		return sb
	}
	sb = &symbolBuffer{
		venue:            strings.ToLower(strings.TrimSpace(venue)),
		canonSymbol:      venueQuerySymbol(venue, symbol),
		candles:          make(map[string][]Candle),
		candlesUpdatedMs: make(map[string]int64),
	}
	fc.shards[key] = sb
	return sb
}

func isDuplicateTailTrade(trades []TradeTick, t TradeTick) bool {
	start := len(trades) - 32
	if start < 0 {
		start = 0
	}
	for i := len(trades) - 1; i >= start; i-- {
		prev := trades[i]
		if t.ID != "" && prev.ID == t.ID && prev.Time == t.Time {
			return true
		}
		if t.ID == "" && prev.Time == t.Time && prev.Price == t.Price && prev.Size == t.Size && prev.Side == t.Side {
			return true
		}
	}
	return false
}

func applyTradeToCandleSlice(bars []Candle, stepSec int64, t TradeTick) ([]Candle, Candle) {
	tsSec := (t.Time / 1000 / stepSec) * stepSec
	if tsSec <= 0 {
		tsSec = stepSec
	}
	n := len(bars)
	if n > 0 && bars[n-1].Time == tsSec {
		c := bars[n-1]
		c.High = math.Max(c.High, t.Price)
		c.Low = math.Min(c.Low, t.Price)
		c.Close = t.Price
		c.Volume += t.Size
		if t.Side == "buy" {
			c.BuyVolume += t.Size
		} else {
			c.SellVolume += t.Size
		}
		c.Trades++
		bars[n-1] = c
		return bars, c
	}
	if n > 0 && tsSec < bars[n-1].Time {
		// Find matching existing bar if slightly out-of-order trade arrives.
		for i := n - 2; i >= 0 && i >= n-6; i-- {
			if bars[i].Time == tsSec {
				c := bars[i]
				c.High = math.Max(c.High, t.Price)
				c.Low = math.Min(c.Low, t.Price)
				c.Volume += t.Size
				if t.Side == "buy" {
					c.BuyVolume += t.Size
				} else {
					c.SellVolume += t.Size
				}
				c.Trades++
				bars[i] = c
				return bars, c
			}
		}
		return bars, bars[n-1]
	}
	c := Candle{
		Time:   tsSec,
		Open:   t.Price,
		High:   t.Price,
		Low:    t.Price,
		Close:  t.Price,
		Volume: t.Size,
		Trades: 1,
	}
	if t.Side == "buy" {
		c.BuyVolume = t.Size
	} else {
		c.SellVolume = t.Size
	}
	bars = append(bars, c)
	if len(bars) > maxCandlesLimit {
		bars = bars[len(bars)-maxCandlesLimit:]
	}
	return bars, c
}

// AppendTrade ingests a live trade tick, updates the trade ring buffer, and rolls
// current interval candles, returning the updated candles keyed by interval.
func (fc *FeedCache) AppendTrade(venue, symbol string, t TradeTick) (map[string]Candle, bool) {
	if !isFinitePositive(t.Price) || !isFiniteNonNeg(t.Size) {
		return nil, false
	}
	sb := fc.shard(venue, symbol, true)
	nowMs := fc.now().UTC().UnixMilli()

	sb.mu.Lock()
	defer sb.mu.Unlock()
	if isDuplicateTailTrade(sb.trades, t) {
		return nil, false
	}
	sb.trades = append(sb.trades, t)
	if n := len(sb.trades); n >= 2 && sb.trades[n-1].Time < sb.trades[n-2].Time {
		// Keep short out-of-order tail sorted without sorting all 4096 entries.
		for i := n - 1; i > 0 && sb.trades[i].Time < sb.trades[i-1].Time && n-i <= 16; i-- {
			sb.trades[i], sb.trades[i-1] = sb.trades[i-1], sb.trades[i]
		}
	}
	if len(sb.trades) > maxRingTrades {
		sb.trades = sb.trades[len(sb.trades)-maxRingTrades:]
	}
	sb.lastTradeWriteMs = nowMs

	updated := make(map[string]Candle, len(validIntervals))
	for iv, stepSec := range validIntervals {
		bars, bar := applyTradeToCandleSlice(sb.candles[iv], stepSec, t)
		sb.candles[iv] = bars
		sb.candlesUpdatedMs[iv] = nowMs
		updated[iv] = bar
	}
	return updated, true
}

// AppendTrades ingests a batch of trades in chronological order without synthesizing
// unbacked interval bars (used to warm the trade tape from REST backfills).
func (fc *FeedCache) AppendTrades(venue, symbol string, ticks []TradeTick) {
	if len(ticks) == 0 {
		return
	}
	sb := fc.shard(venue, symbol, true)
	nowMs := fc.now().UTC().UnixMilli()

	sorted := make([]TradeTick, 0, len(ticks))
	for _, t := range ticks {
		if isFinitePositive(t.Price) && isFiniteNonNeg(t.Size) {
			sorted = append(sorted, t)
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Time < sorted[j].Time
	})

	sb.mu.Lock()
	defer sb.mu.Unlock()
	for _, t := range sorted {
		if isDuplicateTailTrade(sb.trades, t) {
			continue
		}
		sb.trades = append(sb.trades, t)
	}
	if len(sb.trades) > maxRingTrades {
		sb.trades = sb.trades[len(sb.trades)-maxRingTrades:]
	}
	sb.lastTradeWriteMs = nowMs
}

// RecentTrades returns the most recent up-to-limit trades and the last write timestamp in ms.
func (fc *FeedCache) RecentTrades(venue, symbol string, limit int) ([]TradeTick, int64, bool) {
	sb := fc.shard(venue, symbol, false)
	if sb == nil {
		return nil, 0, false
	}
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	if len(sb.trades) == 0 {
		return nil, 0, false
	}
	start := 0
	if limit > 0 && len(sb.trades) > limit {
		start = len(sb.trades) - limit
	}
	out := make([]TradeTick, len(sb.trades)-start)
	copy(out, sb.trades[start:])
	return out, sb.lastTradeWriteMs, true
}

// TradesInRange returns all buffered trades within [startMs, endMs].
func (fc *FeedCache) TradesInRange(venue, symbol string, startMs, endMs int64) []TradeTick {
	sb := fc.shard(venue, symbol, false)
	if sb == nil {
		return nil
	}
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	if len(sb.trades) == 0 {
		return nil
	}
	out := make([]TradeTick, 0, 128)
	for _, t := range sb.trades {
		if t.Time >= startMs && t.Time <= endMs {
			out = append(out, t)
		}
	}
	return out
}

// SetOrderBook stores a cloned L2 snapshot in the symbol shard.
func (fc *FeedCache) SetOrderBook(venue, symbol string, snap OrderBookSnapshot) {
	sb := fc.shard(venue, symbol, true)
	nowMs := fc.now().UTC().UnixMilli()
	cloned := OrderBookSnapshot{
		Venue:    snap.Venue,
		Symbol:   snap.Symbol,
		Time:     snap.Time,
		Bids:     append([]OrderBookLevel(nil), snap.Bids...),
		Asks:     append([]OrderBookLevel(nil), snap.Asks...),
		Spread:   snap.Spread,
		MidPrice: snap.MidPrice,
		Source:   snap.Source,
		Fixture:  snap.Fixture,
	}
	sb.mu.Lock()
	sb.book = &cloned
	sb.bookUpdatedMs = nowMs
	sb.mu.Unlock()
}

// GetOrderBook returns a truncated copy of the cached L2 book and last update time in ms.
func (fc *FeedCache) GetOrderBook(venue, symbol string, limit int) (OrderBookSnapshot, int64, bool) {
	sb := fc.shard(venue, symbol, false)
	if sb == nil {
		return OrderBookSnapshot{}, 0, false
	}
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	if sb.book == nil {
		return OrderBookSnapshot{}, 0, false
	}
	snap := *sb.book
	if limit > 0 {
		if len(snap.Bids) > limit {
			snap.Bids = append([]OrderBookLevel(nil), snap.Bids[:limit]...)
		} else {
			snap.Bids = append([]OrderBookLevel(nil), snap.Bids...)
		}
		if len(snap.Asks) > limit {
			snap.Asks = append([]OrderBookLevel(nil), snap.Asks[:limit]...)
		} else {
			snap.Asks = append([]OrderBookLevel(nil), snap.Asks...)
		}
	} else {
		snap.Bids = append([]OrderBookLevel(nil), snap.Bids...)
		snap.Asks = append([]OrderBookLevel(nil), snap.Asks...)
	}
	return snap, sb.bookUpdatedMs, true
}

// UpsertCandles merges REST/historical bars with any live WS-rolled tip bars.
func (fc *FeedCache) UpsertCandles(venue, symbol, interval string, bars []Candle) []Candle {
	if len(bars) == 0 {
		return nil
	}
	sb := fc.shard(venue, symbol, true)
	nowMs := fc.now().UTC().UnixMilli()

	sb.mu.Lock()
	defer sb.mu.Unlock()
	existing := sb.candles[interval]
	byTime := make(map[int64]Candle, len(existing)+len(bars))
	for _, c := range existing {
		byTime[c.Time] = c
	}
	for _, c := range bars {
		if prev, ok := byTime[c.Time]; ok && prev.Volume > c.Volume && c.Time == existing[len(existing)-1].Time {
			// Preserve live tip volume/close when WS has already advanced beyond REST snapshot.
			merged := c
			merged.High = math.Max(c.High, prev.High)
			merged.Low = math.Min(c.Low, prev.Low)
			merged.Close = prev.Close
			merged.Volume = prev.Volume
			merged.BuyVolume = math.Max(c.BuyVolume, prev.BuyVolume)
			merged.SellVolume = math.Max(c.SellVolume, prev.SellVolume)
			merged.Trades = maxInt64(c.Trades, prev.Trades)
			byTime[c.Time] = merged
			continue
		}
		byTime[c.Time] = c
	}
	merged := make([]Candle, 0, len(byTime))
	for _, c := range byTime {
		merged = append(merged, c)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Time < merged[j].Time
	})
	if len(merged) > maxCandlesLimit {
		merged = merged[len(merged)-maxCandlesLimit:]
	}
	sb.candles[interval] = merged
	sb.candlesUpdatedMs[interval] = nowMs

	out := make([]Candle, len(merged))
	copy(out, merged)
	return out
}

// GetCandles returns a bounded copy of cached candles for (venue, symbol, interval).
func (fc *FeedCache) GetCandles(venue, symbol, interval string, limit int) ([]Candle, int64, bool) {
	sb := fc.shard(venue, symbol, false)
	if sb == nil {
		return nil, 0, false
	}
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	bars := sb.candles[interval]
	if len(bars) == 0 {
		return nil, 0, false
	}
	start := 0
	if limit > 0 && len(bars) > limit {
		start = len(bars) - limit
	}
	out := make([]Candle, len(bars)-start)
	copy(out, bars[start:])
	return out, sb.candlesUpdatedMs[interval], true
}
