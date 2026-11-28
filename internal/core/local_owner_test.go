package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/custody"
	"github.com/fased-ai/fased-app/internal/signer"
)

func TestLocalOwnerLaunchIsPrivateSingleUseAndPreservesPassword(t *testing.T) {
	t.Parallel()
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[existing], func(t *testing.T) {
			s, err := OpenStore(filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if existing {
				code, e := s.IssueClaimCode(t.Context())
				if e != nil {
					t.Fatal(e)
				}
				if _, e = s.Claim(t.Context(), code.Code, "preserved-owner-password"); e != nil {
					t.Fatal(e)
				}
			}
			h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
			if err != nil {
				t.Fatal(err)
			}
			call := func(private bool, action, code string) *httptest.ResponseRecorder {
				b, _ := json.Marshal(map[string]string{"action": action, "code": code})
				r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/auth/local", bytes.NewReader(b))
				r.Header.Set("Origin", "http://127.0.0.1:7331")
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				if private {
					privateIPCHandler(h).ServeHTTP(w, r)
				} else {
					h.ServeHTTP(w, r)
				}
				return w
			}
			for _, action := range []string{"launch", "session"} {
				if w := call(false, action, ""); w.Code != http.StatusForbidden {
					t.Fatalf("HTTP acquired OS authority: %d", w.Code)
				}
			}
			w := call(true, "launch", "")
			if w.Code != 200 {
				t.Fatalf("OS launch: %d %s", w.Code, w.Body)
			}
			var grant struct {
				Code string `json:"code"`
			}
			if json.Unmarshal(w.Body.Bytes(), &grant) != nil || len(grant.Code) != 43 {
				t.Fatal("missing private grant")
			}
			if w = call(false, "exchange", "wrong"); w.Code != 401 {
				t.Fatal("invalid grant admitted", w.Code)
			}
			w = call(false, "exchange", grant.Code)
			if w.Code != 200 {
				t.Fatal("launch exchange", w.Code, w.Body)
			}
			var session struct {
				SessionToken string `json:"sessionToken"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &session)
			if !s.Session(t.Context(), session.SessionToken) {
				t.Fatal("no ordinary authenticated session")
			}
			if w = call(false, "exchange", grant.Code); w.Code != 401 {
				t.Fatal("grant replayed")
			}
			if _, e := s.Login(t.Context(), "preserved-owner-password"); (e == nil) != existing {
				t.Fatal("password identity changed", e)
			}
			h.instanceLocation = "remote"
			if w = call(true, "session", ""); w.Code != 403 {
				t.Fatal("Remote acquired Local identity")
			}
			h.instanceLocation = "local"
			s.fixture = func(context.Context, signer.Request) (signer.Response, error) {
				return signer.Response{}, errors.New("not called")
			}
			if w = call(true, "session", ""); w.Code != 403 {
				t.Fatal("separately owned custody acquired Local identity", w.Code)
			}
			state := httptest.NewRecorder()
			h.ServeHTTP(state, httptest.NewRequest("GET", "http://127.0.0.1:7331/api/v1/auth/state", nil))
			var auth struct {
				LocalAccess bool `json:"localAccess"`
			}
			if state.Code != 200 || json.Unmarshal(state.Body.Bytes(), &auth) != nil || auth.LocalAccess {
				t.Fatal("custody must retain protected sign-in", state.Code, state.Body)
			}
		})
	}
}

func TestLocalOwnerLaunchExpiresAndDoesNotCrossRestart(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "state")
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	code, err := s.issueLocalLaunch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.exchangeLocalLaunch(t.Context(), code); err != nil {
		t.Fatal(err)
	}
	if _, err = s.exchangeLocalLaunch(t.Context(), code); err == nil {
		t.Fatal("replay accepted")
	}
	code, err = s.issueLocalLaunch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	now := s.now()
	s.now = func() time.Time { return now.Add(localLaunchLifetime) }
	if _, err = s.exchangeLocalLaunch(t.Context(), code); err == nil {
		t.Fatal("expired proof admitted")
	}
	s.now = time.Now
	code, err = s.issueLocalLaunch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, err = restarted.exchangeLocalLaunch(t.Context(), code); err == nil {
		t.Fatal("proof survived restart")
	}
}

func TestLocalOwnerCLIAndBrowserShareManualAndAutomaticUpdater(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "state")
	srv, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Drive controlled ticks explicitly; this is API/controller evidence, not
	// a claim of installed file replacement or release authenticity.
	srv.taskCancel()
	<-srv.taskDone
	driver := fixtureUpdateController()
	if err = srv.Store.BindApplicationUpdateController(driver); err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	defer srv.Close(context.Background())
	cli, err := NewClient(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	if err = cli.LoadSession(t.Context(), dir); err != nil {
		t.Fatal("password-free CLI", err)
	}
	if _, err = os.Stat(filepath.Join(dir, "cli-session.json")); err != nil {
		t.Fatal(err)
	}
	proof, err := cli.LocalLaunch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	web := &Client{Endpoint: cli.Endpoint, HTTP: &http.Client{}}
	if err = web.authenticate(t.Context(), "/api/v1/auth/local", map[string]string{"action": "exchange", "code": proof}); err != nil {
		t.Fatal(err)
	}
	read := func(c *Client) ApplicationUpdateView {
		t.Helper()
		var v ApplicationUpdateView
		if e := c.Do(t.Context(), "GET", "/api/v1/updates/application", nil, &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	command := func(c *Client, action string) {
		t.Helper()
		v := read(c)
		r := ApplicationUpdateCommand{Action: action, Revision: v.Revision, PolicyRevision: v.PolicyRevision}
		if action == "apply" {
			r.ManifestSHA256 = v.Operation.Release.ManifestSHA256
		}
		if e := c.Do(t.Context(), "POST", "/api/v1/updates/application", r, nil); e != nil {
			t.Fatal(e)
		}
	}
	command(web, "check")
	updateTickFixture(t, srv.Store)
	if read(cli).Operation.Phase != "available" {
		t.Fatal("CLI cannot observe UI check")
	}
	command(cli, "apply")
	updateTickFixture(t, srv.Store)
	view := read(web)
	if view.Operation.Phase != "completed" || driver.applies != 1 || read(cli).Operation.ID != view.Operation.ID {
		t.Fatal("UI/CLI operation diverged", view)
	}
	// Another release is consumed by the same scheduler/controller after all
	// browser/CLI sessions are gone; no password, wallet unlock or SSH actor.
	driver.release.Sequence++
	driver.release.ManifestSHA256 = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	var prefs UpdatePreferencesView
	if err = cli.Do(t.Context(), "GET", "/api/v1/updates/preferences", nil, &prefs); err != nil {
		t.Fatal(err)
	}
	if err = cli.Do(t.Context(), "PUT", "/api/v1/updates/preferences", UpdatePreferencesRequest{Action: "policy", Revision: prefs.Revision, Policy: &UpdatePolicy{Mode: "automatic", Notify: true}}, nil); err != nil {
		t.Fatal(err)
	}
	if err = srv.Store.Logout(t.Context(), web.Token); err != nil {
		t.Fatal(err)
	}
	if err = srv.Store.Logout(t.Context(), cli.Token); err != nil {
		t.Fatal(err)
	}
	commandErr := cli.loadSavedSession(t.Context(), dir)
	if !errors.Is(commandErr, ErrUnauthorized) {
		t.Fatal("revoked CLI was still authenticated", commandErr)
	}
	nextCheck := srv.Store.now().Add(time.Hour)
	srv.Store.now = func() time.Time { return nextCheck }
	for n := 0; n < 5; n++ {
		updateTickFixture(t, srv.Store)
	}
	after, err := srv.Store.ApplicationUpdates(t.Context())
	if err != nil || !after.Operation.Automatic || after.Operation.Phase != "completed" || driver.applies != 2 {
		t.Fatal("unattended update diverged", after, err, driver)
	}
	updateModeFixture(t, srv.Store, UpdatePolicy{Mode: "manual", Notify: true})
}

func TestLocalBrowserAutoEnterSecurity(t *testing.T) {
	t.Parallel()
	newHandler := func(t *testing.T) (*Store, *HTTPHandler) {
		t.Helper()
		s, err := OpenStore(filepath.Join(t.TempDir(), "state"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		return s, h
	}
	readAuthState := func(t *testing.T, h *HTTPHandler) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7331/api/v1/auth/state", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("auth/state status=%d body=%s", w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	callBrowser := func(h *HTTPHandler, origin, localHdr, secFetchSite string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/auth/local", bytes.NewBufferString(`{"action":"browser"}`))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("Content-Type", "application/json")
		if localHdr != "" {
			r.Header.Set("X-Fased-Local", localHdr)
		}
		if secFetchSite != "" {
			r.Header.Set("Sec-Fetch-Site", secFetchSite)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	s, h := newHandler(t)
	if state := readAuthState(t, h); state["localAutoEnter"] != true || state["passwordAvailable"] != false {
		t.Fatalf("expected default Local auto-enter state, got %#v", state)
	}
	if w := callBrowser(h, "http://127.0.0.1:7331", "", "same-origin"); w.Code != http.StatusForbidden {
		t.Fatalf("missing X-Fased-Local status=%d", w.Code)
	}
	if w := callBrowser(h, "http://127.0.0.1:7331", "1", "cross-site"); w.Code != http.StatusForbidden {
		t.Fatalf("cross-site Sec-Fetch-Site status=%d", w.Code)
	}
	if w := callBrowser(h, "http://127.0.0.1:7331", "1", "same-site"); w.Code != http.StatusForbidden {
		t.Fatalf("same-site Sec-Fetch-Site status=%d", w.Code)
	}
	if w := callBrowser(h, "https://evil.example", "1", "same-origin"); w.Code != http.StatusForbidden {
		t.Fatalf("foreign origin status=%d", w.Code)
	}
	w := callBrowser(h, "http://127.0.0.1:7331", "1", "same-origin")
	if w.Code != http.StatusOK {
		t.Fatalf("same-origin browser entry failed: %d %s", w.Code, w.Body.String())
	}
	var session struct {
		SessionToken string `json:"sessionToken"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil || !s.Session(t.Context(), session.SessionToken) {
		t.Fatalf("invalid browser auto-enter session: %v %q", err, w.Body.String())
	}
	if state := readAuthState(t, h); state["localAutoEnter"] != true || state["passwordAvailable"] != false {
		t.Fatalf("passwordless claim should preserve localAutoEnter, got %#v", state)
	}
	if w = callBrowser(h, "http://127.0.0.1:7331", "1", ""); w.Code != http.StatusOK {
		t.Fatalf("empty Sec-Fetch-Site on same-origin loopback failed: %d %s", w.Code, w.Body.String())
	}

	h.SetRequirePassword(true)
	if state := readAuthState(t, h); state["localAutoEnter"] != false {
		t.Fatalf("expected localAutoEnter=false when requirePassword=true, got %#v", state)
	}
	if w = callBrowser(h, "http://127.0.0.1:7331", "1", "same-origin"); w.Code != http.StatusForbidden {
		t.Fatalf("requirePassword=true admitted browser action: %d", w.Code)
	}
	h.SetRequirePassword(false)

	h.instanceLocation = "remote"
	if w = callBrowser(h, "http://127.0.0.1:7331", "1", "same-origin"); w.Code != http.StatusForbidden {
		t.Fatalf("remote admitted browser action: %d", w.Code)
	}
	h.instanceLocation = "local"

	s.fixture = func(context.Context, signer.Request) (signer.Response, error) {
		return signer.Response{}, errors.New("not called")
	}
	if w = callBrowser(h, "http://127.0.0.1:7331", "1", "same-origin"); w.Code != http.StatusForbidden {
		t.Fatalf("custody fixture admitted browser action: %d", w.Code)
	}

	ps, ph := newHandler(t)
	code, err := ps.IssueClaimCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ps.Claim(t.Context(), code.Code, "owner-password-configured"); err != nil {
		t.Fatal(err)
	}
	if state := readAuthState(t, ph); state["localAutoEnter"] != false || state["passwordAvailable"] != true {
		t.Fatalf("password-claimed instance should disable localAutoEnter, got %#v", state)
	}
	if w = callBrowser(ph, "http://127.0.0.1:7331", "1", "same-origin"); w.Code != http.StatusForbidden {
		t.Fatalf("password-claimed instance admitted passwordless browser action: %d", w.Code)
	}
}

func TestRemoteProxyTrustAndLocalNonceHardening(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "https://proxy.example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	h.instanceLocation = "remote"
	h.remoteProxyTrust = true
	h.remoteTransport = "cloudflare"

	// Ambiguous multi-header X-Forwarded-Proto over plain loopback proxy HTTP must be refused.
	ambig := httptest.NewRequest(http.MethodGet, "http://proxy.example.com/api/v1/health", nil)
	ambig.Host = "proxy.example.com"
	ambig.Header.Add("X-Forwarded-Proto", "http")
	ambig.Header.Add("X-Forwarded-Proto", "https")
	wa := httptest.NewRecorder()
	h.ServeHTTP(wa, ambig)
	if wa.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on multi-value X-Forwarded-Proto, got %d", wa.Code)
	}

	// Non-loopback TCP peer must be refused when LocalAddrContextKey is set (real socket).
	nonLoop := httptest.NewRequest(http.MethodGet, "http://proxy.example.com/api/v1/health", nil)
	nonLoop.Host = "proxy.example.com"
	nonLoop.RemoteAddr = "203.0.113.55:4444"
	nonLoop.Header.Set("X-Forwarded-Proto", "https")
	nonLoop = nonLoop.WithContext(context.WithValue(nonLoop.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 7331}))
	wn := httptest.NewRecorder()
	h.ServeHTTP(wn, nonLoop)
	if wn.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on non-loopback proxy peer, got %d", wn.Code)
	}

	// Nonce initialization writes a 0600 file and validates matching headers only.
	nonceDir := filepath.Join(t.TempDir(), "nonce-state")
	if err := h.initLocalBootstrapNonce(nonceDir); err != nil {
		t.Fatalf("initLocalBootstrapNonce failed: %v", err)
	}
	if h.localNonce == "" {
		t.Fatal("expected non-empty local bootstrap nonce")
	}
	goodReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/auth/local", nil)
	goodReq.Header.Set("X-Fased-Local-Nonce", h.localNonce)
	if !h.validLocalNonce(goodReq) {
		t.Fatal("valid local bootstrap nonce was rejected")
	}
	badReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/auth/local", nil)
	badReq.Header.Set("X-Fased-Local-Nonce", "wrong-nonce")
	if h.validLocalNonce(badReq) {
		t.Fatal("wrong local bootstrap nonce was accepted")
	}
}

