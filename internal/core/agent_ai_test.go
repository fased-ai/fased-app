package core

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type aiTestTransport func(*http.Request) (*http.Response, error)

func (f aiTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func aiPrepare(t *testing.T, s *Store) AgentView {
	t.Helper()
	p := "trading-analyst"
	v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: p, Name: "AI review"})
	return agentApply(t, s, AgentCommand{Action: "ai-prepare", ProfileID: p, ID: v.Sessions[len(v.Sessions)-1].ID, Text: "Compare a paper strategy", Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 3, BudgetMicrousd: 30000}})
}
func aiStart(t *testing.T, s *Store, v AgentView) AgentView {
	t.Helper()
	q := v.AIRequests[len(v.AIRequests)-1]
	n, e := s.changeAgents(t.Context(), AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, Revision: v.Revision, ID: q.ID, ConsentDigest: q.Digest}, true)
	if e != nil {
		t.Fatal(e)
	}
	return n
}
func TestAgentAIConsentContextRoundTripAndReview(t *testing.T) {
	s := fixtureStore(t)
	agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: "news-researcher", Text: "other-profile-secret", Source: "private"})
	agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: "trading-analyst", Text: "expired-note", Source: "old", ExpiresAt: 1})
	agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: "trading-analyst", Text: "current-note", Source: "owner"})
	v := aiPrepare(t, s)
	q := v.AIRequests[0]
	if strings.Contains(q.Context, "other-profile-secret") || strings.Contains(q.Context, "expired-note") || !strings.Contains(q.Context, "current-note") {
		t.Fatal("disclosure scope")
	}
	if q.HeldMicrousd != 0 || q.State != "prepared" || v.ProviderCallsEnabled {
		t.Fatal("preview executed")
	}
	c := AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, Revision: v.Revision, ID: q.ID, ConsentDigest: q.Digest}
	if _, e := s.ChangeAgents(t.Context(), c); e == nil {
		t.Fatal("ordinary runtime enabled fixture")
	}
	c.ConsentDigest = "wrong"
	if _, e := s.changeAgents(t.Context(), c, true); e == nil {
		t.Fatal("missing consent")
	}
	v = aiStart(t, s, v)
	calls := 0
	rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		b, _ := io.ReadAll(r.Body)
		var wire map[string]any
		if json.Unmarshal(b, &wire) != nil {
			t.Fatal("wire")
		}
		if r.URL.String() != "https://api.openai.com/v1/responses" || r.Header.Get("Authorization") != "" || wire["store"] != false || wire["tool_choice"] != "none" || wire["max_output_tokens"] != float64(1024) {
			t.Fatal("unsafe wire")
		}
		r.Body = io.NopCloser(strings.NewReader(string(b)))
		return (fixtureAITransport{}).RoundTrip(r)
	})
	if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
		t.Fatal(e)
	}
	if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	q = v.AIRequests[0]
	if calls != 1 || q.State != "completed" || q.HeldMicrousd != 0 || q.SpentMicrousd <= 0 || q.Answer == "" || len(q.Cards) != 1 || len(v.Tasks) != 0 {
		t.Fatal("completion", q, calls)
	}
	v = agentApply(t, s, AgentCommand{Action: "ai-prepare", ProfileID: v.ProfileID, ID: q.SessionID, Text: "Follow up", Limits: &q.Limits})
	if !strings.Contains(v.AIRequests[1].Context, q.Prompt) || !strings.Contains(v.AIRequests[1].Context, q.Answer) {
		t.Fatal("prior AI question/answer missing from context")
	}
	v = agentApply(t, s, AgentCommand{Action: "ai-card-keep", ProfileID: v.ProfileID, ID: q.Cards[0].ID})
	if v.AIRequests[0].Cards[0].Review != "kept" || len(v.Tasks) != 0 || len(v.Bots) != 0 {
		t.Fatal("card executed")
	}
	other, _ := s.Agents(t.Context(), "news-researcher", "")
	if len(other.AIRequests) != 0 {
		t.Fatal("request disclosure")
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	n, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer n.Close()
	got, _ := n.Agents(t.Context(), v.ProfileID, "")
	if got.AIRequests[0].Cards[0].Review != "kept" {
		t.Fatal("lost review")
	}
}
func TestAgentAIBudgetsStaleConsentCancellationAndRestart(t *testing.T) {
	s := fixtureStore(t)
	v := aiPrepare(t, s)
	q := v.AIRequests[0]
	agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, Text: "changed context", Source: "owner"})
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if _, e := s.changeAgents(t.Context(), AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, Revision: v.Revision, ID: q.ID, ConsentDigest: q.Digest}, true); e == nil {
		t.Fatal("stale disclosure")
	}
	v = aiPrepare(t, s)
	v = aiStart(t, s, v)
	q = v.AIRequests[len(v.AIRequests)-1]
	v = agentApply(t, s, AgentCommand{Action: "ai-cancel", ProfileID: v.ProfileID, ID: q.ID})
	if v.AIRequests[len(v.AIRequests)-1].HeldMicrousd != 0 {
		t.Fatal("undispatched cancellation retained cost")
	}
	v = aiPrepare(t, s)
	v = aiStart(t, s, v)
	rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("do not leak provider-body-or-secret")
	})
	if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	q = v.AIRequests[len(v.AIRequests)-1]
	if q.State != "unknown" || q.HeldMicrousd == 0 || strings.Contains(q.Failure, "provider-body") {
		t.Fatal("uncertain usage released/leaked", q)
	}
	if _, e := s.changeAgents(t.Context(), AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, Revision: v.Revision, ID: q.ID, ConsentDigest: q.Digest}, true); e == nil {
		t.Fatal("uncertain request replayed")
	}
	// A fresh request cannot evade the uncertain hold.
	v = aiPrepare(t, s)
	q = v.AIRequests[len(v.AIRequests)-1]
	if _, e := s.changeAgents(t.Context(), AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, Revision: v.Revision, ID: q.ID, ConsentDigest: q.Digest}, true); e == nil {
		t.Fatal("new request bypassed uncertain hold")
	}
	// A separate disposable store exercises interrupted dispatch/restart.
	s = fixtureStore(t)
	v = aiStart(t, s, aiPrepare(t, s))
	calls := 0
	rt = aiTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		s.bootID = "simulated-new-boot"
		return nil, context.Canceled
	})
	if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
		t.Fatal(e)
	}
	if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatal("replayed")
	}
}
func TestAgentAIActiveCancellationDeadlineAndPause(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "pause"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			v := aiStart(t, s, aiPrepare(t, s))
			q := v.AIRequests[0]
			started := make(chan struct{})
			done := make(chan error, 1)
			rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
				close(started)
				<-r.Context().Done()
				return nil, r.Context().Err()
			})
			go func() { done <- s.advanceAgentAI(t.Context(), rt, true) }()
			<-started
			if mode == "cancel" {
				agentApply(t, s, AgentCommand{Action: "ai-cancel", ProfileID: v.ProfileID, ID: q.ID})
			}
			if mode == "pause" {
				if _, e := s.db.Exec("UPDATE update_pause SET phase='paused'"); e != nil {
					t.Fatal(e)
				}
			}
			select {
			case e := <-done:
				if e != nil && !errors.Is(e, ErrCorePaused) {
					t.Fatal(e)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("deadline/cancellation did not stop transport")
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			q = v.AIRequests[0]
			if q.HeldMicrousd == 0 || q.Answer != "" || q.State == "completed" {
				t.Fatal("unsafe interrupted state", q)
			}
			if mode == "pause" {
				if _, e := s.db.Exec("UPDATE update_pause SET phase='idle'"); e != nil {
					t.Fatal(e)
				}
				s.bootID = "restart"
				if e := s.advanceAgentAI(t.Context(), fixtureAITransport{}, false); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}

func TestAgentAIValidationAndKnownUsageFailures(t *testing.T) {
	for _, mode := range []string{"refusal", "incomplete", "tool", "malformed", "usage", "identity", "large", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			v := aiStart(t, s, aiPrepare(t, s))
			rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
				res, e := (fixtureAITransport{}).RoundTrip(r)
				if e != nil {
					return nil, e
				}
				defer res.Body.Close()
				b, _ := io.ReadAll(res.Body)
				var j map[string]any
				_ = json.Unmarshal(b, &j)
				switch mode {
				case "refusal":
					j["output"] = []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]string{"type": "refusal", "refusal": "no"}}}}
				case "incomplete":
					j["status"] = "incomplete"
				case "tool":
					j["output"] = []any{map[string]string{"type": "function_call", "name": "sign_transaction"}}
				case "malformed":
					j["output"] = []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]string{"type": "output_text", "text": `{"answer":"bad","cards":[{"kind":"trade","title":"send","text":"send funds"}]}`}}}}
				case "usage":
					delete(j, "usage")
				case "identity":
					j["model"] = "another-model"
				case "large":
					b = []byte(strings.Repeat("x", 16385))
				case "redirect":
					res.StatusCode = 302
					res.Header.Set("Location", "https://untrusted.invalid/")
				}
				if mode != "large" {
					b, _ = json.Marshal(j)
				}
				res.Body = io.NopCloser(strings.NewReader(string(b)))
				return res, nil
			})
			if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
				t.Fatal(e)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			q := v.AIRequests[0]
			if q.Answer != "" || len(q.Cards) != 0 || len(v.Tasks) != 0 || q.State == "completed" {
				t.Fatal("unsafe output", q)
			}
			known := mode == "refusal" || mode == "incomplete" || mode == "tool" || mode == "malformed"
			if known && (q.State != "failed" || q.HeldMicrousd != 0 || q.SpentMicrousd <= 0) {
				t.Fatal("known usage not settled", q)
			}
			if !known && (q.State != "unknown" || q.HeldMicrousd == 0) {
				t.Fatal("unverified usage released", q)
			}
		})
	}
}
func TestAgentAIInputBoundsExpiryMigrationAndCapacity(t *testing.T) {
	s := fixtureStore(t)
	v := aiPrepare(t, s)
	q := v.AIRequests[0]
	for _, l := range []AILimits{{24000, 1024, 3, 1}, {-1, 1024, 3, 30000}, {24000, 99999, 3, 30000}, {24000, 1024, 30, 30000}} {
		if _, e := s.ChangeAgents(t.Context(), AgentCommand{Action: "ai-prepare", Revision: v.Revision, ProfileID: v.ProfileID, ID: q.SessionID, Text: "bounded", Limits: &l}); e == nil {
			t.Fatal("invalid budget accepted")
		}
	}
	s.now = func() time.Time { return time.Unix(q.CreatedAt+601, 0) }
	if _, e := s.changeAgents(t.Context(), AgentCommand{Action: "ai-start", Revision: v.Revision, ProfileID: v.ProfileID, ID: q.ID, ConsentDigest: q.Digest}, true); e == nil {
		t.Fatal("expired consent")
	}
	for _, paused := range []bool{false, true} {
		t.Run(map[bool]string{false: "schema4-idle", true: "schema4-paused"}[paused], func(t *testing.T) {
			s := fixtureStore(t)
			v := aiPrepare(t, s)
			if _, e := s.db.Exec("PRAGMA user_version=4"); e != nil {
				t.Fatal(e)
			}
			if paused {
				if _, e := s.db.Exec("UPDATE update_pause SET phase='paused'"); e != nil {
					t.Fatal(e)
				}
			}
			if e := s.Close(); e != nil {
				t.Fatal(e)
			}
			n, e := OpenStore(s.dir)
			if paused {
				if e == nil {
					n.Close()
					t.Fatal("migrated paused schema4")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			defer n.Close()
			got, e := n.Agents(t.Context(), v.ProfileID, "")
			if e != nil || got.AIRequests[0].Digest != v.AIRequests[0].Digest {
				t.Fatal("migration lost request")
			}
		})
	}
}

func TestAgentAISnapshotDispatchFreshnessAndRestart(t *testing.T) {
	s := fixtureStore(t)
	v := aiStart(t, s, aiPrepare(t, s))
	q := v.AIRequests[0]
	before, e := s.UpdateSnapshot(t.Context(), update.NewProbe())
	if e != nil {
		t.Fatal(e)
	}
	agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, Text: "new correction before dispatch", Source: "owner"})
	calls := 0
	rt := aiTestTransport(func(r *http.Request) (*http.Response, error) { calls++; return (fixtureAITransport{}).RoundTrip(r) })
	if e = s.advanceAgentAI(t.Context(), rt, true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if calls != 0 || v.AIRequests[0].State != "cancelled" || v.AIRequests[0].HeldMicrousd != 0 {
		t.Fatal("stale context dispatched")
	}
	v = aiStart(t, s, aiPrepare(t, s))
	q = v.AIRequests[len(v.AIRequests)-1]
	// Reconstruct an exact crash boundary: reservation+dispatch durably committed,
	// no response committed. No actual provider or owner installation is used.
	tx, e := s.db.BeginTx(t.Context(), nil)
	if e != nil {
		t.Fatal(e)
	}
	d, r, e := readAgents(t.Context(), tx)
	if e != nil {
		t.Fatal(e)
	}
	d.AIRequests[len(d.AIRequests)-1].State = "running"
	d.AIRequests[len(d.AIRequests)-1].DispatchBoot = s.bootID
	if e = saveAgents(t.Context(), tx, d, r); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	n, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer n.Close()
	if e = n.advanceAgentAI(t.Context(), rt, false); e != nil {
		t.Fatal(e)
	}
	v, _ = n.Agents(t.Context(), v.ProfileID, "")
	q = v.AIRequests[len(v.AIRequests)-1]
	if calls != 0 || q.State != "unknown" || q.HeldMicrousd == 0 {
		t.Fatal("restart retried/released dispatched request")
	}
	after, e := n.UpdateSnapshot(t.Context(), update.NewProbe())
	if e != nil || before.State.Anchor == after.State.Anchor {
		t.Fatal("request ledger missing from snapshot", e)
	}
	// Metadata capacity leaves room for a bounded in-flight result. Other writes
	// cannot consume that reserved document space.
	tx, e = n.db.BeginTx(t.Context(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	d, r, e = readAgents(t.Context(), tx)
	if e != nil {
		t.Fatal(e)
	}
	d.AIRequests = append(d.AIRequests, AgentAIRequest{State: "running"})
	b, _ := json.Marshal(d)
	d.Profiles[0].Persona = strings.Repeat("x", agentDocumentLimit-len(b)-1000)
	if e = saveAgents(t.Context(), tx, d, r); e == nil {
		t.Fatal("writes consumed result capacity")
	}
}

func TestAgentAIRequiredFieldsAndEscapedResultCapacity(t *testing.T) {
	for _, mode := range []string{"missing-input-usage", "null-output-usage", "missing-cards", "null-cards"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			v := aiStart(t, s, aiPrepare(t, s))
			rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
				res, e := (fixtureAITransport{}).RoundTrip(r)
				if e != nil {
					return nil, e
				}
				defer res.Body.Close()
				b, _ := io.ReadAll(res.Body)
				var j map[string]any
				_ = json.Unmarshal(b, &j)
				switch mode {
				case "missing-input-usage":
					u := j["usage"].(map[string]any)
					delete(u, "input_tokens")
					u["total_tokens"] = u["output_tokens"]
				case "null-output-usage":
					u := j["usage"].(map[string]any)
					u["output_tokens"] = nil
					u["total_tokens"] = u["input_tokens"]
				default:
					out := `{"answer":"review only"}`
					if mode == "null-cards" {
						out = `{"answer":"review only","cards":null}`
					}
					j["output"] = []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]string{"type": "output_text", "text": out}}}}
				}
				b, _ = json.Marshal(j)
				res.Body = io.NopCloser(strings.NewReader(string(b)))
				return res, nil
			})
			if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
				t.Fatal(e)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			q := v.AIRequests[0]
			if strings.Contains(mode, "usage") {
				if q.State != "unknown" || q.HeldMicrousd == 0 {
					t.Fatal("missing/null usage accepted")
				}
			} else if q.State != "failed" || q.Answer != "" || q.SpentMicrousd == 0 {
				t.Fatal("required cards array accepted as absent/null")
			}
		})
	}
	t.Run("escaped-result-capacity", func(t *testing.T) {
		s := fixtureStore(t)
		v := aiPrepare(t, s)
		tx, e := s.db.BeginTx(t.Context(), nil)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		d, r, e := readAgents(t.Context(), tx)
		if e != nil {
			t.Fatal(e)
		}
		q := d.AIRequests[0]
		before, _ := json.Marshal(q)
		q.State = "completed"
		q.Answer = strings.Repeat("a", 2000)
		for n := 0; n < 3; n++ {
			q.Cards = append(q.Cards, AIActionCard{ID: q.ID + string(rune('1'+n)), Kind: "reminder-draft", Title: strings.Repeat("t", 80), Text: strings.Repeat("\x01", 500) + strings.Repeat("b", 500), Review: "unreviewed"})
		}
		after, _ := json.Marshal(q)
		growth := len(after) - len(before)
		b, _ := json.Marshal(d)
		d.Profiles[0].Persona = strings.Repeat("p", agentDocumentLimit-len(b)-growth+100)
		if e = saveAgents(t.Context(), tx, d, r); e == nil {
			t.Fatal("metadata consumed space needed for an admissible escaped result", v.ProfileID, growth)
		}
	})
}

