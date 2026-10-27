package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAgentEvidenceHTTPReadOnly(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	v := agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: "trading-analyst", Text: "Inspect this saved note", Source: "owner"})
	before, rev, _ := readAgents(t.Context(), s.db)
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7331/api/v1/agents?profile=trading-analyst&evidence=memory&evidence-id="+v.Memories[0].ID, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("evidence read got %d: %s", w.Code, w.Body)
	}
	var response map[string]json.RawMessage
	if json.Unmarshal(w.Body.Bytes(), &response) != nil {
		t.Fatal("invalid evidence response")
	}
	after, next, _ := readAgents(t.Context(), s.db)
	if next != rev || !reflect.DeepEqual(before, after) {
		t.Fatal("inspection wrote state")
	}
}

func evidenceFixture(t *testing.T) (*Store, AgentView, *atomic.Int32) {
	t.Helper()
	s, v, calls := newsChatFixture(t)
	agentRunChatFixture(t, s)
	v = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: v.ProfileID, Name: "Evidence conversation"})
	v = agentApply(t, s, AgentCommand{Action: "message-add", ProfileID: v.ProfileID, ID: v.Sessions[0].ID, Text: "Original retained source"})
	v = agentApply(t, s, AgentCommand{Action: "memory-chat-save", ProfileID: v.ProfileID, Source: "message", ID: v.Messages[0].ID, Text: "Current owner-edited note"})
	return s, v, calls
}
func evidenceQueries(v AgentView) []struct{ kind, id, item string } {
	return []struct{ kind, id, item string }{{"memory", v.Memories[0].ID, ""}, {"headline", v.NewsFetches[0].ID, v.NewsFetches[0].Items[0].ID}, {"reminder-run", v.Runs[0].ID, ""}}
}
func TestAgentEvidenceSources(t *testing.T) {
	s, v, calls := evidenceFixture(t)
	before, rev, _ := readAgents(t.Context(), s.db)
	for _, q := range evidenceQueries(v) {
		t.Run(q.kind, func(t *testing.T) {
			got, err := s.AgentEvidence(t.Context(), v.ProfileID, q.kind, q.id, q.item)
			if err != nil {
				t.Fatal(err)
			}
			if got.Version != "agent-evidence-v1" || got.Revision != rev || got.ProfileID != v.ProfileID || got.Kind != q.kind || got.ID != q.id || got.ItemID != q.item || !got.Untrusted || got.RecordedAt <= 0 || got.ObservedAt != s.now().Unix() || len(got.SHA256) != 64 {
				t.Fatal("wrong evidence envelope", got)
			}
			raw, _ := json.Marshal(got)
			if len(raw)+1 > agentEvidenceLimit {
				t.Fatal("unbounded response")
			}
			var data map[string]json.RawMessage
			json.Unmarshal(got.Data, &data)
			switch q.kind {
			case "memory":
				if got.Historical || got.Freshness != "active" || string(data["text"]) != `"Current owner-edited note"` || !strings.Contains(string(got.Data), "Original retained source") {
					t.Fatal("memory provenance lost", got)
				}
				if _, ok := data["history"]; ok {
					t.Fatal("memory history included")
				}
			case "headline":
				if !got.Historical || got.Freshness != "fresh" || !strings.Contains(string(got.Data), v.NewsFetches[0].Items[0].Title) || !strings.Contains(string(got.Data), v.NewsFetches[0].BodySHA256) {
					t.Fatal("headline provenance/freshness lost")
				}
			case "reminder-run":
				if !got.Historical || got.Freshness != "recorded" || !strings.Contains(string(got.Data), v.Tasks[0].Text) {
					t.Fatal("reminder facts lost")
				}
			}
			for _, forbidden := range []string{`"connectionRef"`, `"context"`, `"disclosure"`, `"consentDigest"`, `"heldMicrousd"`, `"aiRequests"`, `"nextAt"`} {
				if strings.Contains(string(raw), forbidden) {
					t.Fatal("authority/private context included", forbidden)
				}
			}
			again, err := s.AgentEvidence(t.Context(), v.ProfileID, q.kind, q.id, q.item)
			if err != nil || again.SHA256 != got.SHA256 {
				t.Fatal("unstable source hash", err)
			}
		})
	}
	after, next, _ := readAgents(t.Context(), s.db)
	if rev != next || !reflect.DeepEqual(before, after) || calls.Load() != 1 {
		t.Fatal("read changed records or fetched")
	}
}

