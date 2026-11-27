package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/fased-ai/fased-app/internal/marketdata"
	"github.com/fased-ai/fased-app/internal/worldintel"
)

const (
	streamSubprotocol        = "fased.stream.v1"
	streamTicketPrefix       = "fased.ticket."
	streamTicketTTL          = 15 * time.Second
	maxStreamTickets         = 128
	maxActiveWSConns         = 32
	maxActiveSSEConns        = 32
	maxStreamTopicsPerConn   = 64
	streamOutboundBufferSize = 256
	wsHeartbeatInterval      = 20 * time.Second
	wsClientWriteTimeout     = 5 * time.Second
	wsInboundReadLimit       = 32 * 1024
)

type activeStreamEntry struct {
	sessionHash string
	cancel      context.CancelFunc
}

func (h *HTTPHandler) registerSessionStream(sessionHash string, cancel context.CancelFunc) func() {
	if h == nil || cancel == nil {
		return func() {}
	}
	h.streamRegMu.Lock()
	if h.activeStreams == nil {
		h.activeStreams = make(map[uint64]activeStreamEntry)
	}
	h.streamRegNext++
	id := h.streamRegNext
	h.activeStreams[id] = activeStreamEntry{
		sessionHash: sessionHash,
		cancel:      cancel,
	}
	h.streamRegMu.Unlock()
	return func() {
		h.streamRegMu.Lock()
		delete(h.activeStreams, id)
		h.streamRegMu.Unlock()
	}
}

func (h *HTTPHandler) closeStreamsForSessionHash(sessHash string) {
	if h == nil || sessHash == "" {
		return
	}
	h.streamTicketMu.Lock()
	for k, entry := range h.streamTickets {
		if tokenHash(entry.sessionToken) == sessHash {
			delete(h.streamTickets, k)
		}
	}
	h.streamTicketMu.Unlock()

	h.streamRegMu.Lock()
	var cancels []context.CancelFunc
	for id, entry := range h.activeStreams {
		if entry.sessionHash == sessHash {
			cancels = append(cancels, entry.cancel)
			delete(h.activeStreams, id)
		}
	}
	h.streamRegMu.Unlock()
	for _, fn := range cancels {
		fn()
	}
}

func (h *HTTPHandler) closeStreamsExceptSessionHash(keepHash string) {
	if h == nil {
		return
	}
	h.streamTicketMu.Lock()
	for k, entry := range h.streamTickets {
		if keepHash == "" || tokenHash(entry.sessionToken) != keepHash {
			delete(h.streamTickets, k)
		}
	}
	h.streamTicketMu.Unlock()

	h.streamRegMu.Lock()
	var cancels []context.CancelFunc
	for id, entry := range h.activeStreams {
		if keepHash == "" || entry.sessionHash != keepHash {
			cancels = append(cancels, entry.cancel)
			delete(h.activeStreams, id)
		}
	}
	h.streamRegMu.Unlock()
	for _, fn := range cancels {
		fn()
	}
}

func (h *HTTPHandler) closeAllStreams() {
	h.closeStreamsExceptSessionHash("")
}

func (h *HTTPHandler) acquireSSEStream(w http.ResponseWriter, r *http.Request, token string) (context.Context, func(), bool) {
	if h.activeSSEConns.Add(1) > maxActiveSSEConns {
		h.activeSSEConns.Add(-1)
		writeError(w, 429, "too many active event streams")
		return nil, nil, false
	}
	ctx, cancel := context.WithCancel(r.Context())
	unreg := h.registerSessionStream(tokenHash(token), cancel)
	release := func() {
		unreg()
		cancel()
		h.activeSSEConns.Add(-1)
	}
	return ctx, release, true
}

type streamTicketEntry struct {
	sessionToken string
	expiresAt    time.Time
}

type streamInboundFrame struct {
	Op     string   `json:"op"`
	Topics []string `json:"topics,omitempty"`
	Ts     int64    `json:"ts,omitempty"`
}

type streamOutboundFrame struct {
	Type      string   `json:"type"`
	Protocol  string   `json:"protocol,omitempty"`
	Topic     string   `json:"topic,omitempty"`
	Event     string   `json:"event,omitempty"`
	Topics    []string `json:"topics,omitempty"`
	Error     string   `json:"error,omitempty"`
	Timestamp int64    `json:"ts"`
	Payload   any      `json:"payload,omitempty"`
}