func TestAgentAISearchHistorySurvivesProviderChangesAndRestart(t *testing.T) {
	s := fixtureStore(t)
	v := aiStart(t, s, aiPrepare(t, s))
	profile, session := v.ProfileID, v.AIRequests[0].SessionID
	if e := s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); e != nil {
		t.Fatal(e)
	}
	v = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: profile, Name: "Ordinary history"})
	ordinary := v.Sessions[len(v.Sessions)-1].ID
	agentApply(t, s, AgentCommand{Action: "message-add", ProfileID: profile, ID: ordinary, Text: "ordinary-search-control"})
	check := func(store *Store) {
		t.Helper()
		for query, id := range map[string]string{"COMPARE A PAPER STRATEGY": session, "NO MODEL, MARKET FEED OR WALLET WAS CONTACTED.": session, "ordinary-search-control": ordinary, "ORDINARY HISTORY": ordinary} {
			got, e := store.Agents(t.Context(), profile, query)
			if e != nil || len(got.Sessions) != 1 || got.Sessions[0].ID != id || got.ProviderCallsEnabled || got.FinancialExecution {
				t.Fatalf("query %q lost scoped history: sessions=%v error=%v", query, got.Sessions, e)
			}
			other, e := store.Agents(t.Context(), "news-researcher", query)
			if e != nil || len(other.Sessions) != 0 || len(other.Messages) != 0 || len(other.AIRequests) != 0 {
				t.Fatal("cross-profile search disclosure", query, e)
			}
		}
	}
	check(s)
	for _, provider := range []string{"openai", "anthropic", "xai"} {
		agentApply(t, s, AgentCommand{Action: "profile-update", ProfileID: profile, Name: "Trading Analyst", Text: "Paper research", Provider: provider, Model: "preference-only"})
		check(s)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	check(reopened)
}

