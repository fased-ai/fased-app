package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func researchChatFixture(t *testing.T) (*Store, *HTTPHandler, string) {
	t.Helper()
	s := fixtureStore(t)
	s.now = func() time.Time { return time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC) }
	token := claimFixture(t, s)
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.URL.Query().Has("profile") {
			t.Error("private context reached producer")
		}
		fmt.Fprint(w, researchChatBody(s.now()))
	}))
	t.Cleanup(up.Close)
	if err = (&Server{HTTP: &http.Server{Handler: h}}).ConfigureWENResearch(up.URL); err != nil {
		t.Fatal(err)
	}
	return s, h, token
}

func readResearchChatReview(t *testing.T, h *HTTPHandler, token string) WENResearchChatReview {
	t.Helper()
	w := apiRequest(h, "GET", "/api/v1/wen/market-data?lookback=1&profile=wen-operator", "", token, "http://127.0.0.1:7331", "")
	var result struct {
		ChatContext *WENResearchChatReview `json:"chatContext"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.ChatContext == nil {
		t.Fatal(w.Code, w.Body.String())
	}
	return *result.ChatContext
}
func researchChatCommand(r WENResearchChatReview) AgentCommand {
	return AgentCommand{Action: "wen-research-chat", ProfileID: r.ProfileID, Revision: r.Revision, ExpiresAt: r.ExpiresAt, Text: r.Text, Source: r.Source}
}
func postResearchChat(h *HTTPHandler, token string, c AgentCommand) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(c)
	return apiRequest(h, "POST", "/api/v1/agents", string(raw), token, "http://127.0.0.1:7331", csrfToken(token))
}
func TestWENResearchChatSaveExactContextAndRestart(t *testing.T) {
	s, h, token := researchChatFixture(t)
	before, _, err := readAgents(t.Context(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	review := readResearchChatReview(t, h, token)
	h.wenResearchOrigin = "http://127.0.0.1:1" // Save must not refetch or replace the selected source.
	later := s.now().Add(time.Minute)
	s.now = func() time.Time { return later }
	c := researchChatCommand(review)
	w := postResearchChat(h, token, c)
	var view AgentView
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || len(view.Sessions) != 1 || len(view.Messages) != 1 || view.Messages[0].Text != review.Text || view.Messages[0].Role != "user" {
		t.Fatal(w.Code, w.Body.String())
	}
	message := view.Messages[0]
	parts := strings.Split(message.Text, "\nSnapshot SHA-256: ")
	if len(parts) != 2 || wenHash([]byte(parts[0])) != parts[1] || !strings.Contains(message.Text, `"sourceCursor":"42"`) || !strings.Contains(message.Text, `"btcUsdMidpoint":101.25`) || !strings.Contains(message.Text, wenHash([]byte(researchChatBody(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))))) {
		t.Fatal("snapshot identity lost")
	}
	after, _, _ := readAgents(t.Context(), s.db)
	after.Sessions, after.Messages = before.Sessions, before.Messages
	if !reflect.DeepEqual(before, after) || view.ProviderCallsEnabled || view.FinancialExecution {
		t.Fatal("capture changed execution state")
	}
	if w = postResearchChat(h, token, c); w.Code != 409 {
		t.Fatal("duplicate stale review", w.Code, w.Body.String())
	}
	c.Revision = view.Revision
	if _, err = s.ChangeAgents(t.Context(), c); err == nil {
		t.Fatal("direct store forged verified research")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	n.now = func() time.Time { return later }
	v, err := n.Agents(t.Context(), "wen-operator", "")
	if err != nil || len(v.Messages) != 1 || v.Messages[0] != message || len(v.AIRequests) != 0 || len(v.Tasks) != 0 {
		t.Fatal("restart lost frozen context", err)
	}
	other, err := n.Agents(t.Context(), "news-researcher", "")
	if err != nil || len(other.Messages) != 0 {
		t.Fatal("profile leak", err)
	}
}
func TestWENResearchChatBindingRefusals(t *testing.T) {
	s, h, token := researchChatFixture(t)
	review := readResearchChatReview(t, h, token)
	for _, tc := range []struct {
		name   string
		change func(*AgentCommand)
	}{
		{"text", func(c *AgentCommand) { c.Text += " altered" }},
		{"source", func(c *AgentCommand) { c.Source += "x" }},
		{"profile", func(c *AgentCommand) { c.ProfileID = "news-researcher" }},
		{"revision", func(c *AgentCommand) { c.Revision++ }},
		{"expiry", func(c *AgentCommand) { c.ExpiresAt++ }},
		{"task", func(c *AgentCommand) { c.BotID = "paper-bot" }},
		{"wallet", func(c *AgentCommand) { c.ConnectionRef = "private-connection" }},
		{"WEN", func(c *AgentCommand) { c.WEN = &WENPaperInput{BudgetSOL: "1"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := researchChatCommand(review)
			tc.change(&c)
			w := postResearchChat(h, token, c)
			if w.Code != 400 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	h2, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if w := postResearchChat(h2, token, researchChatCommand(review)); w.Code != 400 {
		t.Fatal("cross-handler/restart review accepted")
	}
	raw, _ := json.Marshal(researchChatCommand(review))
	for _, tc := range []struct {
		token, origin, csrf string
		code                int
	}{{"", "http://127.0.0.1:7331", "", 401}, {token, "https://foreign.invalid", csrfToken(token), 403}, {token, "http://127.0.0.1:7331", "", 403}} {
		w := apiRequest(h, "POST", "/api/v1/agents", string(raw), tc.token, tc.origin, tc.csrf)
		if w.Code != tc.code {
			t.Fatal(w.Code, tc.code)
		}
	}
	expires := time.Unix(review.ExpiresAt, 0)
	s.now = func() time.Time { return expires }
	if w := postResearchChat(h, token, researchChatCommand(review)); w.Code != 400 {
		t.Fatal("expiry boundary accepted")
	}
	v, _ := s.Agents(t.Context(), "wen-operator", "")
	if v.Revision != 0 || len(v.Messages) != 0 || len(v.Tasks) != 0 || len(v.AIRequests) != 0 {
		t.Fatal("refusal wrote state")
	}
}
func TestWENResearchChatRevisionPauseAndCapacity(t *testing.T) {
	for _, condition := range []string{"revision", "paused", "sessions", "messages"} {
		t.Run(condition, func(t *testing.T) {
			s, h, token := researchChatFixture(t)
			review := readResearchChatReview(t, h, token)
			want := 400
			switch condition {
			case "revision":
				agentApply(t, s, AgentCommand{Action: "memory-put", Text: "Independent edit", Source: "owner"})
				want = 409
			case "paused":
				if _, err := s.db.Exec("UPDATE update_pause SET phase='paused' WHERE id=1"); err != nil {
					t.Fatal(err)
				}
				want = 409
			case "sessions":
				rewriteReminderFixture(t, s, func(d *agentDocument) { d.Sessions = make([]AgentSession, 100) })
			case "messages":
				rewriteReminderFixture(t, s, func(d *agentDocument) { d.Messages = make([]AgentMessage, 200) })
			}
			if condition == "sessions" || condition == "messages" {
				review = readResearchChatReview(t, h, token)
			}
			before, _, _ := readAgents(t.Context(), s.db)
			w := postResearchChat(h, token, researchChatCommand(review))
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
			after, _, _ := readAgents(t.Context(), s.db)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("refusal was not atomic")
			}
		})
	}
}
func TestWENResearchChatMalformedAndStaleSources(t *testing.T) {
	s, h, _ := researchChatFixture(t)
	body := researchChatBody(s.now())
	for _, tc := range []struct {
		name, body string
		bad        bool
	}{
		{"duplicate", strings.Replace(body, `"schema":`, `"schema":"wrong","schema":`, 1), true},
		{"case-alias", strings.Replace(body, `"schema":`, `"Schema":`, 1), true},
		{"trailer", body + `{}`, true},
		{"authority", strings.Replace(body, `"authoritative":false`, `"authoritative":true`, 1), true},
		{"metric", strings.Replace(body, `"returnBps":"5"`, `"returnBps":"5.5"`, 1), true},
		{"quote", strings.Replace(body, `"quote":101.25`, `"quote":1e999`, 1), true},
		{"stale", strings.Replace(body, `"quality":"valid"`, `"quality":"stale"`, 1), false},
		{"disconnected", strings.Replace(body, `"connection":"connected"`, `"connection":"disconnected"`, 1), false},
		{"old", researchChatBody(s.now().Add(-11 * time.Second)), false},
		{"future", researchChatBody(s.now().Add(2 * time.Second)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := h.reviewResearch([]byte(tc.body), "wen-operator", 0, "1")
			if r != nil || (err != nil) != tc.bad {
				t.Fatal("source admitted", r, err)
			}
		})
	}
	if r, err := h.reviewResearch([]byte(strings.Replace(body, `"metrics":{"returnBps":"5","realizedVolatilityBps":"2"}`, `"metrics":null`, 1)), "wen-operator", 0, "1"); err != nil || r == nil || !strings.Contains(r.Text, `"metrics":null`) {
		t.Fatal("fresh quote with unavailable metrics lost", err)
	}
}

func TestWENResearchChatResponseBounds(t *testing.T) {
	for _, tc := range []struct{ name, padding string }{
		{"review-overhead", strings.Repeat("x", 262144-len(researchChatBody(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)))-len(`,"padding":""`))},
		{"escaped-source", strings.Repeat("<", 60000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, h, token := researchChatFixture(t)
			body := strings.TrimSuffix(researchChatBody(s.now()), "}") + `,"padding":"` + tc.padding + `"}`
			if len(body) > 262144 {
				t.Fatal("invalid oversized upstream fixture")
			}
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer up.Close()
			h.wenResearchOrigin = up.URL
			w := apiRequest(h, "GET", "/api/v1/wen/market-data?profile=wen-operator", "", token, "http://127.0.0.1:7331", "")
			if w.Code != 503 || w.Body.Len() > 262144 {
				t.Fatalf("unbounded private envelope: status=%d bytes=%d", w.Code, w.Body.Len())
			}
			v, _ := s.Agents(t.Context(), "wen-operator", "")
			if v.Revision != 0 || len(v.Messages) != 0 {
				t.Fatal("oversized response saved context")
			}
		})
	}
}
func researchChatBody(at time.Time) string {
	stamp := at.UTC().Format(time.RFC3339Nano)
	return fmt.Sprintf(`{"schema":"wen-research-data/1","execution":"disabled","finality":"research","generatedAt":%q,"sourceCursor":"42","method":"wen-kraken-research/1","connection":"connected","data":{"method":"wen-kraken-research/1","quality":"valid","authoritative":false,"quote":101.25,"quoteEventAt":%q,"quoteReceivedAt":%q,"inputDigest":%q,"candles":[],"observationIds":["1:ticker:42"],"metrics":{"returnBps":"5","realizedVolatilityBps":"2"}},"nextCursor":null}`, stamp, stamp, stamp, strings.Repeat("a", 64))
}
func TestWENResearchChatReviewIsReadOnly(t *testing.T) {
	s, h, token := researchChatFixture(t)
	w := apiRequest(h, "GET", "/api/v1/wen/market-data?lookback=1&profile=wen-operator", "", token, "http://127.0.0.1:7331", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"chatContext"`) {
		t.Fatalf("missing source-bound chat review: %d %s", w.Code, w.Body.String())
	}
	v, err := s.Agents(t.Context(), "wen-operator", "")
	if err != nil || v.Revision != 0 || len(v.Sessions) != 0 || len(v.Messages) != 0 || len(v.AIRequests) != 0 || len(v.Tasks) != 0 {
		t.Fatal("opening review changed state", err, v)
	}
	var read map[string]json.RawMessage
	if json.Unmarshal(w.Body.Bytes(), &read) != nil {
		t.Fatal("invalid envelope")
	}
	if !strings.Contains(string(read["chatContext"]), "sourceCursor") || !strings.Contains(string(read["chatContext"]), strings.Repeat("a", 64)) {
		t.Fatal("source evidence missing")
	}
}