func (h *HTTPHandler) issueStreamTicket(sessionToken string) (string, int64, error) {
	if h == nil || sessionToken == "" {
		return "", 0, errors.New("session required")
	}
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", 0, err
	}
	ticket := hex.EncodeToString(raw[:])
	now := h.store.now()
	exp := now.Add(streamTicketTTL)

	h.streamTicketMu.Lock()
	defer h.streamTicketMu.Unlock()
	if h.streamTickets == nil {
		h.streamTickets = make(map[string]streamTicketEntry)
	}
	for k, entry := range h.streamTickets {
		if !now.Before(entry.expiresAt) {
			delete(h.streamTickets, k)
		}
	}
	for len(h.streamTickets) >= maxStreamTickets {
		var oldestKey string
		var oldestExp time.Time
		first := true
		for k, entry := range h.streamTickets {
			if first || entry.expiresAt.Before(oldestExp) {
				oldestKey = k
				oldestExp = entry.expiresAt
				first = false
			}
		}
		if oldestKey == "" {
			break
		}
		delete(h.streamTickets, oldestKey)
	}
	h.streamTickets[ticket] = streamTicketEntry{
		sessionToken: sessionToken,
		expiresAt:    exp,
	}
	return ticket, exp.UTC().UnixMilli(), nil
}

func (h *HTTPHandler) consumeStreamTicket(ticket string) string {
	cleaned := strings.TrimSpace(ticket)
	if h == nil || cleaned == "" {
		return ""
	}
	now := h.store.now()
	h.streamTicketMu.Lock()
	defer h.streamTicketMu.Unlock()
	entry, ok := h.streamTickets[cleaned]
	if !ok {
		return ""
	}
	delete(h.streamTickets, cleaned)
	if !now.Before(entry.expiresAt) {
		return ""
	}
	return entry.sessionToken
}

func (h *HTTPHandler) resolveStreamSession(r *http.Request) string {
	if tok := h.session(r); tok != "" {
		return tok
	}
	if r.URL.Path != "/api/v1/stream" || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return ""
	}
	for _, hdr := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, part := range strings.Split(hdr, ",") {
			p := strings.TrimSpace(part)
			if strings.HasPrefix(p, streamTicketPrefix) {
				if tok := h.consumeStreamTicket(strings.TrimPrefix(p, streamTicketPrefix)); tok != "" {
					return tok
				}
			}
		}
	}
	return ""
}

func (h *HTTPHandler) streamTicketAPI(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !h.v2Mutation(w, r, token, false) {
		return
	}
	if !h.allowRate(r, "stream-ticket", 60) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	ticket, expiresAt, err := h.issueStreamTicket(token)
	if err != nil {
		writeError(w, 500, "stream ticket unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{
		"ticket":    ticket,
		"expiresAt": expiresAt,
		"protocol":  streamSubprotocol,
	})
}

type parsedStreamTopic struct {
	raw      string
	kind     string // "candle", "orderbook", "trade", "footprint", "intel", "automation", "desk"
	venue    string
	symbol   string
	interval string
	style    string
}

func parseStreamTopic(raw string) (parsedStreamTopic, error) {
	trimmed := strings.TrimSpace(raw)
	switch trimmed {
	case "intel", "automation", "desk", "swarm", "tasks", "mining":
		return parsedStreamTopic{raw: trimmed, kind: trimmed}, nil
	}
	if strings.HasPrefix(trimmed, "task:") {
		taskID := strings.TrimSpace(strings.TrimPrefix(trimmed, "task:"))
		if taskID == "" || strings.ContainsAny(taskID, " \t\r\n/") {
			return parsedStreamTopic{}, errors.New("invalid task stream topic")
		}
		return parsedStreamTopic{raw: trimmed, kind: "task", symbol: taskID}, nil
	}
	parts := strings.Split(trimmed, ":")
	if len(parts) < 3 {
		return parsedStreamTopic{}, errors.New("invalid topic format")
	}
	kind := parts[0]
	v, err := marketdata.NormalizeVenue(parts[1])
	if err != nil {
		return parsedStreamTopic{}, err
	}
	sym, err := marketdata.NormalizeSymbol(parts[2])
	if err != nil {
		return parsedStreamTopic{}, err
	}
	switch kind {
	case "candle":
		if len(parts) < 4 || len(parts) > 5 {
			return parsedStreamTopic{}, errors.New("candle topic expects candle:<venue>:<symbol>:<interval>[:style]")
		}
		iv, err := marketdata.NormalizeInterval(parts[3])
		if err != nil {
			return parsedStreamTopic{}, err
		}
		style := marketdata.StyleStandard
		if len(parts) == 5 && strings.TrimSpace(parts[4]) != "" {
			style, err = marketdata.NormalizeStyle(parts[4])
			if err != nil {
				return parsedStreamTopic{}, err
			}
		}
		return parsedStreamTopic{raw: trimmed, kind: "candle", venue: v, symbol: sym, interval: iv, style: style}, nil
	case "footprint":
		if len(parts) != 4 {
			return parsedStreamTopic{}, errors.New("footprint topic expects footprint:<venue>:<symbol>:<interval>")
		}
		iv, err := marketdata.NormalizeInterval(parts[3])
		if err != nil {
			return parsedStreamTopic{}, err
		}
		return parsedStreamTopic{raw: trimmed, kind: "footprint", venue: v, symbol: sym, interval: iv}, nil
	case "orderbook", "trade":
		if len(parts) != 3 {
			return parsedStreamTopic{}, errors.New("topic expects <kind>:<venue>:<symbol>")
		}
		return parsedStreamTopic{raw: trimmed, kind: kind, venue: v, symbol: sym}, nil
	default:
		return parsedStreamTopic{}, errors.New("unsupported stream topic")
	}
}