func TestLocalWalletUnlockPasswordFlow(t *testing.T) {
	t.Parallel()
	newUnlockHandler := func(t *testing.T) (*Store, *HTTPHandler, custody.Backend) {
		t.Helper()
		dir := fixtureDir(t)
		s, err := OpenStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		cust, err := s.custodyBackend()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cust.CreateNativeWallet(t.Context(), "Miner", custody.RoleMining, "https://api.devnet.solana.com"); err != nil {
			t.Fatal(err)
		}
		// Mark instance claimed with localOwnerSalt (matching desktop/local launch with native wallet).
		if _, err := s.localOwnerSession(t.Context()); err != nil {
			t.Fatal(err)
		}
		h, err := NewHTTPHandler(s, "http://127.0.0.1:7332", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		h.loopbackPeerUID = func(_, _ *net.TCPAddr) (int, bool, error) { return os.Getuid(), true, nil }
		return s, h, cust
	}
	readAuthState := func(t *testing.T, h *HTTPHandler) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7332/api/v1/auth/state", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("auth/state status=%d body=%s", w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	callUnlock := func(h *HTTPHandler, password string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"action": "unlock-password", "password": password})
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7332/api/v1/auth/local", bytes.NewReader(body))
		r.Host = "127.0.0.1:7332"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://127.0.0.1:7332")
		r.Header.Set("X-Fased-Local", "1")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// Case 1: Native wallet present, neither core nor signer has password yet -> first unlock sets both & returns session.
	s1, h1, cust1 := newUnlockHandler(t)
	st1 := readAuthState(t, h1)
	if st1["localAutoEnter"] != false || st1["localPasswordRequired"] != true || st1["signerPasswordConfigured"] != false {
		t.Fatalf("unexpected auth state before unlock: %#v", st1)
	}
	if w := callUnlock(h1, "short"); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for short password, got %d: %s", w.Code, w.Body.String())
	}
	w1 := callUnlock(h1, "correct-horse-battery-staple")
	if w1.Code != http.StatusOK {
		t.Fatalf("unlock-password setup failed: %d %s", w1.Code, w1.Body.String())
	}
	if has, _ := cust1.HasOwnerAuth(t.Context()); !has {
		t.Fatal("expected signer owner auth to be enrolled after first unlock")
	}
	if hasPW, _ := s1.hasPassword(t.Context()); !hasPW {
		t.Fatal("expected core owner password to be set after first unlock")
	}
	if wDup := callUnlock(h1, "correct-horse-battery-staple"); wDup.Code != http.StatusConflict {
		t.Fatalf("expected 409 once password is already configured, got %d", wDup.Code)
	}

	// Case 2: Signer already has an owner password, core does not -> wrong password rejected (401), matching password unlocks.
	s2, h2, cust2 := newUnlockHandler(t)
	if err := cust2.ConfigureOwnerAuth(t.Context(), "existing-signer-secret-99"); err != nil {
		t.Fatal(err)
	}
	st2 := readAuthState(t, h2)
	if st2["localPasswordRequired"] != true || st2["signerPasswordConfigured"] != true {
		t.Fatalf("expected signerPasswordConfigured=true, got %#v", st2)
	}
	if wBad := callUnlock(h2, "wrong-password-1234"); wBad.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on wrong signer password, got %d: %s", wBad.Code, wBad.Body.String())
	}
	wGood := callUnlock(h2, "existing-signer-secret-99")
	if wGood.Code != http.StatusOK || !strings.Contains(wGood.Body.String(), "sessionToken") {
		t.Fatalf("expected 200 session on matching signer password, got %d: %s", wGood.Code, wGood.Body.String())
	}
	if _, err := s2.Login(t.Context(), "existing-signer-secret-99"); err != nil {
		t.Fatalf("expected normal Login to succeed after sync: %v", err)
	}

	// Case 3: Out-of-process fased-signer running with --require-owner-auth and no signer owner password yet
	// returns ErrOwnerAuthRequired over app.sock; unlock-password must tolerate it and enroll the Core password cleanly.
	signerDir := t.TempDir()
	signerSvc, err := custody.NewService(custody.Config{
		DataDir:          signerDir,
		FixtureMode:      true,
		FastKDF:          true,
		RequireOwnerAuth: true,
	})
	if err != nil {
		t.Fatalf("custody.NewService: %v", err)
	}
	t.Cleanup(func() { _ = signerSvc.Close() })
	sockPath := filepath.Join(signerDir, "app.sock")
	sockSrv, err := custody.StartSocketServerAt(signerSvc, sockPath, os.Getuid(), 0o600)
	if err != nil {
		t.Fatalf("StartSocketServerAt: %v", err)
	}
	t.Cleanup(func() { _ = sockSrv.Close(t.Context()) })
	s3, err := OpenStore(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s3.Close() })
	s3.ConfigureCustodySocket(sockPath)
	cust3, err := s3.custodyBackend()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cust3.CreateNativeWallet(t.Context(), "Miner", custody.RoleMining, "https://api.devnet.solana.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := s3.localOwnerSession(t.Context()); err != nil {
		t.Fatal(err)
	}
	h3, err := NewHTTPHandler(s3, "http://127.0.0.1:7332", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	h3.loopbackPeerUID = func(_, _ *net.TCPAddr) (int, bool, error) { return os.Getuid(), true, nil }
	w3 := callUnlock(h3, "abc123")
	if w3.Code != http.StatusOK || !strings.Contains(w3.Body.String(), "sessionToken") {
		t.Fatalf("expected 200 session with 6-char Local password under --require-owner-auth, got %d: %s", w3.Code, w3.Body.String())
	}
	if hasPW, _ := s3.hasPassword(t.Context()); !hasPW {
		t.Fatal("expected core owner password to be set after unlock-password")
	}
	if _, err := s3.Login(t.Context(), "abc123"); err != nil {
		t.Fatalf("expected normal Login to succeed after 6-char Case 3 unlock: %v", err)
	}
}
