package marketdata

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultFeedIdleGrace    = 10 * time.Second
	defaultFeedEmitInterval = 100 * time.Millisecond
)

type venueRunner struct {
	venue      string
	refs       map[string]int
	subscribed map[string]bool
	running    bool
	runSeq     uint64
	cancel     context.CancelFunc
	pokeCh     chan struct{}
	idleTimer  *time.Timer
}

func (vr *venueRunner) poke() {
	select {
	case vr.pokeCh <- struct{}{}:
	default:
	}
}

// FeedHub manages strictly lazy, reference-counted upstream exchange WebSocket
// connections and feeds real-time ticks/depth into FeedCache and Bus.
type FeedHub struct {
	fixtureMode  bool
	cache        *FeedCache
	bus          *Bus
	now          func() time.Time
	wsEndpoints  map[string]string
	idleGrace    time.Duration
	emitInterval time.Duration
	wsHTTP       *http.Client

	rootCtx    context.Context
	rootCancel context.CancelFunc
	wg         sync.WaitGroup

	mu       sync.Mutex
	closed   bool
	venues   map[string]*venueRunner
	lastEmit map[string]time.Time
}

type hubOptions struct {
	fixtureMode  bool
	cache        *FeedCache
	bus          *Bus
	now          func() time.Time
	wsEndpoints  map[string]string
	idleGrace    time.Duration
	emitInterval time.Duration
}

func newFeedHub(opts hubOptions) *FeedHub {
	if opts.now == nil {
		opts.now = time.Now
	}
	idle := opts.idleGrace
	if idle <= 0 {
		idle = defaultFeedIdleGrace
	}
	emitIv := opts.emitInterval
	if emitIv < 0 {
		emitIv = defaultFeedEmitInterval
	}
	if opts.emitInterval == 0 && len(opts.wsEndpoints) == 0 {
		emitIv = defaultFeedEmitInterval
	}
	var copiedOverrides map[string]string
	if len(opts.wsEndpoints) > 0 {
		copiedOverrides = make(map[string]string, len(opts.wsEndpoints))
		for k, v := range opts.wsEndpoints {
			copiedOverrides[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	rootCtx, rootCancel := context.WithCancel(context.Background())
	return &FeedHub{
		fixtureMode:  opts.fixtureMode,
		cache:        opts.cache,
		bus:          opts.bus,
		now:          opts.now,
		wsEndpoints:  copiedOverrides,
		idleGrace:    idle,
		emitInterval: emitIv,
		wsHTTP:       newUpstreamWSHttpClient(),
		rootCtx:      rootCtx,
		rootCancel:   rootCancel,
		venues:       make(map[string]*venueRunner),
		lastEmit:     make(map[string]time.Time),
	}
}

func supportsWSFeed(venue string) bool {
	switch venue {
	case VenueHyperliquid, VenueBinance, VenueBybit, VenueKraken:
		return true
	default:
		return false
	}
}

// Acquire reference-counts a live WebSocket subscription for (venue, symbol).
// It never dials anything in FixtureMode or when idle before the first caller acquires.
func (h *FeedHub) Acquire(venue, symbol string) func() {
	if h == nil || h.fixtureMode {
		return func() {}
	}
	v, err := NormalizeVenue(venue)
	if err != nil || !supportsWSFeed(v) {
		return func() {}
	}
	sym, err := NormalizeSymbol(symbol)
	if err != nil {
		return func() {}
	}
	canon := venueQuerySymbol(v, sym)

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return func() {}
	}
	vr, ok := h.venues[v]
	if !ok {
		vr = &venueRunner{
			venue:      v,
			refs:       make(map[string]int),
			subscribed: make(map[string]bool),
			pokeCh:     make(chan struct{}, 1),
		}
		h.venues[v] = vr
	}
	if vr.idleTimer != nil {
		vr.idleTimer.Stop()
		vr.idleTimer = nil
	}
	prevRefs := vr.refs[canon]
	vr.refs[canon] = prevRefs + 1
	if !vr.running {
		ctx, cancel := context.WithCancel(h.rootCtx)
		vr.cancel = cancel
		vr.running = true
		vr.runSeq++
		seq := vr.runSeq
		h.wg.Add(1)
		go func(runner *venueRunner, runCtx context.Context, runSeq uint64) {
			defer h.wg.Done()
			h.runVenueLoop(runCtx, runner, runSeq)
		}(vr, ctx, seq)
	} else if prevRefs == 0 {
		vr.poke()
	}
	h.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			h.release(v, canon)
		})
	}
}