func TestAgentAISearchScopeAndCompletedManualHistory(t *testing.T) {
	d := initialAgents()
	d.Sessions = []AgentSession{{ID: "own", ProfileID: "trading-analyst", Name: "Own"}, {ID: "other", ProfileID: "news-researcher", Name: "Other"}}
	d.AIRequests = []AgentAIRequest{
		{ID: "complete", ProfileID: "trading-analyst", SessionID: "own", State: "completed", Prompt: "question-only", Answer: "answer-only", Disclosure: "disclosure-only", Context: "context-only"},
		{ID: "foreign", ProfileID: "news-researcher", SessionID: "other", State: "completed", Prompt: "foreign-only"},
		{ID: "foreign-link", ProfileID: "news-researcher", SessionID: "own", State: "completed", Prompt: "foreign-link-only"},
		{ID: "task", ProfileID: "trading-analyst", SessionID: "own", TaskID: "task-1", State: "completed", Prompt: "task-only"},
	}
	for _, state := range []string{"prepared", "queued", "running", "cancelled", "failed", "unknown"} {
		d.AIRequests = append(d.AIRequests, AgentAIRequest{ID: state, ProfileID: "trading-analyst", SessionID: "own", State: state, Prompt: state + "-only", Answer: state + "-answer"})
	}
	for _, query := range []string{"question-only", "answer-only", "foreign-only", "foreign-link-only", "task-only", "context-only", "disclosure-only", "prepared-only", "queued-only", "running-only", "cancelled-only", "failed-only", "unknown-only", "unknown-answer", "missing"} {
		t.Run(query, func(t *testing.T) {
			v, e := agentView(d, 1, "trading-analyst", query, 1)
			if e != nil {
				t.Fatal(e)
			}
			want := 0
			if query == "question-only" || query == "answer-only" {
				want = 1
			}
			if len(v.Sessions) != want {
				t.Fatalf("query %q matched %d sessions, want %d", query, len(v.Sessions), want)
			}
		})
	}
}

