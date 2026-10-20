package desktop

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestNativeTransportDoesNotSupplyAuthority(t *testing.T) {
	root, err := filepath.Abs("../../.evidence/desktop-fixtures")
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
	defer os.RemoveAll(dir)
	server, err := core.Start(t.Context(), dir, "127.0.0.1:0", "", nil)
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
	h := NewHandler(dir, fstest.MapFS{"index.html": {Data: []byte("fixture UI")}})
	request := func(method, path, body, origin, token, csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://wails.localhost"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
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
	if got := request("GET", "/api/v1/setup", "", "", "", ""); got.Code != 401 {
		t.Fatal("native window silently inherited a session")
	}
	for _, tc := range []struct {
		method, path, origin string
		status               int
	}{
		{"GET", "/api/v1/health", "https://evil.example", 403},
		{"GET", "/api/v1/health", "null", 403},
		{"POST", "/api/v1/auth/login", "", 403},
		{"POST", "/api/v1/signer/sign", "http://wails.localhost", 404},
		{"GET", "/api/v1/health?url=https://evil.example", "", 404},
	} {
		if got := request(tc.method, tc.path, `{}`, tc.origin, "", ""); got.Code != tc.status {
			t.Fatalf("%s: got %d", tc.path, got.Code)
		}
	}
	claim, err := server.Store.IssueClaimCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Actual WebKit/Wails requests retain the native host but omit scheme and
	// Origin. This must work without relaxing the network HTTP handler.
	claimRequest := httptest.NewRequest("POST", "http://wails/api/v1/auth/claim", strings.NewReader(`{"code":"`+claim.Code+`","password":"fixture-native-password"}`))
	claimRequest.URL.Scheme = ""
	claimRequest.Header.Set("Content-Type", "application/json")
	claimed := httptest.NewRecorder()
	h.ServeHTTP(claimed, claimRequest)
	if claimed.Code != 200 {
		t.Fatalf("claim status %d", claimed.Code)
	}
	var session struct {
		Token string `json:"sessionToken"`
		CSRF  string `json:"csrfToken"`
		Mode  string `json:"sessionMode"`
	}
	if err = json.Unmarshal(claimed.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.Mode != "bearer" || claimed.Header().Get("Set-Cookie") != "" {
		t.Fatal("incorrect native session mode")
	}
	if got := request("GET", "/api/v1/setup", "", "", session.Token, ""); got.Code != 200 {
		t.Fatal("authenticated native request failed")
	}
	for _, csrf := range []string{"", session.CSRF} {
		r := httptest.NewRequest("PUT", "wails://wails/api/v1/setup", strings.NewReader(`{"revision":0,"settings":{"step":0,"complete":false,"host":"vps","walletMode":"skip","provider":"none"}}`))
		r.Header.Set("Origin", "null")
		r.Header.Set("Authorization", "Bearer "+session.Token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 200
		if csrf == "" {
			want = 403
		}
		if w.Code != want {
			t.Fatalf("opaque native request status=%d, want=%d", w.Code, want)
		}
	}
	if got := request("POST", "/api/v1/auth/logout", `{}`, "http://wails.localhost", session.Token, ""); got.Code != 403 {
		t.Fatal("native transport bypassed CSRF")
	}
	if got := request("POST", "/api/v1/auth/logout", `{}`, "http://wails.localhost", session.Token, session.CSRF); got.Code != 200 {
		t.Fatal("logout failed")
	}
	if got := request("GET", "/api/v1/setup", "", "", session.Token, ""); got.Code != 401 {
		t.Fatal("revoked native session accepted")
	}
}
