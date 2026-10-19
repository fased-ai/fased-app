package core

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

const testPassword = "fixture-only-owner-password"

// Reconstructible fixtures only; no owner state or signing material. Test results
// are retained by the runner before these exact temporary directories are removed.
func fixtureDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../.evidence/core-fixtures")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "f-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	return dir
}
func fixtureStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func claimFixture(t *testing.T, s *Store) string {
	t.Helper()
	code, err := s.IssueClaimCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Claim(t.Context(), code.Code, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestClaimSessionAndPasswordLifecycle(t *testing.T) {
	s := fixtureStore(t)
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	code, err := s.IssueClaimCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(t.Context(), code.Code, "short"); err == nil {
		t.Fatal("weak password accepted")
	}
	now = now.Add(ClaimLifetime)
	if _, err = s.Claim(t.Context(), code.Code, testPassword); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("expired claim accepted")
	}
	code, err = s.IssueClaimCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Claim(t.Context(), code.Code, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(t.Context(), code.Code, testPassword); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("claim replay accepted")
	}
	if _, err = s.IssueClaimCode(t.Context()); !errors.Is(err, ErrClaimed) {
		t.Fatal("claimed owner could be replaced")
	}
	if _, err = os.Stat(filepath.Join(s.dir, "claim-code.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("claim code file was retained")
	}
	var salt, hash []byte
	if err = s.db.QueryRow("SELECT password_salt,password_hash FROM instance").Scan(&salt, &hash); err != nil {
		t.Fatal(err)
	}
	if len(salt) != 16 || len(hash) != 32 || string(hash) == testPassword {
		t.Fatal("password not stored as a derived hash")
	}
	var stored string
	if err = s.db.QueryRow("SELECT token_hash FROM sessions").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token || stored != tokenHash(token) {
		t.Fatal("session stored without hashing")
	}
	if !s.Session(t.Context(), token) {
		t.Fatal("new session invalid")
	}
	if _, err = s.Login(t.Context(), "wrong"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("wrong password accepted")
	}
	now = now.Add(SessionLifetime)
	if s.Session(t.Context(), token) {
		t.Fatal("expired session accepted")
	}
	token, err = s.Login(t.Context(), testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Logout(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	if s.Session(t.Context(), token) {
		t.Fatal("revoked session accepted")
	}
}

func TestSetupConflictPersistenceAndDowngrade(t *testing.T) {
	dir := fixtureDir(t)
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := s.instanceID
	token := claimFixture(t, s)
	setup := DefaultSetup()
	setup.Host, setup.Complete, setup.Step = "vps", true, 3
	record, err := s.UpdateSetup(t.Context(), 0, setup)
	if err != nil || record.Revision != 1 {
		t.Fatalf("update: %v", err)
	}
	if _, err = s.UpdateSetup(t.Context(), 0, DefaultSetup()); !errors.Is(err, ErrConflict) {
		t.Fatal("stale client overwrote setup")
	}
	setup.Provider = "arbitrary-shell"
	if _, err = s.UpdateSetup(t.Context(), 1, setup); err == nil {
		t.Fatal("unsupported provider accepted")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Setup(t.Context())
	if err != nil || got != record || s.instanceID != id || !s.Session(t.Context(), token) {
		t.Fatal("restart lost setup, ownership or session")
	}
	if _, err = s.db.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenStore(dir); err == nil {
		t.Fatal("newer schema opened by older binary")
	}
}

func apiRequest(h http.Handler, method, route, body, token, origin, csrf string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:7331"+route, strings.NewReader(body))
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHTTPBoundariesAndConflict(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", fstest.MapFS{"index.html": {Data: []byte("<html>fixture</html>")}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(SetupRecord{Settings: DefaultSetup()})
	for _, test := range []struct {
		name, method, route, body, token, origin, csrf string
		want                                           int
	}{
		{"unauthenticated", "GET", "/api/v1/setup", "", "", "", "", 401},
		{"foreign origin", "GET", "/api/v1/setup", "", token, "https://attacker.example", "", 403},
		{"missing origin", "PUT", "/api/v1/setup", string(body), token, "", csrfToken(token), 403},
		{"missing csrf", "PUT", "/api/v1/setup", string(body), token, "http://127.0.0.1:7331", "", 403},
		{"wrong method", "POST", "/api/v1/setup", string(body), token, "http://127.0.0.1:7331", csrfToken(token), 405},
		{"unknown fields", "PUT", "/api/v1/setup", `{"extra":true}`, token, "http://127.0.0.1:7331", csrfToken(token), 400},
		{"large body", "PUT", "/api/v1/setup", strings.Repeat(" ", 17000) + string(body), token, "http://127.0.0.1:7331", csrfToken(token), 400},
		{"valid", "PUT", "/api/v1/setup", string(body), token, "http://127.0.0.1:7331", csrfToken(token), 200},
		{"stale", "PUT", "/api/v1/setup", string(body), token, "http://127.0.0.1:7331", csrfToken(token), 409},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := apiRequest(h, test.method, test.route, test.body, test.token, test.origin, test.csrf); got.Code != test.want {
				t.Fatalf("status=%d, want %d: %s", got.Code, test.want, got.Body)
			}
		})
	}
	r := httptest.NewRequest("GET", "http://attacker.example/api/v1/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("DNS rebinding host accepted")
	}
	r = httptest.NewRequest("GET", "http://127.0.0.1:7331/api/v1/health", nil)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-site fetch accepted")
	}
	status := apiRequest(h, "GET", "/api/v1/signer/status", "", token, "", "")
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"available":false`) {
		t.Fatal("signer capability is not denied")
	}
	static := apiRequest(h, "GET", "/", "", "", "", "")
	if static.Code != 200 || static.Header().Get("Cache-Control") != "no-store" || !strings.Contains(static.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("missing static security policy")
	}
}

func TestAuthTransportAndRateLimit(t *testing.T) {
	s := fixtureStore(t)
	claimFixture(t, s)
	for _, secure := range []bool{false, true} {
		origin := "http://127.0.0.1:7331"
		public := ""
		if secure {
			public = "https://private.example"
			origin = public
		}
		h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", public, nil)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", origin+"/api/v1/auth/login", strings.NewReader(`{"password":"`+testPassword+`"}`))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal("login failed")
		}
		cookies := w.Result().Cookies()
		if !secure && len(cookies) != 0 {
			t.Fatal("loopback cookie leaks across ports")
		}
		if secure && (len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode) {
			t.Fatal("insecure HTTPS session cookie")
		}
		// HTTP cookies must not authenticate a local request, even if valid.
		if !secure {
			var session struct {
				Token string `json:"sessionToken"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &session)
			r = httptest.NewRequest("GET", origin+"/api/v1/setup", nil)
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: session.Token})
			w = httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 401 {
				t.Fatal("HTTP cookie authenticated across ports")
			}
		}
		for i := 0; i < 7; i++ {
			apiRequest(h, "POST", "/api/v1/auth/login", `{"password":"wrong"}`, "", "http://127.0.0.1:7331", "")
		}
		if got := apiRequest(h, "POST", "/api/v1/auth/login", `{}`, "", "http://127.0.0.1:7331", ""); got.Code != 429 {
			t.Fatal("auth rate limit missing")
		}
	}
}

