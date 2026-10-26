package core

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

const sessionCookie = "fased_session"

type HTTPHandler struct {
	wenResearchOrigin string
	wenResearchKey    [32]byte
	agentFixture      bool
	store             *Store
	origins           map[string]bool
	hosts             map[string]bool
	publicHost        string
	instanceLocation  string
	assets            fs.FS
	authSlots         chan struct{}
	limiter           struct {
		sync.Mutex
		start    time.Time
		attempts int
	}
}

func NewHTTPHandler(store *Store, baseURL, publicOrigin string, assets fs.FS) (*HTTPHandler, error) {
	h := &HTTPHandler{store: store, origins: map[string]bool{}, hosts: map[string]bool{}, assets: assets, authSlots: make(chan struct{}, 2), instanceLocation: "local"}
	if _, err := rand.Read(h.wenResearchKey[:]); err != nil {
		return nil, err
	}
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme != "http" || !isLoopbackHost(base.Hostname()) {
		return nil, errors.New("core listener must use an explicit loopback address")
	}
	h.origins[baseURL] = true
	h.hosts[base.Host] = true
	if base.Hostname() == "127.0.0.1" || base.Hostname() == "::1" {
		local := "http://" + net.JoinHostPort("localhost", base.Port())
		h.origins[local] = true
		h.hosts[net.JoinHostPort("localhost", base.Port())] = true
	}
	if publicOrigin != "" {
		parsed, err := url.Parse(publicOrigin)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, errors.New("public origin must be an exact HTTPS origin without credentials or a path")
		}
		publicOrigin = strings.TrimSuffix(publicOrigin, "/")
		h.origins[publicOrigin] = true
		h.hosts[parsed.Host] = true
		h.publicHost = parsed.Host
	}
	return h, nil
}
func isLoopbackHost(host string) bool { ip := net.ParseIP(host); return ip != nil && ip.IsLoopback() }
func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; object-src 'none'; base-uri 'none'; form-action 'self'")
	w.Header().Set("X-Frame-Options", "DENY")
	if !h.hosts[r.Host] {
		writeError(w, http.StatusForbidden, "unexpected request host")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Cache-Control", "no-store")
		if r.Header.Get("Origin") != "" && !h.origins[r.Header.Get("Origin")] {
			writeError(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			writeError(w, http.StatusForbidden, "cross-site request refused")
			return
		}
		h.api(w, r)
		return
	}
	h.static(w, r)
}
func (h *HTTPHandler) api(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v1/health":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		health := h.store.Health()
		location, transport := h.instanceLocation, "loopback"
		if h.publicHost != "" && r.Host == h.publicHost {
			location, transport = "remote", "private-https"
		}
		health.Connection = &ConnectionInfo{Location: location, Transport: transport}
		writeJSON(w, 200, health)
		return
	case "/api/v1/auth/state":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		claimed, err := h.store.Claimed(r.Context())
		if err != nil {
			writeError(w, 500, "state unavailable")
			return
		}
		token := h.session(r)
		valid := h.store.Session(r.Context(), token)
		result := map[string]any{"claimed": claimed, "authenticated": valid}
		if valid {
			result["csrfToken"] = csrfToken(token)
		}
		writeJSON(w, 200, result)
		return
	case "/api/v1/auth/claim", "/api/v1/auth/login":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.mutation(w, r) {
			return
		}
		if !h.allowAuth() {
			writeError(w, 429, ErrRateLimited.Error())
			return
		}
		select {
		case h.authSlots <- struct{}{}:
			defer func() { <-h.authSlots }()
		default:
			writeError(w, 429, "authentication is busy; retry shortly")
			return
		}
		var request struct {
			Code     string `json:"code"`
			Password string `json:"password"`
		}
		if !decodeJSON(w, r, &request) {
			return
		}
		var token string
		var err error
		if r.URL.Path == "/api/v1/auth/claim" {
			token, err = h.store.Claim(r.Context(), request.Code, request.Password)
		} else {
			token, err = h.store.Login(r.Context(), request.Password)
		}
		if err != nil {
			writeError(w, 401, "authentication failed; check credentials and claim-code expiry")
			return
		}
		h.setCookie(w, r, token, int(SessionLifetime.Seconds()))
		writeJSON(w, 200, map[string]any{"authenticated": true, "csrfToken": csrfToken(token), "sessionToken": token, "sessionMode": h.sessionMode(r)})
		return
	}
	token := h.session(r)
	if !h.store.Session(r.Context(), token) {
		writeError(w, 401, "owner authentication required")
		return
	}
	switch r.URL.Path {
	case "/api/v1/wen/sessions":
		h.wenSessionsAPI(w, r, token, "view")
		return
	case "/api/v1/wen/session-runs":
		h.wenFixtureRunsAPI(w, r, token, "view")
	case "/api/v1/wen/session-runs/review", "/api/v1/wen/session-runs/start", "/api/v1/wen/session-runs/control", "/api/v1/wen/session-runs/reconcile":
		h.wenFixtureRunsAPI(w, r, token, strings.TrimPrefix(r.URL.Path, "/api/v1/wen/session-runs/"))
	case "/api/v1/wen/sessions/review", "/api/v1/wen/sessions/create", "/api/v1/wen/sessions/step", "/api/v1/wen/sessions/action-review", "/api/v1/wen/sessions/control":
		h.wenSessionsAPI(w, r, token, strings.TrimPrefix(r.URL.Path, "/api/v1/wen/sessions/"))
		return
	case "/api/v1/wen/research/ai-prepare-next":
		h.wenResearchAIAPI(w, r, token, "next")
		return
	case "/api/v1/wen/research/ai-start":
		h.wenResearchAIAPI(w, r, token, "start")
		return
	case "/api/v1/wen/research/ai-prepare":
		h.wenResearchAIAPI(w, r, token, "prepare")
		return
	case "/api/v1/wen/research/ai-inspect":
		h.wenResearchAIAPI(w, r, token, "inspect")
		return
	case "/api/v1/wen/research/ai-cancel":
		h.wenResearchAIAPI(w, r, token, "cancel")
		return
	case "/api/v1/wen/research/read":
		h.wenResearchReadAPI(w, r, token)
		return
	case "/api/v1/wen/research/proposal-review":
		h.wenRevisionAPI(w, r, token, false, true)
		return
	case "/api/v1/wen/research/proposal-save":
		h.wenRevisionAPI(w, r, token, true, true)
		return
	case "/api/v1/wen/research/revision-review":
		h.wenStrategyRevisionAPI(w, r, token, false)
		return
	case "/api/v1/wen/research/revision-save":
		h.wenStrategyRevisionAPI(w, r, token, true)
		return
	case "/api/v1/wen/research/chat-review":
		h.wenSavedChatReviewAPI(w, r, token)
		return
	case "/api/v1/ai/connection":
		h.aiConnectionAPI(w, r, token)
		return
	case "/api/v1/wen/market-data":
		h.wenMarketDataAPI(w, r)
		return
	case "/api/v1/wen/research":
		h.wenResearchAPI(w, r, token)
		return
	case "/api/v1/agents":
		h.agentsAPI(w, r, token)
		return
	case "/api/v1/updates/preferences":
		h.updatePreferencesAPI(w, r, token)
		return
	case "/api/v1/updates/status":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		writeJSON(w, 200, h.store.ReadUpdateStatus(r.Context()))
		return
	case "/api/v1/updates/backup", "/api/v1/updates/recovery":
		h.backupAPI(w, r, token)
		return
	case "/api/v1/updates/pause":
		h.pauseAPI(w, r, token)
		return
	case "/api/v1/updates/inventory":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		if len(r.URL.Query()) != 1 || len(r.URL.Query()["probe"]) != 1 {
			writeError(w, 400, "one inventory challenge required")
			return
		}
		v, e := h.store.UpdateSnapshot(r.Context(), r.URL.Query().Get("probe"))
		if e != nil {
			writeError(w, 400, "core inventory unavailable")
			return
		}
		writeJSON(w, 200, v)
		return
	case "/api/v1/accounts/rpc-check":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return
		}
		var input RPCCheckRequest
		if !decodeJSON(w, r, &input) {
			return
		}
		result, err := CheckFixtureRPC(r.Context(), input)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, result)
		return
	case "/api/v1/accounts":
		if r.Method == http.MethodGet {
			v, e := h.store.Accounts(r.Context())
			if e != nil {
				writeError(w, 500, "accounts unavailable")
				return
			}
			writeJSON(w, 200, v)
			return
		}
		if r.Method != http.MethodPut {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return
		}
		var input AccountsRecord
		if !decodeJSON(w, r, &input) {
			return
		}
		v, e := h.store.UpdateAccounts(r.Context(), input)
		if errors.Is(e, ErrConflict) || errors.Is(e, ErrCorePaused) {
			writeError(w, 409, e.Error())
			return
		}
		if e != nil {
			writeError(w, 400, e.Error())
			return
		}
		writeJSON(w, 200, v)
		return
	case "/api/v1/setup":
		if r.Method == http.MethodGet {
			record, err := h.store.Setup(r.Context())
			if err != nil {
				writeError(w, 500, "setup unavailable")
				return
			}
			writeJSON(w, 200, record)
			return
		}
		if r.Method != http.MethodPut {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return
		}
		var request SetupRecord
		if !decodeJSON(w, r, &request) {
			return
		}
		if request.Revision < 0 {
			writeError(w, 400, "invalid revision")
			return
		}
		if err := request.Settings.Validate(); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		record, err := h.store.UpdateSetup(r.Context(), request.Revision, request.Settings)
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrCorePaused) {
			writeError(w, 409, err.Error())
			return
		}
		if err != nil {
			writeError(w, 500, "setup update failed")
			return
		}
		writeJSON(w, 200, record)
		return
	case "/api/v1/auth/logout":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return
		}
		if err := h.store.Logout(r.Context(), token); err != nil {
			writeError(w, 500, "logout failed")
			return
		}
		h.setCookie(w, r, "", -1)
		writeJSON(w, 200, map[string]bool{"authenticated": false})
		return
	case "/api/v1/signer/status":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		writeJSON(w, 200, map[string]any{"available": false, "executionEnabled": false, "reason": "real wallets unavailable; public fixture references are managed through accounts"})
		return
	default:
		writeError(w, 404, "unknown API operation")
	}
}
func (h *HTTPHandler) allowAuth() bool {
	h.limiter.Lock()
	defer h.limiter.Unlock()
	now := h.store.now()
	if h.limiter.start.IsZero() || now.Sub(h.limiter.start) >= time.Minute {
		h.limiter.start = now
		h.limiter.attempts = 0
	}
	if h.limiter.attempts >= 8 {
		return false
	}
	h.limiter.attempts++
	return true
}
func (h *HTTPHandler) mutation(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeError(w, 405, "method not allowed")
		return false
	}
	if !h.origins[r.Header.Get("Origin")] {
		writeError(w, 403, "same-origin request required")
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, 415, "JSON content type required")
		return false
	}
	return true
}
func (h *HTTPHandler) csrf(w http.ResponseWriter, r *http.Request, token string) bool {
	expected := csrfToken(token)
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(expected)) != 1 {
		writeError(w, 403, "CSRF token required")
		return false
	}
	return true
}
func (h *HTTPHandler) session(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if r.TLS == nil && !(h.publicHost != "" && r.Host == h.publicHost) {
		return ""
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}
func (h *HTTPHandler) setCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	if r.TLS == nil && !(h.publicHost != "" && r.Host == h.publicHost) {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/api/", HttpOnly: true, Secure: r.TLS != nil || (h.publicHost != "" && r.Host == h.publicHost), SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}
func (h *HTTPHandler) sessionMode(r *http.Request) string {
	if r.TLS != nil || (h.publicHost != "" && r.Host == h.publicHost) {
		return "cookie"
	}
	return "bearer"
}
func decodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(w, 400, "invalid JSON request")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, 400, "one JSON object required")
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func (h *HTTPHandler) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, 405, "method not allowed")
		return
	}
	if h.assets == nil {
		writeError(w, 503, "UI assets unavailable")
		return
	}
	clean := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if clean == "" || !strings.Contains(path.Base(clean), ".") {
		clean = "index.html"
	}
	if !fs.ValidPath(clean) {
		writeError(w, 404, "not found")
		return
	}
	data, err := fs.ReadFile(h.assets, clean)
	if err != nil {
		writeError(w, 404, "not found")
		return
	}
	kind := mime.TypeByExtension(path.Ext(clean))
	if kind != "" {
		w.Header().Set("Content-Type", kind)
	}
	if clean == "index.html" {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method == http.MethodGet {
		_, _ = w.Write(data)
	}
}
