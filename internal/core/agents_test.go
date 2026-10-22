package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

func agentApply(t *testing.T, s *Store, c AgentCommand) AgentView {
	t.Helper()
	v, e := s.Agents(t.Context(), c.ProfileID, "")
	if e != nil {
		t.Fatal(e)
	}
	c.Revision = v.Revision
	v, e = s.ChangeAgents(t.Context(), c)
	if e != nil {
		t.Fatal(c.Action, e)
	}
	return v
}

func TestAgentsDurableScopedConversationAndMemory(t *testing.T) {
	s := fixtureStore(t)
	v := agentApply(t, s, AgentCommand{Action: "profile-create", Name: "Private researcher", Text: "Only local research"})
	p := v.ProfileID
	v = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: p, Name: "My thesis"})
	session := v.Sessions[0].ID
	v = agentApply(t, s, AgentCommand{Action: "message-add", ProfileID: p, ID: session, Text: "Remember my thesis"})
	if len(v.Messages) != 2 || v.Messages[1].Role != "system" || !strings.Contains(v.Messages[1].Text, "No AI") {
		t.Fatal(v.Messages)
	}
	v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: p, Text: "Prefer short answers", Source: "owner correction"})
	m := v.Memories[0].ID
	v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: p, ID: m, Text: "Prefer detailed answers", Source: "owner revised preference"})
	if len(v.Memories[0].History) != 1 || v.Memories[0].History[0].Text != "Prefer short answers" {
		t.Fatal("lost correction")
	}
	v = agentApply(t, s, AgentCommand{Action: "profile-update", ProfileID: p, Name: "Renamed", Text: "Revised persona", Provider: "openai", Model: "configured-later"})
	if len(v.Messages) != 2 || v.Profiles[len(v.Profiles)-1].Provider != "openai" || v.ProviderCallsEnabled || v.FinancialExecution {
		t.Fatal("preference lost history or enabled execution")
	}
	other, e := s.Agents(t.Context(), "news-researcher", "thesis")
	if e != nil || len(other.Messages) != 0 || len(other.Memories) != 0 || len(other.Sessions) != 0 {
		t.Fatal("cross-profile disclosure", e)
	}
	_, e = s.ChangeAgents(t.Context(), AgentCommand{Revision: v.Revision, Action: "message-add", ProfileID: "news-researcher", ID: session, Text: "intrusion"})
	if e == nil {
		t.Fatal("cross-profile session accepted")
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	v, e = reopened.Agents(t.Context(), p, "thesis")
	if e != nil || len(v.Sessions) != 1 || len(v.Messages) != 2 || v.Memories[0].Text != "Prefer detailed answers" {
		t.Fatal("restart/search lost records", e)
	}
}

