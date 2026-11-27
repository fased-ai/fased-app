package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/tailnet"
)

func TestPairingLifecycleSecurityAndCookieMode(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "fased")
	now := time.Unix(1_760_000_000, 0).UTC()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.now = func() time.Time { return now }

	if _, err := s.IssueClaimCode(t.Context()); err != nil {
		t.Fatal(err)
	}
	issued, err := s.IssuePairingCode(t.Context(), "MacBook Safari", 10*time.Minute)
	if err != nil || len(issued.Code) != 43 {
		t.Fatal(issued, err)
	}

	// Plaintext pairing code must never appear on disk in pairing.json.
	raw, err := os.ReadFile(filepath.Join(dir, pairingStateFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), issued.Code) {
		t.Fatal("plaintext pairing code leaked to disk")
	}

	// Setup HTTP handler configured as a remote Cloudflare proxy origin.
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "https://fased.example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	h.instanceLocation = "remote"
	h.remoteProxyTrust = true
	h.remoteTransport = "cloudflare"

	// Non-IPC HTTP cannot issue pairing codes or manage paired devices.
	for _, route := range []string{"/api/v1/auth/pair/issue", "/api/v1/auth/pair/manage"} {
		req := httptest.NewRequest(http.MethodPost, "https://fased.example.com"+route, strings.NewReader(`{}`))
		req.Host = "fased.example.com"
		req.Header.Set("Origin", "https://fased.example.com")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s admitted non-IPC caller: %d %s", route, w.Code, w.Body.String())
		}
	}

	// Plain HTTP proxy forwarding on publicHost is rejected.
	downgrade := httptest.NewRequest(http.MethodGet, "http://fased.example.com/api/v1/health", nil)
	downgrade.Host = "fased.example.com"
	wDown := httptest.NewRecorder()
	h.ServeHTTP(wDown, downgrade)
	if wDown.Code != http.StatusForbidden {
		t.Fatalf("proxy HTTP downgrade admitted: %d", wDown.Code)
	}

	// Auth state advertises pairingAvailable and passwordAvailable=false when unclaimed.
	stateReq := httptest.NewRequest(http.MethodGet, "https://fased.example.com/api/v1/auth/state", nil)
	stateReq.Host = "fased.example.com"
	stateReq.Header.Set("X-Forwarded-Proto", "https")
	wState := httptest.NewRecorder()
	h.ServeHTTP(wState, stateReq)
	if wState.Code != http.StatusOK || !strings.Contains(wState.Body.String(), `"pairingAvailable":true`) || !strings.Contains(wState.Body.String(), `"remoteTransport":"cloudflare"`) || !strings.Contains(wState.Body.String(), `"passwordAvailable":false`) {
		t.Fatalf("unexpected auth state: %d %s", wState.Code, wState.Body.String())
	}

	// Missing X-Fased-Pair header or cross-site Sec-Fetch-Site must fail.
	pairBody, _ := json.Marshal(map[string]string{"code": issued.Code})
	noHeader := httptest.NewRequest(http.MethodPost, "https://fased.example.com/api/v1/auth/pair", bytes.NewReader(pairBody))
	noHeader.Host = "fased.example.com"
	noHeader.Header.Set("Origin", "https://fased.example.com")
	noHeader.Header.Set("Content-Type", "application/json")
	noHeader.Header.Set("X-Forwarded-Proto", "https")
	wNoHdr := httptest.NewRecorder()
	h.ServeHTTP(wNoHdr, noHeader)
	if wNoHdr.Code != http.StatusForbidden {
		t.Fatalf("missing X-Fased-Pair admitted: %d", wNoHdr.Code)
	}

	crossSite := httptest.NewRequest(http.MethodPost, "https://fased.example.com/api/v1/auth/pair", bytes.NewReader(pairBody))
	crossSite.Host = "fased.example.com"
	crossSite.Header.Set("Origin", "https://fased.example.com")
	crossSite.Header.Set("Content-Type", "application/json")
	crossSite.Header.Set("X-Forwarded-Proto", "https")
	crossSite.Header.Set("X-Fased-Pair", "1")
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	wCross := httptest.NewRecorder()
	h.ServeHTTP(wCross, crossSite)
	if wCross.Code != http.StatusForbidden {
		t.Fatalf("cross-site pair admitted: %d", wCross.Code)
	}

	// Valid pairing exchange on proxy HTTPS must use cookie-only mode without leaking sessionToken.
	pairReq := httptest.NewRequest(http.MethodPost, "https://fased.example.com/api/v1/auth/pair", bytes.NewReader(pairBody))
	pairReq.Host = "fased.example.com"
	pairReq.Header.Set("Origin", "https://fased.example.com")
	pairReq.Header.Set("Content-Type", "application/json")
	pairReq.Header.Set("X-Forwarded-Proto", "https")
	pairReq.Header.Set("X-Fased-Pair", "1")
	pairReq.Header.Set("Sec-Fetch-Site", "same-origin")
	wPair := httptest.NewRecorder()
	h.ServeHTTP(wPair, pairReq)
	if wPair.Code != http.StatusOK {
		t.Fatalf("valid pair failed: %d %s", wPair.Code, wPair.Body.String())
	}
	var sessionResp struct {
		Authenticated bool   `json:"authenticated"`
		CSRFToken     string `json:"csrfToken"`
		SessionToken  string `json:"sessionToken"`
		SessionMode   string `json:"sessionMode"`
	}
	if err = json.Unmarshal(wPair.Body.Bytes(), &sessionResp); err != nil {
		t.Fatal(err)
	}
	if !sessionResp.Authenticated || sessionResp.CSRFToken == "" || sessionResp.SessionMode != "cookie" || sessionResp.SessionToken != "" {
		t.Fatalf("cookie-mode leaked sessionToken or invalid response: %+v", sessionResp)
	}
	var cookie1 *http.Cookie
	for _, ck := range wPair.Result().Cookies() {
		if ck.Name == hostSessionCookie {
			cookie1 = ck
			break
		}
	}
	if cookie1 == nil || !cookie1.HttpOnly || !cookie1.Secure || cookie1.SameSite != http.SameSiteStrictMode {
		t.Fatalf("expected HttpOnly; Secure; SameSite=Strict cookie, got %+v", cookie1)
	}

	// Single-use: replay of the exact same code fails even immediately.
	wReplay := httptest.NewRecorder()
	replayReq := httptest.NewRequest(http.MethodPost, "https://fased.example.com/api/v1/auth/pair", bytes.NewReader(pairBody))
	replayReq.Host = "fased.example.com"
	replayReq.Header.Set("Origin", "https://fased.example.com")
	replayReq.Header.Set("Content-Type", "application/json")
	replayReq.Header.Set("X-Forwarded-Proto", "https")
	replayReq.Header.Set("X-Fased-Pair", "1")
	h.ServeHTTP(wReplay, replayReq)
	if wReplay.Code != http.StatusUnauthorized {
		t.Fatalf("replayed code succeeded: %d", wReplay.Code)
	}

	// Unclaimed instance is now claimed with pairedOwnerSalt, keeping PRAGMA user_version == StateSchema and disabling password login.
	var schema int
	var salt []byte
	if err = s.db.QueryRow("PRAGMA user_version").Scan(&schema); err != nil || schema != StateSchema {
		t.Fatalf("expected user_version %d, got %d (err=%v)", StateSchema, schema, err)
	}
	if err = s.db.QueryRow("SELECT password_salt FROM instance WHERE id=1").Scan(&salt); err != nil || !bytes.Equal(salt, pairedOwnerSalt) {
		t.Fatalf("unexpected salt after pair claim: %x %v", salt, err)
	}
	if _, err = s.Login(t.Context(), "anything-at-all"); err == nil {
		t.Fatal("password login allowed after pairing-only claim")
	}

	// Issue a second pairing code from the authenticated browser device.
	issueReq := httptest.NewRequest(http.MethodPost, "https://fased.example.com/api/v1/auth/pair/devices", strings.NewReader(`{"action":"issue","label":"Phone Browser"}`))
	issueReq.Host = "fased.example.com"
	issueReq.Header.Set("Origin", "https://fased.example.com")
	issueReq.Header.Set("Content-Type", "application/json")
	issueReq.Header.Set("X-Forwarded-Proto", "https")
	issueReq.Header.Set("X-CSRF-Token", sessionResp.CSRFToken)
	issueReq.AddCookie(cookie1)
	wIssue := httptest.NewRecorder()
	h.ServeHTTP(wIssue, issueReq)
	if wIssue.Code != http.StatusOK {
		t.Fatalf("authenticated device issue failed: %d %s", wIssue.Code, wIssue.Body.String())
	}
	var issued2 struct {
		ID   string `json:"id"`
		Code string `json:"code"`
		URL  string `json:"url"`
	}
	if err = json.Unmarshal(wIssue.Body.Bytes(), &issued2); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(issued2.URL, "https://fased.example.com/?instance="+s.instanceID+"#pair=") {
		t.Fatalf("unexpected pairing URL: %s", issued2.URL)
	}

	// Exchange second code from second browser.
	pair2Body, _ := json.Marshal(map[string]string{"code": issued2.Code})
	pair2Req := httptest.NewRequest(http.MethodPost, "https://fased.example.com/api/v1/auth/pair", bytes.NewReader(pair2Body))
	pair2Req.Host = "fased.example.com"
	pair2Req.Header.Set("Origin", "https://fased.example.com")
	pair2Req.Header.Set("Content-Type", "application/json")
	pair2Req.Header.Set("X-Forwarded-Proto", "https")
	pair2Req.Header.Set("X-Fased-Pair", "1")
	wPair2 := httptest.NewRecorder()
	h.ServeHTTP(wPair2, pair2Req)
	if wPair2.Code != http.StatusOK {
		t.Fatalf("second pair failed: %d %s", wPair2.Code, wPair2.Body.String())
	}
	var cookie2 *http.Cookie
	for _, ck := range wPair2.Result().Cookies() {
		if ck.Name == hostSessionCookie {
			cookie2 = ck
			break
		}
	}
	if cookie2 == nil {
		t.Fatal("missing cookie2")
	}

	// List devices as device 1 and verify both devices show up, with device 1 marked Current.
	listReq := httptest.NewRequest(http.MethodGet, "https://fased.example.com/api/v1/auth/pair/devices", nil)
	listReq.Host = "fased.example.com"
	listReq.Header.Set("X-Forwarded-Proto", "https")
	listReq.AddCookie(cookie1)
	wList := httptest.NewRecorder()
	h.ServeHTTP(wList, listReq)
	if wList.Code != http.StatusOK {
		t.Fatalf("list devices failed: %d %s", wList.Code, wList.Body.String())
	}
	var listResp struct {
		Devices []PairedDevice `json:"devices"`
		Current string         `json:"current"`
	}
	if err = json.Unmarshal(wList.Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Devices) != 2 || listResp.Current == "" {
		t.Fatalf("expected 2 devices, got %+v", listResp)
	}
	var dev2ID string
	for _, d := range listResp.Devices {
		if d.ID != listResp.Current {
			dev2ID = d.ID
		}
	}
	if dev2ID == "" {
		t.Fatal("missing second device ID")
	}

	// Cannot revoke own current device via revoke; use sign out instead.
	selfRevoke := httptest.NewRequest(http.MethodPost, "https://fased.example.com/api/v1/auth/pair/devices", strings.NewReader(`{"action":"revoke","deviceId":"`+listResp.Current+`"}`))
	selfRevoke.Host = "fased.example.com"
	selfRevoke.Header.Set("Origin", "https://fased.example.com")
	selfRevoke.Header.Set("Content-Type", "application/json")
	selfRevoke.Header.Set("X-Forwarded-Proto", "https")
	selfRevoke.Header.Set("X-CSRF-Token", sessionResp.CSRFToken)
	selfRevoke.AddCookie(cookie1)
	wSelf := httptest.NewRecorder()
	h.ServeHTTP(wSelf, selfRevoke)
	if wSelf.Code != http.StatusBadRequest {
		t.Fatalf("self revoke should fail: %d", wSelf.Code)
	}

	// Revoke device 2; device 2 session immediately stops authenticating.
	revReq := httptest.NewRequest(http.MethodPost, "https://fased.example.com/api/v1/auth/pair/devices", strings.NewReader(`{"action":"revoke","deviceId":"`+dev2ID+`"}`))
	revReq.Host = "fased.example.com"
	revReq.Header.Set("Origin", "https://fased.example.com")
	revReq.Header.Set("Content-Type", "application/json")
	revReq.Header.Set("X-Forwarded-Proto", "https")
	revReq.Header.Set("X-CSRF-Token", sessionResp.CSRFToken)
	revReq.AddCookie(cookie1)
	wRev := httptest.NewRecorder()
	h.ServeHTTP(wRev, revReq)
	if wRev.Code != http.StatusOK {
		t.Fatalf("revoke failed: %d %s", wRev.Code, wRev.Body.String())
	}

	check2 := httptest.NewRequest(http.MethodGet, "https://fased.example.com/api/v1/setup", nil)
	check2.Host = "fased.example.com"
	check2.Header.Set("X-Forwarded-Proto", "https")
	check2.AddCookie(cookie2)
	wCheck2 := httptest.NewRecorder()
	h.ServeHTTP(wCheck2, check2)
	if wCheck2.Code != http.StatusUnauthorized {
		t.Fatalf("revoked device session remained active: %d", wCheck2.Code)
	}

	// Sign out device 1 removes its paired device record and invalidates cookie1.
	logoutReq := httptest.NewRequest(http.MethodPost, "https://fased.example.com/api/v1/auth/logout", strings.NewReader(`{}`))
	logoutReq.Host = "fased.example.com"
	logoutReq.Header.Set("Origin", "https://fased.example.com")
	logoutReq.Header.Set("Content-Type", "application/json")
	logoutReq.Header.Set("X-Forwarded-Proto", "https")
	logoutReq.Header.Set("X-CSRF-Token", sessionResp.CSRFToken)
	logoutReq.AddCookie(cookie1)
	wLogout := httptest.NewRecorder()
	h.ServeHTTP(wLogout, logoutReq)
	if wLogout.Code != http.StatusOK {
		t.Fatalf("logout failed: %d", wLogout.Code)
	}
	devicesAfter, _, err := s.ListPairedDevices(t.Context(), "")
	if err != nil || len(devicesAfter) != 0 {
		t.Fatalf("expected 0 devices after revoke+logout, got %+v (%v)", devicesAfter, err)
	}

	// Expired pairing code must be rejected cleanly.
	expired, err := s.IssuePairingCode(t.Context(), "Old", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, _, err = s.ExchangePairingCode(t.Context(), expired.Code, "", "Browser", "cloudflare", ""); err == nil {
		t.Fatal("expired pairing code accepted")
	}
}

