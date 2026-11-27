package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestNativePublicWalletRoutesMatchCoreMethods(t *testing.T) {
	if !PublicRequestAllowed(httptest.NewRequest(http.MethodGet, "http://wails.localhost/api/v1/accounts/portable", nil)) ||
		!PublicRequestAllowed(httptest.NewRequest(http.MethodPut, "http://wails.localhost/api/v1/accounts/portable", nil)) {
		t.Error("portable wallet backup routes denied")
	}
	if PublicRequestAllowed(httptest.NewRequest(http.MethodPost, "http://wails.localhost/api/v1/accounts/portable", nil)) ||
		PublicRequestAllowed(httptest.NewRequest(http.MethodGet, "http://wails.localhost/api/v1/accounts/portable?override=true", nil)) {
		t.Error("unsupported portable wallet method/query admitted")
	}
	for _, row := range [][2]string{
		{http.MethodPut, "/api/v1/accounts/setup"},
		{http.MethodPut, "/api/v1/accounts/rpc"},
		{http.MethodPut, "/api/v1/accounts/rpc-credential"},
		{http.MethodPut, "/api/v1/accounts/venue-credential"},
		{http.MethodPost, "/api/v1/accounts/rpc-check"},
		{http.MethodPost, "/api/v1/accounts/read"},
	} {
		if !PublicRequestAllowed(httptest.NewRequest(row[0], "http://wails.localhost"+row[1], nil)) {
			t.Error("public Wallets operation denied", row)
		}
		if PublicRequestAllowed(httptest.NewRequest(http.MethodGet, "http://wails.localhost"+row[1], nil)) || PublicRequestAllowed(httptest.NewRequest(row[0], "http://wails.localhost"+row[1]+"?override=true", nil)) {
			t.Error("unsupported Wallets method/query admitted", row)
		}
	}
}

func TestNativeWalletResponseFitsBoundedCoreProjection(t *testing.T) {
	key, amount := strings.Repeat("1", 44), "18446744073709551615"
	v := core.WalletSnapshot{Address: key, Network: "devnet", Lamports: amount, Transaction: &core.WalletTransaction{Payer: key}}
	for i := 0; i < 128; i++ {
		v.Tokens = append(v.Tokens, core.WalletTokenBalance{Program: key, Mint: key, Amount: amount, Decimals: 255, Slot: amount})
	}
	for i := 0; i < 20; i++ {
		v.Activity = append(v.Activity, core.WalletActivity{Signature: strings.Repeat("1", 88), Slot: amount})
	}
	for i := 0; i < 256; i++ {
		v.Transaction.Tokens = append(v.Transaction.Tokens, core.WalletTokenChange{Account: key, Program: key, Mint: key, Decimals: 255, Before: amount, After: amount, Change: "-" + amount})
	}
	b, err := json.Marshal(v)
	if err != nil || len(b) <= 32*1024 || int64(len(b)) > responseLimit("/api/v1/accounts/read") {
		t.Fatal("valid bounded wallet output would be truncated", len(b), err)
	}
	if responseLimit("/api/v1/accounts/read") != core.WalletReadResponseLimit || responseLimit("/api/v1/accounts") != 32*1024 {
		t.Fatal("response allowance expanded beyond the affected read route")
	}
}