func TestAgentEvidenceIsolation(t *testing.T) {
	s, v, calls := evidenceFixture(t)
	d, rev, _ := readAgents(t.Context(), s.db)
	for _, q := range evidenceQueries(v) {
		t.Run(q.kind, func(t *testing.T) {
			for _, profile := range []string{"", "news-researcher", "absent", strings.Repeat("x", 201), "bad\x00", string([]byte{255})} {
				if _, err := inspectAgentEvidence(d, rev, profile, q.kind, q.id, q.item, s.now().Unix()); err == nil {
					t.Fatal("invalid profile accepted", profile)
				}
			}
			for _, id := range []string{"", "absent", strings.Repeat("x", 201), "bad\x00", string([]byte{255})} {
				if _, err := inspectAgentEvidence(d, rev, v.ProfileID, q.kind, id, q.item, s.now().Unix()); err == nil {
					t.Fatal("invalid ID accepted")
				}
			}
			for _, kind := range []string{"", "shell", "http", "future", "MEMORY"} {
				if _, err := inspectAgentEvidence(d, rev, v.ProfileID, kind, q.id, q.item, s.now().Unix()); err == nil {
					t.Fatal("unsupported kind accepted")
				}
			}
		})
	}
	cases := []struct {
		name, kind string
		change     func(*agentDocument)
	}{
		{"profile-duplicate", "memory", func(d *agentDocument) { d.Profiles = append(d.Profiles, d.Profiles[agentProfile(*d, v.ProfileID)]) }},
		{"memory-duplicate", "memory", func(d *agentDocument) { d.Memories = append(d.Memories, d.Memories[0]) }},
		{"memory-foreign", "memory", func(d *agentDocument) { d.Memories[0].ProfileID = "news-researcher" }},
		{"memory-changed-provenance", "memory", func(d *agentDocument) { d.Messages[0].Text = "Changed original source" }},
		{"memory-missing-provenance", "memory", func(d *agentDocument) { d.Messages = nil }},
		{"memory-ambiguous-provenance", "memory", func(d *agentDocument) { d.Messages = append(d.Messages, d.Messages[0]) }},
		{"memory-invalid-provenance", "memory", func(d *agentDocument) { d.Memories[0].Source = "chat-memory-v1 invalid" }},
		{"memory-future", "memory", func(d *agentDocument) { d.Memories[0].UpdatedAt = s.now().Unix() + 1 }},
		{"headline-duplicate-fetch", "headline", func(d *agentDocument) { d.NewsFetches = append(d.NewsFetches, d.NewsFetches[0]) }},
		{"headline-duplicate-item", "headline", func(d *agentDocument) {
			d.NewsFetches[0].Items = append(d.NewsFetches[0].Items, d.NewsFetches[0].Items[0])
		}},
		{"headline-foreign", "headline", func(d *agentDocument) { d.NewsFetches[0].ProfileID = "news-researcher" }},
		{"headline-source-duplicate", "headline", func(d *agentDocument) { d.NewsSources = append(d.NewsSources, d.NewsSources[0]) }},
		{"headline-source-changed", "headline", func(d *agentDocument) { d.NewsSources[0].URL = "https://example.com/changed.xml" }},
		{"headline-item-changed", "headline", func(d *agentDocument) { d.NewsFetches[0].Items[0].Title = "Changed immutable headline" }},
		{"headline-incomplete", "headline", func(d *agentDocument) { d.NewsFetches[0].State = "fetching" }},
		{"headline-private-link", "headline", func(d *agentDocument) { d.NewsFetches[0].Items[0].URL = "https://localhost/private" }},
		{"run-duplicate", "reminder-run", func(d *agentDocument) { d.Runs = append(d.Runs, d.Runs[0]) }},
		{"run-foreign", "reminder-run", func(d *agentDocument) { d.Runs[0].ProfileID = "news-researcher" }},
		{"run-notice-changed", "reminder-run", func(d *agentDocument) { d.Notices[0].Text = "Changed source" }},
		{"run-notice-missing", "reminder-run", func(d *agentDocument) { d.Notices = nil }},
		{"run-task-duplicate", "reminder-run", func(d *agentDocument) { d.Tasks = append(d.Tasks, d.Tasks[0]) }},
		{"run-task-changed", "reminder-run", func(d *agentDocument) { d.Tasks[0].Text = "Changed task" }},
		{"run-financial", "reminder-run", func(d *agentDocument) { d.Runs[0].CostCents = 1 }},
		{"run-unsupported", "reminder-run", func(d *agentDocument) { d.Runs[0].Strategy = "paper-v1" }},
		{"run-ai-binding", "reminder-run", func(d *agentDocument) { d.Tasks[0].AI = &TaskAIStep{} }},
		{"run-news-binding", "reminder-run", func(d *agentDocument) { d.Tasks[0].News = &TaskNewsInput{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(d)
			var copy agentDocument
			json.Unmarshal(raw, &copy)
			tc.change(&copy)
			before, _ := json.Marshal(copy)
			var id, item string
			for _, q := range evidenceQueries(v) {
				if q.kind == tc.kind {
					id, item = q.id, q.item
				}
			}
			if _, err := inspectAgentEvidence(copy, rev, v.ProfileID, tc.kind, id, item, s.now().Unix()); err == nil {
				t.Fatal("invalid source accepted")
			}
			after, _ := json.Marshal(copy)
			if string(before) != string(after) {
				t.Fatal("refusal mutated source")
			}
		})
	}
	if calls.Load() != 1 {
		t.Fatal("inspection fetched")
	}
}

func TestAgentEvidenceReadOnlyFreshnessAndRestart(t *testing.T) {
	s, v, calls := evidenceFixture(t)
	note := v.Memories[0]
	v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: note.ID, Text: note.Text, Source: note.Source, ExpiresAt: s.now().Unix() + 30})
	before, rev, _ := readAgents(t.Context(), s.db)
	hashes := map[string]string{}
	for _, q := range evidenceQueries(v) {
		got, err := s.AgentEvidence(t.Context(), v.ProfileID, q.kind, q.id, q.item)
		if err != nil {
			t.Fatal(err)
		}
		hashes[q.kind] = got.SHA256
	}
	later := s.now().Add(time.Hour)
	s.now = func() time.Time { return later }
	for _, q := range evidenceQueries(v) {
		got, err := s.AgentEvidence(t.Context(), v.ProfileID, q.kind, q.id, q.item)
		if err != nil || got.SHA256 != hashes[q.kind] {
			t.Fatal("read time changed source", err)
		}
		want := map[string]string{"memory": "expired", "headline": "stale", "reminder-run": "recorded"}[q.kind]
		if got.Freshness != want {
			t.Fatal("false freshness", got)
		}
	}
	after, next, _ := readAgents(t.Context(), s.db)
	if next != rev || !reflect.DeepEqual(before, after) {
		t.Fatal("read/expiry wrote state")
	}
	v = agentApply(t, s, AgentCommand{Action: "news-source-disable", ProfileID: v.ProfileID, ID: v.NewsSources[0].ID})
	headline, err := s.AgentEvidence(t.Context(), v.ProfileID, "headline", v.NewsFetches[0].ID, v.NewsFetches[0].Items[0].ID)
	if err != nil || headline.Freshness != "disabled" || !headline.Historical {
		t.Fatal("disabled history hidden or made current", err)
	}
	fixtureProfileProvider(t, s, AgentCommand{Action: "profile-update", ProfileID: v.ProfileID, Name: "Trading Analyst", Provider: "xai", Text: v.Profiles[agentProfile(agentDocument{Profiles: v.Profiles}, v.ProfileID)].Persona})
	clock := s.now
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	n.now = clock
	got, err := n.AgentEvidence(t.Context(), v.ProfileID, "headline", v.NewsFetches[0].ID, v.NewsFetches[0].Items[0].ID)
	if err != nil || got.SHA256 != headline.SHA256 || got.Freshness != "disabled" || calls.Load() != 1 {
		t.Fatal("restart/provider change fetched or changed evidence", err)
	}
	current, _ := n.Agents(t.Context(), v.ProfileID, "")
	if current.ProviderCallsEnabled || current.FinancialExecution || len(current.AIRequests) != 0 {
		t.Fatal("inspection enabled authority")
	}
}

