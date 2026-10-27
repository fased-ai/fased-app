package desktop

import (
	"context"
	"encoding/json"
	"fmt"
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

	for _, path := range []string{"/api/v1/ai/connection", "/api/v1/agents?profile=news-researcher", "/api/v1/agents?profile=trading-analyst&memory-search=retained", "/api/v1/accounts", "/api/v1/updates/status"} {
		if got := request("GET", path, "", "", session.Token, ""); got.Code != 200 {
			t.Fatalf("native flow %s: %d %s", path, got.Code, got.Body)
		}
	}
	if got := request("POST", "/api/v1/ai/connection", `{"action":"lock"}`, "http://wails.localhost", session.Token, session.CSRF); got.Code != 200 {
		t.Fatalf("provider control: %d %s", got.Code, got.Body)
	}
	got := request("GET", "/api/v1/agents?profile=news-researcher", "", "", session.Token, "")
	var view core.AgentView
	if json.Unmarshal(got.Body.Bytes(), &view) != nil || view.ProfileID != "news-researcher" {
		t.Fatal("profile query dropped")
	}
	command := `{"action":"session-create","profileId":"news-researcher","name":"Native conversation","revision":` + fmt.Sprint(view.Revision) + `}`
	if got := request("POST", "/api/v1/agents", command, "http://wails.localhost", session.Token, ""); got.Code != 403 {
		t.Fatal("agent CSRF bypass")
	}
	if got := request("POST", "/api/v1/agents", command, "http://wails.localhost", session.Token, session.CSRF); got.Code != 200 {
		t.Fatalf("agent mutation: %d %s", got.Code, got.Body)
	}
	// A real retained view can exceed the old 32 KiB bridge limit.
	for i := 0; i < 10; i++ {
		current, err := server.Store.Agents(t.Context(), "news-researcher", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = server.Store.ChangeAgents(t.Context(), core.AgentCommand{Action: "memory-put", Revision: current.Revision, ProfileID: current.ProfileID, Text: strings.Repeat("n", 3900), Source: "owner"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := request("GET", "/api/v1/agents?profile=news-researcher", "", "", session.Token, ""); got.Code != 200 || got.Body.Len() <= 32*1024 {
		t.Fatal("retained view truncated", got.Code, got.Body.Len())
	}
	if got := request("POST", "/api/v1/agents", strings.Repeat(" ", 16*1024+1), "http://wails.localhost", session.Token, session.CSRF); got.Code != 413 {
		t.Fatal("request bound lost")
	}
	if got := request("POST", "/api/v1/ai/connection", `{"action":"save","key":"fased-test-disposable-native-key","passphrase":"disposable-native-passphrase"}`, "http://wails.localhost", session.Token, session.CSRF); got.Code != 200 {
		t.Fatalf("provider save: %d %s", got.Code, got.Body)
	}
	for _, path := range []string{"/api/v1/agents?profile=a&profile=b", "/api/v1/agents?url=https://evil.example", "/api/v1/ai/connection?profile=a"} {
		if got := request("GET", path, "", "", session.Token, ""); got.Code != 404 {
			t.Fatal("unbounded query accepted", path, got.Code)
		}
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
