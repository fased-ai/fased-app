// Package desktop contains the native window's bounded, unprivileged transport.
// It never loads CLI sessions, starts another engine, or owns signing material.
package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/connection"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/ownerclient"
)

type Handler struct {
	openBrowser     func(string, string) error
	openWalletOwner func(string) error
	dir             string
	assets          http.Handler
	newClient       func(context.Context, string) (*core.Client, bool, json.RawMessage, error)
	mu              sync.Mutex
	instance        string
}

// The native launcher installs this public-intent callback. It cannot receive a
// key/password or provide owner RPC authority to the ordinary core-proxy window.
func (h *Handler) SetWalletOwnerOpener(open func(string) error) { h.openWalletOwner = open }

func NewHandler(dir string, assets fs.FS) *Handler {
	return &Handler{dir: dir, assets: http.FileServer(http.FS(assets)), newClient: newPublicClient, openBrowser: func(url, id string) error { return connection.OpenBrowser(url, id) }}
}

// Wails v2 uses these fixed local origins on its supported platforms. No network
// origin, external URL, path argument, file reader or generic Go binding is exposed.
func nativeOrigin(origin string) bool {
	return origin == "wails://wails" || origin == "http://wails.localhost" || origin == "https://wails.localhost"
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self' wails://wails; script-src 'self' wails://wails; style-src 'self' wails://wails; img-src 'self' wails://wails data:; connect-src 'self' wails://wails; frame-ancestors 'none'; object-src 'none'; base-uri 'none'; form-action 'self'")
	if r.Host != "wails" && r.Host != "wails.localhost" {
		fail(w, 403, "unexpected desktop host")
		return
	}
	origin := r.Header.Get("Origin")
	// WebKit's Wails adapter normalizes away the scheme and Origin. Its fixed
	// native asset host identifies this transport; it is never a TCP listener.
	// The custom WebKit scheme may also have an opaque origin. This exception is
	// limited to the native asset handler, never the browser's HTTP listener.
	// It supplies no credentials: bearer authentication and CSRF remain required.
	opaque := (r.URL.Scheme == "wails" || r.URL.Scheme == "") && r.Host == "wails" && (origin == "null" || origin == "")
	if origin != "" && !nativeOrigin(origin) && !opaque || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		fail(w, 403, "unexpected desktop origin")
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		if r.Method != "GET" && r.Method != "HEAD" {
			fail(w, 405, "method not allowed")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		h.assets.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if !allowed(r.Method, r.URL.Path) || !allowedQuery(r) {
		fail(w, 404, "unknown desktop operation")
		return
	}
	if r.Method != "GET" && !nativeOrigin(origin) && !opaque {
		fail(w, 403, "desktop origin required")
		return
	}
	reqLimit := RequestBodyLimit(r.URL.Path)
	body, err := io.ReadAll(io.LimitReader(r.Body, reqLimit+1))
	if err != nil || int64(len(body)) > reqLimit {
		fail(w, 413, "request too large")
		return
	}
	if r.URL.Path == "/api/v1/installation/wallet-owner" {
		var request struct {
			Link string `json:"link"`
		}
		d := json.NewDecoder(bytes.NewReader(body))
		d.DisallowUnknownFields()
		if d.Decode(&request) != nil || d.Decode(new(any)) != io.EOF {
			fail(w, 400, "invalid wallet owner intent")
			return
		}
		if _, _, err := ownerclient.ParseLink(request.Link); err != nil {
			fail(w, 400, "invalid wallet owner intent")
			return
		}
		if h.openWalletOwner == nil {
			fail(w, 503, "open the protected wallet in Fased")
			return
		}
		if err := h.openWalletOwner(request.Link); err != nil {
			fail(w, 503, "cannot open the protected owner window")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"opened": true})
		return
	}
	if r.URL.Path == "/api/v1/installation/connect" {
		var target struct {
			URL        string `json:"url"`
			InstanceID string `json:"instanceId"`
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&target) != nil || decoder.Decode(new(any)) != io.EOF {
			fail(w, 400, "choose one instance address")
			return
		}
		result, e := connection.Probe(r.Context(), target.URL, target.InstanceID)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		if e = h.openBrowser(result.URL, result.InstanceID); e != nil {
			fail(w, 503, "cannot open the browser; use fased-app connect --url with this address")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	client, enrolled, health, err := h.newClient(r.Context(), h.dir)
	if err != nil {
		fail(w, 503, "Fased is unavailable. Reopen the app.")
		return
	}
	defer client.Close()
	h.mu.Lock()
	if h.instance == "" {
		h.instance = client.Endpoint.InstanceID
	}
	match := h.instance == client.Endpoint.InstanceID
	h.mu.Unlock()
	if !match {
		fail(w, 409, "instance identity changed; close the window and select the instance again")
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/v1/health" && health != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(health)
		return
	}
	if r.URL.Path == "/api/v1/installation/local-owner" {
		if len(bytes.TrimSpace(body)) != 2 || string(bytes.TrimSpace(body)) != "{}" {
			fail(w, 400, "invalid Local access request")
			return
		}
		if enrolled {
			err = client.NativeLocalOwner(r.Context())
		} else {
			err = client.LocalOwner(r.Context())
		}
		if err != nil {
			fail(w, 403, "Local OS owner access required")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": true, "sessionToken": client.Token, "csrfToken": client.CSRF, "sessionMode": "bearer"})
		return
	}
	if r.URL.Path == "/api/v1/installation/claim-code" {
		var auth struct {
			Claimed bool `json:"claimed"`
		}
		if err = client.Do(r.Context(), http.MethodGet, "/api/v1/auth/state", nil, &auth); err != nil || auth.Claimed {
			fail(w, 409, "workspace is already claimed or unavailable")
			return
		}
		data, e := core.ReadPrivateFile(filepath.Join(h.dir, "claim-code.json"))
		var code core.ClaimCode
		if e != nil || len(data) > 4096 || json.Unmarshal(data, &code) != nil || time.Now().After(code.ExpiresAt) {
			setupLock, lockErr := core.LockOwnerSetup(h.dir)
			if lockErr != nil {
				fail(w, 409, "Reopen Fased to finish setup.")
				return
			}
			store, openErr := core.OpenStore(h.dir)
			if openErr != nil {
				setupLock.Close()
				fail(w, 409, "Reopen Fased to finish setup.")
				return
			}
			code, e = store.IssueClaimCode(r.Context())
			store.Close()
			setupLock.Close()
			if e != nil {
				fail(w, 409, "Reopen Fased to finish setup.")
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(code)
		return
	}
	response, err := forward(r.Context(), client, r, body)
	if err != nil {
		fail(w, 503, "Fased is unavailable. Reconnect.")
		return
	}
	defer response.Body.Close()
	if r.URL.Path == "/api/v1/mcp" && response.StatusCode == http.StatusAccepted {
		n, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 1025))
		if readErr != nil || n > 1024 {
			fail(w, 502, "invalid private core response")
			return
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if StreamingRoute(r.URL.Path) && response.StatusCode == http.StatusOK && strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		streamSSEResponse(w, response.Body)
		return
	}
	limit := responseLimit(r.URL.Path)
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(data)) > limit || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		fail(w, 502, "invalid private core response")
		return
	}
	if enrolled && r.URL.Path == "/api/v1/auth/state" && response.StatusCode == 200 {
		var state map[string]any
		if json.Unmarshal(data, &state) != nil {
			fail(w, 502, "invalid protected owner state")
			return
		}
		state["localAccess"] = true
		data, _ = json.Marshal(state)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(data)
}

const (
	// MaxNativeRequestBytes caps the raw HTTP wire request read by the protected
	// app-identity stdio transport (headers + largest allowed desktop body).
	MaxNativeRequestBytes  = 704 * 1024
	sseStreamResponseLimit = 16 * 1024 * 1024
)

func streamSSEResponse(w http.ResponseWriter, body io.Reader) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}
	buf := make([]byte, 4096)
	var written int64
	for {
		n, err := body.Read(buf)
		if n > 0 {
			remaining := sseStreamResponseLimit - written
			if remaining <= 0 {
				return
			}
			if int64(n) > remaining {
				n = int(remaining)
			}
			if _, wErr := w.Write(buf[:n]); wErr != nil {
				return
			}
			written += int64(n)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

func validPathSegment(seg string) bool {
	if len(seg) == 0 || len(seg) > 128 || seg == "." || seg == ".." {
		return false
	}
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.'
		if !ok {
			return false
		}
	}
	return true
}

// StreamingRoute reports whether route may return a long-lived text/event-stream
// response from the core.
func StreamingRoute(route string) bool {
	if route == "/api/v1/market/stream" || route == "/api/v1/intel/stream" || route == "/api/v1/automation/stream" || route == "/api/v1/desk/stream" {
		return true
	}
	if !strings.HasPrefix(route, "/api/v1/agents/v2/") {
		return false
	}
	rel := strings.TrimPrefix(route, "/api/v1/agents/v2/")
	if strings.HasPrefix(rel, "threads/") {
		sub := strings.TrimPrefix(rel, "threads/")
		if strings.HasSuffix(sub, "/messages") {
			return validPathSegment(strings.TrimSuffix(sub, "/messages"))
		}
		if strings.HasSuffix(sub, "/side") {
			return validPathSegment(strings.TrimSuffix(sub, "/side"))
		}
		return false
	}
	if strings.HasPrefix(rel, "approvals/") {
		return validPathSegment(strings.TrimPrefix(rel, "approvals/"))
	}
	return false
}

// RequestBodyLimit returns the maximum request body size admitted for route.
// Legacy routes keep their original 16 KiB bound while Agent V2 and MCP match
// the core's validated schema limits.
func RequestBodyLimit(route string) int64 {
	switch {
	case route == "/api/v1/agents/v2/model-manifest":
		return 640 * 1024
	case route == "/api/v1/market/ingest":
		return 512 * 1024
	case strings.HasPrefix(route, "/api/v1/agents/v2/"):
		return 256 * 1024
	case route == "/api/v1/mcp" || strings.HasPrefix(route, "/api/v1/mcp/clients") || strings.HasPrefix(route, "/api/v1/decision/") || strings.HasPrefix(route, "/api/v1/market/") || strings.HasPrefix(route, "/api/v1/intel/") || strings.HasPrefix(route, "/api/v1/automation/") || strings.HasPrefix(route, "/api/v1/lab/") || strings.HasPrefix(route, "/api/v1/desk/") || strings.HasPrefix(route, "/api/v1/trade/"):
		return 64 * 1024
	default:
		return 16 * 1024
	}
}

func allowedAgentsV2(method, route string) bool {
	if !strings.HasPrefix(route, "/api/v1/agents/v2/") {
		return false
	}
	rel := strings.TrimPrefix(route, "/api/v1/agents/v2/")
	switch rel {
	case "tools", "usage", "task-threads":
		return method == http.MethodGet
	case "model-manifest":
		return method == http.MethodGet || method == http.MethodPut || method == http.MethodDelete
	case "threads":
		return method == http.MethodGet || method == http.MethodPost
	case "memories":
		return method == http.MethodGet || method == http.MethodPost
	}
	if strings.HasPrefix(rel, "threads/") {
		sub := strings.TrimPrefix(rel, "threads/")
		if strings.HasSuffix(sub, "/messages") {
			return method == http.MethodPost && validPathSegment(strings.TrimSuffix(sub, "/messages"))
		}
		if strings.HasSuffix(sub, "/compact") {
			return method == http.MethodPost && validPathSegment(strings.TrimSuffix(sub, "/compact"))
		}
		if strings.HasSuffix(sub, "/side") {
			return method == http.MethodPost && validPathSegment(strings.TrimSuffix(sub, "/side"))
		}
		if !validPathSegment(sub) {
			return false
		}
		return method == http.MethodGet || method == http.MethodPatch || method == http.MethodPut || method == http.MethodDelete
	}
	if strings.HasPrefix(rel, "approvals/") {
		return method == http.MethodPost && validPathSegment(strings.TrimPrefix(rel, "approvals/"))
	}
	if strings.HasPrefix(rel, "memories/") {
		return (method == http.MethodPatch || method == http.MethodDelete) && validPathSegment(strings.TrimPrefix(rel, "memories/"))
	}
	return false
}

func allowed(method, route string) bool {
	if allowedAgentsV2(method, route) {
		return true
	}
	if strings.HasPrefix(route, "/api/v1/market/watchlists/") {
		return method == http.MethodDelete && validPathSegment(strings.TrimPrefix(route, "/api/v1/market/watchlists/"))
	}
	if strings.HasPrefix(route, "/api/v1/market/drawings/") {
		return method == http.MethodDelete && validPathSegment(strings.TrimPrefix(route, "/api/v1/market/drawings/"))
	}
	if strings.HasPrefix(route, "/api/v1/mcp/clients/") {
		rel := strings.TrimPrefix(route, "/api/v1/mcp/clients/")
		switch {
		case strings.HasSuffix(rel, "/sync"):
			return method == http.MethodPost && validPathSegment(strings.TrimSuffix(rel, "/sync"))
		case strings.HasSuffix(rel, "/call"):
			return method == http.MethodPost && validPathSegment(strings.TrimSuffix(rel, "/call"))
		default:
			return method == http.MethodDelete && validPathSegment(rel)
		}
	}
	if strings.HasPrefix(route, "/api/v1/intel/sources/") {
		return method == http.MethodDelete && validPathSegment(strings.TrimPrefix(route, "/api/v1/intel/sources/"))
	}
	if strings.HasPrefix(route, "/api/v1/automation/triggers/") {
		rel := strings.TrimPrefix(route, "/api/v1/automation/triggers/")
		if strings.HasSuffix(rel, "/test") {
			return method == http.MethodPost && validPathSegment(strings.TrimSuffix(rel, "/test"))
		}
		return method == http.MethodDelete && validPathSegment(rel)
	}
	if strings.HasPrefix(route, "/api/v1/lab/scripts/") {
		return method == http.MethodDelete && validPathSegment(strings.TrimPrefix(route, "/api/v1/lab/scripts/"))
	}
	if strings.HasPrefix(route, "/api/v1/lab/runs/") {
		return method == http.MethodGet && validPathSegment(strings.TrimPrefix(route, "/api/v1/lab/runs/"))
	}
	if strings.HasPrefix(route, "/api/v1/desk/tickets/") {
		rel := strings.TrimPrefix(route, "/api/v1/desk/tickets/")
		if strings.HasSuffix(rel, "/approve") {
			return method == http.MethodPost && validPathSegment(strings.TrimSuffix(rel, "/approve"))
		}
		if strings.HasSuffix(rel, "/reject") {
			return method == http.MethodPost && validPathSegment(strings.TrimSuffix(rel, "/reject"))
		}
		return false
	}
	if strings.HasPrefix(route, "/api/v1/desk/positions/") {
		rel := strings.TrimPrefix(route, "/api/v1/desk/positions/")
		if rel == "mark" {
			return method == http.MethodPost
		}
		if strings.HasSuffix(rel, "/close") {
			return method == http.MethodPost && validPathSegment(strings.TrimSuffix(rel, "/close"))
		}
		return false
	}
	if strings.HasPrefix(route, "/api/v1/desk/lessons/") {
		return method == http.MethodDelete && validPathSegment(strings.TrimPrefix(route, "/api/v1/desk/lessons/"))
	}
	if strings.HasPrefix(route, "/api/v1/trade/orders/") {
		rel := strings.TrimPrefix(route, "/api/v1/trade/orders/")
		if strings.HasSuffix(rel, "/cancel") {
			return method == http.MethodPost && validPathSegment(strings.TrimSuffix(rel, "/cancel"))
		}
		return false
	}
	switch route {
	case "/api/v1/health", "/api/v1/auth/state", "/api/v1/signer/status", "/api/v1/installation/claim-code":
		return method == "GET"
	case "/api/v1/installation/connect", "/api/v1/installation/local-owner", "/api/v1/installation/wallet-owner":
		return method == "POST"
	case "/api/v1/agents":
		return method == "GET" || method == "POST"
	case "/api/v1/mcp":
		return method == "GET" || method == "POST"
	case "/api/v1/updates/application":
		return method == "GET" || method == "POST"
	case "/api/v1/ai/connection":
		return method == "GET" || method == "POST"
	case "/api/v1/accounts", "/api/v1/accounts/portable", "/api/v1/updates/preferences":
		return method == "GET" || method == "PUT"
	case "/api/v1/accounts/setup", "/api/v1/accounts/rpc", "/api/v1/accounts/rpc-credential", "/api/v1/accounts/venue-credential":
		return method == "PUT"
	case "/api/v1/accounts/rpc-check", "/api/v1/accounts/read":
		return method == "POST"
	case "/api/v1/updates/status", "/api/v1/updates/desktop-release":
		return method == "GET"
	case "/api/v1/setup":
		return method == "GET" || method == "PUT"
	case "/api/v1/auth/claim", "/api/v1/auth/login", "/api/v1/auth/logout", "/api/v1/auth/pair":
		return method == "POST"
	case "/api/v1/auth/devices", "/api/v1/auth/pair/devices":
		return method == "GET" || method == "POST"
	case "/api/v1/market/instruments", "/api/v1/market/candles", "/api/v1/market/orderbook", "/api/v1/market/trades", "/api/v1/market/footprint", "/api/v1/market/macro", "/api/v1/market/sec", "/api/v1/market/cohorts", "/api/v1/market/custom-feeds", "/api/v1/market/stream":
		return method == http.MethodGet
	case "/api/v1/market/ingest":
		return method == http.MethodPost
	case "/api/v1/market/watchlists", "/api/v1/market/drawings":
		return method == http.MethodGet || method == http.MethodPut
	case "/api/v1/decision/config":
		return method == http.MethodGet || method == http.MethodPut
	case "/api/v1/decision/evaluate":
		return method == http.MethodPost
	case "/api/v1/mcp/clients":
		return method == http.MethodGet || method == http.MethodPut
	case "/api/v1/intel/events", "/api/v1/intel/map", "/api/v1/intel/predictions", "/api/v1/intel/stream":
		return method == http.MethodGet
	case "/api/v1/intel/sources":
		return method == http.MethodGet || method == http.MethodPut
	case "/api/v1/intel/sync":
		return method == http.MethodPost
	case "/api/v1/automation/state":
		return method == http.MethodGet || method == http.MethodPost
	case "/api/v1/automation/triggers", "/api/v1/automation/webhook", "/api/v1/automation/telegram":
		return method == http.MethodGet || method == http.MethodPut
	case "/api/v1/automation/evaluate", "/api/v1/automation/notifications/read", "/api/v1/automation/webhook/test", "/api/v1/automation/telegram/test", "/api/v1/automation/telegram/simulate":
		return method == http.MethodPost
	case "/api/v1/automation/notifications", "/api/v1/automation/stream":
		return method == http.MethodGet
	case "/api/v1/lab/scripts":
		return method == http.MethodGet || method == http.MethodPut
	case "/api/v1/lab/evaluate", "/api/v1/lab/backtest", "/api/v1/lab/validate":
		return method == http.MethodPost
	case "/api/v1/lab/runs":
		return method == http.MethodGet
	case "/api/v1/desk/state", "/api/v1/desk/cycles", "/api/v1/desk/tickets", "/api/v1/desk/positions", "/api/v1/desk/journal", "/api/v1/desk/report", "/api/v1/desk/stream":
		return method == http.MethodGet
	case "/api/v1/desk/limits":
		return method == http.MethodPut
	case "/api/v1/desk/control", "/api/v1/desk/cycle", "/api/v1/desk/positions/mark":
		return method == http.MethodPost
	case "/api/v1/desk/lessons":
		return method == http.MethodGet || method == http.MethodPut
	case "/api/v1/execution/status", "/api/v1/vaults", "/api/v1/execution/polymarket/status", "/api/v1/execution/kalshi/status", "/api/v1/execution/lighter/status":
		return method == http.MethodGet
	case "/api/v1/execution/solana-devnet/rehearse", "/api/v1/execution/polymarket/rehearse", "/api/v1/execution/kalshi/rehearse", "/api/v1/execution/lighter/rehearse":
		return method == http.MethodPost
	case "/api/v1/cex/status", "/api/v1/cex/venues", "/api/v1/equities/status", "/api/v1/equities/quotes", "/api/v1/equities/options":
		return method == http.MethodGet
	case "/api/v1/cex/credentials":
		return method == http.MethodPost || method == http.MethodDelete
	case "/api/v1/cex/orders", "/api/v1/equities/orders":
		return method == http.MethodPost
	case "/api/v1/hub/packages", "/api/v1/swarm/status":
		return method == http.MethodGet
	case "/api/v1/hub/publish", "/api/v1/hub/install":
		return method == http.MethodPost
	case "/api/v1/swarm/config":
		return method == http.MethodPut
	case "/api/v1/swarm/peers", "/api/v1/swarm/signals":
		return method == http.MethodGet || method == http.MethodPost
	case "/api/v1/trade/state":
		return method == http.MethodGet
	case "/api/v1/trade/orders", "/api/v1/trade/evaluate", "/api/v1/trade/kill":
		return method == http.MethodPost
	case "/api/v1/trade/policy":
		return method == http.MethodPut
	}
	return false
}

// PublicRequestAllowed is shared by the protected app-identity stdio client.
// Native-only bootstrap and every private owner/signer route stay excluded.
func PublicRequestAllowed(r *http.Request) bool {
	return !strings.HasPrefix(r.URL.Path, "/api/v1/installation/") && allowed(r.Method, r.URL.Path) && allowedQuery(r)
}
func forward(ctx context.Context, client *core.Client, original *http.Request, body []byte) (*http.Response, error) {
	r, err := http.NewRequestWithContext(ctx, original.Method, client.Endpoint.URL+original.URL.RequestURI(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"Authorization", "Content-Type", "X-CSRF-Token", "Accept", "X-Fased-Pair"} {
		if value := original.Header.Get(name); value != "" {
			r.Header.Set(name, value)
		}
	}
	if r.Method != "GET" {
		r.Header.Set("Origin", client.Endpoint.URL)
	}
	httpClient := *client.HTTP
	if StreamingRoute(original.URL.Path) {
		httpClient.Timeout = 0
	} else if strings.HasPrefix(original.URL.Path, "/api/v1/agents/v2/") || original.URL.Path == "/api/v1/mcp" || original.URL.Path == "/api/v1/ai/connection" || strings.HasPrefix(original.URL.Path, "/api/v1/lab/") || strings.HasPrefix(original.URL.Path, "/api/v1/desk/") || strings.HasPrefix(original.URL.Path, "/api/v1/intel/") {
		if httpClient.Timeout > 0 && httpClient.Timeout < 2*time.Minute {
			httpClient.Timeout = 2 * time.Minute
		}
	}
	return httpClient.Do(r)
}
func fail(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func responseLimit(route string) int64 {
	// The agent view includes the bounded 512 KiB document plus JSON escaping,
	// progress and view fields. Other routes keep their original small bound.
	if route == "/api/v1/agents" || strings.HasPrefix(route, "/api/v1/agents/v2/") {
		return 4 * 1024 * 1024
	}
	if strings.HasPrefix(route, "/api/v1/lab/") {
		return 2 * 1024 * 1024
	}
	if route == "/api/v1/mcp" || strings.HasPrefix(route, "/api/v1/mcp/clients") || strings.HasPrefix(route, "/api/v1/decision/") || strings.HasPrefix(route, "/api/v1/market/") || strings.HasPrefix(route, "/api/v1/intel/") || strings.HasPrefix(route, "/api/v1/automation/") || strings.HasPrefix(route, "/api/v1/desk/") || strings.HasPrefix(route, "/api/v1/trade/") || strings.HasPrefix(route, "/api/v1/cex/") || strings.HasPrefix(route, "/api/v1/equities/") || strings.HasPrefix(route, "/api/v1/hub/") || strings.HasPrefix(route, "/api/v1/swarm/") {
		return 1 * 1024 * 1024
	}
	if route == "/api/v1/accounts/read" {
		// Bounded wallet projection (up to 1024 token accounts per program, activity, and tx detail).
		return core.WalletReadResponseLimit
	}
	if route == "/api/v1/accounts/portable" {
		return 256 * 1024
	}
	if route == "/api/v1/updates/desktop-release" {
		return 128 * 1024
	}
	return 32 * 1024
}
func allowedQuery(r *http.Request) bool {
	if r.URL.RawQuery == "" {
		return true
	}
	if len(r.URL.RawQuery) > 4096 {
		return false
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return false
	}
	for _, values := range q {
		if len(values) != 1 || len(values[0]) > 800 {
			return false
		}
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/agents":
		for key := range q {
			switch key {
			case "profile", "q", "history", "before", "history-task", "history-q", "playbook-draft", "playbook-version", "memory-restore-draft", "memory-revision", "reminder-draft", "evidence", "evidence-id", "evidence-item", "ai-draft", "memory-search", "memory-source":
			default:
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/agents/v2/usage":
		for key := range q {
			if key != "profileId" && key != "days" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/agents/v2/task-threads":
		for key := range q {
			if key != "profileId" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/agents/v2/threads":
		for key := range q {
			if key != "profileId" && key != "archived" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/agents/v2/threads/") && validPathSegment(strings.TrimPrefix(r.URL.Path, "/api/v1/agents/v2/threads/")):
		for key := range q {
			if key != "limit" && key != "beforeSeq" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/agents/v2/memories":
		for key := range q {
			switch key {
			case "profileId", "q", "scope", "limit", "excludeDrafts":
			default:
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/market/instruments":
		for key := range q {
			if key != "venue" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/market/candles":
		for key := range q {
			switch key {
			case "venue", "symbol", "interval", "style", "limit", "brickSize", "rangeSize", "reversal", "boxSize", "lineBreakCount", "tickCount", "volumeThreshold":
			default:
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && (r.URL.Path == "/api/v1/market/orderbook" || r.URL.Path == "/api/v1/market/trades"):
		for key := range q {
			if key != "venue" && key != "symbol" && key != "limit" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/market/footprint":
		for key := range q {
			if key != "venue" && key != "symbol" && key != "interval" && key != "stepSize" && key != "limit" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/market/macro":
		for key := range q {
			if key != "seriesId" && key != "limit" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/market/sec":
		for key := range q {
			if key != "cik" && key != "limit" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/market/cohorts":
		for key := range q {
			if key != "window" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/market/drawings":
		for key := range q {
			if key != "venue" && key != "symbol" && key != "interval" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/market/stream":
		for key := range q {
			if key != "venue" && key != "symbol" && key != "interval" && key != "style" && key != "subs" && key != "noSnapshot" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/intel/events":
		for key := range q {
			switch key {
			case "category", "region", "ticker", "q", "minUrgency", "since", "limit":
			default:
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/intel/map":
		for key := range q {
			if key != "category" && key != "minUrgency" && key != "hours" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/intel/predictions":
		for key := range q {
			if key != "venue" && key != "category" && key != "q" && key != "limit" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/intel/stream":
		for key := range q {
			if key != "category" && key != "region" && key != "ticker" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/automation/notifications":
		for key := range q {
			if key != "limit" && key != "unreadOnly" && key != "severity" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/lab/runs":
		for key := range q {
			if key != "limit" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && (r.URL.Path == "/api/v1/desk/cycles" || r.URL.Path == "/api/v1/desk/lessons"):
		for key := range q {
			if key != "limit" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && (r.URL.Path == "/api/v1/desk/tickets" || r.URL.Path == "/api/v1/desk/positions"):
		for key := range q {
			if key != "status" && key != "limit" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/desk/journal":
		for key := range q {
			if key != "symbol" && key != "outcome" && key != "limit" {
				return false
			}
		}
		return true
	case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/cex/credentials":
		for key := range q {
			if key != "venue" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && (r.URL.Path == "/api/v1/equities/quotes" || r.URL.Path == "/api/v1/equities/options"):
		for key := range q {
			if key != "symbol" {
				return false
			}
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/swarm/signals":
		for key := range q {
			if key != "limit" {
				return false
			}
		}
		return true
	case r.Method == http.MethodPost && StreamingRoute(r.URL.Path):
		for key, values := range q {
			if key != "stream" || (values[0] != "0" && values[0] != "1") {
				return false
			}
		}
		return true
	default:
		return false
	}
}