func TestNativeTransportDoesNotSupplyAuthority(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "fd-")
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
	if got := request("GET", "/api/v1/updates/application", "", "", "", ""); got.Code != 401 {
		t.Fatal("native update status silently inherited a session")
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
	// The native OS-owner bridge renews expired setup proof internally. Network
	// clients have no equivalent endpoint and never receive this proof.
	if err = core.WritePrivateJSON(filepath.Join(dir, "claim-code.json"), core.ClaimCode{Code: "expired", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	setupLock, err := core.LockOwnerSetup(dir)
	if err != nil {
		t.Fatal(err)
	}
	if busy := request("GET", "/api/v1/installation/claim-code", "", "", "", ""); busy.Code != 409 {
		setupLock.Close()
		t.Fatal("native claim fallback wrote during enrollment")
	}
	if err = setupLock.Close(); err != nil {
		t.Fatal(err)
	}
	renewed := request("GET", "/api/v1/installation/claim-code", "", "", "", "")
	var setup core.ClaimCode
	if renewed.Code != 200 || json.Unmarshal(renewed.Body.Bytes(), &setup) != nil || len(setup.Code) != 43 || !setup.ExpiresAt.After(time.Now()) {
		t.Fatal("native setup did not renew")
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

	for _, path := range []string{"/api/v1/ai/connection", "/api/v1/agents?profile=news-researcher", "/api/v1/agents?profile=trading-analyst&memory-search=retained", "/api/v1/accounts", "/api/v1/updates/status", "/api/v1/updates/application"} {
		if got := request("GET", path, "", "", session.Token, ""); got.Code != 200 {
			t.Fatalf("native flow %s: %d %s", path, got.Code, got.Body)
		}
	}
	if got := request("POST", "/api/v1/updates/application", `{"action":"check"}`, "http://wails.localhost", session.Token, ""); got.Code != 403 {
		t.Fatal("native update CSRF bypass", got.Code)
	}
	if got := request("POST", "/api/v1/updates/application/owner", `{}`, "http://wails.localhost", session.Token, session.CSRF); got.Code != 404 {
		t.Fatal("native update owner endpoint exposed", got.Code)
	}
	if got := request("POST", "/api/v1/updates/desktop-release", `{}`, "http://wails.localhost", session.Token, session.CSRF); got.Code != 404 {
		t.Fatal("desktop-release POST should be refused by desktop allowlist", got.Code)
	}
	getDesktopReq := httptest.NewRequest("GET", "http://wails.localhost/api/v1/updates/desktop-release", nil)
	if !PublicRequestAllowed(getDesktopReq) {
		t.Fatal("GET /api/v1/updates/desktop-release should be allowed by desktop proxy")
	}
	if PublicRequestAllowed(httptest.NewRequest("GET", "http://wails.localhost/api/v1/updates/desktop-release?foo=1", nil)) {
		t.Fatal("GET /api/v1/updates/desktop-release with query should be refused by desktop proxy")
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

func TestNativeAgentV2MCPAndAuthRoutesAllowlist(t *testing.T) {
	t.Parallel()
	allowedCases := [][2]string{
		{http.MethodGet, "/api/v1/agents/v2/tools"},
		{http.MethodGet, "/api/v1/agents/v2/usage?profileId=news-researcher&days=7"},
		{http.MethodGet, "/api/v1/agents/v2/task-threads?profileId=news-researcher"},
		{http.MethodGet, "/api/v1/agents/v2/model-manifest"},
		{http.MethodPut, "/api/v1/agents/v2/model-manifest"},
		{http.MethodDelete, "/api/v1/agents/v2/model-manifest"},
		{http.MethodGet, "/api/v1/agents/v2/threads?profileId=news-researcher&archived=1"},
		{http.MethodPost, "/api/v1/agents/v2/threads"},
		{http.MethodGet, "/api/v1/agents/v2/threads/thr_123-abc"},
		{http.MethodPatch, "/api/v1/agents/v2/threads/thr_123-abc"},
		{http.MethodPut, "/api/v1/agents/v2/threads/thr_123-abc"},
		{http.MethodDelete, "/api/v1/agents/v2/threads/thr_123-abc"},
		{http.MethodPost, "/api/v1/agents/v2/threads/thr_123-abc/messages"},
		{http.MethodPost, "/api/v1/agents/v2/threads/thr_123-abc/messages?stream=1"},
		{http.MethodPost, "/api/v1/agents/v2/threads/thr_123-abc/compact"},
		{http.MethodPost, "/api/v1/agents/v2/threads/thr_123-abc/side?stream=0"},
		{http.MethodPost, "/api/v1/agents/v2/approvals/apr_123-abc"},
		{http.MethodGet, "/api/v1/agents/v2/memories?profileId=news-researcher&q=test&scope=chat&limit=20&excludeDrafts=1"},
		{http.MethodPost, "/api/v1/agents/v2/memories"},
		{http.MethodPatch, "/api/v1/agents/v2/memories/mem_123-abc"},
		{http.MethodDelete, "/api/v1/agents/v2/memories/mem_123-abc"},
		{http.MethodGet, "/api/v1/mcp"},
		{http.MethodPost, "/api/v1/mcp"},
		{http.MethodGet, "/api/v1/auth/devices"},
		{http.MethodPost, "/api/v1/auth/devices"},
		{http.MethodPost, "/api/v1/auth/pair"},
		{http.MethodGet, "/api/v1/auth/pair/devices"},
		{http.MethodPost, "/api/v1/auth/pair/devices"},
		{http.MethodGet, "/api/v1/trade/state"},
		{http.MethodPost, "/api/v1/trade/orders"},
		{http.MethodPost, "/api/v1/trade/orders/ord_123-abc/cancel"},
		{http.MethodPost, "/api/v1/trade/evaluate"},
		{http.MethodPut, "/api/v1/trade/policy"},
		{http.MethodPost, "/api/v1/trade/kill"},
	}
	for _, tc := range allowedCases {
		req := httptest.NewRequest(tc[0], "http://wails.localhost"+tc[1], nil)
		if !PublicRequestAllowed(req) {
			t.Errorf("expected allowed: %s %s", tc[0], tc[1])
		}
	}

	deniedCases := [][2]string{
		{http.MethodPost, "/api/v1/auth/pair/issue"},
		{http.MethodGet, "/api/v1/auth/pair/manage"},
		{http.MethodPost, "/api/v1/auth/pair/manage"},
		{http.MethodGet, "/api/v1/auth/pair"},
		{http.MethodDelete, "/api/v1/mcp"},
		{http.MethodGet, "/api/v1/mcp?foo=1"},
		{http.MethodGet, "/api/v1/agents/v2/tools?foo=1"},
		{http.MethodPost, "/api/v1/agents/v2/threads/../mcp"},
		{http.MethodGet, "/api/v1/agents/v2/threads/.."},
		{http.MethodPost, "/api/v1/agents/v2/threads/thr_1/messages?stream=2"},
		{http.MethodPost, "/api/v1/agents/v2/threads/thr_1/messages?extra=1"},
		{http.MethodGet, "/api/v1/agents/v2/threads/thr_1/messages"},
		{http.MethodGet, "/api/v1/agents/v2/memories/mem_1"},
		{http.MethodPost, "/api/v1/agents/v2/memories/mem_1"},
		{http.MethodGet, "/api/v1/agents/v2/unknown"},
		{http.MethodGet, "/api/v1/agents/v2/usage?bad=1"},
		{http.MethodPost, "/api/v1/trade/state"},
		{http.MethodDelete, "/api/v1/trade/orders/ord_1"},
		{http.MethodPost, "/api/v1/trade/orders/../kill/cancel"},
	}
	for _, tc := range deniedCases {
		req := httptest.NewRequest(tc[0], "http://wails.localhost"+tc[1], nil)
		if PublicRequestAllowed(req) {
			t.Errorf("expected denied: %s %s", tc[0], tc[1])
		}
	}
}

func TestNativeDesktopAgentV2SSEMemoryAndMCP(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	server, err := core.Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve() }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := server.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	h := NewHandler(dir, fstest.MapFS{"index.html": {Data: []byte("fixture UI")}})
	claim, err := server.Store.IssueClaimCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	req := func(method, path, body, token, csrf, accept string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://wails.localhost"+path, strings.NewReader(body))
		r.Header.Set("Origin", "http://wails.localhost")
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		if accept != "" {
			r.Header.Set("Accept", accept)
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
	claimed := req("POST", "/api/v1/auth/claim", `{"code":"`+claim.Code+`","password":"fixture-native-password"}`, "", "", "")
	if claimed.Code != 200 {
		t.Fatalf("claim failed: %d %s", claimed.Code, claimed.Body)
	}
	var sess struct {
		Token string `json:"sessionToken"`
		CSRF  string `json:"csrfToken"`
	}
	if err := json.Unmarshal(claimed.Body.Bytes(), &sess); err != nil {
		t.Fatal(err)
	}

	// Agent V2 thread create -> list -> patch -> delete
	created := req("POST", "/api/v1/agents/v2/threads", `{"profileId":"news-researcher","title":"Desktop thread"}`, sess.Token, sess.CSRF, "")
	if created.Code != 201 {
		t.Fatalf("create thread: %d %s", created.Code, created.Body)
	}
	var threadResp struct {
		Thread struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &threadResp); err != nil || threadResp.Thread.ID == "" {
		t.Fatalf("bad thread payload: %s", created.Body)
	}
	tid := threadResp.Thread.ID

	if got := req("PATCH", "/api/v1/agents/v2/threads/"+tid, `{"title":"Renamed desktop thread"}`, sess.Token, sess.CSRF, ""); got.Code != 200 || !strings.Contains(got.Body.String(), "Renamed desktop thread") {
		t.Fatalf("patch thread: %d %s", got.Code, got.Body)
	}
	if got := req("GET", "/api/v1/agents/v2/threads?profileId=news-researcher", "", sess.Token, "", ""); got.Code != 200 || !strings.Contains(got.Body.String(), tid) {
		t.Fatalf("list threads: %d %s", got.Code, got.Body)
	}

	// Large (>16 KiB) Agent V2 body succeeds through the route-scoped 256 KiB cap.
	bigContent := strings.Repeat("a", 18*1024)
	if got := req("POST", "/api/v1/agents/v2/memories", `{"profileId":"news-researcher","scope":"chat","title":"Big","content":"`+bigContent+`"}`, sess.Token, sess.CSRF, ""); got.Code == 413 {
		t.Fatalf("expected >16 KiB Agent V2 body not to hit desktop 16 KiB limit: %d %s", got.Code, got.Body)
	}

	// Agent V2 memory CRUD
	memCreated := req("POST", "/api/v1/agents/v2/memories", `{"profileId":"news-researcher","scope":"chat","title":"Rule","content":"Always verify","pinned":true}`, sess.Token, sess.CSRF, "")
	if memCreated.Code != 201 {
		t.Fatalf("create memory: %d %s", memCreated.Code, memCreated.Body)
	}
	var memResp struct {
		Memory struct {
			ID string `json:"id"`
		} `json:"memory"`
	}
	if err := json.Unmarshal(memCreated.Body.Bytes(), &memResp); err != nil || memResp.Memory.ID == "" {
		t.Fatalf("bad memory payload: %s", memCreated.Body)
	}
	if got := req("PATCH", "/api/v1/agents/v2/memories/"+memResp.Memory.ID, `{"content":"Always verify twice"}`, sess.Token, sess.CSRF, ""); got.Code != 200 {
		t.Fatalf("patch memory: %d %s", got.Code, got.Body)
	}
	if got := req("DELETE", "/api/v1/agents/v2/memories/"+memResp.Memory.ID, "", sess.Token, sess.CSRF, ""); got.Code != 200 {
		t.Fatalf("delete memory: %d %s", got.Code, got.Body)
	}

	// MCP GET, JSON-RPC ping, and 202 notifications/initialized
	if got := req("GET", "/api/v1/mcp", "", sess.Token, "", ""); got.Code != 200 || !strings.Contains(got.Body.String(), "fased-mcp") {
		t.Fatalf("GET /api/v1/mcp: %d %s", got.Code, got.Body)
	}
	if got := req("POST", "/api/v1/mcp", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, sess.Token, sess.CSRF, ""); got.Code != 200 || !strings.Contains(got.Body.String(), `"result":{}`) {
		t.Fatalf("POST /api/v1/mcp ping: %d %s", got.Code, got.Body)
	}
	if got := req("POST", "/api/v1/mcp", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, sess.Token, sess.CSRF, ""); got.Code != http.StatusAccepted {
		t.Fatalf("POST /api/v1/mcp notifications/initialized: got %d want 202 (%s)", got.Code, got.Body)
	}

	// Real SSE streaming passthrough via a stubbed core endpoint to verify incremental flushing and non-SSE rejection.
	sseServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agents/v2/threads/thr_sse/messages":
			if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
				t.Errorf("Accept header not forwarded: %q", r.Header.Get("Accept"))
			}
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)
			_, _ = w.Write([]byte("event: delta\ndata: {\"delta\":\"hello \"}\n\n"))
			flusher.Flush()
			_, _ = w.Write([]byte("event: done\ndata: {\"delta\":\"world\"}\n\n"))
			flusher.Flush()
		case "/api/v1/agents/v2/tools":
			// Non-streaming route returning text/event-stream must be refused with 502.
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: spoof\ndata: {}\n\n"))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer sseServer.Close()
	sseHandler := NewHandler("fixture-sse", nil)
	sseHandler.newClient = func(context.Context, string) (*core.Client, bool, json.RawMessage, error) {
		// Deliberately attach a short client Timeout to prove streaming bypasses it.
		c := sseServer.Client()
		c.Timeout = 50 * time.Millisecond
		return &core.Client{Endpoint: core.Endpoint{URL: sseServer.URL, InstanceID: "fixture-sse-id"}, HTTP: c}, false, nil, nil
	}
	sseReq := httptest.NewRequest(http.MethodPost, "http://wails.localhost/api/v1/agents/v2/threads/thr_sse/messages", strings.NewReader(`{"content":"hi"}`))
	sseReq.Header.Set("Origin", "http://wails.localhost")
	sseReq.Header.Set("Content-Type", "application/json")
	sseReq.Header.Set("Accept", "text/event-stream")
	sseRec := httptest.NewRecorder()
	sseHandler.ServeHTTP(sseRec, sseReq)
	if sseRec.Code != http.StatusOK || !strings.HasPrefix(sseRec.Header().Get("Content-Type"), "text/event-stream") || !strings.Contains(sseRec.Body.String(), `"delta":"hello "`) || !strings.Contains(sseRec.Body.String(), `"delta":"world"`) || !sseRec.Flushed {
		t.Fatalf("SSE passthrough failed: code=%d ct=%q flushed=%v body=%q", sseRec.Code, sseRec.Header().Get("Content-Type"), sseRec.Flushed, sseRec.Body.String())
	}
	badSSEReq := httptest.NewRequest(http.MethodGet, "http://wails.localhost/api/v1/agents/v2/tools", nil)
	badSSERec := httptest.NewRecorder()
	sseHandler.ServeHTTP(badSSERec, badSSEReq)
	if badSSERec.Code != http.StatusBadGateway {
		t.Fatalf("non-streaming route accepted text/event-stream: code=%d", badSSERec.Code)
	}

	if got := req("DELETE", "/api/v1/agents/v2/threads/"+tid, "", sess.Token, sess.CSRF, ""); got.Code != 200 {
		t.Fatalf("delete thread: %d %s", got.Code, got.Body)
	}
}

func TestNativeMarketDataRoutesAndSSE(t *testing.T) {
	for _, tc := range []struct {
		method string
		url    string
		want   bool
	}{
		{http.MethodGet, "http://wails.localhost/api/v1/market/instruments?venue=hyperliquid", true},
		{http.MethodGet, "http://wails.localhost/api/v1/market/instruments?evil=1", false},
		{http.MethodGet, "http://wails.localhost/api/v1/market/candles?venue=hyperliquid&symbol=BTC&interval=1h&style=renko&limit=50", true},
		{http.MethodGet, "http://wails.localhost/api/v1/market/candles?badParam=1", false},
		{http.MethodGet, "http://wails.localhost/api/v1/market/orderbook?venue=binance&symbol=BTCUSDT&limit=10", true},
		{http.MethodGet, "http://wails.localhost/api/v1/market/trades?venue=hyperliquid&symbol=SOL&limit=20", true},
		{http.MethodGet, "http://wails.localhost/api/v1/market/footprint?venue=hyperliquid&symbol=BTC&interval=15m&stepSize=5&limit=10", true},
		{http.MethodGet, "http://wails.localhost/api/v1/market/macro?seriesId=CPIAUCSL&limit=20", true},
		{http.MethodGet, "http://wails.localhost/api/v1/market/sec?cik=0000320193&limit=10", true},
		{http.MethodGet, "http://wails.localhost/api/v1/market/cohorts?window=week", true},
		{http.MethodGet, "http://wails.localhost/api/v1/market/watchlists", true},
		{http.MethodPut, "http://wails.localhost/api/v1/market/watchlists", true},
		{http.MethodPost, "http://wails.localhost/api/v1/market/watchlists", false},
		{http.MethodDelete, "http://wails.localhost/api/v1/market/watchlists/core-desk", true},
		{http.MethodDelete, "http://wails.localhost/api/v1/market/watchlists/../bad", false},
		{http.MethodGet, "http://wails.localhost/api/v1/market/drawings?venue=hyperliquid&symbol=BTC&interval=1h", true},
		{http.MethodGet, "http://wails.localhost/api/v1/market/drawings?evil=1", false},
		{http.MethodPut, "http://wails.localhost/api/v1/market/drawings", true},
		{http.MethodPost, "http://wails.localhost/api/v1/market/drawings", false},
		{http.MethodDelete, "http://wails.localhost/api/v1/market/drawings/draw-1", true},
		{http.MethodDelete, "http://wails.localhost/api/v1/market/drawings/../bad", false},
		{http.MethodGet, "http://wails.localhost/api/v1/market/custom-feeds", true},
		{http.MethodPost, "http://wails.localhost/api/v1/market/ingest", true},
		{http.MethodGet, "http://wails.localhost/api/v1/market/ingest", false},
		{http.MethodPost, "http://wails.localhost/api/v1/automation/webhook/test", true},
		{http.MethodGet, "http://wails.localhost/api/v1/automation/webhook/test", false},
		{http.MethodGet, "http://wails.localhost/api/v1/market/stream?venue=hyperliquid&symbol=BTC&interval=1h&style=standard", true},
		{http.MethodGet, "http://wails.localhost/api/v1/market/stream?noSnapshot=1&subs=hyperliquid:BTC:1h:standard,kraken:ETHUSD:1h:standard", true},
		{http.MethodPost, "http://wails.localhost/api/v1/market/stream", false},
	} {
		req := httptest.NewRequest(tc.method, tc.url, nil)
		if got := PublicRequestAllowed(req); got != tc.want {
			t.Fatalf("PublicRequestAllowed(%s %s) = %v, want %v", tc.method, tc.url, got, tc.want)
		}
	}

	if !StreamingRoute("/api/v1/market/stream") {
		t.Fatal("expected StreamingRoute(/api/v1/market/stream) to return true")
	}
	if RequestBodyLimit("/api/v1/market/watchlists") != 64*1024 {
		t.Fatal("expected /api/v1/market/watchlists RequestBodyLimit = 64 KiB")
	}
	if RequestBodyLimit("/api/v1/market/ingest") != 512*1024 {
		t.Fatal("expected /api/v1/market/ingest RequestBodyLimit = 512 KiB")
	}
	if responseLimit("/api/v1/market/candles") != 1*1024*1024 {
		t.Fatal("expected /api/v1/market/candles responseLimit = 1 MiB")
	}

	sseServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/market/stream" {
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)
			_, _ = w.Write([]byte("event: snapshot\ndata: {\"symbol\":\"BTC\"}\n\n"))
			flusher.Flush()
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer sseServer.Close()

	h := NewHandler("fixture-market-sse", nil)
	h.newClient = func(context.Context, string) (*core.Client, bool, json.RawMessage, error) {
		c := sseServer.Client()
		c.Timeout = 50 * time.Millisecond
		return &core.Client{Endpoint: core.Endpoint{URL: sseServer.URL, InstanceID: "fixture-market-sse"}, HTTP: c}, false, nil, nil
	}
	req := httptest.NewRequest(http.MethodGet, "http://wails.localhost/api/v1/market/stream?venue=hyperliquid&symbol=BTC&interval=1h", nil)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"symbol":"BTC"`) || !rec.Flushed {
		t.Fatalf("desktop market SSE passthrough failed: code=%d flushed=%v body=%q", rec.Code, rec.Flushed, rec.Body.String())
	}
}

func TestNativeDecisionAndExternalMCPRoutes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		method string
		url    string
		want   bool
	}{
		{http.MethodGet, "http://wails.localhost/api/v1/decision/config", true},
		{http.MethodPut, "http://wails.localhost/api/v1/decision/config", true},
		{http.MethodPost, "http://wails.localhost/api/v1/decision/evaluate", true},
		{http.MethodGet, "http://wails.localhost/api/v1/decision/config?bad=1", false},
		{http.MethodGet, "http://wails.localhost/api/v1/mcp/clients", true},
		{http.MethodPut, "http://wails.localhost/api/v1/mcp/clients", true},
		{http.MethodPost, "http://wails.localhost/api/v1/mcp/clients/alpha/sync", true},
		{http.MethodPost, "http://wails.localhost/api/v1/mcp/clients/alpha/call", true},
		{http.MethodDelete, "http://wails.localhost/api/v1/mcp/clients/alpha", true},
		{http.MethodDelete, "http://wails.localhost/api/v1/mcp/clients/alpha/sync", false},
		{http.MethodPost, "http://wails.localhost/api/v1/mcp/clients/bad/path/sync", false},
		{http.MethodGet, "http://wails.localhost/api/v1/mcp/clients/bad/route", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.url, nil)
		if got := PublicRequestAllowed(req); got != tc.want {
			t.Fatalf("PublicRequestAllowed(%s %s) = %v, want %v", tc.method, tc.url, got, tc.want)
		}
	}
	if RequestBodyLimit("/api/v1/decision/evaluate") != 64*1024 || RequestBodyLimit("/api/v1/mcp/clients/alpha/call") != 64*1024 {
		t.Fatal("expected 64 KiB request limit on decision and external MCP client routes")
	}
	if responseLimit("/api/v1/decision/evaluate") != 1*1024*1024 || responseLimit("/api/v1/mcp/clients/alpha/call") != 1*1024*1024 {
		t.Fatal("expected 1 MiB response limit on decision and external MCP client routes")
	}
}

func TestNativeWorldIntelRoutesAndSSE(t *testing.T) {
	t.Parallel()
	cases := []struct {
		method string
		url    string
		want   bool
	}{
		{http.MethodGet, "http://wails.localhost/api/v1/intel/events?category=macro&region=na&ticker=BTC&minUrgency=0.5&limit=20", true},
		{http.MethodGet, "http://wails.localhost/api/v1/intel/events?evil=1", false},
		{http.MethodGet, "http://wails.localhost/api/v1/intel/map?hours=48", true},
		{http.MethodGet, "http://wails.localhost/api/v1/intel/predictions?venue=polymarket&limit=20", true},
		{http.MethodGet, "http://wails.localhost/api/v1/intel/sources", true},
		{http.MethodPut, "http://wails.localhost/api/v1/intel/sources", true},
		{http.MethodDelete, "http://wails.localhost/api/v1/intel/sources/custom-1", true},
		{http.MethodDelete, "http://wails.localhost/api/v1/intel/sources/bad/path", false},
		{http.MethodPost, "http://wails.localhost/api/v1/intel/sync", true},
		{http.MethodGet, "http://wails.localhost/api/v1/intel/stream?region=global", true},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.url, nil)
		if got := PublicRequestAllowed(req); got != tc.want {
			t.Fatalf("PublicRequestAllowed(%s %s) = %v, want %v", tc.method, tc.url, got, tc.want)
		}
	}
	if !StreamingRoute("/api/v1/intel/stream") {
		t.Fatal("expected StreamingRoute(/api/v1/intel/stream) to return true")
	}
	if RequestBodyLimit("/api/v1/intel/sources") != 64*1024 || responseLimit("/api/v1/intel/events") != 1*1024*1024 {
		t.Fatal("expected 64 KiB request limit and 1 MiB response limit on intel routes")
	}

	sseServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/intel/stream" {
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)
			_, _ = w.Write([]byte("event: snapshot\ndata: {\"events\":[]}\n\n"))
			flusher.Flush()
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer sseServer.Close()

	h := NewHandler("fixture-intel-sse", nil)
	h.newClient = func(context.Context, string) (*core.Client, bool, json.RawMessage, error) {
		c := sseServer.Client()
		c.Timeout = 50 * time.Millisecond
		return &core.Client{Endpoint: core.Endpoint{URL: sseServer.URL, InstanceID: "fixture-intel-sse"}, HTTP: c}, false, nil, nil
	}
	req := httptest.NewRequest(http.MethodGet, "http://wails.localhost/api/v1/intel/stream?region=global", nil)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"events":[]`) || !rec.Flushed {
		t.Fatalf("desktop intel SSE passthrough failed: code=%d flushed=%v body=%q", rec.Code, rec.Flushed, rec.Body.String())
	}
}

func TestNativeAutomationRoutesAndSSE(t *testing.T) {
	t.Parallel()
	cases := []struct {
		method string
		url    string
		want   bool
	}{
		{http.MethodGet, "http://wails.localhost/api/v1/automation/state", true},
		{http.MethodPost, "http://wails.localhost/api/v1/automation/state", true},
		{http.MethodGet, "http://wails.localhost/api/v1/automation/triggers", true},
		{http.MethodPut, "http://wails.localhost/api/v1/automation/triggers", true},
		{http.MethodPost, "http://wails.localhost/api/v1/automation/triggers/btc-price/test", true},
		{http.MethodDelete, "http://wails.localhost/api/v1/automation/triggers/btc-price", true},
		{http.MethodDelete, "http://wails.localhost/api/v1/automation/triggers/bad/path", false},
		{http.MethodPost, "http://wails.localhost/api/v1/automation/evaluate", true},
		{http.MethodGet, "http://wails.localhost/api/v1/automation/notifications?limit=25&unreadOnly=1&severity=warn", true},
		{http.MethodGet, "http://wails.localhost/api/v1/automation/notifications?evil=1", false},
		{http.MethodPost, "http://wails.localhost/api/v1/automation/notifications/read", true},
		{http.MethodGet, "http://wails.localhost/api/v1/automation/webhook", true},
		{http.MethodPut, "http://wails.localhost/api/v1/automation/webhook", true},
		{http.MethodGet, "http://wails.localhost/api/v1/automation/telegram", true},
		{http.MethodPut, "http://wails.localhost/api/v1/automation/telegram", true},
		{http.MethodPost, "http://wails.localhost/api/v1/automation/telegram/test", true},
		{http.MethodPost, "http://wails.localhost/api/v1/automation/telegram/simulate", true},
		{http.MethodGet, "http://wails.localhost/api/v1/automation/stream", true},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.url, nil)
		if got := PublicRequestAllowed(req); got != tc.want {
			t.Fatalf("PublicRequestAllowed(%s %s) = %v, want %v", tc.method, tc.url, got, tc.want)
		}
	}
	if !StreamingRoute("/api/v1/automation/stream") {
		t.Fatal("expected StreamingRoute(/api/v1/automation/stream) to return true")
	}
	if RequestBodyLimit("/api/v1/automation/telegram") != 64*1024 || responseLimit("/api/v1/automation/notifications") != 1*1024*1024 {
		t.Fatal("expected 64 KiB request limit and 1 MiB response limit on automation routes")
	}

	sseServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/automation/stream" {
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)
			_, _ = w.Write([]byte("event: ready\ndata: {\"unreadCount\":0}\n\n"))
			flusher.Flush()
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer sseServer.Close()

	h := NewHandler("fixture-auto-sse", nil)
	h.newClient = func(context.Context, string) (*core.Client, bool, json.RawMessage, error) {
		c := sseServer.Client()
		c.Timeout = 50 * time.Millisecond
		return &core.Client{Endpoint: core.Endpoint{URL: sseServer.URL, InstanceID: "fixture-auto-sse"}, HTTP: c}, false, nil, nil
	}
	req := httptest.NewRequest(http.MethodGet, "http://wails.localhost/api/v1/automation/stream", nil)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"unreadCount":0`) || !rec.Flushed {
		t.Fatalf("desktop automation SSE passthrough failed: code=%d flushed=%v body=%q", rec.Code, rec.Flushed, rec.Body.String())
	}
}

func TestNativeQuantLabRoutes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		method string
		url    string
		want   bool
	}{
		{http.MethodGet, "http://wails.localhost/api/v1/lab/scripts", true},
		{http.MethodPut, "http://wails.localhost/api/v1/lab/scripts", true},
		{http.MethodDelete, "http://wails.localhost/api/v1/lab/scripts/custom-1", true},
		{http.MethodDelete, "http://wails.localhost/api/v1/lab/scripts/bad/path", false},
		{http.MethodPost, "http://wails.localhost/api/v1/lab/evaluate", true},
		{http.MethodPost, "http://wails.localhost/api/v1/lab/backtest", true},
		{http.MethodPost, "http://wails.localhost/api/v1/lab/validate", true},
		{http.MethodGet, "http://wails.localhost/api/v1/lab/runs?limit=25", true},
		{http.MethodGet, "http://wails.localhost/api/v1/lab/runs?evil=1", false},
		{http.MethodGet, "http://wails.localhost/api/v1/lab/runs/run-123", true},
		{http.MethodPost, "http://wails.localhost/api/v1/lab/runs/run-123", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.url, nil)
		if got := PublicRequestAllowed(req); got != tc.want {
			t.Fatalf("PublicRequestAllowed(%s %s) = %v, want %v", tc.method, tc.url, got, tc.want)
		}
	}
	if RequestBodyLimit("/api/v1/lab/scripts") != 64*1024 || responseLimit("/api/v1/lab/validate") != 2*1024*1024 {
		t.Fatal("expected 64 KiB request limit and 2 MiB response limit on /api/v1/lab routes")
	}
}

func TestNativeDeskRoutes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		method string
		url    string
		want   bool
	}{
		{http.MethodGet, "http://wails.localhost/api/v1/desk/state", true},
		{http.MethodPut, "http://wails.localhost/api/v1/desk/limits", true},
		{http.MethodPost, "http://wails.localhost/api/v1/desk/limits", false},
		{http.MethodPost, "http://wails.localhost/api/v1/desk/control", true},
		{http.MethodPost, "http://wails.localhost/api/v1/desk/cycle", true},
		{http.MethodGet, "http://wails.localhost/api/v1/desk/cycles?limit=25", true},
		{http.MethodGet, "http://wails.localhost/api/v1/desk/cycles?evil=1", false},
		{http.MethodGet, "http://wails.localhost/api/v1/desk/tickets?status=PENDING_PM&limit=20", true},
		{http.MethodPost, "http://wails.localhost/api/v1/desk/tickets/tkt-1/approve", true},
		{http.MethodPost, "http://wails.localhost/api/v1/desk/tickets/tkt-1/reject", true},
		{http.MethodPost, "http://wails.localhost/api/v1/desk/tickets/bad/path/approve", false},
		{http.MethodGet, "http://wails.localhost/api/v1/desk/positions?status=OPEN", true},
		{http.MethodPost, "http://wails.localhost/api/v1/desk/positions/mark", true},
		{http.MethodPost, "http://wails.localhost/api/v1/desk/positions/pos-1/close", true},
		{http.MethodGet, "http://wails.localhost/api/v1/desk/journal?symbol=BTC&outcome=SHADOW&limit=50", true},
		{http.MethodGet, "http://wails.localhost/api/v1/desk/journal?bad=1", false},
		{http.MethodGet, "http://wails.localhost/api/v1/desk/lessons?limit=20", true},
		{http.MethodPut, "http://wails.localhost/api/v1/desk/lessons", true},
		{http.MethodDelete, "http://wails.localhost/api/v1/desk/lessons/lesson-1", true},
		{http.MethodGet, "http://wails.localhost/api/v1/desk/report", true},
		{http.MethodGet, "http://wails.localhost/api/v1/desk/stream", true},
		{http.MethodGet, "http://wails.localhost/api/v1/execution/status", true},
		{http.MethodPost, "http://wails.localhost/api/v1/execution/status", false},
		{http.MethodGet, "http://wails.localhost/api/v1/vaults", true},
		{http.MethodPost, "http://wails.localhost/api/v1/execution/solana-devnet/rehearse", true},
		{http.MethodGet, "http://wails.localhost/api/v1/execution/solana-devnet/rehearse", false},
		{http.MethodGet, "http://wails.localhost/api/v1/execution/polymarket/status", true},
		{http.MethodPost, "http://wails.localhost/api/v1/execution/polymarket/status", false},
		{http.MethodPost, "http://wails.localhost/api/v1/execution/polymarket/rehearse", true},
		{http.MethodGet, "http://wails.localhost/api/v1/execution/polymarket/rehearse", false},
		{http.MethodGet, "http://wails.localhost/api/v1/execution/kalshi/status", true},
		{http.MethodPost, "http://wails.localhost/api/v1/execution/kalshi/status", false},
		{http.MethodPost, "http://wails.localhost/api/v1/execution/kalshi/rehearse", true},
		{http.MethodGet, "http://wails.localhost/api/v1/execution/kalshi/rehearse", false},
		{http.MethodGet, "http://wails.localhost/api/v1/execution/lighter/status", true},
		{http.MethodPost, "http://wails.localhost/api/v1/execution/lighter/status", false},
		{http.MethodPost, "http://wails.localhost/api/v1/execution/lighter/rehearse", true},
		{http.MethodGet, "http://wails.localhost/api/v1/execution/lighter/rehearse", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.url, nil)
		if got := PublicRequestAllowed(req); got != tc.want {
			t.Fatalf("PublicRequestAllowed(%s %s) = %v, want %v", tc.method, tc.url, got, tc.want)
		}
	}
	if !StreamingRoute("/api/v1/desk/stream") {
		t.Fatal("expected /api/v1/desk/stream to be a StreamingRoute")
	}
	if RequestBodyLimit("/api/v1/desk/cycle") != 64*1024 || responseLimit("/api/v1/desk/state") != 1*1024*1024 {
		t.Fatal("expected 64 KiB request limit and 1 MiB response limit on /api/v1/desk routes")
	}
}

func TestNativeUniversalTaskRoutes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		method string
		url    string
		want   bool
	}{
		{http.MethodGet, "http://wails.localhost/api/v1/tasks", true},
		{http.MethodGet, "http://wails.localhost/api/v1/tasks?goal=mining&status=active&q=wen&limit=25", true},
		{http.MethodGet, "http://wails.localhost/api/v1/tasks?evil=1", false},
		{http.MethodPost, "http://wails.localhost/api/v1/tasks", true},
		{http.MethodGet, "http://wails.localhost/api/v1/tasks/libraries", true},
		{http.MethodPost, "http://wails.localhost/api/v1/tasks/from-template", true},
		{http.MethodPost, "http://wails.localhost/api/v1/tasks/swarm", true},
		{http.MethodGet, "http://wails.localhost/api/v1/tasks/tsk-1", true},
		{http.MethodPatch, "http://wails.localhost/api/v1/tasks/tsk-1", true},
		{http.MethodDelete, "http://wails.localhost/api/v1/tasks/tsk-1", true},
		{http.MethodPost, "http://wails.localhost/api/v1/tasks/tsk-1/activate", true},
		{http.MethodPost, "http://wails.localhost/api/v1/tasks/tsk-1/pause", true},
		{http.MethodPost, "http://wails.localhost/api/v1/tasks/tsk-1/stop", true},
		{http.MethodPost, "http://wails.localhost/api/v1/tasks/tsk-1/run", true},
		{http.MethodGet, "http://wails.localhost/api/v1/tasks/tsk-1/runs?limit=20", true},
		{http.MethodGet, "http://wails.localhost/api/v1/tasks/tsk-1/runs?bad=1", false},
		{http.MethodGet, "http://wails.localhost/api/v1/tasks/tsk-1/logs?limit=50", true},
		{http.MethodGet, "http://wails.localhost/api/v1/tasks/bad/path/runs", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.url, nil)
		if got := PublicRequestAllowed(req); got != tc.want {
			t.Fatalf("PublicRequestAllowed(%s %s) = %v, want %v", tc.method, tc.url, got, tc.want)
		}
	}
	if RequestBodyLimit("/api/v1/tasks") != 256*1024 || responseLimit("/api/v1/tasks") != 1*1024*1024 {
		t.Fatal("expected 256 KiB request limit and 1 MiB response limit on /api/v1/tasks routes")
	}
}

func TestNativeLV1XMiningRoutes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		method string
		url    string
		want   bool
	}{
		{http.MethodGet, "http://wails.localhost/api/v1/mining/status?cluster=devnet", true},
		{http.MethodGet, "http://wails.localhost/api/v1/mining/status?evil=1", false},
		{http.MethodGet, "http://wails.localhost/api/v1/mining/round?cluster=devnet", true},
		{http.MethodGet, "http://wails.localhost/api/v1/mining/presets", true},
		{http.MethodGet, "http://wails.localhost/api/v1/mining/history?limit=24", true},
		{http.MethodPost, "http://wails.localhost/api/v1/mining/preview", true},
		{http.MethodPost, "http://wails.localhost/api/v1/mining/commit", true},
		{http.MethodPost, "http://wails.localhost/api/v1/mining/claim", true},
		{http.MethodGet, "http://wails.localhost/api/v1/mining/sessions", true},
		{http.MethodPost, "http://wails.localhost/api/v1/mining/sessions", true},
		{http.MethodPost, "http://wails.localhost/api/v1/mining/sessions/msess-1/stop", true},
		{http.MethodPost, "http://wails.localhost/api/v1/mining/sessions/msess-1/close", true},
		{http.MethodGet, "http://wails.localhost/api/v1/mining/policy?walletId=w1", true},
		{http.MethodPut, "http://wails.localhost/api/v1/mining/policy", true},
		{http.MethodDelete, "http://wails.localhost/api/v1/mining/sessions/msess-1/close", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.url, nil)
		if got := PublicRequestAllowed(req); got != tc.want {
			t.Fatalf("PublicRequestAllowed(%s %s) = %v, want %v", tc.method, tc.url, got, tc.want)
		}
	}
}
