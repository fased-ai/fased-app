package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	wsReadLimitBytes = 1 << 20 // 1 MiB max upstream WS frame
	wsWriteTimeout   = 5 * time.Second
)

type wsVenueAdapter interface {
	subscribePayloads(canonSyms []string) [][]byte
	unsubscribePayloads(canonSyms []string) [][]byte
	pingPayload() []byte
	pingInterval() time.Duration
	handleFrame(raw []byte, emitTrade func(canonSym string, t TradeTick), emitBook func(canonSym string, snap OrderBookSnapshot))
}

func (h *FeedHub) endpointsForVenue(venue string) ([]string, bool) {
	if len(h.wsEndpoints) > 0 {
		if raw, ok := h.wsEndpoints[venue]; ok && raw != "" {
			return []string{raw}, true
		}
	}
	switch venue {
	case VenueHyperliquid:
		return []string{"wss://api.hyperliquid.xyz/ws"}, false
	case VenueBinance:
		return []string{
			"wss://data-stream.binance.vision/stream",
			"wss://stream.binance.com:9443/stream",
		}, false
	case VenueBybit:
		return []string{"wss://stream.bybit.com/v5/public/linear"}, false
	case VenueKraken:
		return []string{"wss://ws.kraken.com/v2"}, false
	default:
		return nil, false
	}
}