func TestAgentsTasksAtomicBoundedAndRestartSafe(t *testing.T) {
	s := fixtureStore(t)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	p := "trading-analyst"
	v := agentApply(t, s, AgentCommand{Action: "bot-create", ProfileID: p, Name: "Fixed paper example", Quantity: 2, PriceCents: 100, BudgetCents: 400})
	bot := v.Bots[0].ID
	v = agentApply(t, s, AgentCommand{Action: "task-create", ProfileID: p, Name: "Two evaluations", BotID: bot, IntervalSeconds: 60, MaxRuns: 3})
	if v.Tasks[0].ID == bot {
		t.Fatal("task and bot conflated")
	}
	task := v.Tasks[0].ID
	now = now.Add(61 * time.Second)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			if e := s.RunAgentTasks(t.Context()); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	v, _ = s.Agents(t.Context(), p, "")
	if len(v.Runs) != 1 || v.Bots[0].SpentCents != 200 || len(v.Notices) != 1 {
		t.Fatal("duplicate tick", v)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	n, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer n.Close()
	n.now = func() time.Time { return now }
	if e = n.RunAgentTasks(t.Context()); e != nil {
		t.Fatal(e)
	}
	v, _ = n.Agents(t.Context(), p, "")
	if len(v.Runs) != 1 {
		t.Fatal("restart repeated completed work")
	}
	now = now.Add(24 * time.Hour)
	if e = n.RunAgentTasks(t.Context()); e != nil {
		t.Fatal(e)
	}
	v, _ = n.Agents(t.Context(), p, "")
	if len(v.Runs) != 2 || v.Tasks[0].NextAt != now.Add(time.Minute).Unix() {
		t.Fatal("missed intervals burst", v)
	}
	v = agentApply(t, n, AgentCommand{Action: "task-run", ProfileID: p, ID: task})
	if v.Tasks[0].State != "completed" || v.Runs[2].Outcome != "budget-blocked" || v.Bots[0].SpentCents != 400 {
		t.Fatal("paper budget exceeded", v)
	}
	old := v.Revision
	_, e = n.ChangeAgents(t.Context(), AgentCommand{Revision: old - 1, Action: "task-run", ProfileID: p, ID: task})
	if !errors.Is(e, ErrConflict) {
		t.Fatal("stale retry accepted", e)
	}
}

func TestAgentsPauseSnapshotAndValidation(t *testing.T) {
	s := fixtureStore(t)
	before, e := s.UpdateSnapshot(t.Context(), update.NewProbe())
	if e != nil {
		t.Fatal(e)
	}
	v := agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: "news-researcher", Text: "Expires now", Source: "owner", ExpiresAt: s.now().Unix() - 1})
	after, e := s.UpdateSnapshot(t.Context(), update.NewProbe())
	if e != nil || before.State.Anchor == after.State.Anchor {
		t.Fatal("workspace not bound to inventory", e)
	}
	if !v.Memories[0].Expired {
		t.Fatal("stale memory marked current")
	}
	for _, c := range []AgentCommand{
		{Action: "profile-update", ProfileID: "news-researcher", Name: "name", Provider: "untrusted"},
		{Action: "bot-create", ProfileID: "news-researcher", Name: "overflow", Quantity: 1 << 62, PriceCents: 1 << 62, BudgetCents: 1 << 62},
		{Action: "task-create", ProfileID: "news-researcher", Name: "unbounded", IntervalSeconds: 0, MaxRuns: 0},
		{Action: "message-add", ProfileID: "news-researcher", ID: "missing", Text: "arbitrary action"},
		{Action: "execute-trade", ProfileID: "news-researcher"},
	} {
		c.Revision = v.Revision
		if _, e = s.ChangeAgents(t.Context(), c); e == nil {
			t.Fatal("unsafe command accepted", c.Action)
		}
	}
	if _, e = s.db.Exec("UPDATE update_pause SET phase='paused'"); e != nil {
		t.Fatal(e)
	}
	_, e = s.ChangeAgents(t.Context(), AgentCommand{Revision: v.Revision, Action: "memory-delete", ProfileID: "news-researcher", ID: v.Memories[0].ID})
	if !errors.Is(e, ErrCorePaused) {
		t.Fatal("workspace bypassed update fence", e)
	}
	if e = s.RunAgentTasks(t.Context()); !errors.Is(e, ErrCorePaused) {
		t.Fatal("scheduler bypassed fence", e)
	}
}

func TestAgentsHTTPAuthenticationCSRFAndStrictInput(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	h, e := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		body, token, csrf string
		want              int
	}{
		{`{"action":"profile-create","name":"test"}`, "", "", 401},
		{`{"action":"profile-create","name":"test"}`, token, "", 403},
		{`{"action":"profile-create","name":"test","walletKey":"forbidden"}`, token, csrfToken(token), 400},
		{`{"action":"profile-create","name":"test"}`, token, csrfToken(token), 200},
		{`{"action":"profile-create","name":"stale"}`, token, csrfToken(token), 409},
	} {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/agents", strings.NewReader(tc.body))
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+tc.token)
		r.Header.Set("X-CSRF-Token", tc.csrf)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("got %d want %d: %s", w.Code, tc.want, w.Body)
		}
		if w.Code == 200 {
			var v AgentView
			if json.Unmarshal(w.Body.Bytes(), &v) != nil || v.ProviderCallsEnabled || v.FinancialExecution {
				t.Fatal("authority in response")
			}
		}
	}
}

