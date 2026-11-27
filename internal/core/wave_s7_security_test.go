package core

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/auth"
)

func TestWaveS7ServerKeyedCSRFAndPersistence(t *testing.T) {
	t.Parallel()
	dir := fixtureDir(t)
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := s.NewSession(t.Context())
	if err != nil {
		_ = s.Close()
		t.Fatal(err)
	}
	v1 := auth.CSRFToken(tok)
	v2 := s.CSRFToken(tok)
	if v1 == v2 {
		_ = s.Close()
		t.Fatal("expected server-keyed v2 CSRF token to differ from session-only v1 token")
	}
	if s.VerifyCSRF(v1, tok) {
		_ = s.Close()
		t.Fatal("expected legacy v1 CSRF token to be rejected")
	}
	if !s.VerifyCSRF(v2, tok) {
		_ = s.Close()
		t.Fatal("expected server-keyed v2 CSRF token to verify")
	}
	_ = s.Close()

	// Reopening the store must load the same persistent csrf_secret from core.sqlite.
	s2, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if !s2.VerifyCSRF(v2, tok) {
		t.Fatal("expected server-keyed CSRF secret to persist across store restarts")
	}
}

func TestWaveS7SessionIdleTimeoutAndSecurityHeaders(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	tok, err := s.NewSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !s.Session(t.Context(), tok) {
		t.Fatal("expected fresh session to be valid")
	}
	// Advance 90 minutes (< 2h idle): still valid and refreshes activity.
	now = now.Add(90 * time.Minute)
	if !s.Session(t.Context(), tok) {
		t.Fatal("expected session active at 90m to remain valid")
	}
	// Advance another 121 minutes (> 2h idle, still < 12h absolute): must expire!
	now = now.Add(121 * time.Minute)
	if s.Session(t.Context(), tok) {
		t.Fatal("expected session idle for > 2h to expire")
	}

	// Verify strict security headers including Permissions-Policy, COOP, CORP, and HSTS.
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "https://remote.example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	h.remoteProxyTrust = true
	req := httptest.NewRequest(http.MethodGet, "https://remote.example.com/api/v1/health", nil)
	req.Host = "remote.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Header().Get("Permissions-Policy") == "" ||
		w.Header().Get("Cross-Origin-Opener-Policy") != "same-origin" ||
		w.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" ||
		w.Header().Get("Strict-Transport-Security") == "" {
		t.Fatalf("missing Wave S-7 security headers: %+v", w.Header())
	}

	// Verify HTTPS login sets __Host-fased_session cookie.
	wCookie := httptest.NewRecorder()
	h.setCookie(wCookie, req, "dummy-token-value", 3600)
	cookies := wCookie.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != "__Host-fased_session" || cookies[0].Path != "/" || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatalf("expected __Host-fased_session secure HttpOnly cookie, got %+v", cookies)
	}
}

func TestWaveS7SplitLimitersAuditChainAndRedaction(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Flood unauthLimiter with 1,200 distinct spoofed IPs; owner authLimiter must stay unaffected!
	for i := 0; i < 1200; i++ {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/auth/login", nil)
		r.RemoteAddr = "198.51.100." + string(rune('1'+i%9)) + ":12345"
		_ = h.allowRate(r, "auth-login", 8)
	}
	ownerReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/auth/step-up", nil)
	ownerReq.RemoteAddr = "127.0.0.1:54321"
	if !h.allowRate(ownerReq, "auth-step-up", 10) {
		t.Fatal("unauth login flood must not exhaust owner authLimiter")
	}

	// Hash-chained security_audit_log verification & tamper detection.
	s.RecordSecurityAudit(t.Context(), "auth.login.failed", "ip:1.2.3.4", "/api/v1/auth/login", "denied", "bad pass")
	s.RecordSecurityAudit(t.Context(), "wallet.policy.update", "owner", "w1", "ok", "tightened")
	cnt, err := s.VerifySecurityAuditChain(t.Context())
	if err != nil || cnt < 2 {
		t.Fatalf("expected valid security audit chain >= 2, got count=%d err=%v", cnt, err)
	}
	// Tamper row 1 -> chain verification must fail!
	if _, err := s.db.ExecContext(t.Context(), `UPDATE security_audit_log SET detail = 'tampered' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifySecurityAuditChain(t.Context()); err == nil {
		t.Fatal("expected tampered security_audit_log to fail chain verification")
	}

	// Redaction checks: slog.KindAny errors, path-segment RPC keys, and sanitizePublicURL.
	var logBuf bytes.Buffer
	logger := newRedactingLogger(&logBuf)
	logger.Error("rpc failed", "err", errors.New("Post https://eth.g.alchemy.com/v2/secretAlchemyKey1234567890?api-key=heliusSecret999 and fased_mcp_supersecrettoken12345"))
	out := logBuf.String()
	if strings.Contains(out, "secretAlchemyKey1234567890") || strings.Contains(out, "heliusSecret999") || strings.Contains(out, "fased_mcp_supersecrettoken12345") {
		t.Fatalf("slog.KindAny error leaked secrets: %s", out)
	}

	redAlchemy := redactedPluginEndpoint("https://solana-mainnet.g.alchemy.com/v2/superSecretAlchemyKey987654321")
	if strings.Contains(redAlchemy, "superSecretAlchemyKey987654321") {
		t.Fatalf("redactedPluginEndpoint leaked Alchemy path key: %s", redAlchemy)
	}
	redQN := redactedPluginEndpoint("https://my-node.solana-mainnet.quiknode.pro/a1b2c3d4e5f67890a1b2c3d4e5f67890a1b2c3d4/")
	if strings.Contains(redQN, "a1b2c3d4e5f67890a1b2c3d4e5f67890a1b2c3d4") {
		t.Fatalf("redactedPluginEndpoint leaked QuickNode path token: %s", redQN)
	}
	if got := sanitizePublicURL("https://mainnet.helius-rpc.com/v2/secret123?api-key=topsecret#frag"); got != "https://mainnet.helius-rpc.com" {
		t.Fatalf("sanitizePublicURL leaked path/query: %s", got)
	}
}