func TestChatMemoryFixtureContextCorrectionExpiryAndDeletion(t *testing.T) {
	s := fixtureStore(t)
	now := time.Unix(1800000000, 0)
	s.now = func() time.Time { return now }
	v := reminderFixture(t, s)
	source := v.AIRequests[0]
	v = agentApply(t, s, AgentCommand{Action: "memory-chat-save", ProfileID: v.ProfileID, ID: source.ID, Source: "answer", Text: "Owner preference: concise answers", ExpiresAt: now.Unix() + 60})
	note := v.Memories[0]
	prepare := func() AgentView {
		return agentApply(t, s, AgentCommand{Action: "ai-prepare", ProfileID: v.ProfileID, ID: source.SessionID, Text: "Use my preferences", Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 3, BudgetMicrousd: 30000}})
	}
	memories := func(v AgentView) []struct{ Text, Source string } {
		t.Helper()
		var context struct {
			Memories []struct{ Text, Source string }
		}
		if err := json.Unmarshal([]byte(v.AIRequests[len(v.AIRequests)-1].Context), &context); err != nil {
			t.Fatal(err)
		}
		return context.Memories
	}
	v = prepare()
	m := memories(v)
	if len(m) != 1 || m[0].Text != note.Text || m[0].Source != note.Source {
		t.Fatal("review omitted memory source", m)
	}
	q := v.AIRequests[len(v.AIRequests)-1]
	if _, err := s.ChangeAgents(t.Context(), AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, Revision: v.Revision, ID: q.ID, ConsentDigest: q.Digest}); err == nil {
		t.Fatal("memory enabled real execution")
	}
	v = aiStart(t, s, v)
	calls := 0
	transport := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		b, _ := io.ReadAll(r.Body)
		var wire map[string]any
		if json.Unmarshal(b, &wire) != nil || wire["instructions"] != aiInstructions || !strings.Contains(string(b), note.Source) || !strings.Contains(string(b), note.Text) {
			t.Fatal("wire lost untrusted source-backed context")
		}
		r.Body = io.NopCloser(strings.NewReader(string(b)))
		return (fixtureAITransport{}).RoundTrip(r)
	})
	if err := s.advanceAgentAI(t.Context(), transport, true); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: note.ID, Text: "Owner corrected preference", Source: note.Source, ExpiresAt: note.ExpiresAt})
	v = prepare()
	m = memories(v)
	if len(m) != 1 || m[0].Text != "Owner corrected preference" {
		t.Fatal("correction not effective", m)
	}
	now = now.Add(61 * time.Second)
	v = prepare()
	if len(memories(v)) != 0 {
		t.Fatal("expired note disclosed")
	}
	v = agentApply(t, s, AgentCommand{Action: "memory-delete", ProfileID: v.ProfileID, ID: note.ID})
	v = prepare()
	if len(memories(v)) != 0 {
		t.Fatal("deleted note disclosed")
	}
}