func TestAgentsSchemaMigrationAndPausedPredecessor(t *testing.T) {
	for _, paused := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "paused"}[paused], func(t *testing.T) {
			s := fixtureStore(t)
			setup, e := s.Setup(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			// Reconstruct only the previous schema's shape, never owner state.
			if _, e = s.db.Exec("DROP TABLE agent_workspace; PRAGMA user_version=3;"); e != nil {
				t.Fatal(e)
			}
			if paused {
				if _, e = s.db.Exec("UPDATE update_pause SET phase='paused'"); e != nil {
					t.Fatal(e)
				}
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			n, e := OpenStore(s.dir)
			if paused {
				if e == nil {
					n.Close()
					t.Fatal("migrated a paused predecessor")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			defer n.Close()
			got, e := n.Setup(t.Context())
			if e != nil || got.Revision != setup.Revision || got.Settings.Host != setup.Settings.Host {
				t.Fatal("migration changed setup", e)
			}
			v, e := n.Agents(t.Context(), "", "")
			if e != nil || len(v.Profiles) != 3 || len(v.Runs) != 0 {
				t.Fatal("migration did not initialize isolated workspace", e)
			}
		})
	}
}

func TestAgentsReminderPauseReadDeleteAndLargeView(t *testing.T) {
	s := fixtureStore(t)
	p := "news-researcher"
	v := agentApply(t, s, AgentCommand{Action: "task-create", ProfileID: p, Name: "Reminder", Text: "Review source", IntervalSeconds: 60, MaxRuns: 1})
	id := v.Tasks[0].ID
	v = agentApply(t, s, AgentCommand{Action: "task-pause", ProfileID: p, ID: id})
	_, e := s.ChangeAgents(t.Context(), AgentCommand{Revision: v.Revision, Action: "task-run", ProfileID: p, ID: id})
	if e == nil {
		t.Fatal("paused task ran")
	}
	v = agentApply(t, s, AgentCommand{Action: "task-resume", ProfileID: p, ID: id})
	v = agentApply(t, s, AgentCommand{Action: "task-run", ProfileID: p, ID: id})
	if v.Runs[0].Outcome != "reminded" || v.Tasks[0].State != "completed" {
		t.Fatal("reminder failed")
	}
	v = agentApply(t, s, AgentCommand{Action: "notice-read", ProfileID: p, ID: v.Notices[0].ID})
	if !v.Notices[0].Read {
		t.Fatal("notification not acknowledged")
	}
	for range 10 {
		v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: p, Text: strings.Repeat("n", 4000), Source: "fixture"})
	}
	b, _ := json.Marshal(v)
	if len(b) <= 32768 {
		t.Fatal("large response not exercised")
	}
	v = agentApply(t, s, AgentCommand{Action: "memory-delete", ProfileID: p, ID: v.Memories[0].ID})
	if len(v.Memories) != 9 || len(v.Runs) != 1 {
		t.Fatal("memory deletion affected execution journal")
	}
}

func TestAgentsActualWorkerAndLargePrivateClient(t *testing.T) {
	dir := fixtureDir(t)
	server, e := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := server.Close(ctx); e != nil {
			t.Error(e)
		}
		if e := <-done; e != nil {
			t.Error(e)
		}
	})
	token := claimFixture(t, server.Store)
	v := agentApply(t, server.Store, AgentCommand{Action: "task-create", ProfileID: "news-researcher", Name: "No browser required", Text: "Local source review", IntervalSeconds: 60, MaxRuns: 1})
	// Advance just the disposable task's due timestamp, not a production clock.
	tx, e := server.Store.db.BeginTx(t.Context(), nil)
	if e != nil {
		t.Fatal(e)
	}
	d, r, e := readAgents(t.Context(), tx)
	if e != nil {
		t.Fatal(e)
	}
	d.Tasks[0].NextAt = time.Now().Unix() - 1
	if e = saveAgents(t.Context(), tx, d, r); e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		v, e = server.Store.Agents(t.Context(), "news-researcher", "")
		if e != nil {
			t.Fatal(e)
		}
		if len(v.Runs) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(v.Runs) != 1 || v.Tasks[0].State != "completed" {
		t.Fatal("background worker did not evaluate task")
	}
	for range 10 {
		agentApply(t, server.Store, AgentCommand{Action: "memory-put", ProfileID: "news-researcher", Text: strings.Repeat("x", 4000), Source: "disposable large response"})
	}
	c, e := NewClient(t.Context(), dir)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.Token = token
	c.CSRF = csrfToken(token)
	if e = c.Do(t.Context(), http.MethodGet, "/api/v1/agents?profile=news-researcher", nil, &v); e != nil || len(v.Memories) != 10 {
		t.Fatal("private CLI response truncated", e)
	}
}