func TestPairingRateLimitAndPasswordPreservationAndTailscaleBinding(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "fased")
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Claim instance with an owner password first; pairing must preserve it.
	claim, err := s.IssueClaimCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(t.Context(), claim.Code, "existing-owner-password-12"); err != nil {
		t.Fatal(err)
	}
	issued, err := s.IssuePairingCode(t.Context(), "Backup", 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "https://node.tail-test.ts.net", nil)
	if err != nil {
		t.Fatal(err)
	}
	h.instanceLocation = "remote"
	h.remoteIngress = true
	h.remoteTransport = "tailscale"

	// Over Tailscale, pair binds to NodeID and denies replay from a different NodeID.
	idA := tailnet.Identity{UserID: 99, NodeID: "node-alpha", Login: "owner@test"}
	idB := tailnet.Identity{UserID: 99, NodeID: "node-beta", Login: "owner@test"}
	tok, _, err := s.ExchangePairingCode(t.Context(), issued.Code, "Phone", "", "tailscale", idA.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	reqA := httptest.NewRequest("GET", "https://node.tail-test.ts.net/api/v1/setup", nil).WithContext(context.WithValue(t.Context(), remoteIdentityKey{}, idA))
	if !h.ownerSession(reqA, tok) {
		t.Fatal("Tailscale paired node denied")
	}
	reqB := httptest.NewRequest("GET", "https://node.tail-test.ts.net/api/v1/setup", nil).WithContext(context.WithValue(t.Context(), remoteIdentityKey{}, idB))
	if h.ownerSession(reqB, tok) {
		t.Fatal("Tailscale paired token accepted from wrong node")
	}

	// Original owner password remains fully functional after pairing.
	if _, err = s.Login(t.Context(), "existing-owner-password-12"); err != nil {
		t.Fatalf("existing password broken by pairing: %v", err)
	}

	// Rate limit check on /api/v1/auth/pair (8 attempts per minute per peer).
	sshH, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	sshH.instanceLocation = "remote"
	sshH.remoteTransport = "ssh"
	for i := 0; i < 8; i++ {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/auth/pair", strings.NewReader(`{"code":"bad"}`))
		r.Host = "127.0.0.1:7331"
		r.RemoteAddr = "127.0.0.1:51000"
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Fased-Pair", "1")
		w := httptest.NewRecorder()
		sshH.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d returned %d", i, w.Code)
		}
	}
	r9 := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/auth/pair", strings.NewReader(`{"code":"bad"}`))
	r9.Host = "127.0.0.1:7331"
	r9.RemoteAddr = "127.0.0.1:51000"
	r9.Header.Set("Origin", "http://127.0.0.1:7331")
	r9.Header.Set("Content-Type", "application/json")
	r9.Header.Set("X-Fased-Pair", "1")
	w9 := httptest.NewRecorder()
	sshH.ServeHTTP(w9, r9)
	if w9.Code != http.StatusTooManyRequests {
		t.Fatalf("9th attempt was not rate-limited: %d", w9.Code)
	}
}

