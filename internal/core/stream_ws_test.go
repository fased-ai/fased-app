package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/fased-ai/fased-app/internal/marketdata"
)

func httpToWSURL(raw string) string {
	return "ws" + strings.TrimPrefix(raw, "http")
}

func TestStreamWSTicketSecurityTopicsAndShutdown(t *testing.T) {
	t.Parallel()
	dir := fixtureDir(t)
	server, err := StartAgentFixture(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatalf("StartAgentFixture failed: %v", err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve() }()
	closed := false
	t.Cleanup(func() {
		if !closed {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = server.Close(ctx)
			<-serveDone
		}
	})

	token := claimFixture(t, server.Store)
	csrf := csrfToken(token)

	// 1. /api/v1/stream/ticket enforces auth & CSRF.
	unauthTicket := doV2JSONRequest(t, http.MethodPost, server.URL+"/api/v1/stream/ticket", server.URL, "", "", `{}`, "application/json")
	unauthTicket.Body.Close()
	if unauthTicket.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthenticated stream/ticket, got %d", unauthTicket.StatusCode)
	}

	noCSRF := doV2JSONRequest(t, http.MethodPost, server.URL+"/api/v1/stream/ticket", server.URL, token, "", `{}`, "application/json")
	noCSRF.Body.Close()
	if noCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF on stream/ticket, got %d", noCSRF.StatusCode)
	}

	ticketResp := doV2JSONRequest(t, http.MethodPost, server.URL+"/api/v1/stream/ticket", server.URL, token, csrf, `{}`, "application/json")
	var ticketPayload struct {
		Ticket   string `json:"ticket"`
		Protocol string `json:"protocol"`
	}
	if ticketResp.StatusCode != http.StatusOK || json.NewDecoder(ticketResp.Body).Decode(&ticketPayload) != nil || ticketPayload.Ticket == "" {
		ticketResp.Body.Close()
		t.Fatalf("expected 200 valid ticket response, got %d (%+v)", ticketResp.StatusCode, ticketPayload)
	}
	ticketResp.Body.Close()

	// 2. Reject cross-origin WS upgrade cleanly with 403.
	if _, resp, dialErr := websocket.Dial(t.Context(), httpToWSURL(server.URL)+"/api/v1/stream", &websocket.DialOptions{
		Subprotocols: []string{"fased.stream.v1", "fased.ticket." + ticketPayload.Ticket},
		HTTPHeader:   http.Header{"Origin": []string{"https://evil.example"}},
	}); dialErr == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 cross-origin WS rejection, got resp=%v err=%v", resp, dialErr)
	}

	// 3. Reject missing Origin without Bearer header.
	if _, resp, dialErr := websocket.Dial(t.Context(), httpToWSURL(server.URL)+"/api/v1/stream", &websocket.DialOptions{
		Subprotocols: []string{"fased.stream.v1", "fased.ticket." + ticketPayload.Ticket},
	}); dialErr == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 missing-Origin without Bearer WS rejection, got resp=%v err=%v", resp, dialErr)
	}

	// 4. Reject unauthenticated WS upgrade cleanly with 401.
	if _, resp, dialErr := websocket.Dial(t.Context(), httpToWSURL(server.URL)+"/api/v1/stream", &websocket.DialOptions{
		Subprotocols: []string{"fased.stream.v1"},
		HTTPHeader:   http.Header{"Origin": []string{server.URL}},
	}); dialErr == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthenticated WS rejection, got resp=%v err=%v", resp, dialErr)
	}

	// 5. Connect with valid single-use ticket + same Origin, verify handshake & topic multiplexing.
	conn, resp, err := websocket.Dial(t.Context(), httpToWSURL(server.URL)+"/api/v1/stream", &websocket.DialOptions{
		Subprotocols: []string{"fased.stream.v1", "fased.ticket." + ticketPayload.Ticket},
		HTTPHeader:   http.Header{"Origin": []string{server.URL}},
	})
	if err != nil {
		t.Fatalf("WS Dial failed: %v (resp=%v)", err, resp)
	}
	if conn.Subprotocol() != "fased.stream.v1" {
		t.Fatalf("expected negotiated subprotocol fased.stream.v1, got %q", conn.Subprotocol())
	}

	// Ticket must be strictly single-use (replay must fail 401).
	if _, replayResp, replayErr := websocket.Dial(t.Context(), httpToWSURL(server.URL)+"/api/v1/stream", &websocket.DialOptions{
		Subprotocols: []string{"fased.stream.v1", "fased.ticket." + ticketPayload.Ticket},
		HTTPHeader:   http.Header{"Origin": []string{server.URL}},
	}); replayErr == nil || replayResp == nil || replayResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected replayed ticket to fail 401, got resp=%v err=%v", replayResp, replayErr)
	}

	readFrame := func() streamOutboundFrame {
		t.Helper()
		rCtx, rCancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer rCancel()
		_, raw, rErr := conn.Read(rCtx)
		if rErr != nil {
			t.Fatalf("WS Read failed: %v", rErr)
		}
		var out streamOutboundFrame
		if jErr := json.Unmarshal(raw, &out); jErr != nil {
			t.Fatalf("decode WS frame %q: %v", raw, jErr)
		}
		return out
	}

	ready := readFrame()
	if ready.Type != "ready" || ready.Protocol != "fased.stream.v1" {
		t.Fatalf("expected initial ready frame, got %+v", ready)
	}

	// Ping -> pong check.
	if err := conn.Write(t.Context(), websocket.MessageText, []byte(`{"op":"ping"}`)); err != nil {
		t.Fatalf("write ping failed: %v", err)
	}
	pong := readFrame()
	if pong.Type != "pong" {
		t.Fatalf("expected pong, got %+v", pong)
	}

	// Seed standard candle into fixture cache before subscribing Heikin Ashi + standard + orderbook topics.
	mdSvc, err := server.Store.marketDataService()
	if err != nil {
		t.Fatalf("marketDataService failed: %v", err)
	}
	if _, err := mdSvc.GetCandles(t.Context(), marketdata.VenueHyperliquid, "SOL", "15m", marketdata.TransformOptions{Style: marketdata.StyleStandard}, 20); err != nil {
		t.Fatalf("seed GetCandles failed: %v", err)
	}

	subReq := `{"op":"sub","topics":["candle:hyperliquid:BTC:1h:standard","candle:hyperliquid:SOL:15m:heikin_ashi","orderbook:hyperliquid:BTC"]}`
	if err := conn.Write(t.Context(), websocket.MessageText, []byte(subReq)); err != nil {
		t.Fatalf("write sub failed: %v", err)
	}
	ack := readFrame()
	if ack.Type != "ack" || ack.Event != "sub" || len(ack.Topics) != 3 {
		t.Fatalf("expected 3-topic sub ack, got %+v", ack)
	}

	// Verify optional SQLite DBs were NOT initialized when only market topics were subscribed.
	for _, relDB := range []string{"worldintel/intel.sqlite", "automation/automation.sqlite", "desk/desk.sqlite"} {
		if _, statErr := os.Stat(filepath.Join(dir, relDB)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("expected %s to remain absent on market-only WS subscription, statErr=%v", relDB, statErr)
		}
	}

	// Publish filtered + matching events.
	mdSvc.Publish(marketdata.Event{
		Type: "candle", Venue: "hyperliquid", Symbol: "BTC", Interval: "5m", Style: "standard",
		Payload: marketdata.Candle{Time: 1760000000, Open: 1, High: 2, Low: 1, Close: 1.5, Volume: 10},
	})
	mdSvc.Publish(marketdata.Event{
		Type: "candle", Venue: "hyperliquid", Symbol: "BTC", Interval: "1h", Style: "standard",
		Payload: marketdata.Candle{Time: 1760003600, Open: 94000, High: 94200, Low: 93900, Close: 94123, Volume: 55},
	})
	mdSvc.Publish(marketdata.Event{
		Type: "orderbook", Venue: "hyperliquid", Symbol: "BTC",
		Payload: marketdata.OrderBookSnapshot{Venue: "hyperliquid", Symbol: "BTC", Spread: 5, MidPrice: 94123},
	})

	got := make(map[string]streamOutboundFrame, 2)
	for i := 0; i < 2; i++ {
		evt := readFrame()
		if evt.Type != "event" {
			t.Fatalf("unexpected non-event WS frame: %+v", evt)
		}
		got[evt.Topic] = evt
	}
	if evt, ok := got["candle:hyperliquid:BTC:1h:standard"]; !ok || evt.Event != "candle" {
		t.Fatalf("missing or unexpected candle WS event: got=%+v", got)
	}
	if evt, ok := got["orderbook:hyperliquid:BTC"]; !ok || evt.Event != "orderbook" {
		t.Fatalf("missing or unexpected orderbook WS event: got=%+v", got)
	}

	// Unsub from candle topic.
	if err := conn.Write(t.Context(), websocket.MessageText, []byte(`{"op":"unsub","topics":["candle:hyperliquid:BTC:1h:standard"]}`)); err != nil {
		t.Fatalf("write unsub failed: %v", err)
	}
	unsubAck := readFrame()
	if unsubAck.Type != "ack" || unsubAck.Event != "unsub" || len(unsubAck.Topics) != 1 {
		t.Fatalf("expected unsub ack, got %+v", unsubAck)
	}

	// Verify Server.Close terminates cleanly even while WS connection remains open!
	closeStart := time.Now()
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer closeCancel()
	if err := server.Close(closeCtx); err != nil {
		t.Fatalf("Server.Close with open WS failed: %v", err)
	}
	closed = true
	if elapsed := time.Since(closeStart); elapsed >= 2*time.Second {
		t.Fatalf("Server.Close with open WS stalled (%s)", elapsed)
	}
	if err := <-serveDone; err != nil {
		t.Fatalf("server.Serve returned error after Close: %v", err)
	}
}