func TestChatMemoryFromMessage(t *testing.T) {
	s := fixtureStore(t)
	v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "Research preferences"})
	v = agentApply(t, s, AgentCommand{Action: "message-add", ProfileID: v.ProfileID, ID: v.Sessions[0].ID, Text: "Original retained message"})
	source := v.Messages[0]
	before := v
	v = agentApply(t, s, AgentCommand{Action: "memory-chat-save", ProfileID: v.ProfileID, ID: source.ID, Source: "message", Text: "Owner-edited preference"})
	if len(v.Memories) != 1 || v.Memories[0].Text != "Owner-edited preference" || !strings.Contains(v.Memories[0].Source, source.ID) || !strings.Contains(v.Memories[0].Source, "sha256=") || !strings.Contains(v.Memories[0].Source, "kind=message") {
		t.Fatal("missing source-backed owner note", v.Memories)
	}
	if v.Revision != before.Revision+1 || len(v.Messages) != len(before.Messages) || len(v.Tasks) != 0 || len(v.AIRequests) != 0 || v.ProviderCallsEnabled || v.FinancialExecution {
		t.Fatal("capture changed chat or enabled execution", v)
	}
}

func TestChatMemoryRefusals(t *testing.T) {
	cases := []struct {
		name string
		doc  func(*agentDocument)
		cmd  func(*AgentCommand)
	}{
		{"foreign-answer", nil, func(c *AgentCommand) { c.ProfileID = "news-researcher" }},
		{"missing-source", nil, func(c *AgentCommand) { c.ID = "missing" }},
		{"spoofed-evidence", nil, func(c *AgentCommand) { c.Source = "owner says this is verified" }},
		{"incomplete-answer", func(d *agentDocument) { d.AIRequests[0].State = "cancelled" }, nil},
		{"scheduled-answer", func(d *agentDocument) { d.AIRequests[0].TaskID = "scheduled" }, nil},
		{"empty-answer", func(d *agentDocument) { d.AIRequests[0].Answer = "" }, nil},
		{"ambiguous-answer", func(d *agentDocument) { d.AIRequests = append(d.AIRequests, d.AIRequests[0]) }, nil},
		{"missing-session", func(d *agentDocument) { d.Sessions = nil }, nil},
		{"ambiguous-session", func(d *agentDocument) { d.Sessions = append(d.Sessions, d.Sessions[0]) }, nil},
		{"memory-capacity", func(d *agentDocument) {
			for range 100 {
				d.Memories = append(d.Memories, AgentMemory{Text: "retained", Source: "owner"})
			}
		}, nil},
		{"empty-note", nil, func(c *AgentCommand) { c.Text = " " }},
		{"oversized-note", nil, func(c *AgentCommand) { c.Text = strings.Repeat("界", 1400) }},
		{"expired-note", nil, func(c *AgentCommand) { c.ExpiresAt = 1 }},
		{"negative-expiry", nil, func(c *AgentCommand) { c.ExpiresAt = -1 }},
		{"system-message", nil, func(c *AgentCommand) { c.Source = "message"; c.ID = "system-source" }},
		{"foreign-message", nil, func(c *AgentCommand) { c.Source = "message"; c.ID = "user-source"; c.ProfileID = "news-researcher" }},
		{"ambiguous-message", func(d *agentDocument) { d.Messages = append(d.Messages, d.Messages[0]) }, func(c *AgentCommand) { c.Source = "message"; c.ID = "user-source" }},
		{"oversized-reference", func(d *agentDocument) {
			d.Sessions[0].ID = strings.Repeat("s", 301)
			d.AIRequests[0].SessionID = d.Sessions[0].ID
		}, nil},
		{"stale", nil, nil}, {"paused", nil, nil},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := fixtureStore(t)
			v := reminderFixture(t, s)
			rewriteReminderFixture(t, s, func(d *agentDocument) {
				d.Messages = append(d.Messages, AgentMessage{ID: "user-source", ProfileID: v.ProfileID, SessionID: v.Sessions[0].ID, Role: "user", Text: "original"}, AgentMessage{ID: "system-source", ProfileID: v.ProfileID, SessionID: v.Sessions[0].ID, Role: "system", Text: "acknowledgement"})
				if tt.doc != nil {
					tt.doc(d)
				}
			})
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			c := AgentCommand{Action: "memory-chat-save", ProfileID: v.ProfileID, Revision: v.Revision, ID: v.AIRequests[0].ID, Source: "answer", Text: "Owner edited"}
			if tt.cmd != nil {
				tt.cmd(&c)
			}
			if tt.name == "stale" {
				agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, Text: "another change", Source: "owner"})
			}
			if tt.name == "paused" {
				if _, err := s.db.Exec("UPDATE update_pause SET phase='paused'"); err != nil {
					t.Fatal(err)
				}
			}
			before, revision, _ := readAgents(t.Context(), s.db)
			if _, err := s.ChangeAgents(t.Context(), c); err == nil {
				t.Fatal("unsafe memory accepted")
			}
			after, next, _ := readAgents(t.Context(), s.db)
			if revision != next || !reflect.DeepEqual(before, after) {
				t.Fatal("refusal changed retained state")
			}
		})
	}
}

