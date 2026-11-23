package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSecurityHeadersAndOrigins(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	SetSecurityHeaders(rec)
	if rec.Header().Get("X-Frame-Options") != "DENY" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatalf("unexpected security headers: %v", rec.Header())
	}
	if !IsLoopbackHost("127.0.0.1") || !IsLoopbackHost("::1") || IsLoopbackHost("10.0.0.1") || IsLoopbackHost("localhost") {
		t.Fatal("unexpected IsLoopbackHost result")
	}
	canon, host, err := ValidatePublicOrigin("https://fased.tail123.ts.net/")
	if err != nil || canon != "https://fased.tail123.ts.net" || host != "fased.tail123.ts.net" {
		t.Fatalf("ValidatePublicOrigin failed: %q %q %v", canon, host, err)
	}
	for _, bad := range []string{"http://example.com", "https://user@example.com", "https://example.com/api", "https://example.com?a=1"} {
		if _, _, err := ValidatePublicOrigin(bad); err == nil {
			t.Fatalf("admitted bad public origin %q", bad)
		}
	}
}

func TestDecodeStrictJSONGuards(t *testing.T) {
	t.Parallel()
	type payload struct {
		Name string `json:"name"`
	}

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"ok"}`))
	rec := httptest.NewRecorder()
	var dst payload
	if !DecodeStrictJSON(rec, req, 1024, &dst) || dst.Name != "ok" {
		t.Fatalf("expected valid decode, got ok=%v dst=%+v", false, dst)
	}

	for _, body := range []string{
		`{"name":"ok","extra":1}`,
		`{"name":"ok"}{"name":"two"}`,
		`{"name":"` + strings.Repeat("x", 2000) + `"}`,
	} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		if DecodeStrictJSON(rec, req, 128, &dst) {
			t.Fatalf("admitted strict JSON violation: %q", body)
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 status, got %d", rec.Code)
		}
	}

	rec = httptest.NewRecorder()
	WriteError(rec, http.StatusForbidden, "denied")
	var errBody map[string]string
	if rec.Code != http.StatusForbidden || json.Unmarshal(rec.Body.Bytes(), &errBody) != nil || errBody["error"] != "denied" {
		t.Fatalf("unexpected WriteError output: %d %q", rec.Code, rec.Body.String())
	}
}

func TestServeStaticAsset(t *testing.T) {
	t.Parallel()
	assets := fstest.MapFS{
		"index.html":        {Data: []byte("<html>ok</html>")},
		"assets/app-123.js": {Data: []byte("console.log(1)")},
	}

	rec := httptest.NewRecorder()
	ServeStaticAsset(rec, httptest.NewRequest(http.MethodGet, "/unknown-route", nil), assets)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" || rec.Body.String() != "<html>ok</html>" {
		t.Fatalf("unexpected SPA fallback response: %d %v %q", rec.Code, rec.Header(), rec.Body.String())
	}

	rec = httptest.NewRecorder()
	ServeStaticAsset(rec, httptest.NewRequest(http.MethodGet, "/assets/app-123.js", nil), assets)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("unexpected hashed asset response: %d %v", rec.Code, rec.Header())
	}

	rec = httptest.NewRecorder()
	ServeStaticAsset(rec, httptest.NewRequest(http.MethodPost, "/", nil), assets)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 on POST, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	ServeStaticAsset(rec, httptest.NewRequest(http.MethodGet, "/", nil), nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on nil FS, got %d", rec.Code)
	}
}