func TestMarketDataSSEDerivedHeikinAshiStyleTransform(t *testing.T) {
	t.Parallel()
	dir := fixtureDir(t)
	server, err := StartAgentFixture(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatalf("StartAgentFixture failed: %v", err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Close(ctx)
		<-serveDone
	})

	token := claimFixture(t, server.Store)
	mdSvc, err := server.Store.marketDataService()
	if err != nil {
		t.Fatalf("marketDataService failed: %v", err)
	}
	if _, err := mdSvc.GetCandles(t.Context(), "hyperliquid", "BTC", "15m", marketdata.TransformOptions{Style: marketdata.StyleStandard}, 20); err != nil {
		t.Fatalf("warm GetCandles failed: %v", err)
	}

	sseCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(sseCtx, http.MethodGet, server.URL+"/api/v1/market/stream?noSnapshot=1&subs=hyperliquid:BTC:15m:heikin_ashi", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{}
	httpResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("SSE GET failed: %v", err)
	}
	defer httpResp.Body.Close()

	sc := bufio.NewScanner(httpResp.Body)
	sawReady := false
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event: ready") {
			sawReady = true
			break
		}
	}
	if !sawReady {
		t.Fatalf("expected SSE ready event")
	}

	mdSvc.Publish(marketdata.Event{
		Type:     "candle",
		Venue:    "hyperliquid",
		Symbol:   "BTC",
		Interval: "15m",
		Style:    marketdata.StyleStandard,
		Payload:  marketdata.Candle{Time: 1760003600, Open: 94000, High: 94200, Low: 93900, Close: 94150, Volume: 10},
	})

	sawHA := false
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"style":"heikin_ashi"`) {
			sawHA = true
			break
		}
	}
	if !sawHA {
		t.Fatalf("expected SSE heikin_ashi transformed candle event")
	}
}