func TestChatMemoryCorrectionExpiryRestartAndDelete(t *testing.T) {
	s := fixtureStore(t)
	now := time.Unix(1800000000, 0)
	s.now = func() time.Time { return now }
	v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "Preferences"})
	session := v.Sessions[0].ID
	v = agentApply(t, s, AgentCommand{Action: "message-add", ProfileID: v.ProfileID, ID: session, Text: "Original message"})
	original := v.Messages[0]
	v = agentApply(t, s, AgentCommand{Action: "memory-chat-save", ProfileID: v.ProfileID, ID: original.ID, Source: "message", Text: "First owner note", ExpiresAt: now.Unix() + 60})
	note := v.Memories[0]
	digest := fmt.Sprintf("sha256=%x", sha256.Sum256([]byte(original.Text)))
	if !strings.Contains(note.Source, digest) || strings.Contains(note.Source, "First owner note") {
		t.Fatal("source hash does not bind original", note)
	}
	v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: note.ID, Text: "Corrected owner note", Source: note.Source, ExpiresAt: note.ExpiresAt})
	if len(v.Memories[0].History) != 1 || v.Memories[0].History[0].Text != note.Text || v.Memories[0].History[0].Source != note.Source {
		t.Fatal("lost correction evidence")
	}
	v = agentApply(t, s, AgentCommand{Action: "profile-update", ProfileID: v.ProfileID, Name: "Analyst", Provider: "xai", Text: "Research"})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopened.now = func() time.Time { return now }
	v, err = reopened.Agents(t.Context(), v.ProfileID, "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Memories[0].Text != "Corrected owner note" || v.Memories[0].Source != note.Source || v.Messages[0] != original || len(v.AIRequests) != 0 {
		t.Fatal("restart changed memory/source", v)
	}
	other, _ := reopened.Agents(t.Context(), "news-researcher", "")
	if len(other.Memories) != 0 {
		t.Fatal("memory leaked")
	}
	now = now.Add(61 * time.Second)
	v, _ = reopened.Agents(t.Context(), v.ProfileID, "")
	if !v.Memories[0].Expired {
		t.Fatal("expiry not exposed")
	}
	v = agentApply(t, reopened, AgentCommand{Action: "memory-delete", ProfileID: v.ProfileID, ID: note.ID})
	if len(v.Memories) != 0 || len(v.Messages) != 2 || len(v.Tasks) != 0 || len(v.Runs) != 0 || len(v.Notices) != 0 {
		t.Fatal("deletion affected original records", v)
	}
}
