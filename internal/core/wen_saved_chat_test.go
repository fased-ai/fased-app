package core

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/wenstrategy"
)

func savedChatFixture(t *testing.T) (*Store, *HTTPHandler, string, WENLibrary) {
	t.Helper()
	s := fixtureStore(t)
	s.now = func() time.Time { return time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC) }
	token := claimFixture(t, s)
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	library, err := s.ChangeWENResearch(t.Context(), WENResearchCommand{Profile: "wen-operator", Action: "save", Raw: researchRaw(t)})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/wen-saved-history.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.ReplaceAll(string(raw), strings.Repeat("a", 64), library.Versions[0].Digest))
	library, err = s.ChangeWENResearch(t.Context(), WENResearchCommand{Profile: "wen-operator", Revision: library.Revision, Action: "history", Raw: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	return s, h, token, library
}
func savedSelection(l WENLibrary) WENSavedChatSelection {
	i := 0
	return WENSavedChatSelection{Profile: "wen-operator", Revision: l.Revision, StrategyDigest: l.Versions[0].Digest, BundleIndex: &i}
}
func readSavedReview(t *testing.T, h *HTTPHandler, token string, selection WENSavedChatSelection) WENResearchChatReview {
	t.Helper()
	raw, _ := json.Marshal(selection)
	w := apiRequest(h, "POST", "/api/v1/wen/research/chat-review", string(raw), token, "http://127.0.0.1:7331", csrfToken(token))
	var result WENResearchChatReview
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatalf("review %d: %s", w.Code, w.Body.String())
	}
	return result
}