func (h *FeedHub) runVenueLoop(ctx context.Context, vr *venueRunner, seq uint64) {
	defer func() {
		h.mu.Lock()
		if vr.runSeq == seq {
			if vr.idleTimer != nil {
				vr.idleTimer.Stop()
				vr.idleTimer = nil
			}
			vr.running = false
		}
		h.mu.Unlock()
	}()

	backoffs := []time.Duration{
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		15 * time.Second,
		30 * time.Second,
	}
	attempt := 0

	for {
		if ctx.Err() != nil {
			return
		}
		// Wait briefly if no symbols are currently desired before dialing.
		desired := h.snapshotDesiredSymbols(vr)
		if len(desired) == 0 {
			timer := time.NewTimer(250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-vr.pokeCh:
				timer.Stop()
				continue
			case <-timer.C:
				continue
			}
		}

		connectedAt := h.now()
		_ = h.runVenueSession(ctx, vr)
		if ctx.Err() != nil {
			return
		}
		if h.now().Sub(connectedAt) >= 15*time.Second {
			attempt = 0
		}
		wait := backoffs[minInt(attempt, len(backoffs)-1)]
		attempt++
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (h *FeedHub) dialVenue(ctx context.Context, venue string) (*websocket.Conn, error) {
	urls, isOverride := h.endpointsForVenue(venue)
	if len(urls) == 0 {
		return nil, fmt.Errorf("no WebSocket endpoints configured for %q", venue)
	}
	var lastErr error
	for _, rawURL := range urls {
		var dialOpts *websocket.DialOptions
		if !isOverride {
			if _, err := validateUpstreamWSURL(rawURL); err != nil {
				lastErr = err
				continue
			}
			dialOpts = &websocket.DialOptions{
				HTTPClient: h.wsHTTP,
				HTTPHeader: http.Header{"User-Agent": []string{defaultUserAgent}},
			}
		}
		dialCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		conn, _, err := websocket.Dial(dialCtx, rawURL, dialOpts)
		cancel()
		if err == nil {
			conn.SetReadLimit(wsReadLimitBytes)
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("WebSocket dial failed")
	}
	return nil, lastErr
}

func newVenueAdapter(venue string, nowFn func() time.Time) wsVenueAdapter {
	switch venue {
	case VenueHyperliquid:
		return &hyperliquidWSAdapter{now: nowFn}
	case VenueBinance:
		return &binanceWSAdapter{now: nowFn}
	case VenueBybit:
		return &bybitWSAdapter{
			books: make(map[string]*l2BookState),
		}
	case VenueKraken:
		return &krakenWSAdapter{
			books: make(map[string]*l2BookState),
			now:   nowFn,
		}
	default:
		return nil
	}
}

func (h *FeedHub) runVenueSession(ctx context.Context, vr *venueRunner) error {
	conn, err := h.dialVenue(ctx, vr.venue)
	if err != nil {
		return err
	}
	defer conn.CloseNow()

	adapter := newVenueAdapter(vr.venue, h.now)
	if adapter == nil {
		return fmt.Errorf("unsupported WS adapter %q", vr.venue)
	}

	sessCtx, sessCancel := context.WithCancel(ctx)
	defer sessCancel()

	// Dedicated writer owns all conn.Write calls and tracks active upstream subscriptions.
	var writeWG sync.WaitGroup
	writeErrCh := make(chan error, 1)
	writeWG.Add(1)
	go func() {
		defer writeWG.Done()
		active := make(map[string]bool)
		syncSubs := func() error {
			desiredList := h.snapshotDesiredSymbols(vr)
			desired := make(map[string]bool, len(desiredList))
			var toSub, toUnsub []string
			for _, sym := range desiredList {
				desired[sym] = true
				if !active[sym] {
					toSub = append(toSub, sym)
				}
			}
			for sym := range active {
				if !desired[sym] {
					toUnsub = append(toUnsub, sym)
				}
			}
			sort.Strings(toUnsub)
			sort.Strings(toSub)
			if len(toUnsub) > 0 {
				for _, payload := range adapter.unsubscribePayloads(toUnsub) {
					wCtx, cancel := context.WithTimeout(sessCtx, wsWriteTimeout)
					err := conn.Write(wCtx, websocket.MessageText, payload)
					cancel()
					if err != nil {
						return err
					}
				}
				for _, sym := range toUnsub {
					delete(active, sym)
				}
			}
			if len(toSub) > 0 {
				for _, payload := range adapter.subscribePayloads(toSub) {
					wCtx, cancel := context.WithTimeout(sessCtx, wsWriteTimeout)
					err := conn.Write(wCtx, websocket.MessageText, payload)
					cancel()
					if err != nil {
						return err
					}
				}
				for _, sym := range toSub {
					active[sym] = true
				}
			}
			return nil
		}

		if err := syncSubs(); err != nil {
			writeErrCh <- err
			sessCancel()
			return
		}

		pingIv := adapter.pingInterval()
		var pingC <-chan time.Time
		if pingIv > 0 {
			ticker := time.NewTicker(pingIv)
			defer ticker.Stop()
			pingC = ticker.C
		}

		for {
			select {
			case <-sessCtx.Done():
				return
			case <-vr.pokeCh:
				if err := syncSubs(); err != nil {
					writeErrCh <- err
					sessCancel()
					return
				}
			case <-pingC:
				if payload := adapter.pingPayload(); len(payload) > 0 {
					wCtx, cancel := context.WithTimeout(sessCtx, wsWriteTimeout)
					err := conn.Write(wCtx, websocket.MessageText, payload)
					cancel()
					if err != nil {
						writeErrCh <- err
						sessCancel()
						return
					}
				}
			}
		}
	}()

	emitTrade := func(canonSym string, t TradeTick) {
		h.ingestTrade(vr.venue, canonSym, t)
	}
	emitBook := func(canonSym string, snap OrderBookSnapshot) {
		h.ingestOrderBook(vr.venue, canonSym, snap)
	}

	var readErr error
	for {
		msgType, raw, err := conn.Read(sessCtx)
		if err != nil {
			readErr = err
			break
		}
		if msgType != websocket.MessageText && msgType != websocket.MessageBinary {
			continue
		}
		adapter.handleFrame(raw, emitTrade, emitBook)
	}

	sessCancel()
	_ = conn.Close(websocket.StatusNormalClosure, "")
	_ = conn.CloseNow()
	writeWG.Wait()

	select {
	case wErr := <-writeErrCh:
		if readErr == nil {
			return wErr
		}
	default:
	}
	return readErr
}

// --- Hyperliquid adapter ---

type hyperliquidWSAdapter struct {
	now func() time.Time
}

func (a *hyperliquidWSAdapter) pingInterval() time.Duration { return 25 * time.Second }
func (a *hyperliquidWSAdapter) pingPayload() []byte         { return []byte(`{"method":"ping"}`) }

func (a *hyperliquidWSAdapter) subscribePayloads(canonSyms []string) [][]byte {
	return a.opPayloads("subscribe", canonSyms)
}

func (a *hyperliquidWSAdapter) unsubscribePayloads(canonSyms []string) [][]byte {
	return a.opPayloads("unsubscribe", canonSyms)
}

func (a *hyperliquidWSAdapter) opPayloads(method string, canonSyms []string) [][]byte {
	out := make([][]byte, 0, len(canonSyms)*2)
	for _, sym := range canonSyms {
		for _, subType := range []string{"trades", "l2Book"} {
			if raw, err := json.Marshal(map[string]any{
				"method": method,
				"subscription": map[string]string{
					"type": subType,
					"coin": sym,
				},
			}); err == nil {
				out = append(out, raw)
			}
		}
	}
	return out
}

func (a *hyperliquidWSAdapter) handleFrame(raw []byte, emitTrade func(string, TradeTick), emitBook func(string, OrderBookSnapshot)) {
	var env struct {
		Channel string          `json:"channel"`
		Data    json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &env) != nil || len(env.Data) == 0 {
		return
	}
	switch env.Channel {
	case "trades":
		var rows []struct {
			Coin string `json:"coin"`
			Px   string `json:"px"`
			Sz   string `json:"sz"`
			Side string `json:"side"`
			Time int64  `json:"time"`
			Tid  int64  `json:"tid"`
		}
		if json.Unmarshal(env.Data, &rows) != nil {
			return
		}
		for _, r := range rows {
			px, okP := parseFloatStrict(r.Px)
			sz, okS := parseFloatStrict(r.Sz)
			if !okP || !okS || px <= 0 || strings.TrimSpace(r.Coin) == "" {
				continue
			}
			side := "sell"
			if strings.EqualFold(r.Side, "B") || strings.EqualFold(r.Side, "buy") {
				side = "buy"
			}
			ts := r.Time
			if ts <= 0 {
				ts = a.now().UTC().UnixMilli()
			}
			canon := venueQuerySymbol(VenueHyperliquid, r.Coin)
			emitTrade(canon, TradeTick{
				ID:    strconv.FormatInt(r.Tid, 10),
				Time:  ts,
				Price: px,
				Size:  sz,
				Side:  side,
			})
		}
	case "l2Book":
		var book struct {
			Coin   string `json:"coin"`
			Time   int64  `json:"time"`
			Levels [][]struct {
				Px string `json:"px"`
				Sz string `json:"sz"`
			} `json:"levels"`
		}
		if json.Unmarshal(env.Data, &book) != nil || len(book.Levels) < 2 || strings.TrimSpace(book.Coin) == "" {
			return
		}
		bids := parseBookLevels(book.Levels[0], true)
		asks := parseBookLevels(book.Levels[1], false)
		canon := venueQuerySymbol(VenueHyperliquid, book.Coin)
		ts := book.Time
		if ts <= 0 {
			ts = a.now().UTC().UnixMilli()
		}
		emitBook(canon, buildOrderBookSnapshot(VenueHyperliquid, canon, ts, bids, asks, VenueHyperliquid))
	}
}

// --- Binance adapter ---

type binanceWSAdapter struct {
	now   func() time.Time
	reqID int64
}

func (a *binanceWSAdapter) pingInterval() time.Duration { return 0 }
func (a *binanceWSAdapter) pingPayload() []byte         { return nil }

func (a *binanceWSAdapter) subscribePayloads(canonSyms []string) [][]byte {
	return a.opPayloads("SUBSCRIBE", canonSyms)
}

func (a *binanceWSAdapter) unsubscribePayloads(canonSyms []string) [][]byte {
	return a.opPayloads("UNSUBSCRIBE", canonSyms)
}

func (a *binanceWSAdapter) opPayloads(method string, canonSyms []string) [][]byte {
	if len(canonSyms) == 0 {
		return nil
	}
	params := make([]string, 0, len(canonSyms)*2)
	for _, sym := range canonSyms {
		lower := strings.ToLower(sym)
		params = append(params, lower+"@trade", lower+"@depth20@100ms")
	}
	a.reqID++
	raw, err := json.Marshal(map[string]any{
		"method": method,
		"params": params,
		"id":     a.reqID,
	})
	if err != nil {
		return nil
	}
	return [][]byte{raw}
}

func (a *binanceWSAdapter) handleFrame(raw []byte, emitTrade func(string, TradeTick), emitBook func(string, OrderBookSnapshot)) {
	var env struct {
		Stream string          `json:"stream"`
		Data   json.RawMessage `json:"data"`
	}
	payload := raw
	streamSym := ""
	if json.Unmarshal(raw, &env) == nil && env.Stream != "" && len(env.Data) > 0 {
		payload = env.Data
		if idx := strings.IndexByte(env.Stream, '@'); idx > 0 {
			streamSym = strings.ToUpper(env.Stream[:idx])
		}
	}
	if strings.Contains(env.Stream, "@trade") || (env.Stream == "" && strings.Contains(string(payload), `"e":"trade"`)) {
		var tr struct {
			Symbol       string `json:"s"`
			TradeID      int64  `json:"t"`
			Price        string `json:"p"`
			Qty          string `json:"q"`
			TradeTime    int64  `json:"T"`
			IsBuyerMaker bool   `json:"m"`
		}
		if json.Unmarshal(payload, &tr) != nil {
			return
		}
		px, okP := parseFloatStrict(tr.Price)
		sz, okS := parseFloatStrict(tr.Qty)
		if !okP || !okS || px <= 0 {
			return
		}
		canon := strings.ToUpper(strings.TrimSpace(tr.Symbol))
		if canon == "" {
			canon = streamSym
		}
		if canon == "" {
			return
		}
		side := "buy"
		if tr.IsBuyerMaker {
			side = "sell"
		}
		ts := tr.TradeTime
		if ts <= 0 {
			ts = a.now().UTC().UnixMilli()
		}
		emitTrade(canon, TradeTick{
			ID:    strconv.FormatInt(tr.TradeID, 10),
			Time:  ts,
			Price: px,
			Size:  sz,
			Side:  side,
		})
		return
	}
	if strings.Contains(env.Stream, "@depth") || strings.Contains(string(payload), `"bids"`) {
		if streamSym == "" {
			return
		}
		snap, err := ParseBinanceDepth(payload, VenueBinance, streamSym, a.now().UTC().UnixMilli())
		if err == nil {
			emitBook(streamSym, snap)
		}
	}
}

// --- Shared mutable L2 orderbook delta helper (used by Bybit & Kraken adapters) ---

type l2BookState struct {
	bids map[float64]float64
	asks map[float64]float64
}

func newL2BookState() *l2BookState {
	return &l2BookState{
		bids: make(map[float64]float64),
		asks: make(map[float64]float64),
	}
}

func (b *l2BookState) reset() {
	clear(b.bids)
	clear(b.asks)
}

func (b *l2BookState) applyPairs(bidsPairs, asksPairs [][2]float64) {
	for _, lv := range bidsPairs {
		if lv[0] <= 0 {
			continue
		}
		if lv[1] <= 0 {
			delete(b.bids, lv[0])
		} else {
			b.bids[lv[0]] = lv[1]
		}
	}
	for _, lv := range asksPairs {
		if lv[0] <= 0 {
			continue
		}
		if lv[1] <= 0 {
			delete(b.asks, lv[0])
		} else {
			b.asks[lv[0]] = lv[1]
		}
	}
}

func (b *l2BookState) snapshot(venue, symbol string, tsMs int64, maxLevels int) OrderBookSnapshot {
	if maxLevels <= 0 {
		maxLevels = 25
	}
	bids := make([]OrderBookLevel, 0, len(b.bids))
	for px, sz := range b.bids {
		if sz > 0 {
			bids = append(bids, OrderBookLevel{Price: px, Size: sz})
		}
	}
	sort.Slice(bids, func(i, j int) bool { return bids[i].Price > bids[j].Price })
	if len(bids) > maxLevels {
		bids = bids[:maxLevels]
	}
	var runBid float64
	for i := range bids {
		runBid += bids[i].Size
		bids[i].Total = runBid
	}

	asks := make([]OrderBookLevel, 0, len(b.asks))
	for px, sz := range b.asks {
		if sz > 0 {
			asks = append(asks, OrderBookLevel{Price: px, Size: sz})
		}
	}
	sort.Slice(asks, func(i, j int) bool { return asks[i].Price < asks[j].Price })
	if len(asks) > maxLevels {
		asks = asks[:maxLevels]
	}
	var runAsk float64
	for i := range asks {
		runAsk += asks[i].Size
		asks[i].Total = runAsk
	}

	return buildOrderBookSnapshot(venue, symbol, tsMs, bids, asks, venue)
}

// --- Bybit v5 adapter ---

type bybitWSAdapter struct {
	books map[string]*l2BookState
}

func (a *bybitWSAdapter) pingInterval() time.Duration { return 20 * time.Second }
func (a *bybitWSAdapter) pingPayload() []byte         { return []byte(`{"op":"ping"}`) }

func (a *bybitWSAdapter) subscribePayloads(canonSyms []string) [][]byte {
	return a.opPayloads("subscribe", canonSyms)
}

func (a *bybitWSAdapter) unsubscribePayloads(canonSyms []string) [][]byte {
	return a.opPayloads("unsubscribe", canonSyms)
}

func (a *bybitWSAdapter) opPayloads(op string, canonSyms []string) [][]byte {
	if len(canonSyms) == 0 {
		return nil
	}
	args := make([]string, 0, len(canonSyms)*2)
	for _, sym := range canonSyms {
		args = append(args, "publicTrade."+sym, "orderbook.50."+sym)
	}
	raw, err := json.Marshal(map[string]any{
		"op":   op,
		"args": args,
	})
	if err != nil {
		return nil
	}
	return [][]byte{raw}
}

func parseStringLevelPairs(pairs [][]string) [][2]float64 {
	out := make([][2]float64, 0, len(pairs))
	for _, p := range pairs {
		if len(p) < 2 {
			continue
		}
		px, okP := parseFloatStrict(p[0])
		sz, okS := parseFloatStrict(p[1])
		if !okP || !okS || px <= 0 {
			continue
		}
		out = append(out, [2]float64{px, sz})
	}
	return out
}

func (a *bybitWSAdapter) handleFrame(raw []byte, emitTrade func(string, TradeTick), emitBook func(string, OrderBookSnapshot)) {
	var env struct {
		Topic string          `json:"topic"`
		Type  string          `json:"type"`
		Ts    int64           `json:"ts"`
		Data  json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &env) != nil || env.Topic == "" || len(env.Data) == 0 {
		return
	}
	if strings.HasPrefix(env.Topic, "publicTrade.") {
		sym := strings.TrimPrefix(env.Topic, "publicTrade.")
		var rows []struct {
			ExecID string `json:"i"`
			Time   int64  `json:"T"`
			Price  string `json:"p"`
			Size   string `json:"v"`
			Side   string `json:"S"`
		}
		if json.Unmarshal(env.Data, &rows) != nil {
			return
		}
		for _, r := range rows {
			px, okP := parseFloatStrict(r.Price)
			sz, okS := parseFloatStrict(r.Size)
			if !okP || !okS || px <= 0 {
				continue
			}
			side := "sell"
			if strings.EqualFold(r.Side, "Buy") {
				side = "buy"
			}
			emitTrade(sym, TradeTick{
				ID:    r.ExecID,
				Time:  r.Time,
				Price: px,
				Size:  sz,
				Side:  side,
			})
		}
		return
	}
	if strings.HasPrefix(env.Topic, "orderbook.") {
		var d struct {
			S string     `json:"s"`
			B [][]string `json:"b"`
			A [][]string `json:"a"`
		}
		if json.Unmarshal(env.Data, &d) != nil || d.S == "" {
			return
		}
		state, ok := a.books[d.S]
		if !ok {
			state = newL2BookState()
			a.books[d.S] = state
		}
		if env.Type == "snapshot" {
			state.reset()
		}
		state.applyPairs(parseStringLevelPairs(d.B), parseStringLevelPairs(d.A))
		emitBook(d.S, state.snapshot(VenueBybit, d.S, env.Ts, 25))
	}
}

// --- Kraken v2 adapter ---

type krakenWSAdapter struct {
	books map[string]*l2BookState
	now   func() time.Time
}

func (a *krakenWSAdapter) pingInterval() time.Duration { return 25 * time.Second }
func (a *krakenWSAdapter) pingPayload() []byte         { return []byte(`{"method":"ping"}`) }

func krakenToWSSymbol(canonSym string) string {
	up := strings.ToUpper(strings.TrimSpace(canonSym))
	if strings.Contains(up, "/") {
		return strings.ReplaceAll(up, "XBT/", "BTC/")
	}
	up = strings.ReplaceAll(up, "XBT", "BTC")
	for _, q := range []string{"USDT", "USDC", "USD", "EUR"} {
		if strings.HasSuffix(up, q) && len(up) > len(q) {
			return strings.TrimSuffix(up, q) + "/" + q
		}
	}
	return up + "/USD"
}

func krakenFromWSSymbol(wsSym string) string {
	noSlash := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(wsSym), "/", ""))
	if strings.HasPrefix(noSlash, "BTC") && noSlash == "BTCUSD" {
		return "XBTUSD"
	}
	return noSlash
}

func (a *krakenWSAdapter) subscribePayloads(canonSyms []string) [][]byte {
	return a.opPayloads("subscribe", canonSyms)
}

func (a *krakenWSAdapter) unsubscribePayloads(canonSyms []string) [][]byte {
	return a.opPayloads("unsubscribe", canonSyms)
}

func (a *krakenWSAdapter) opPayloads(method string, canonSyms []string) [][]byte {
	if len(canonSyms) == 0 {
		return nil
	}
	pairs := make([]string, 0, len(canonSyms))
	for _, sym := range canonSyms {
		pairs = append(pairs, krakenToWSSymbol(sym))
	}
	var out [][]byte
	if raw, err := json.Marshal(map[string]any{
		"method": method,
		"params": map[string]any{
			"channel": "trade",
			"symbol":  pairs,
		},
	}); err == nil {
		out = append(out, raw)
	}
	if raw, err := json.Marshal(map[string]any{
		"method": method,
		"params": map[string]any{
			"channel": "book",
			"symbol":  pairs,
			"depth":   25,
		},
	}); err == nil {
		out = append(out, raw)
	}
	return out
}

func parseKrakenTimestampMs(raw string, fallbackMs int64) int64 {
	if raw != "" {
		if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return t.UTC().UnixMilli()
		}
	}
	return fallbackMs
}