func (h *HTTPHandler) streamWSAPI(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	hasBearer := strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
	if r.Header.Get("Origin") == "" && !hasBearer {
		writeError(w, 403, "origin or bearer auth required")
		return
	}
	if !h.allowRate(r, "stream-ws", 30) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	if h.activeWSConns.Add(1) > maxActiveWSConns {
		h.activeWSConns.Add(-1)
		writeError(w, 429, "too many active streams")
		return
	}
	defer h.activeWSConns.Add(-1)

	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	// HTTPHandler.ServeHTTP enforces r.Host, h.origins, and Sec-Fetch-Site before
	// dispatch, so InsecureSkipVerify simply skips coder/websocket's Host==Origin
	// duplicate check and allows configured --public-origin or loopback ports cleanly.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:       []string{streamSubprotocol},
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionNoContextTakeover,
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(wsInboundReadLimit)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	unreg := h.registerSessionStream(tokenHash(token), cancel)
	defer unreg()

	sendCh := make(chan streamOutboundFrame, streamOutboundBufferSize)
	enqueue := func(frame streamOutboundFrame) {
		select {
		case sendCh <- frame:
		case <-ctx.Done():
		default:
			select {
			case <-sendCh:
			default:
			}
			select {
			case sendCh <- frame:
			case <-ctx.Done():
			default:
			}
		}
	}

	var writeWG sync.WaitGroup
	writeWG.Add(1)
	go func() {
		defer writeWG.Done()
		heartbeat := time.NewTicker(wsHeartbeatInterval)
		defer heartbeat.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeat.C:
				wCtx, wCancel := context.WithTimeout(ctx, wsClientWriteTimeout)
				err := conn.Ping(wCtx)
				wCancel()
				if err != nil {
					cancel()
					return
				}
			case frame := <-sendCh:
				raw, mErr := json.Marshal(frame)
				if mErr != nil {
					continue
				}
				wCtx, wCancel := context.WithTimeout(ctx, wsClientWriteTimeout)
				err := conn.Write(wCtx, websocket.MessageText, raw)
				wCancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	var topicMu sync.Mutex
	activeTopics := make(map[string]func())
	var subWG sync.WaitGroup

	stopAllTopics := func() {
		topicMu.Lock()
		cancels := make([]func(), 0, len(activeTopics))
		for k, fn := range activeTopics {
			cancels = append(cancels, fn)
			delete(activeTopics, k)
		}
		topicMu.Unlock()
		for _, fn := range cancels {
			fn()
		}
	}

	subscribeTopic := func(rawTopic string) error {
		parsed, err := parseStreamTopic(rawTopic)
		if err != nil {
			return err
		}
		topicMu.Lock()
		if _, exists := activeTopics[parsed.raw]; exists {
			topicMu.Unlock()
			return nil
		}
		if len(activeTopics) >= maxStreamTopicsPerConn {
			topicMu.Unlock()
			return errors.New("stream topic subscription limit exceeded")
		}
		subCtx, subCancel := context.WithCancel(ctx)
		activeTopics[parsed.raw] = subCancel
		topicMu.Unlock()

		stopEntry := func() {
			subCancel()
			topicMu.Lock()
			delete(activeTopics, parsed.raw)
			topicMu.Unlock()
		}

		switch parsed.kind {
		case "intel":
			svc, sErr := h.store.worldIntelService()
			if sErr != nil {
				stopEntry()
				return errors.New("world intelligence unavailable")
			}
			ch, unsub := svc.Subscribe(worldintel.StreamFilter{})
			subWG.Add(1)
			go func() {
				defer subWG.Done()
				defer unsub()
				for {
					select {
					case <-subCtx.Done():
						return
					case evt, ok := <-ch:
						if !ok {
							return
						}
						enqueue(streamOutboundFrame{
							Type:      "event",
							Topic:     parsed.raw,
							Event:     evt.Type,
							Timestamp: evt.Timestamp,
							Payload:   evt,
						})
					}
				}
			}()
		case "automation":
			svc, sErr := h.store.automationService()
			if sErr != nil {
				stopEntry()
				return errors.New("automation unavailable")
			}
			ch, unsub := svc.Subscribe()
			subWG.Add(1)
			go func() {
				defer subWG.Done()
				defer unsub()
				for {
					select {
					case <-subCtx.Done():
						return
					case evt, ok := <-ch:
						if !ok {
							return
						}
						enqueue(streamOutboundFrame{
							Type:      "event",
							Topic:     parsed.raw,
							Event:     evt.Type,
							Timestamp: evt.Timestamp,
							Payload:   evt,
						})
					}
				}
			}()
		case "desk":
			svc, sErr := h.store.deskService()
			if sErr != nil {
				stopEntry()
				return errors.New("desk unavailable")
			}
			ch, unsub := svc.Subscribe()
			subWG.Add(1)
			go func() {
				defer subWG.Done()
				defer unsub()
				for {
					select {
					case <-subCtx.Done():
						return
					case evt, ok := <-ch:
						if !ok {
							return
						}
						enqueue(streamOutboundFrame{
							Type:      "event",
							Topic:     parsed.raw,
							Event:     evt.Type,
							Timestamp: evt.Timestamp,
							Payload:   evt,
						})
					}
				}
			}()
		case "tasks", "task":
			eng, tErr := h.store.tasksEngine()
			if tErr != nil {
				stopEntry()
				return errors.New("tasks engine unavailable")
			}
			ch, unsub := eng.Subscribe()
			subWG.Add(1)
			go func() {
				defer subWG.Done()
				defer unsub()
				for {
					select {
					case <-subCtx.Done():
						return
					case evt, ok := <-ch:
						if !ok {
							return
						}
						if parsed.kind == "task" && evt.TaskID != parsed.symbol && evt.Event != "tasks.killed" {
							continue
						}
						enqueue(streamOutboundFrame{
							Type:      "event",
							Topic:     parsed.raw,
							Event:     evt.Event,
							Timestamp: evt.Timestamp,
							Payload:   evt,
						})
					}
				}
			}()
		case "mining":
			mEng, mErr := h.store.wenLV1XEngine()
			if mErr != nil {
				stopEntry()
				return errors.New("mining engine unavailable")
			}
			ch, unsub := mEng.Subscribe()
			subWG.Add(1)
			go func() {
				defer subWG.Done()
				defer unsub()
				for {
					select {
					case <-subCtx.Done():
						return
					case evt, ok := <-ch:
						if !ok {
							return
						}
						enqueue(streamOutboundFrame{
							Type:      "event",
							Topic:     parsed.raw,
							Event:     evt.Event,
							Timestamp: evt.Timestamp,
							Payload:   evt,
						})
					}
				}
			}()
		case "swarm":
			svc, sErr := h.store.swarmService()
			if sErr != nil {
				stopEntry()
				return errors.New("swarm unavailable")
			}
			ch, unsub := svc.Subscribe()
			subWG.Add(1)
			go func() {
				defer subWG.Done()
				defer unsub()
				for {
					select {
					case <-subCtx.Done():
						return
					case evt, ok := <-ch:
						if !ok {
							return
						}
						enqueue(streamOutboundFrame{
							Type:      "event",
							Topic:     parsed.raw,
							Event:     evt.Type,
							Timestamp: evt.Timestamp,
							Payload:   evt.Envelope,
						})
					}
				}
			}()
		default:
			svc, sErr := h.store.marketDataService()
			if sErr != nil {
				stopEntry()
				return errors.New("market data service unavailable")
			}
			var filter marketdata.Filter
			switch parsed.kind {
			case "candle":
				filter = marketdata.Filter{Type: "candle", Venue: parsed.venue, Symbol: parsed.symbol, Interval: parsed.interval}
			case "orderbook":
				filter = marketdata.Filter{Type: "orderbook", Venue: parsed.venue, Symbol: parsed.symbol}
			case "trade":
				filter = marketdata.Filter{Type: "trade", Venue: parsed.venue, Symbol: parsed.symbol}
			case "footprint":
				filter = marketdata.Filter{Type: "footprint", Venue: parsed.venue, Symbol: parsed.symbol, Interval: parsed.interval}
			}
			ch, unsub := svc.Subscribe(filter)
			releaseFeed := svc.AcquireFeed(parsed.venue, parsed.symbol)
			subWG.Add(1)
			go func() {
				defer subWG.Done()
				defer releaseFeed()
				defer unsub()
				for {
					select {
					case <-subCtx.Done():
						return
					case evt, ok := <-ch:
						if !ok {
							return
						}
						payload := evt.Payload
						if parsed.kind == "candle" && parsed.style != "" && evt.Style != "" && evt.Style != parsed.style {
							if evt.Style != marketdata.StyleStandard {
								continue
							}
							if parsed.style == marketdata.StyleTick || parsed.style == marketdata.StyleVolume {
								continue
							}
							cached, has := svc.CachedCandles(parsed.venue, parsed.symbol, parsed.interval, 160)
							if !has || len(cached) == 0 {
								continue
							}
							transformed, tErr := marketdata.TransformCandles(cached, marketdata.TransformOptions{Style: parsed.style})
							if tErr != nil || len(transformed) == 0 {
								continue
							}
							payload = transformed[len(transformed)-1]
						}
						enqueue(streamOutboundFrame{
							Type:      "event",
							Topic:     parsed.raw,
							Event:     evt.Type,
							Timestamp: evt.Timestamp,
							Payload:   payload,
						})
					}
				}
			}()
		}
		return nil
	}

	enqueue(streamOutboundFrame{
		Type:      "ready",
		Protocol:  streamSubprotocol,
		Timestamp: h.store.now().UTC().UnixMilli(),
	})

	for {
		msgType, raw, readErr := conn.Read(ctx)
		if readErr != nil {
			break
		}
		if msgType != websocket.MessageText && msgType != websocket.MessageBinary {
			continue
		}
		var req streamInboundFrame
		if json.Unmarshal(raw, &req) != nil {
			enqueue(streamOutboundFrame{
				Type:      "error",
				Error:     "invalid stream control frame",
				Timestamp: h.store.now().UTC().UnixMilli(),
			})
			continue
		}
		switch strings.ToLower(strings.TrimSpace(req.Op)) {
		case "ping":
			enqueue(streamOutboundFrame{
				Type:      "pong",
				Timestamp: h.store.now().UTC().UnixMilli(),
			})
		case "sub":
			var subscribed []string
			for _, topic := range req.Topics {
				if sErr := subscribeTopic(topic); sErr != nil {
					enqueue(streamOutboundFrame{
						Type:      "error",
						Topic:     topic,
						Error:     sErr.Error(),
						Timestamp: h.store.now().UTC().UnixMilli(),
					})
				} else {
					subscribed = append(subscribed, strings.TrimSpace(topic))
				}
			}
			if len(subscribed) > 0 {
				enqueue(streamOutboundFrame{
					Type:      "ack",
					Event:     "sub",
					Topics:    subscribed,
					Timestamp: h.store.now().UTC().UnixMilli(),
				})
			}
		case "unsub":
			var removed []string
			topicMu.Lock()
			for _, topic := range req.Topics {
				k := strings.TrimSpace(topic)
				if fn, ok := activeTopics[k]; ok {
					fn()
					delete(activeTopics, k)
					removed = append(removed, k)
				}
			}
			topicMu.Unlock()
			if len(removed) > 0 {
				enqueue(streamOutboundFrame{
					Type:      "ack",
					Event:     "unsub",
					Topics:    removed,
					Timestamp: h.store.now().UTC().UnixMilli(),
				})
			}
		default:
			enqueue(streamOutboundFrame{
				Type:      "error",
				Error:     "unsupported stream operation",
				Timestamp: h.store.now().UTC().UnixMilli(),
			})
		}
	}

	cancel()
	stopAllTopics()
	_ = conn.Close(websocket.StatusGoingAway, "")
	_ = conn.CloseNow()
	writeWG.Wait()
	subWG.Wait()
}
