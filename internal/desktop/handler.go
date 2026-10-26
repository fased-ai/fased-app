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
)

type Handler struct {
	openBrowser func(string, string) error
	dir         string
	assets      http.Handler
	mu          sync.Mutex
	instance    string
}

func NewHandler(dir string, assets fs.FS) *Handler {
	return &Handler{dir: dir, assets: http.FileServer(http.FS(assets)), openBrowser: func(url, id string) error { return connection.OpenBrowser(url, id) }}
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
	body, err := io.ReadAll(io.LimitReader(r.Body, 16*1024+1))
	if err != nil || len(body) > 16*1024 {
		fail(w, 413, "request too large")
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
	client, err := core.NewClient(r.Context(), h.dir)
	if err != nil {
		fail(w, 503, "Private core unavailable. Start the installed engine and retry.")
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
			fail(w, 409, "claim code expired; renew it from the OS owner's terminal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(code)
		return
	}
	response, err := forward(r.Context(), client, r, body)
	if err != nil {
		fail(w, 503, "Private core unavailable. Retry the connection.")
		return
	}
	defer response.Body.Close()
	limit := responseLimit(r.URL.Path)
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(data)) > limit || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		fail(w, 502, "invalid private core response")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(data)
}
func allowed(method, route string) bool {
	switch route {
	case "/api/v1/health", "/api/v1/auth/state", "/api/v1/signer/status", "/api/v1/installation/claim-code":
		return method == "GET"
	case "/api/v1/installation/connect":
		return method == "POST"
	case "/api/v1/agents":
		return method == "GET" || method == "POST"
	case "/api/v1/ai/connection":
		return method == "GET" || method == "POST"
	case "/api/v1/accounts", "/api/v1/updates/preferences":
		return method == "GET" || method == "PUT"
	case "/api/v1/accounts/rpc", "/api/v1/accounts/rpc-credential":
		return method == "PUT"
	case "/api/v1/accounts/rpc-check":
		return method == "POST"
	case "/api/v1/updates/status":
		return method == "GET"
	case "/api/v1/setup":
		return method == "GET" || method == "PUT"
	case "/api/v1/auth/claim", "/api/v1/auth/login", "/api/v1/auth/logout":
		return method == "POST"
	}
	return false
}
func forward(ctx context.Context, client *core.Client, original *http.Request, body []byte) (*http.Response, error) {
	r, err := http.NewRequestWithContext(ctx, original.Method, client.Endpoint.URL+original.URL.RequestURI(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"Authorization", "Content-Type", "X-CSRF-Token"} {
		if value := original.Header.Get(name); value != "" {
			r.Header.Set(name, value)
		}
	}
	if r.Method != "GET" {
		r.Header.Set("Origin", client.Endpoint.URL)
	}
	return client.HTTP.Do(r)
}
func fail(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func responseLimit(route string) int64 {
	// The agent view includes the bounded 512 KiB document plus JSON escaping,
	// progress and view fields. Other routes keep their original small bound.
	if route == "/api/v1/agents" {
		return 4 * 1024 * 1024
	}
	return 32 * 1024
}
func allowedQuery(r *http.Request) bool {
	if r.URL.RawQuery == "" {
		return true
	}
	if r.Method != "GET" || r.URL.Path != "/api/v1/agents" || len(r.URL.RawQuery) > 4096 {
		return false
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return false
	}
	for key, values := range q {
		if len(values) != 1 || len(values[0]) > 800 {
			return false
		}
		switch key {
		case "profile", "q", "history", "before", "history-task", "history-q", "playbook-draft", "playbook-version", "memory-restore-draft", "memory-revision", "reminder-draft", "evidence", "evidence-id", "evidence-item", "ai-draft", "memory-search", "memory-source":
		default:
			return false
		}
	}
	return true
}
