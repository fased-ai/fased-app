package core

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
)

const testPassword = "fixture-only-owner-password"

// Reconstructible fixtures only; no owner state or signing material. Keeping temp
// paths under os.TempDir keeps SQLite on tmpfs and socket paths short.
func fixtureDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "fc-")
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
	s, err := openStoreWithTaskSecrets(fixtureDir(t), fixtureTaskSecrets{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.providerFixture = true
	s.rpcAdmission = false
	s.provider.apiCredentials = false
	return s
}

func reopenFixtureStore(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := openStoreWithTaskSecrets(dir, fixtureTaskSecrets{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.rpcAdmission = false
	s.provider.apiCredentials = false
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
		if secure {
			r.TLS = nil
		} // Private HTTPS proxy terminates TLS before loopback.
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal("login failed")
		}
		var declared struct {
			Mode string `json:"sessionMode"`
		}
		if err = json.Unmarshal(w.Body.Bytes(), &declared); err != nil {
			t.Fatal(err)
		}
		wantMode := "bearer"
		if secure {
			wantMode = "cookie"
		}
		if declared.Mode != wantMode {
			t.Fatal("wrong declared session transport")
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
	t.Parallel()
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
	t.Parallel()
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

func TestCookieModeOmitsSessionTokenAndRejectsBearer(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	claimFixture(t, s)
	const origin = "https://node.owner.ts.net"
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", origin, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, origin+"/api/v1/auth/login", strings.NewReader(`{"password":"`+testPassword+`"}`))
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("cookie login failed: %d %s", w.Code, w.Body.String())
	}
	var payload map[string]any
	if err = json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if _, leaked := payload["sessionToken"]; leaked || payload["sessionMode"] != "cookie" {
		t.Fatalf("cookie response leaked sessionToken or wrong mode: %v", payload)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != hostSessionCookie {
		t.Fatalf("missing session cookie: %+v", cookies)
	}
	cookieVal := cookies[0].Value

	// Authorization: Bearer must be ignored/rejected on cookie-mode HTTPS routes.
	bearerReq := httptest.NewRequest(http.MethodGet, origin+"/api/v1/setup", nil)
	bearerReq.Header.Set("Authorization", "Bearer "+cookieVal)
	bearerRes := httptest.NewRecorder()
	h.ServeHTTP(bearerRes, bearerReq)
	if bearerRes.Code != http.StatusUnauthorized {
		t.Fatalf("cookie mode accepted Bearer header: %d", bearerRes.Code)
	}

	// Valid cookie succeeds.
	cookieReq := httptest.NewRequest(http.MethodGet, origin+"/api/v1/setup", nil)
	cookieReq.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookieVal})
	cookieRes := httptest.NewRecorder()
	h.ServeHTTP(cookieRes, cookieReq)
	if cookieRes.Code != http.StatusOK {
		t.Fatalf("valid cookie rejected: %d %s", cookieRes.Code, cookieRes.Body.String())
	}
}

func TestPartitionedRateLimitAndLoopbackPeerUID(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	claimFixture(t, s)
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Login(t.Context(), testPassword)
	if err != nil {
		t.Fatal(err)
	}

	// Exhaust /api/v1/auth/login from one loopback source IP.
	for i := 0; i < 8; i++ {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/auth/login", strings.NewReader(`{"password":"wrong"}`))
		r.RemoteAddr = "127.0.0.1:41001"
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
	}
	blocked := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/auth/login", strings.NewReader(`{"password":"`+testPassword+`"}`))
	blocked.RemoteAddr = "127.0.0.1:41002"
	blocked.Header.Set("Origin", "http://127.0.0.1:7331")
	blocked.Header.Set("Content-Type", "application/json")
	blockedRes := httptest.NewRecorder()
	h.ServeHTTP(blockedRes, blocked)
	if blockedRes.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on saturated auth-login bucket, got %d", blockedRes.Code)
	}

	// Different peer IP on /api/v1/auth/login is unaffected.
	otherIP := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/auth/login", strings.NewReader(`{"password":"`+testPassword+`"}`))
	otherIP.RemoteAddr = "127.0.0.2:41003"
	otherIP.Header.Set("Origin", "http://127.0.0.1:7331")
	otherIP.Header.Set("Content-Type", "application/json")
	otherRes := httptest.NewRecorder()
	h.ServeHTTP(otherRes, otherIP)
	if otherRes.Code != http.StatusOK {
		t.Fatalf("login rate limit bled across peer IPs: %d %s", otherRes.Code, otherRes.Body.String())
	}

	// /api/v1/ai/connection from the first peer IP stays unthrottled by auth-login.
	aiReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/ai/connection", strings.NewReader(`{"action":"lock"}`))
	aiReq.RemoteAddr = "127.0.0.1:41004"
	aiReq.Header.Set("Origin", "http://127.0.0.1:7331")
	aiReq.Header.Set("Content-Type", "application/json")
	aiReq.Header.Set("Authorization", "Bearer "+token)
	aiReq.Header.Set("X-CSRF-Token", csrfToken(token))
	aiRes := httptest.NewRecorder()
	h.ServeHTTP(aiRes, aiReq)
	if aiRes.Code == http.StatusTooManyRequests {
		t.Fatal("auth-login rate limit throttled /api/v1/ai/connection")
	}

	// Real TCP loopback request exercises /proc/net/tcp same-UID enforcement on Linux.
	h2, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h2.origins["http://"+r.Host] = true
		h2.hosts[r.Host] = true
		h2.ServeHTTP(w, r)
	}))
	defer ts.Close()
	client := ts.Client()
	loginURL := ts.URL + "/api/v1/auth/login"
	sendRealLogin := func() *http.Response {
		req, _ := http.NewRequest(http.MethodPost, loginURL, strings.NewReader(`{"password":"`+testPassword+`"}`))
		req.Header.Set("Origin", ts.URL)
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	if res := sendRealLogin(); res.StatusCode != http.StatusOK {
		t.Fatalf("real same-UID loopback TCP login failed (GOOS=%s): %d", runtime.GOOS, res.StatusCode)
	}

	// Simulated foreign UID must be refused with 403 on Local auth routes.
	h2.loopbackPeerUID = func(*net.TCPAddr, *net.TCPAddr) (int, bool, error) {
		return os.Getuid() + 1, true, nil
	}
	if res := sendRealLogin(); res.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign loopback UID login accepted: %d", res.StatusCode)
	}
	localReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/auth/local", strings.NewReader(`{"action":"exchange","code":"`+strings.Repeat("a", 43)+`"}`))
	localReq.Header.Set("Origin", ts.URL)
	localReq.Header.Set("Content-Type", "application/json")
	localRes, err := client.Do(localReq)
	if err != nil {
		t.Fatal(err)
	}
	localRes.Body.Close()
	if localRes.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign loopback UID local exchange accepted: %d", localRes.StatusCode)
	}

	// Verified v1 custody OwnerUID must be allowed on Local loopback auth even while instanceOwnerUID stays 0.
	if uid := os.Getuid(); uid > 0 && os.Geteuid() == uid {
		sV1 := fixtureStore(t)
		claimFixture(t, sV1)
		v1Config := signer.Config{
			Version:   1,
			Mode:      signer.FixtureNetwork,
			SignerID:  strings.Repeat("a", 32),
			OwnerUID:  uid + 7,
			AppUID:    uid,
			SignerUID: uid + 8,
			StateDir:  "/fixture/signer-state",
			SocketDir: "/fixture/signer-ipc",
		}
		if err = sV1.configureVerifiedSigner(v1Config); err != nil {
			t.Fatal(err)
		}
		hV1, err := NewHTTPHandler(sV1, "http://127.0.0.1:7331", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if hV1.instanceOwnerUID != 0 {
			t.Fatalf("unexpected non-zero instanceOwnerUID for v1 custody: %d", hV1.instanceOwnerUID)
		}
		peerUID := v1Config.OwnerUID
		hV1.loopbackPeerUID = func(*net.TCPAddr, *net.TCPAddr) (int, bool, error) {
			return peerUID, true, nil
		}
		tsV1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hV1.origins["http://"+r.Host] = true
			hV1.hosts[r.Host] = true
			hV1.ServeHTTP(w, r)
		}))
		defer tsV1.Close()
		doV1Login := func() int {
			req, _ := http.NewRequest(http.MethodPost, tsV1.URL+"/api/v1/auth/login", strings.NewReader(`{"password":"`+testPassword+`"}`))
			req.Header.Set("Origin", tsV1.URL)
			req.Header.Set("Content-Type", "application/json")
			res, e := tsV1.Client().Do(req)
			if e != nil {
				t.Fatal(e)
			}
			res.Body.Close()
			return res.StatusCode
		}
		if code := doV1Login(); code != http.StatusOK {
			t.Fatalf("v1 custody OwnerUID loopback login rejected: %d", code)
		}
		peerUID = v1Config.OwnerUID + 1
		if code := doV1Login(); code != http.StatusForbidden {
			t.Fatalf("unrelated UID accepted under v1 custody: %d", code)
		}
		if hV1.instanceOwnerUID != 0 {
			t.Fatalf("v1 custody instanceOwnerUID mutated: %d", hV1.instanceOwnerUID)
		}
	}
}