func TestWENSavedChatExactScopeReadOnlyAndRestart(t *testing.T) {
	s, h, token, library := savedChatFixture(t)
	before, revision, _ := readAgents(t.Context(), s.db)
	review := readSavedReview(t, h, token, savedSelection(library))
	var projection savedChatProjection
	if json.Unmarshal([]byte(strings.Split(review.Text, "\n")[1]), &projection) != nil {
		t.Fatal("projection")
	}
	if projection.Strategy.Digest != library.Versions[0].Digest || projection.Selection.Revision != library.Revision || projection.History.Summary["netSOL"] != "-100001015" || projection.History.Scope.Evidence != "historical-replay" || projection.History.Verified || len(projection.History.Gaps) != 2 || projection.History.Reverted != 1 || projection.History.RelevantRows != 8 {
		t.Fatalf("lost scope/results: %+v", projection)
	}
	if !strings.Contains(review.Text, `"skipped":null`) || !strings.Contains(review.Text, `"pendingExposure":null`) {
		t.Fatal("unknown became zero")
	}
	if got, rev, _ := readAgents(t.Context(), s.db); rev != revision || !reflect.DeepEqual(before, got) {
		t.Fatal("review wrote state")
	}
	if got, _ := s.WENResearch(t.Context(), "wen-operator"); !reflect.DeepEqual(library, got) {
		t.Fatal("review changed library")
	}
	w := postResearchChat(h, token, researchChatCommand(review))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	after, _, _ := readAgents(t.Context(), s.db)
	if len(after.Messages) != 1 || after.Messages[0].Text != review.Text || after.Sessions[0].Name != "Strategy: Fixture session draft" {
		t.Fatal("exact context lost")
	}
	after.Messages, after.Sessions = before.Messages, before.Sessions
	if !reflect.DeepEqual(before, after) {
		t.Fatal("Chat changed execution authority/state")
	}
	if w = postResearchChat(h, token, researchChatCommand(review)); w.Code != 409 {
		t.Fatal("duplicate capture", w.Code)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	view, err := n.Agents(t.Context(), "wen-operator", "")
	if err != nil || view.Messages[0].Text != review.Text || view.FinancialExecution || view.ProviderCallsEnabled || len(view.Tasks) != 0 {
		t.Fatal("restart", err)
	}
	other, _ := n.Agents(t.Context(), "news-researcher", "")
	if len(other.Messages) != 0 {
		t.Fatal("profile leak")
	}
}

func TestWENSavedChatSelectorsAndAuthority(t *testing.T) {
	s, h, token, library := savedChatFixture(t)
	base := savedSelection(library)
	for _, tc := range []struct {
		name   string
		mutate func(*WENSavedChatSelection)
		status int
	}{
		{"whole-history", func(c *WENSavedChatSelection) { c.StrategyDigest = "" }, 200},
		{"strategy-only", func(c *WENSavedChatSelection) { c.BundleIndex = nil }, 200},
		{"empty", func(c *WENSavedChatSelection) { c.StrategyDigest = ""; c.BundleIndex = nil }, 400},
		{"foreign-profile", func(c *WENSavedChatSelection) { c.Profile = "news-researcher" }, 400},
		{"unknown-profile", func(c *WENSavedChatSelection) { c.Profile = "missing" }, 400},
		{"missing-version", func(c *WENSavedChatSelection) { c.StrategyDigest = strings.Repeat("f", 64) }, 400},
		{"stale-library", func(c *WENSavedChatSelection) { c.Revision-- }, 409},
		{"negative-index", func(c *WENSavedChatSelection) { i := -1; c.BundleIndex = &i }, 400},
		{"unknown-index", func(c *WENSavedChatSelection) { i := 50; c.BundleIndex = &i }, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			tc.mutate(&c)
			raw, _ := json.Marshal(c)
			w := apiRequest(h, "POST", "/api/v1/wen/research/chat-review", string(raw), token, "http://127.0.0.1:7331", csrfToken(token))
			if w.Code != tc.status {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	raw, _ := json.Marshal(base)
	for _, tc := range []struct {
		name, method, path, body, auth, csrf, origin string
		status                                       int
	}{
		{"unauthenticated", "POST", "", string(raw), "", "", "http://127.0.0.1:7331", 401},
		{"csrf", "POST", "", string(raw), token, "", "http://127.0.0.1:7331", 403},
		{"origin", "POST", "", string(raw), token, csrfToken(token), "http://elsewhere.test", 403},
		{"query", "POST", "?profile=other", string(raw), token, csrfToken(token), "http://127.0.0.1:7331", 400},
		{"method", "GET", "", "", token, "", "http://127.0.0.1:7331", 405},
		{"unknown-field", "POST", "", strings.TrimSuffix(string(raw), "}") + `,"authority":true}`, token, csrfToken(token), "http://127.0.0.1:7331", 400},
		{"duplicate-field", "POST", "", strings.TrimSuffix(string(raw), "}") + `,"profile":"wen-operator"}`, token, csrfToken(token), "http://127.0.0.1:7331", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := apiRequest(h, tc.method, "/api/v1/wen/research/chat-review"+tc.path, tc.body, tc.auth, tc.origin, tc.csrf)
			if w.Code != tc.status {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	view, _ := s.Agents(t.Context(), base.Profile, "")
	if view.Revision != 0 || len(view.Messages) != 0 {
		t.Fatal("review wrote")
	}
}

func TestWENSavedChatStaleAndTamperedSave(t *testing.T) {
	for _, kind := range []string{"text", "profile", "agent-revision", "library-revision", "expired", "core-restart", "extra-authority"} {
		t.Run(kind, func(t *testing.T) {
			s, h, token, l := savedChatFixture(t)
			r := readSavedReview(t, h, token, savedSelection(l))
			c := researchChatCommand(r)
			switch kind {
			case "text":
				c.Text = strings.Replace(c.Text, "-100001015", "100", 1)
			case "profile":
				c.ProfileID = "news-researcher"
			case "agent-revision":
				if _, err := s.ChangeAgents(t.Context(), AgentCommand{Action: "session-create", ProfileID: "wen-operator", Name: "other"}); err != nil {
					t.Fatal(err)
				}
			case "library-revision":
				raw := strings.Replace(researchRaw(t), "Fixture session draft", "new saved version", 1)
				if _, err := s.ChangeWENResearch(t.Context(), WENResearchCommand{Profile: "wen-operator", Revision: l.Revision, Action: "save", Raw: raw}); err != nil {
					t.Fatal(err)
				}
			case "expired":
				later := s.now().Add(121 * time.Second)
				s.now = func() time.Time { return later }
			case "core-restart":
				var err error
				h, err = NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
				if err != nil {
					t.Fatal(err)
				}
			case "extra-authority":
				c.BotID = "hidden-task"
			}
			before, rev, _ := readAgents(t.Context(), s.db)
			w := postResearchChat(h, token, c)
			if w.Code != 400 && w.Code != 409 {
				t.Fatal(w.Code, w.Body.String())
			}
			after, got, _ := readAgents(t.Context(), s.db)
			if rev != got || !reflect.DeepEqual(before, after) {
				t.Fatal("refusal wrote")
			}
		})
	}
}

func TestWENSavedChatCapacityPauseAndOversize(t *testing.T) {
	for _, kind := range []string{"sessions", "messages", "paused", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			s, h, token, l := savedChatFixture(t)
			if kind == "oversize" {
				history := l.Bundles[0]
				history.Gaps = []string{strings.Repeat("g", 256), strings.Repeat("h", 256), strings.Repeat("i", 256), strings.Repeat("j", 256), strings.Repeat("k", 256), strings.Repeat("l", 256), strings.Repeat("m", 256), strings.Repeat("n", 256), strings.Repeat("o", 256), strings.Repeat("p", 256)}
				raw, _ := json.Marshal(history)
				var err error
				l, err = s.ChangeWENResearch(t.Context(), WENResearchCommand{Profile: "wen-operator", Action: "history", Revision: l.Revision, Raw: string(raw)})
				if err != nil {
					t.Fatal(err)
				}
				sel := savedSelection(l)
				i := 1
				sel.BundleIndex = &i
				b, _ := json.Marshal(sel)
				w := apiRequest(h, "POST", "/api/v1/wen/research/chat-review", string(b), token, "http://127.0.0.1:7331", csrfToken(token))
				if w.Code != 400 || !strings.Contains(w.Body.String(), "no records were omitted") {
					t.Fatal(w.Code, w.Body.String())
				}
				return
			}
			if kind != "paused" {
				d, _, _ := readAgents(t.Context(), s.db)
				if kind == "sessions" {
					for i := 0; i < 100; i++ {
						d.Sessions = append(d.Sessions, AgentSession{ID: strings.Repeat("s", i+1), ProfileID: "wen-operator", Name: "retained"})
					}
				} else {
					for i := 0; i < 200; i++ {
						d.Messages = append(d.Messages, AgentMessage{ID: strings.Repeat("m", i+1), ProfileID: "wen-operator", Text: "retained", Role: "user"})
					}
				}
				raw, _ := json.Marshal(d)
				if _, err := s.db.Exec("UPDATE agent_workspace SET document=? WHERE id=1", string(raw)); err != nil {
					t.Fatal(err)
				}
			}
			r := readSavedReview(t, h, token, savedSelection(l))
			if kind == "paused" {
				p := pauseCommand(t, s, 1)
				if _, err := s.UpdatePause(t.Context(), p); err != nil {
					t.Fatal(err)
				}
			}
			before, rev, _ := readAgents(t.Context(), s.db)
			w := postResearchChat(h, token, researchChatCommand(r))
			if w.Code != 400 && w.Code != 409 {
				t.Fatal(w.Code, w.Body.String())
			}
			after, got, _ := readAgents(t.Context(), s.db)
			if rev != got || !reflect.DeepEqual(before, after) {
				t.Fatal("capacity/pause wrote")
			}
		})
	}
}

func TestWENSavedHistorySummaryParity(t *testing.T) {
	raw, err := os.ReadFile("testdata/wen-saved-history.json")
	if err != nil {
		t.Fatal(err)
	}
	h, err := wenstrategy.ParseHistory(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, digest := range []string{strings.Repeat("a", 64), strings.Repeat("b", 64), ""} {
		summary, rows, reverted := savedHistorySummary(h, digest)
		expected := "-100001015"
		expectedRows := 8
		expectedReverted := 1
		if digest == strings.Repeat("b", 64) {
			expected = "-100000000"
			expectedRows = 1
			expectedReverted = 0
		}
		if digest == "" {
			expected = "-200001015"
			expectedRows = 9
		}
		if summary["netSOL"] != expected || rows != expectedRows || reverted != expectedReverted {
			t.Fatal(summary, rows, reverted)
		}
		if digest == strings.Repeat("a", 64) {
			want := map[string]any{"entries": 2, "unsettled": 1, "committed": "1000000100", "returned": "900000080", "fees": "20", "forfeitures": "100000000", "userCosts": "1000", "btcReceived": "5", "btcPending": "7", "satEarned": "11", "satClaimed": "9", "satSold": "0", "btcSaleSOL": "3", "satSaleSOL": "2", "netSOL": expected, "overlays": map[string]string{"staking": "23", "prize": "23", "carry": "23", "transfer": "23"}}
			if !reflect.DeepEqual(summary, want) {
				t.Fatal("shared comparison totals diverged", summary)
			}
		}
	}
}