func TestIPCSharedStateAndSingleEngine(t *testing.T) {
	dir := fixtureDir(t)
	server, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve() }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	if _, err = Start(t.Context(), dir, "127.0.0.1:0", "", nil); err == nil {
		t.Fatal("duplicate engine accepted")
	}
	if _, err = Start(t.Context(), fixtureDir(t), "0.0.0.0:7331", "", nil); err == nil {
		t.Fatal("public listener accepted")
	}
	client, err := NewClient(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	code, err := server.Store.IssueClaimCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Claim(t.Context(), code.Code, testPassword); err != nil {
		t.Fatal(err)
	}
	if err = client.Login(t.Context(), testPassword); err != nil {
		t.Fatal(err)
	}
	if err = client.SaveSession(dir); err != nil {
		t.Fatal(err)
	}
	if err = client.LoadSession(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	var setup SetupRecord
	if err = client.Do(t.Context(), "GET", "/api/v1/setup", nil, &setup); err != nil {
		t.Fatal(err)
	}
	setup.Settings.Host = "vps"
	if err = client.Do(t.Context(), "PUT", "/api/v1/setup", setup, &setup); err != nil {
		t.Fatal(err)
	}
	got, err := server.Store.Setup(t.Context())
	if err != nil || got != setup {
		t.Fatal("CLI and engine state diverged")
	}
	client.Endpoint.InstanceID = "different-instance"
	if err = WritePrivateJSON(filepath.Join(dir, "endpoint.json"), client.Endpoint); err != nil {
		t.Fatal(err)
	}
	if _, err = NewClient(t.Context(), dir); err == nil {
		t.Fatal("mismatched endpoint trusted")
	}
}

func TestConcurrentSetupHasOneWinner(t *testing.T) {
	s := fixtureStore(t)
	results := make(chan error, 2)
	for _, host := range []string{"local", "vps"} {
		go func() {
			setup := DefaultSetup()
			setup.Host = host
			_, err := s.UpdateSetup(t.Context(), 0, setup)
			results <- err
		}()
	}
	wins, conflicts := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			wins++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("concurrent edits silently overwrote each other")
	}
}