func TestAgentEvidenceBoundsAndCapacity(t *testing.T) {
	base := AgentEvidenceView{Version: "agent-evidence-v1", ProfileID: "test-profile", Kind: "memory", ID: "test-memory", Data: json.RawMessage(`{"text":""}`), Untrusted: true, Freshness: "active"}
	small, err := finishAgentEvidence(base)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(small)
	n := agentEvidenceLimit - len(b) - 1
	base.Data = json.RawMessage(`{"text":"` + strings.Repeat("x", n) + `"}`)
	exact, err := finishAgentEvidence(base)
	b, _ = json.Marshal(exact)
	if err != nil || len(b)+1 != agentEvidenceLimit {
		t.Fatal("exact encoded bound", len(b)+1, err)
	}
	base.Data = json.RawMessage(`{"text":"` + strings.Repeat("x", n+1) + `"}`)
	if got, err := finishAgentEvidence(base); err == nil || !reflect.DeepEqual(got, AgentEvidenceView{}) {
		t.Fatal("oversized partial result returned")
	}
	s, v, _ := evidenceFixture(t)
	d, rev, _ := readAgents(t.Context(), s.db)
	for len(d.Sessions) < 100 {
		d.Sessions = append(d.Sessions, AgentSession{ID: "irrelevant-session"})
	}
	for len(d.Messages) < 200 {
		d.Messages = append(d.Messages, AgentMessage{ID: "irrelevant-message"})
	}
	for _, q := range evidenceQueries(v) {
		if _, err := inspectAgentEvidence(d, rev, v.ProfileID, q.kind, q.id, q.item, s.now().Unix()); err != nil {
			t.Fatal("read inherited capture capacity", q.kind, err)
		}
	}
	v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, Text: strings.Repeat("<", 3000), Source: "owner"})
	id := v.Memories[len(v.Memories)-1].ID
	before, rev, _ := readAgents(t.Context(), s.db)
	if got, err := s.AgentEvidence(t.Context(), v.ProfileID, "memory", id, ""); err == nil || !reflect.DeepEqual(got, AgentEvidenceView{}) {
		t.Fatal("escaped oversized evidence returned")
	}
	after, next, _ := readAgents(t.Context(), s.db)
	if rev != next || !reflect.DeepEqual(before, after) {
		t.Fatal("bound refusal wrote state")
	}
}