func (h *FeedHub) release(venue, canon string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	vr, ok := h.venues[venue]
	if !ok {
		return
	}
	cur := vr.refs[canon]
	if cur <= 1 {
		delete(vr.refs, canon)
		vr.poke()
	} else {
		vr.refs[canon] = cur - 1
	}
	if len(vr.refs) == 0 && vr.running && vr.idleTimer == nil {
		vr.idleTimer = time.AfterFunc(h.idleGrace, func() {
			h.stopIdleVenue(venue)
		})
	}
}

func (h *FeedHub) stopIdleVenue(venue string) {
	h.mu.Lock()
	vr, ok := h.venues[venue]
	if !ok || h.closed || len(vr.refs) > 0 || !vr.running {
		if ok && vr.idleTimer != nil && len(vr.refs) > 0 {
			vr.idleTimer = nil
		}
		h.mu.Unlock()
		return
	}
	vr.idleTimer = nil
	vr.running = false
	cancel := vr.cancel
	vr.cancel = nil
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// ActiveConnections returns the count of active venue WS worker goroutines (useful for diagnostics/tests).
func (h *FeedHub) ActiveConnections() int {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, vr := range h.venues {
		if vr.running {
			n++
		}
	}
	return n
}

// Close shuts down all active venue WebSocket connections cleanly and waits for exit.
func (h *FeedHub) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	for _, vr := range h.venues {
		if vr.idleTimer != nil {
			vr.idleTimer.Stop()
			vr.idleTimer = nil
		}
		if vr.cancel != nil {
			vr.cancel()
		}
		vr.running = false
	}
	h.mu.Unlock()
	h.rootCancel()
	h.wg.Wait()
}

func (h *FeedHub) snapshotDesiredSymbols(vr *venueRunner) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	syms := make([]string, 0, len(vr.refs))
	for sym := range vr.refs {
		syms = append(syms, sym)
	}
	sort.Strings(syms)
	return syms
}

func (h *FeedHub) shouldEmitDerived(venue, canonSym string) bool {
	if h.emitInterval <= 0 {
		return true
	}
	now := h.now()
	key := venue + ":" + canonSym
	h.mu.Lock()
	defer h.mu.Unlock()
	if prev, ok := h.lastEmit[key]; ok && now.Sub(prev) < h.emitInterval {
		return false
	}
	h.lastEmit[key] = now
	return true
}

func (h *FeedHub) ingestTrade(venue, canonSym string, t TradeTick) {
	if h.cache == nil {
		return
	}
	updated, ok := h.cache.AppendTrade(venue, canonSym, t)
	if !ok {
		return
	}
	nowMs := h.now().UTC().UnixMilli()
	if h.bus != nil {
		h.bus.Publish(Event{
			Type:      "trade",
			Venue:     venue,
			Symbol:    canonSym,
			Timestamp: nowMs,
			Payload:   t,
		})
	}
	if !h.shouldEmitDerived(venue, canonSym) || h.bus == nil {
		return
	}
	for iv, bar := range updated {
		h.bus.Publish(Event{
			Type:      "candle",
			Venue:     venue,
			Symbol:    canonSym,
			Interval:  iv,
			Style:     StyleStandard,
			Timestamp: nowMs,
			Payload:   bar,
		})
	}
	for _, iv := range []string{"1m", "5m", "15m", "1h", "4h", "1d"} {
		if bars, _, has := h.cache.GetCandles(venue, canonSym, iv, 8); has && len(bars) > 0 {
			startMs := bars[0].Time * 1000
			ticks := h.cache.TradesInRange(venue, canonSym, startMs, nowMs+1000)
			if fpBars, err := ComputeFootprintAndProfile(bars, ticks, 0); err == nil && len(fpBars) > 0 {
				h.bus.Publish(Event{
					Type:      "footprint",
					Venue:     venue,
					Symbol:    canonSym,
					Interval:  iv,
					Timestamp: nowMs,
					Payload:   fpBars[len(fpBars)-1],
				})
			}
		}
	}
}

func (h *FeedHub) ingestOrderBook(venue, canonSym string, snap OrderBookSnapshot) {
	if h.cache == nil {
		return
	}
	h.cache.SetOrderBook(venue, canonSym, snap)
	if h.bus != nil {
		h.bus.Publish(Event{
			Type:      "orderbook",
			Venue:     venue,
			Symbol:    canonSym,
			Timestamp: h.now().UTC().UnixMilli(),
			Payload:   snap,
		})
	}
}