func (a *krakenWSAdapter) handleFrame(raw []byte, emitTrade func(string, TradeTick), emitBook func(string, OrderBookSnapshot)) {
	var env struct {
		Channel string          `json:"channel"`
		Type    string          `json:"type"`
		Data    json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &env) != nil || len(env.Data) == 0 {
		return
	}
	nowMs := a.now().UTC().UnixMilli()
	switch env.Channel {
	case "trade":
		var rows []struct {
			Symbol    string  `json:"symbol"`
			Side      string  `json:"side"`
			Qty       float64 `json:"qty"`
			Price     float64 `json:"price"`
			TradeID   int64   `json:"trade_id"`
			Timestamp string  `json:"timestamp"`
		}
		if json.Unmarshal(env.Data, &rows) != nil {
			return
		}
		for _, r := range rows {
			if !isFinitePositive(r.Price) || !isFiniteNonNeg(r.Qty) || r.Symbol == "" {
				continue
			}
			canon := krakenFromWSSymbol(r.Symbol)
			side := "sell"
			if strings.EqualFold(r.Side, "buy") {
				side = "buy"
			}
			emitTrade(canon, TradeTick{
				ID:    strconv.FormatInt(r.TradeID, 10),
				Time:  parseKrakenTimestampMs(r.Timestamp, nowMs),
				Price: r.Price,
				Size:  r.Qty,
				Side:  side,
			})
		}
	case "book":
		var rows []struct {
			Symbol string `json:"symbol"`
			Bids   []struct {
				Price float64 `json:"price"`
				Qty   float64 `json:"qty"`
			} `json:"bids"`
			Asks []struct {
				Price float64 `json:"price"`
				Qty   float64 `json:"qty"`
			} `json:"asks"`
			Timestamp string `json:"timestamp"`
		}
		if json.Unmarshal(env.Data, &rows) != nil {
			return
		}
		for _, r := range rows {
			if r.Symbol == "" {
				continue
			}
			canon := krakenFromWSSymbol(r.Symbol)
			state, ok := a.books[canon]
			if !ok {
				state = newL2BookState()
				a.books[canon] = state
			}
			if env.Type == "snapshot" {
				state.reset()
			}
			bidsPairs := make([][2]float64, 0, len(r.Bids))
			for _, b := range r.Bids {
				bidsPairs = append(bidsPairs, [2]float64{b.Price, b.Qty})
			}
			asksPairs := make([][2]float64, 0, len(r.Asks))
			for _, ak := range r.Asks {
				asksPairs = append(asksPairs, [2]float64{ak.Price, ak.Qty})
			}
			state.applyPairs(bidsPairs, asksPairs)
			emitBook(canon, state.snapshot(VenueKraken, canon, parseKrakenTimestampMs(r.Timestamp, nowMs), 25))
		}
	}
}