func TestConfigureConnectionWithTransportModes(t *testing.T) {
	t.Parallel()
	dir := fixtureDir(t)
	srv, err := Start(t.Context(), dir, "127.0.0.1:0", "https://fased.example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close(context.Background())

	if err = srv.ConfigureConnectionWithTransport("remote", "tailscale"); err == nil {
		t.Fatal("non-.ts.net origin accepted for tailscale transport")
	}
	if err = srv.ConfigureConnectionWithTransport("remote", "ssh"); err == nil {
		t.Fatal("--public-origin accepted with ssh transport")
	}
	if err = srv.ConfigureConnectionWithTransport("remote", "cloudflare"); err != nil {
		t.Fatalf("cloudflare transport rejected: %v", err)
	}
	data, err := readPrivateFile(filepath.Join(dir, "endpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ep Endpoint
	if err = json.Unmarshal(data, &ep); err != nil {
		t.Fatal(err)
	}
	if ep.PublicOrigin != "https://fased.example.com" || ep.RemoteTransport != "cloudflare" {
		t.Fatalf("endpoint.json missing remote transport metadata: %+v", ep)
	}

	sshDir := fixtureDir(t)
	sshSrv, err := Start(t.Context(), sshDir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sshSrv.Close(context.Background())
	if err = sshSrv.ConfigureConnectionWithTransport("remote", "cloudflare"); err == nil {
		t.Fatal("cloudflare accepted without public-origin")
	}
	if err = sshSrv.ConfigureConnectionWithTransport("remote", "ssh"); err != nil {
		t.Fatalf("ssh transport failed: %v", err)
	}
}
