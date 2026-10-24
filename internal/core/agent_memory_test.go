package core

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestAgentMemoryRestoreReadOnlyHTTP(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	v := agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: "trading-analyst", Text: "Retained expired preference", Source: "Owner original", ExpiresAt: 1})
	id := v.Memories[0].ID
	v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: id, Text: "Current preference", Source: "Owner current"})
	before, revision, _ := readAgents(t.Context(), s.db)
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7331/api/v1/agents?profile=trading-analyst&memory-restore-draft="+id+"&memory-revision=0", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("read-only restore draft got %d: %s", w.Code, w.Body)
	}
	var got struct {
		Version string       `json:"version"`
		Command AgentCommand `json:"command"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Version != "memory-restore-draft-v1" || got.Command.Action != "memory-put" || got.Command.Revision != revision || got.Command.ID != id || got.Command.ProfileID != v.ProfileID || got.Command.Text != before.Memories[0].History[0].Text || got.Command.Source != "Owner original" || got.Command.ExpiresAt != 1 {
		t.Fatal("draft lost its history source, expiry or revision binding", w.Body, err)
	}
	after, next, _ := readAgents(t.Context(), s.db)
	if revision != next || !reflect.DeepEqual(before, after) {
		t.Fatal("preparing a restoration wrote state")
	}
}

func TestAgentMemoryRestoreHTTPSelectorsAndAuth(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	v := agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: "trading-analyst", Text: "Private original note", Source: "Owner"})
	id := v.Memories[0].ID
	agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: id, Text: "Private current note", Source: "Owner"})
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	base := "profile=trading-analyst&memory-restore-draft=" + id + "&memory-revision="
	queries := []string{
		base + "", base + "-1", base + "8", base + "01", base + "+0", base + "0.0", base + "99999999999999999999999",
		base + "0&memory-revision=0", base + "0&profile=trading-analyst", base + "0&memory-search=",
		base + "0&ai-draft=x", base + "0&reminder-draft=x", "profile=trading-analyst&memory-restore-draft=" + id,
		"profile=trading-analyst&memory-revision=0", "memory-restore-draft=" + id + "&memory-revision=0",
		"profile=news-researcher&memory-restore-draft=" + id + "&memory-revision=0", base + "1",
	}
	before, revision, _ := readAgents(t.Context(), s.db)
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7331/api/v1/agents?"+query, nil)
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 400 || strings.Contains(w.Body.String(), "Private") {
				t.Fatal("invalid read accepted or disclosed text", w.Code, w.Body)
			}
		})
	}
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7331/api/v1/agents?"+base+"0", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("unauthenticated restoration read", w.Code)
	}
	after, next, _ := readAgents(t.Context(), s.db)
	if revision != next || !reflect.DeepEqual(before, after) {
		t.Fatal("rejected read changed state")
	}
}

func TestAgentMemoryRestoreHistoryExpiryEvidenceAndRestart(t *testing.T) {
	s, v, id := memoryRecallFixture(t, "message")
	note := v.Memories[0]
	v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: id, Text: "Expired retained preference", Source: note.Source, ExpiresAt: 1})
	v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: id, Text: "Unselected history secret", Source: "Owner middle"})
	v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: id, Text: "Current owner preference", Source: "Owner current"})
	agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: "news-researcher", Text: "Foreign memory secret", Source: "Foreign"})
	before, revision, _ := readAgents(t.Context(), s.db)
	draft, err := s.AgentMemoryRestore(t.Context(), v.ProfileID, id, 1)
	if err != nil || draft.Revision != revision || draft.Current.Text != "Current owner preference" || draft.Selected.Text != "Expired retained preference" || draft.Evidence.Status != "matched" || draft.Evidence.Text != before.Messages[0].Text || draft.Command.ExpiresAt != 1 {
		t.Fatal("wrong restoration facts", draft, err)
	}
	b, _ := json.Marshal(draft)
	if strings.Contains(string(b), "Unselected history secret") || strings.Contains(string(b), "Foreign memory secret") || len(b) > 16000 {
		t.Fatal("draft disclosed unrelated history")
	}
	after, next, _ := readAgents(t.Context(), s.db)
	if next != revision || !reflect.DeepEqual(before, after) {
		t.Fatal("abandoning draft wrote state")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	got, err := n.ChangeAgents(t.Context(), draft.Command)
	if err != nil {
		t.Fatal(err)
	}
	m := got.Memories[0]
	if m.ID != id || m.Text != draft.Selected.Text || m.Source != note.Source || m.ExpiresAt != 1 || !m.Expired || len(m.History) != 4 || !reflect.DeepEqual(m.History[:3], before.Memories[0].History) || m.History[3].Text != draft.Current.Text {
		t.Fatal("restore lost expiry/history/identity", m)
	}
	d, _, _ := readAgents(t.Context(), n.db)
	context, err := aiContext(d, v.ProfileID, v.Sessions[0].ID, "Inspect notes", n.now().Unix())
	if err != nil || strings.Contains(context, "Expired retained preference") || strings.Contains(context, "Unselected history secret") || strings.Contains(context, "Current owner preference") || strings.Contains(context, "Foreign memory secret") {
		t.Fatal("expired/history/foreign note entered context", context, err)
	}
	agentApply(t, n, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: id, Text: m.Text, Source: m.Source, ExpiresAt: n.now().Unix() + 317})
	d, _, _ = readAgents(t.Context(), n.db)
	context, err = aiContext(d, v.ProfileID, v.Sessions[0].ID, "Inspect notes", n.now().Unix())
	if err != nil || !strings.Contains(context, "Expired retained preference") || strings.Contains(context, "Unselected history secret") || strings.Contains(context, "Current owner preference") || strings.Contains(context, "Foreign memory secret") {
		t.Fatal("current-only restored context", context, err)
	}
	if len(d.AIRequests) != 0 || len(d.Tasks) != 0 || len(d.Runs) != 0 {
		t.Fatal("restoration dispatched work")
	}
}

func TestAgentMemoryRestoreStaleAndForeignSave(t *testing.T) {
	for _, change := range []string{"correct", "delete", "foreign-command"} {
		t.Run(change, func(t *testing.T) {
			s, v, id := memoryRecallFixture(t, "message")
			v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: id, Text: "Current text", Source: "Owner"})
			draft, err := s.AgentMemoryRestore(t.Context(), v.ProfileID, id, 0)
			if err != nil {
				t.Fatal(err)
			}
			if change == "delete" {
				agentApply(t, s, AgentCommand{Action: "memory-delete", ProfileID: v.ProfileID, ID: id})
			}
			if change == "correct" {
				agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: id, Text: "Newer correction", Source: "Owner"})
			}
			if change == "foreign-command" {
				draft.Command.ProfileID = "news-researcher"
			}
			before, revision, _ := readAgents(t.Context(), s.db)
			_, err = s.ChangeAgents(t.Context(), draft.Command)
			if err == nil || (change != "foreign-command" && !errors.Is(err, ErrConflict)) {
				t.Fatal("stale/foreign restore accepted", err)
			}
			after, next, _ := readAgents(t.Context(), s.db)
			if revision != next || !reflect.DeepEqual(before, after) {
				t.Fatal("rejected restore wrote state")
			}
		})
	}
}

func TestAgentMemoryRestoreRefusesAmbiguityCapacityAndInvalidHistory(t *testing.T) {
	for _, change := range []string{"empty", "full", "overflow", "duplicate-memory", "duplicate-profile", "invalid-text", "negative-expiry"} {
		t.Run(change, func(t *testing.T) {
			s, v, id := memoryRecallFixture(t, "message")
			v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: id, Text: "Current text", Source: "Owner"})
			rewriteReminderFixture(t, s, func(d *agentDocument) {
				switch change {
				case "empty":
					d.Memories[0].History = nil
				case "full", "overflow":
					count := 8
					if change == "overflow" {
						count = 9
					}
					for len(d.Memories[0].History) < count {
						d.Memories[0].History = append(d.Memories[0].History, d.Memories[0].History[0])
					}
				case "duplicate-memory":
					d.Memories = append(d.Memories, d.Memories[0])
				case "duplicate-profile":
					d.Profiles = append(d.Profiles, d.Profiles[1])
				case "invalid-text":
					d.Memories[0].History[0].Text = "bad\x00text"
				case "negative-expiry":
					d.Memories[0].History[0].ExpiresAt = -1
				}
			})
			before, revision, _ := readAgents(t.Context(), s.db)
			if _, err := s.AgentMemoryRestore(t.Context(), v.ProfileID, id, 0); err == nil {
				t.Fatal("invalid restoration admitted")
			}
			after, next, _ := readAgents(t.Context(), s.db)
			if revision != next || !reflect.DeepEqual(before, after) {
				t.Fatal("invalid read repaired state")
			}
		})
	}
}

func TestAgentMemoryRecallHTTPReadOnly(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	v := agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: "trading-analyst", Text: "Prefer short updates", Source: "Owner"})
	before, revision, _ := readAgents(t.Context(), s.db)
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7331/api/v1/agents?profile=trading-analyst&memory-search=SHORT", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("memory search got %d: %s", w.Code, w.Body)
	}
	var got struct {
		Revision int64         `json:"revision"`
		Memories []AgentMemory `json:"memories"`
	}
	if json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Revision != revision || len(got.Memories) != 1 || !reflect.DeepEqual(got.Memories[0], v.Memories[0]) {
		t.Fatal("memory search did not resolve the current profile", w.Body)
	}
	after, next, _ := readAgents(t.Context(), s.db)
	if revision != next || !reflect.DeepEqual(before, after) {
		t.Fatal("read changed agent state")
	}
}

func memoryRecallFixture(t *testing.T, kind string) (*Store, AgentView, string) {
	t.Helper()
	s := fixtureStore(t)
	v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "Retained source"})
	session := v.Sessions[0].ID
	v = agentApply(t, s, AgentCommand{Action: "message-add", ProfileID: v.ProfileID, ID: session, Text: "Original source claims are unverified"})
	id := v.Messages[0].ID
	if kind == "answer" {
		id = "retained-answer"
		rewriteReminderFixture(t, s, func(d *agentDocument) {
			d.AIRequests = append(d.AIRequests, AgentAIRequest{ID: id, ProfileID: v.ProfileID, SessionID: session, State: "completed", Answer: "Retained fixture answer", Context: "PRIVATE-CREDENTIAL-CONTEXT", Prompt: "PRIVATE-PROMPT"})
		})
	}
	v = agentApply(t, s, AgentCommand{Action: "memory-chat-save", ProfileID: v.ProfileID, Source: kind, ID: id, Text: "Original note phrase"})
	return s, v, v.Memories[0].ID
}

func TestAgentMemoryRecallSearchCorrectionExpiryAndReadOnly(t *testing.T) {
	s, v, id := memoryRecallFixture(t, "message")
	note := v.Memories[0]
	v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: id, Text: "Corrected concise preference", Source: note.Source})
	v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, Text: "Expired preference", Source: "Owner calendar", ExpiresAt: 1})
	agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: "news-researcher", Text: "PROFILE-SECRET preference", Source: "foreign"})
	before, revision, _ := readAgents(t.Context(), s.db)
	contextBefore, err := aiContext(before, v.ProfileID, v.Sessions[0].ID, "Review notes", s.now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		query string
		count int
	}{
		{"", 2}, {"  CoNcIsE  ", 1}, {"chat-memory-v1", 1}, {"original note phrase", 0}, {"PROFILE-SECRET", 0}, {"no-match", 0}, {"calendar", 1}, {strings.Repeat("x", 200), 0},
	}
	for _, tt := range cases {
		got, err := s.AgentMemories(t.Context(), v.ProfileID, tt.query, "")
		if err != nil || got.Revision != revision || len(got.Memories) != tt.count || got.Source != nil {
			t.Fatalf("query %q: %+v %v", tt.query, got, err)
		}
	}
	got, err := s.AgentMemories(t.Context(), v.ProfileID, "preference", "")
	if err != nil || len(got.Memories) != 2 || got.Memories[0].Expired || !got.Memories[1].Expired || got.Memories[0].History[0].Text != note.Text {
		t.Fatal("current/expired/history projection", got, err)
	}
	for _, q := range []string{strings.Repeat("x", 201), "bad\x00query", string([]byte{255})} {
		if _, err := s.AgentMemories(t.Context(), v.ProfileID, q, ""); err == nil {
			t.Fatal("invalid search accepted")
		}
	}
	for _, identity := range []string{strings.Repeat("i", 81), "bad\x00id", string([]byte{255})} {
		if _, err := s.AgentMemories(t.Context(), v.ProfileID, "", identity); err == nil {
			t.Fatal("invalid identity accepted")
		}
	}
	for _, p := range []string{"", "missing", "news-researcher"} {
		if _, err := s.AgentMemories(t.Context(), p, "", id); err == nil {
			t.Fatal("foreign/missing profile resolved memory")
		}
	}
	if _, err := s.db.Exec("UPDATE update_pause SET phase='paused'"); err != nil {
		t.Fatal(err)
	}
	got, err = s.AgentMemories(t.Context(), v.ProfileID, "", id)
	if err != nil || got.Source == nil || got.Source.Status != "matched" || got.Source.Text != before.Messages[0].Text {
		t.Fatal("paused read unavailable", got, err)
	}
	after, next, _ := readAgents(t.Context(), s.db)
	contextAfter, err := aiContext(after, v.ProfileID, v.Sessions[0].ID, "Review notes", s.now().Unix())
	if err != nil || contextBefore != contextAfter || strings.Contains(contextAfter, "Expired preference") || !reflect.DeepEqual(before, after) || revision != next {
		t.Fatal("recall changed AI context/document/revision")
	}
}

func TestAgentMemoryRecallSourceStatusesAndPrivacy(t *testing.T) {
	cases := []struct {
		name, kind, status string
		edit               func(*agentDocument)
	}{
		{"message", "message", "matched", nil}, {"answer", "answer", "matched", nil},
		{"free-form-url", "message", "free-form", func(d *agentDocument) { d.Memories[0].Source = "https://never-fetch.invalid/private" }},
		{"malformed", "message", "invalid", func(d *agentDocument) { d.Memories[0].Source = "chat-memory-v1 bogus" }},
		{"extra-field", "message", "invalid", func(d *agentDocument) { d.Memories[0].Source += " owner=trusted" }},
		{"double-space", "message", "invalid", func(d *agentDocument) {
			d.Memories[0].Source = strings.Replace(d.Memories[0].Source, " kind=", "  kind=", 1)
		}},
		{"wrong-kind", "message", "invalid", func(d *agentDocument) {
			d.Memories[0].Source = strings.Replace(d.Memories[0].Source, "kind=message", "kind=system", 1)
		}},
		{"wrong-hash", "message", "invalid", func(d *agentDocument) { d.Memories[0].Source = d.Memories[0].Source[:len(d.Memories[0].Source)-1] }},
		{"non-hex", "message", "invalid", func(d *agentDocument) {
			d.Memories[0].Source = d.Memories[0].Source[:len(d.Memories[0].Source)-1] + "z"
		}},
		{"uppercase-hash", "message", "invalid", func(d *agentDocument) {
			a, b, _ := strings.Cut(d.Memories[0].Source, "sha256=")
			d.Memories[0].Source = a + "sha256=" + strings.ToUpper(b)
		}},
		{"foreign-reference", "message", "unavailable", func(d *agentDocument) {
			d.Memories[0].Source = strings.Replace(d.Memories[0].Source, "profile=trading-analyst", "profile=news-researcher", 1)
		}},
		{"missing-session", "message", "unavailable", func(d *agentDocument) { d.Sessions = nil }},
		{"foreign-session", "message", "unavailable", func(d *agentDocument) { d.Sessions[0].ProfileID = "news-researcher" }},
		{"ambiguous-session", "message", "ambiguous", func(d *agentDocument) { d.Sessions = append(d.Sessions, d.Sessions[0]) }},
		{"missing-message", "message", "unavailable", func(d *agentDocument) { d.Messages = nil }},
		{"foreign-message", "message", "unavailable", func(d *agentDocument) { d.Messages[0].ProfileID = "news-researcher" }},
		{"wrong-session", "message", "unavailable", func(d *agentDocument) { d.Messages[0].SessionID = "other" }},
		{"system-message", "message", "unavailable", func(d *agentDocument) { d.Messages[0].Role = "system" }},
		{"ambiguous-message", "message", "ambiguous", func(d *agentDocument) { d.Messages = append(d.Messages, d.Messages[0]) }},
		{"changed-message", "message", "changed", func(d *agentDocument) { d.Messages[0].Text = "Changed retained claim" }},
		{"empty-message", "message", "unavailable", func(d *agentDocument) { d.Messages[0].Text = "" }},
		{"oversized-message", "message", "unavailable", func(d *agentDocument) { d.Messages[0].Text = strings.Repeat("x", 4001) }},
		{"missing-answer", "answer", "unavailable", func(d *agentDocument) { d.AIRequests = nil }},
		{"foreign-answer", "answer", "unavailable", func(d *agentDocument) { d.AIRequests[0].ProfileID = "news-researcher" }},
		{"scheduled-answer", "answer", "unavailable", func(d *agentDocument) { d.AIRequests[0].TaskID = "scheduled" }},
		{"unfinished-answer", "answer", "unavailable", func(d *agentDocument) { d.AIRequests[0].State = "running" }},
		{"ambiguous-answer", "answer", "ambiguous", func(d *agentDocument) { d.AIRequests = append(d.AIRequests, d.AIRequests[0]) }},
		{"changed-answer", "answer", "changed", func(d *agentDocument) { d.AIRequests[0].Answer = "Changed answer" }},
		{"expired-note", "message", "matched", func(d *agentDocument) { d.Memories[0].ExpiresAt = 1 }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s, v, id := memoryRecallFixture(t, tt.kind)
			if tt.edit != nil {
				rewriteReminderFixture(t, s, tt.edit)
			}
			before, revision, _ := readAgents(t.Context(), s.db)
			got, err := s.AgentMemories(t.Context(), v.ProfileID, "", id)
			if err != nil || got.Source == nil || got.Source.Status != tt.status {
				t.Fatal("wrong source disposition", got, err)
			}
			b, _ := json.Marshal(got)
			if strings.Contains(string(b), "PRIVATE-CREDENTIAL-CONTEXT") || strings.Contains(string(b), "PRIVATE-PROMPT") {
				t.Fatal("private request fields disclosed")
			}
			if tt.status != "matched" && (got.Source.Text != "" || got.Source.SessionID != "" || got.Source.ID != "" || got.Source.SHA256 != "") {
				t.Fatal("unavailable source disclosed data", got.Source)
			}
			if tt.status == "matched" && (got.Source.Text == "" || got.Source.Kind != tt.kind || got.Source.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(got.Source.Text)))) {
				t.Fatal("source identity/hash missing")
			}
			after, next, _ := readAgents(t.Context(), s.db)
			if revision != next || !reflect.DeepEqual(before, after) {
				t.Fatal("inspection wrote state")
			}
		})
	}
	for _, collision := range []string{"memory", "profile"} {
		t.Run("ambiguous-"+collision, func(t *testing.T) {
			s, v, id := memoryRecallFixture(t, "message")
			rewriteReminderFixture(t, s, func(d *agentDocument) {
				if collision == "memory" {
					d.Memories = append(d.Memories, d.Memories[0])
				} else {
					d.Profiles = append(d.Profiles, d.Profiles[1])
				}
			})
			if _, err := s.AgentMemories(t.Context(), v.ProfileID, "", id); err == nil {
				t.Fatal("ambiguous identity accepted")
			}
		})
	}
}

func TestAgentMemoryRecallRestartProviderAndDeletion(t *testing.T) {
	s, v, id := memoryRecallFixture(t, "answer")
	original, err := s.AgentMemories(t.Context(), v.ProfileID, "", id)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	agentApply(t, n, AgentCommand{Action: "profile-update", ProfileID: v.ProfileID, Name: "Trading Analyst", Provider: "xai", Model: "preference-only"})
	got, err := n.AgentMemories(t.Context(), v.ProfileID, "", id)
	if err != nil || !reflect.DeepEqual(original.Source, got.Source) || !reflect.DeepEqual(original.Memories, got.Memories) {
		t.Fatal("restart/provider changed recall", got, err)
	}
	d, _, _ := readAgents(t.Context(), n.db)
	agentApply(t, n, AgentCommand{Action: "memory-delete", ProfileID: v.ProfileID, ID: id})
	got, err = n.AgentMemories(t.Context(), v.ProfileID, "", "")
	if err != nil || len(got.Memories) != 0 {
		t.Fatal("deleted note remains searchable")
	}
	if _, err = n.AgentMemories(t.Context(), v.ProfileID, "", id); err == nil {
		t.Fatal("deleted note resolved")
	}
	after, _, _ := readAgents(t.Context(), n.db)
	if !reflect.DeepEqual(d.Messages, after.Messages) || !reflect.DeepEqual(d.AIRequests, after.AIRequests) {
		t.Fatal("note deletion erased source")
	}
}

func TestAgentMemoryRecallHTTPStrictScopeAndInput(t *testing.T) {
	s, v, id := memoryRecallFixture(t, "message")
	token := claimFixture(t, s)
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query, token, method string
		want                 int
	}{
		{"profile=trading-analyst&memory-search=", token, "GET", 200},
		{"profile=trading-analyst&memory-source=" + id, token, "GET", 200},
		{"profile=trading-analyst&memory-source=" + id, "", "GET", 401},
		{"profile=news-researcher&memory-source=" + id, token, "GET", 400},
		{"memory-search=x", token, "GET", 400},
		{"profile=trading-analyst&profile=news-researcher&memory-search=", token, "GET", 400},
		{"profile=trading-analyst&memory-search=x&memory-search=y", token, "GET", 400},
		{"profile=trading-analyst&memory-source=", token, "GET", 400},
		{"profile=trading-analyst&memory-search=x&q=y", token, "GET", 400},
		{"profile=trading-analyst&memory-search=x&unknown=y", token, "GET", 400},
		{"profile=trading-analyst&memory-search=" + strings.Repeat("x", 201), token, "GET", 400},
		{"profile=trading-analyst&memory-search=%00", token, "GET", 400},
		{"profile=trading-analyst&memory-search=%ff", token, "GET", 400},
		{"profile=trading-analyst&memory-source=" + id, token, "POST", 400},
	} {
		r := httptest.NewRequest(tc.method, "http://127.0.0.1:7331/api/v1/agents?"+tc.query, nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s got %d want %d: %s", tc.query, w.Code, tc.want, w.Body)
		}
	}
	got, _ := s.AgentMemories(t.Context(), v.ProfileID, "", id)
	if got.Source.Status != "matched" {
		t.Fatal("HTTP reads damaged source")
	}
}