func TestAgentEvidenceHTTPStrict(t *testing.T) {
	s, v, _ := evidenceFixture(t)
	token := claimFixture(t, s)
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	q := "profile=" + v.ProfileID + "&evidence=memory&evidence-id=" + v.Memories[0].ID
	before, rev, _ := readAgents(t.Context(), s.db)
	for _, tc := range []struct {
		query, token, method string
		status               int
	}{
		{q, token, "GET", 200}, {q, "", "GET", 401}, {q, token, "POST", 400}, {q, token, "DELETE", 405},
		{q + "&profile=news-researcher", token, "GET", 400}, {q + "&evidence=memory", token, "GET", 400}, {q + "&evidence-id=x", token, "GET", 400},
		{q + "&evidence-item=", token, "GET", 400}, {q + "&q=", token, "GET", 400}, {q + "&memory-search=", token, "GET", 400}, {q + "&ai-draft=x", token, "GET", 400}, {q + "&unknown=x", token, "GET", 400},
		{"evidence=memory&evidence-id=x", token, "GET", 400}, {"profile=trading-analyst&evidence-id=x", token, "GET", 400},
		{"profile=trading-analyst&evidence=headline&evidence-id=" + v.NewsFetches[0].ID, token, "GET", 400},
		{"profile=trading-analyst&evidence=reminder-run&evidence-id=" + v.Runs[0].ID, token, "GET", 200},
		{"profile=trading-analyst&evidence=headline&evidence-id=" + v.NewsFetches[0].ID + "&evidence-item=" + v.NewsFetches[0].Items[0].ID, token, "GET", 200},
	} {
		r := httptest.NewRequest(tc.method, "http://127.0.0.1:7331/api/v1/agents?"+tc.query, nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s got %d want %d: %s", tc.method, tc.query, w.Code, tc.status, w.Body)
		}
	}
	after, next, _ := readAgents(t.Context(), s.db)
	if rev != next || !reflect.DeepEqual(before, after) {
		t.Fatal("HTTP reads changed source")
	}
}
