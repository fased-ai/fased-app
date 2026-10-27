package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAgentRunChatAIHistoryAndExplicitExplanation(t *testing.T) {
	for _, mode := range []string{"completed", "unknown", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			agentApply(t, s, AgentCommand{Action: "profile-update", ProfileID: "trading-analyst", Name: "Trading Analyst", Provider: "none", Text: "PRIVATE-CONTEXT-MARKER"})
			v := taskAIAuthorize(t, s, taskAIPlan(t, s, 1))
			c := AgentCommand{Action: "task-run", ProfileID: v.ProfileID, Revision: v.Revision, ID: v.Tasks[0].ID}
			var err error
			v, err = s.changeAgents(t.Context(), c, mode != "disabled")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if mode == "unknown" {
					return nil, errors.New("PRIVATE-PROVIDER-BODY")
				}
				return (fixtureAITransport{}).RoundTrip(r)
			})
			if err := s.advanceAgentAI(t.Context(), rt, mode != "disabled"); err != nil {
				t.Fatal(err)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			before, rev, _ := readAgents(t.Context(), s.db)
			// A historical capture must still work after the original consent expires.
			later := s.now().Add(2 * time.Hour)
			s.now = func() time.Time { return later }
			v = agentApply(t, s, AgentCommand{Action: "task-run-explain", ProfileID: v.ProfileID, ID: v.Runs[0].ID})
			after, next, _ := readAgents(t.Context(), s.db)
			m := v.Messages[len(v.Messages)-1]
			if next != rev+1 || strings.Contains(m.Text, "PRIVATE-CONTEXT-MARKER") || strings.Contains(m.Text, "PRIVATE-PROVIDER-BODY") || strings.Contains(m.Text, "ConnectionRef") || strings.Contains(m.Text, "Disclosure") || strings.Contains(m.Text, `"Context"`) {
				t.Fatal("private context copied")
			}
			if mode != "disabled" && (!strings.Contains(m.Text, before.AIRequests[0].State) || !strings.Contains(m.Text, "InputTokens") || !strings.Contains(m.Text, "Limits")) {
				t.Fatal("missing recorded AI facts")
			}
			if mode == "completed" && !strings.Contains(m.Text, before.AIRequests[0].Answer) {
				t.Fatal("completed answer omitted")
			}
			after.Sessions, after.Messages = before.Sessions, before.Messages
			if !reflect.DeepEqual(before, after) || (mode == "disabled" && calls != 0) || (mode != "disabled" && calls != 1) {
				t.Fatal("capture changed source or dispatched")
			}
			v = agentApply(t, s, AgentCommand{Action: "ai-prepare", ProfileID: v.ProfileID, ID: m.SessionID, Text: "Explain this historical run", Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 3, BudgetMicrousd: 30000}})
			q := v.AIRequests[len(v.AIRequests)-1]
			if !strings.Contains(q.Context, "Historical reminder run") || q.State != "prepared" {
				t.Fatal("missing explicit disclosure")
			}
			if _, err := s.ChangeAgents(t.Context(), AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, Revision: v.Revision, ID: q.ID, ConsentDigest: q.Digest}); err == nil {
				t.Fatal("ordinary core enabled AI")
			}
			if mode == "completed" {
				v = aiStart(t, s, v)
				if err := s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
					t.Fatal(err)
				}
				v, _ = s.Agents(t.Context(), v.ProfileID, "")
				if v.AIRequests[len(v.AIRequests)-1].State != "completed" || !reflect.DeepEqual(v.Tasks[0], before.Tasks[0]) {
					t.Fatal("manual explanation changed task")
				}
			}
		})
	}
}

func TestAgentRunChatAIReferenceRefusals(t *testing.T) {
	cases := []struct {
		name   string
		change func(*agentDocument)
	}{
		{"missing", func(d *agentDocument) { d.AIRequests = nil }},
		{"ambiguous", func(d *agentDocument) { d.AIRequests = append(d.AIRequests, d.AIRequests[0]) }},
		{"foreign", func(d *agentDocument) { d.AIRequests[0].ProfileID = "news-researcher" }},
		{"wrong-task", func(d *agentDocument) { d.AIRequests[0].TaskID = "other" }},
		{"changed-wire", func(d *agentDocument) { d.AIRequests[0].Prompt = "changed" }},
		{"changed-consent", func(d *agentDocument) { d.Tasks[0].AI.Digest = "changed" }},
		{"changed-task", func(d *agentDocument) { d.Tasks[0].Text = "changed" }},
		{"wrong-binding", func(d *agentDocument) { d.AIRequests[0].TaskDigest = "changed" }},
		{"in-progress", func(d *agentDocument) { d.AIRequests[0].State = "running" }},
		{"missing-session", func(d *agentDocument) { d.Sessions = nil }},
		{"foreign-session", func(d *agentDocument) { d.Sessions[0].ProfileID = "news-researcher" }},
		{"ambiguous-session", func(d *agentDocument) { d.Sessions = append(d.Sessions, d.Sessions[0]) }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := fixtureStore(t)
			v := taskAIRun(t, s, taskAIAuthorize(t, s, taskAIPlan(t, s, 1)))
			if err := s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
				t.Fatal(err)
			}
			rewriteReminderFixture(t, s, tt.change)
			d, rev, _ := readAgents(t.Context(), s.db)
			if _, err := s.ChangeAgents(t.Context(), AgentCommand{Action: "task-run-explain", ProfileID: v.ProfileID, Revision: rev, ID: d.Runs[0].ID}); err == nil {
				t.Fatal("invalid request joined")
			}
			after, next, _ := readAgents(t.Context(), s.db)
			if rev != next || !reflect.DeepEqual(d, after) {
				t.Fatal("refusal mutated state")
			}
		})
	}
}

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
			dropEmptyHistoryFixture(t, s)
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
		fixtureProfileProvider(t, s, AgentCommand{Action: "profile-update", ProfileID: profile, Name: "Trading Analyst", Text: "Paper research", Provider: provider})
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

// This is a command fixture, not a credential or model transport.
func memorySelectionCommand(t *testing.T, session string, selection any) AgentCommand {
	t.Helper()
	b, err := json.Marshal(map[string]any{"action": "ai-prepare", "profileId": "trading-analyst", "id": session, "text": "Review selected context", "memorySelection": selection, "limits": AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 3, BudgetMicrousd: 30000}})
	if err != nil {
		t.Fatal(err)
	}
	var c AgentCommand
	if err = json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}
func TestAgentAIMemorySelectionDisclosure(t *testing.T) {
	for _, mode := range []string{"all", "none", "selected"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "Selection"})
			session := v.Sessions[0].ID
			v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, Text: "selected-private-note", Source: "owner-reference", ExpiresAt: s.now().Unix() + 300})
			note := v.Memories[0]
			agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, Text: "unselected-private-note", Source: "other-reference"})
			agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: "news-researcher", Text: "foreign-private-note", Source: "foreign"})
			agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, Text: "expired-private-note", Source: "expired", ExpiresAt: 1})
			selection := map[string]any{"mode": mode}
			if mode == "selected" {
				selection["ids"] = []string{note.ID}
			}
			v = agentApply(t, s, memorySelectionCommand(t, session, selection))
			q := v.AIRequests[0]
			var envelope struct {
				Version   string `json:"version"`
				Selection struct {
					Mode string
					IDs  []string
				} `json:"memorySelection"`
				Context struct {
					Memories []struct {
						ID, Text, Source     string
						UpdatedAt, ExpiresAt int64
					}
				} `json:"context"`
			}
			if err := json.Unmarshal([]byte(q.Context), &envelope); err != nil {
				t.Fatal(err)
			}
			want := 1
			if mode == "all" {
				want = 2
			}
			if mode == "none" {
				want = 0
			}
			if envelope.Version != "manual-ai-memory-v1" || envelope.Selection.Mode != mode || len(envelope.Context.Memories) != want {
				t.Fatal("selection not frozen", q.Context)
			}
			if want > 0 {
				m := envelope.Context.Memories[0]
				for _, candidate := range envelope.Context.Memories {
					if candidate.ID == note.ID {
						m = candidate
					}
				}
				if m.ID != note.ID || m.Text != note.Text || m.Source != note.Source || m.UpdatedAt != note.UpdatedAt || m.ExpiresAt != note.ExpiresAt {
					t.Fatal("inexact note", m)
				}
			}
			if strings.Contains(q.Context, "foreign-private-note") || strings.Contains(q.Context, "expired-private-note") || (mode != "all" && strings.Contains(q.Context, "unselected-private-note")) || (mode == "none" && strings.Contains(q.Context, note.Text)) {
				t.Fatal("disclosure broadened")
			}
			wire, _ := aiWire(q)
			if q.Disclosure != string(wire) || q.Digest != aiDigest(q) || q.State != "prepared" || q.HeldMicrousd != 0 || v.ProviderCallsEnabled {
				t.Fatal("inert review/digest")
			}
			d, _, _ := readAgents(t.Context(), s.db)
			current, err := aiRequestContext(d, q, s.now().Unix())
			if err != nil || current != q.Context {
				t.Fatal("context does not round trip", err)
			}
			legacy, err := aiContext(d, q.ProfileID, q.SessionID, q.Prompt, s.now().Unix())
			if err != nil || legacy == q.Context || strings.Contains(legacy, "manual-ai-memory-v1") {
				t.Fatal("older-reader refusal or legacy shape")
			}
		})
	}
}

func selectionFixture(t *testing.T, s *Store, mode string) (AgentView, AgentMemory) {
	t.Helper()
	v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "Selection boundary"})
	session := v.Sessions[0].ID
	v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, Text: "selected evidence", Source: "owner", ExpiresAt: s.now().Unix() + 300})
	note := v.Memories[0]
	agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, Text: "undisclosed evidence", Source: "private"})
	choice := map[string]any{"mode": mode}
	if mode == "selected" {
		choice["ids"] = []string{note.ID}
	}
	return agentApply(t, s, memorySelectionCommand(t, session, choice)), note
}
func selectionRefused(t *testing.T, s *Store, c AgentCommand, fixture bool) {
	t.Helper()
	before, rev, err := readAgents(t.Context(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	c.Revision = rev
	if _, err = s.changeAgents(t.Context(), c, fixture); err == nil {
		t.Fatal("selection should refuse", c.Action)
	}
	after, next, err := readAgents(t.Context(), s.db)
	if err != nil || next != rev || !reflect.DeepEqual(before, after) {
		t.Fatal("refusal changed metadata", err)
	}
}
func TestAgentAIMemorySelectionRefusals(t *testing.T) {
	cases := []struct {
		name    string
		choice  func(string, string) any
		corrupt bool
	}{
		{"empty mode", func(a, b string) any { return map[string]any{} }, false},
		{"unknown mode", func(a, b string) any { return map[string]any{"mode": "search"} }, false},
		{"empty subset", func(a, b string) any { return map[string]any{"mode": "selected", "ids": []string{}} }, false},
		{"all with IDs", func(a, b string) any { return map[string]any{"mode": "all", "ids": []string{a}} }, false},
		{"none with IDs", func(a, b string) any { return map[string]any{"mode": "none", "ids": []string{a}} }, false},
		{"missing", func(a, b string) any { return map[string]any{"mode": "selected", "ids": []string{"missing"}} }, false},
		{"foreign", func(a, b string) any { return map[string]any{"mode": "selected", "ids": []string{b}} }, false},
		{"duplicate input", func(a, b string) any { return map[string]any{"mode": "selected", "ids": []string{a, a}} }, false},
		{"ambiguous retained", func(a, b string) any { return map[string]any{"mode": "selected", "ids": []string{a}} }, true},
		{"ambiguous all", func(a, b string) any { return map[string]any{"mode": "all"} }, true},
		{"oversized IDs", func(a, b string) any {
			ids := make([]string, 101)
			return map[string]any{"mode": "selected", "ids": ids}
		}, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := fixtureStore(t)
			v, note := selectionFixture(t, s, "none")
			other := agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: "news-researcher", Text: "other profile", Source: "private"}).Memories[0]
			if tt.corrupt {
				rewriteReminderFixture(t, s, func(d *agentDocument) {
					copy := d.Memories[0]
					copy.ProfileID = "news-researcher"
					d.Memories = append(d.Memories, copy)
				})
			}
			selectionRefused(t, s, memorySelectionCommand(t, v.Sessions[0].ID, tt.choice(note.ID, other.ID)), false)
		})
	}
}
func TestAgentAIMemorySelectionRevalidation(t *testing.T) {
	mutations := []struct {
		name   string
		change func(*agentDocument)
	}{
		{"text", func(d *agentDocument) { d.Memories[0].Text = "corrected" }},
		{"source", func(d *agentDocument) { d.Memories[0].Source = "corrected source" }},
		{"expiry", func(d *agentDocument) { d.Memories[0].ExpiresAt++ }},
		{"expired", func(d *agentDocument) { d.Memories[0].ExpiresAt = 1 }},
		{"updated", func(d *agentDocument) { d.Memories[0].UpdatedAt++ }},
		{"deleted", func(d *agentDocument) { d.Memories = d.Memories[1:] }},
		{"foreign", func(d *agentDocument) { d.Memories[0].ProfileID = "news-researcher" }},
		{"ambiguous", func(d *agentDocument) { d.Memories = append(d.Memories, d.Memories[0]) }},
	}
	for _, stage := range []string{"consent", "dispatch"} {
		for _, tt := range mutations {
			t.Run(stage+"/"+tt.name, func(t *testing.T) {
				s := fixtureStore(t)
				v, _ := selectionFixture(t, s, "selected")
				q := v.AIRequests[0]
				if stage == "dispatch" {
					v = aiStart(t, s, v)
				}
				rewriteReminderFixture(t, s, tt.change)
				if stage == "consent" {
					selectionRefused(t, s, AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, ID: q.ID, ConsentDigest: q.Digest}, true)
					return
				}
				calls := 0
				rt := aiTestTransport(func(r *http.Request) (*http.Response, error) { calls++; return (fixtureAITransport{}).RoundTrip(r) })
				if err := s.advanceAgentAI(t.Context(), rt, true); err != nil {
					t.Fatal(err)
				}
				v, _ = s.Agents(t.Context(), v.ProfileID, "")
				if calls != 0 || v.AIRequests[0].State != "cancelled" || v.AIRequests[0].HeldMicrousd != 0 {
					t.Fatal("stale memory dispatched", calls, v.AIRequests[0].State)
				}
			})
		}
	}
	t.Run("clock expiry", func(t *testing.T) {
		s := fixtureStore(t)
		now := s.now()
		s.now = func() time.Time { return now }
		v, _ := selectionFixture(t, s, "selected")
		q := v.AIRequests[0]
		now = now.Add(300 * time.Second)
		selectionRefused(t, s, AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, ID: q.ID, ConsentDigest: q.Digest}, true)
	})
}
func TestAgentAIMemorySelectionPrivacyRestartAndProvider(t *testing.T) {
	for _, mode := range []string{"none", "selected"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			v, note := selectionFixture(t, s, mode)
			q := v.AIRequests[0]
			// Unselected memory changes must neither broaden nor invalidate disclosure.
			for _, m := range v.Memories {
				if mode == "none" || m.ID != note.ID {
					agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: m.ID, Text: "NEVER-DISCLOSE-THIS", Source: "changed private"})
				}
			}
			agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: "news-researcher", Text: "FOREIGN-SECRET", Source: "private"})
			d, _, _ := readAgents(t.Context(), s.db)
			profile := d.Profiles[agentProfile(d, v.ProfileID)]
			agentApply(t, s, AgentCommand{Action: "profile-update", ProfileID: v.ProfileID, Name: profile.Name, Text: profile.Persona, Provider: "openai"})
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			n, err := OpenStore(s.dir)
			if err != nil {
				t.Fatal(err)
			}
			defer n.Close()
			v, _ = n.Agents(t.Context(), v.ProfileID, "")
			if v.AIRequests[0].Context != q.Context || v.AIRequests[0].Digest != q.Digest || v.ProviderCallsEnabled {
				t.Fatal("restart changed review or enabled AI")
			}
			selectionRefused(t, n, AgentCommand{Action: "ai-start", ProfileID: "news-researcher", ID: q.ID, ConsentDigest: q.Digest}, true)
			selectionRefused(t, n, AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, ID: q.ID, ConsentDigest: q.Digest}, false)
			v = aiStart(t, n, v)
			calls := 0
			rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				b, _ := io.ReadAll(r.Body)
				if strings.Contains(string(b), "NEVER-DISCLOSE-THIS") || strings.Contains(string(b), "FOREIGN-SECRET") || (mode == "none" && strings.Contains(string(b), note.Text)) {
					t.Fatal("undisclosed memory sent")
				}
				r.Body = io.NopCloser(strings.NewReader(string(b)))
				return (fixtureAITransport{}).RoundTrip(r)
			})
			if err = n.advanceAgentAI(t.Context(), rt, true); err != nil {
				t.Fatal(err)
			}
			v, _ = n.Agents(t.Context(), v.ProfileID, "")
			if calls != 1 || v.AIRequests[0].State != "completed" {
				t.Fatal("fixture completion", calls, v.AIRequests[0].State)
			}
		})
	}
}
func TestAgentAIMemorySelectionLegacyBoundsAndUnsupported(t *testing.T) {
	s := fixtureStore(t)
	v, note := selectionFixture(t, s, "selected")
	q := v.AIRequests[0]
	d, _, _ := readAgents(t.Context(), s.db)
	legacy, err := aiContext(d, v.ProfileID, q.SessionID, q.Prompt, s.now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	// Pin the old field order/shape, including no identity/history metadata.
	expected := `{"persona":"Research and compare bounded paper strategies.","messages":[],"memories":[{"text":"selected evidence","source":"owner","expiresAt":` + fmt.Sprint(note.ExpiresAt) + `},{"text":"undisclosed evidence","source":"private","expiresAt":0}],"prompt":"Review selected context"}`
	if legacy != expected {
		t.Fatal("legacy context bytes changed", legacy)
	}
	c := memorySelectionCommand(t, q.SessionID, nil)
	c.MemorySelection = nil
	v = agentApply(t, s, c)
	if v.AIRequests[1].Context != legacy {
		t.Fatal("omitted selection changed legacy context")
	}
	for _, version := range []string{"manual-ai-memory-v2", ""} {
		changed := q
		changed.Context = strings.Replace(q.Context, aiMemoryContextVersion, version, 1)
		if _, err := aiRequestContext(d, changed, s.now().Unix()); err == nil {
			t.Fatal("unsupported context accepted")
		}
	}
	changed := q
	changed.TaskID = "scheduled"
	if _, err := aiRequestContext(d, changed, s.now().Unix()); err == nil {
		t.Fatal("scheduled manual selection accepted")
	}
	// The same 8 KB and token/cost checks apply to explicit selections.
	for _, m := range v.Memories {
		agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: m.ID, Text: strings.Repeat("x", 4000), Source: m.Source})
	}
	selectionRefused(t, s, memorySelectionCommand(t, q.SessionID, map[string]any{"mode": "all"}), false)
	v = agentApply(t, s, memorySelectionCommand(t, q.SessionID, map[string]any{"mode": "none"}))
	if strings.Contains(v.AIRequests[len(v.AIRequests)-1].Context, strings.Repeat("x", 100)) {
		t.Fatal("none disclosed oversized notes")
	}
	c = memorySelectionCommand(t, q.SessionID, map[string]any{"mode": "selected", "ids": []string{note.ID}})
	c.Limits.MaxInputTokens = 1000
	selectionRefused(t, s, c, false)
	c = memorySelectionCommand(t, q.SessionID, map[string]any{"mode": "none"})
	c.Limits.BudgetMicrousd = 1
	selectionRefused(t, s, c, false)
}

func TestAgentAIMemorySelectionAdmissionAndCompletion(t *testing.T) {
	for _, boundary := range []string{"access", "count", "create", "inflight"} {
		t.Run(boundary, func(t *testing.T) {
			s, connection := admissionStore(t)
			v, note := selectionFixture(t, s, "selected")
			c := memorySelectionCommand(t, v.Sessions[0].ID, map[string]any{"mode": "selected", "ids": []string{note.ID}})
			c.Model = admittedOpenAIModel
			c.ConnectionRef = connection.Ref
			v = agentApply(t, s, c)
			q := v.AIRequests[len(v.AIRequests)-1]
			v = aiStart(t, s, v)
			calls := 0
			rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				res, err := (fixtureAITransport{}).RoundTrip(r)
				mutate := (boundary == "access" && r.Method == "GET") || (boundary == "count" && strings.HasSuffix(r.URL.Path, "input_tokens")) || ((boundary == "create" || boundary == "inflight") && r.URL.Path == "/v1/responses")
				if mutate {
					agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: note.ID, Text: "changed during provider operation", Source: note.Source, ExpiresAt: note.ExpiresAt})
					if boundary == "inflight" {
						select {
						case <-r.Context().Done():
						case <-time.After(2 * time.Second):
							t.Fatal("changed selected context did not cancel in-flight operation")
						}
						return nil, r.Context().Err()
					}
				}
				return res, err
			})
			if err := s.advanceAgentAI(t.Context(), rt, true); err != nil {
				t.Fatal(err)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			result := v.AIRequests[len(v.AIRequests)-1]
			want := map[string]int{"access": 1, "count": 2, "create": 3, "inflight": 3}[boundary]
			if calls != want || result.State == "completed" || result.Answer != "" || len(result.Cards) != 0 {
				t.Fatal("stale selection crossed boundary", boundary, calls, result.State)
			}
			if result.ID != q.ID || len(v.Tasks) != 0 {
				t.Fatal("request identity or authority changed")
			}
			if err := s.advanceAgentAI(t.Context(), rt, true); err != nil || calls != want {
				t.Fatal("failed admission replayed", err)
			}
		})
	}
}

func TestAgentAIMemorySelectionNewsRoundTrip(t *testing.T) {
	s := newsTestStore(t)
	v := newsAdd(t, s, "trading-analyst")
	source := v.NewsSources[0]
	v = agentApply(t, s, AgentCommand{Action: "news-fetch", ProfileID: v.ProfileID, ID: source.ID})
	rt := newsHTTPS(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		io.WriteString(w, newsRSS(s.now().Unix()))
	})
	if err := s.advanceAgentNews(t.Context(), rt, false); err != nil {
		t.Fatal(err)
	}
	v, note := selectionFixture(t, s, "selected")
	c := memorySelectionCommand(t, v.Sessions[0].ID, map[string]any{"mode": "selected", "ids": []string{note.ID}})
	c.NewsSourceID = source.ID
	c.NewsMaxAgeSeconds = 300
	v = agentApply(t, s, c)
	q := v.AIRequests[len(v.AIRequests)-1]
	d, _, _ := readAgents(t.Context(), s.db)
	current, err := aiRequestContext(d, q, s.now().Unix())
	if err != nil || q.News == nil || current != q.Context || !strings.Contains(current, `"untrusted":true`) || strings.Contains(current, "undisclosed evidence") {
		t.Fatal("selected/news context did not round trip", err)
	}
	v = aiStart(t, s, v)
	if err = s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if v.AIRequests[len(v.AIRequests)-1].State != "completed" {
		t.Fatal("news/selected fixture failed")
	}
	v = agentApply(t, s, c)
	q = v.AIRequests[len(v.AIRequests)-1]
	agentApply(t, s, AgentCommand{Action: "news-source-disable", ProfileID: v.ProfileID, ID: source.ID})
	selectionRefused(t, s, AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, ID: q.ID, ConsentDigest: q.Digest}, true)
}

func TestAgentAIRequestDraftHTTPReadOnly(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	v := aiPrepare(t, s)
	before, rev, _ := readAgents(t.Context(), s.db)
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:7331/api/v1/agents?profile="+v.ProfileID+"&ai-draft="+v.AIRequests[0].ID, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("draft read got %d: %s", w.Code, w.Body)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"digest", "disclosure", "context", "state", "answer", "cards", "admission", "policy", "heldMicrousd", "spentMicrousd"} {
		if _, ok := fields[forbidden]; ok {
			t.Fatal("draft carries authority/history", forbidden)
		}
	}
	after, next, _ := readAgents(t.Context(), s.db)
	if next != rev || !reflect.DeepEqual(before, after) {
		t.Fatal("draft read wrote state")
	}
}

func draftCommand(v AgentAIRequestDraft) AgentCommand {
	return AgentCommand{Action: "ai-prepare", ProfileID: v.ProfileID, ID: v.SessionID, Text: v.Prompt, Model: v.Model, ConnectionRef: v.ConnectionRef, Limits: &v.Limits, MemorySelection: v.MemorySelection, NewsSourceID: v.NewsSourceID, NewsMaxAgeSeconds: v.NewsMaxAgeSeconds}
}
func TestAgentAIRequestDraftModesFreshReviewAndReadOnly(t *testing.T) {
	for _, mode := range []string{"legacy", "all", "none", "selected"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			var v AgentView
			if mode == "legacy" {
				v = aiPrepare(t, s)
			} else {
				v, _ = selectionFixture(t, s, mode)
			}
			q := v.AIRequests[0]
			before, rev, _ := readAgents(t.Context(), s.db)
			draft, err := s.AgentAIRequestDraft(t.Context(), v.ProfileID, q.ID)
			if err != nil {
				t.Fatal(err)
			}
			if draft.Revision != rev || draft.ProfileID != q.ProfileID || draft.SessionID != q.SessionID || draft.Prompt != q.Prompt || draft.Model != q.Model || draft.Limits != q.Limits || draft.ConnectionRef != q.ConnectionRef {
				t.Fatal("settings differ", draft)
			}
			if mode == "legacy" {
				if draft.MemorySelection != nil {
					t.Fatal("legacy broadened")
				}
			} else if draft.MemorySelection == nil || draft.MemorySelection.Mode != mode {
				t.Fatal("selection lost")
			}
			after, next, _ := readAgents(t.Context(), s.db)
			if next != rev || !reflect.DeepEqual(before, after) {
				t.Fatal("read wrote document")
			}
			// Edits/cancel are caller-owned values; neither can write the source.
			draft.Prompt = "Edited fresh request"
			draft.Limits.MaxOutputTokens = 256
			if draft.MemorySelection != nil && mode == "selected" {
				draft.MemorySelection.IDs[0] = "local-only-edit"
			}
			draft, err = s.AgentAIRequestDraft(t.Context(), v.ProfileID, q.ID)
			if err != nil || draft.Prompt != q.Prompt {
				t.Fatal("draft edits mutated source", err)
			}
			draft.Prompt = "Explicit new question"
			draft.Limits.MaxOutputTokens = 256
			fresh := agentApply(t, s, draftCommand(draft))
			newQ := fresh.AIRequests[1]
			if newQ.ID == q.ID || newQ.Digest == q.Digest || newQ.State != "prepared" || newQ.HeldMicrousd != 0 || newQ.Answer != "" || len(newQ.Cards) != 0 || !reflect.DeepEqual(fresh.AIRequests[0], q) {
				t.Fatal("fresh preparation inherited authority or changed source")
			}
			selectionRefused(t, s, AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, ID: newQ.ID, ConsentDigest: q.Digest}, true)
			selectionRefused(t, s, AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, ID: newQ.ID, ConsentDigest: newQ.Digest}, false)
			fresh = aiStart(t, s, fresh)
			if err = s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
				t.Fatal(err)
			}
			fresh, _ = s.Agents(t.Context(), v.ProfileID, "")
			if fresh.AIRequests[1].State != "completed" {
				t.Fatal("separate fixture consent failed")
			}
		})
	}
}

func TestAgentAIRequestDraftIntegrityAndIsolation(t *testing.T) {
	cases := []struct {
		name   string
		change func(*agentDocument)
		resign bool
	}{
		{"missing", func(d *agentDocument) { d.AIRequests = nil }, false},
		{"duplicate", func(d *agentDocument) { d.AIRequests = append(d.AIRequests, d.AIRequests[0]) }, false},
		{"foreign", func(d *agentDocument) { d.AIRequests[0].ProfileID = "news-researcher" }, true},
		{"scheduled", func(d *agentDocument) { d.AIRequests[0].TaskID = "task" }, true},
		{"task-digest", func(d *agentDocument) { d.AIRequests[0].TaskDigest = "task-consent" }, true},
		{"missing-session", func(d *agentDocument) { d.Sessions = nil }, false},
		{"duplicate-session", func(d *agentDocument) { d.Sessions = append(d.Sessions, d.Sessions[0]) }, false},
		{"foreign-session", func(d *agentDocument) { d.Sessions[0].ProfileID = "news-researcher" }, false},
		{"duplicate-profile", func(d *agentDocument) {
			d.Profiles = append(d.Profiles, d.Profiles[agentProfile(*d, "trading-analyst")])
		}, false},
		{"wire", func(d *agentDocument) { d.AIRequests[0].Disclosure += " " }, false},
		{"digest", func(d *agentDocument) { d.AIRequests[0].Digest = "changed" }, false},
		{"prompt", func(d *agentDocument) { d.AIRequests[0].Prompt = "Changed prompt" }, true},
		{"malformed", func(d *agentDocument) { d.AIRequests[0].Context = "{" }, true},
		{"unknown-version", func(d *agentDocument) {
			d.AIRequests[0].Context = strings.Replace(d.AIRequests[0].Context, aiMemoryContextVersion, "future-context-v2", 1)
		}, true},
		{"missing-version", func(d *agentDocument) {
			d.AIRequests[0].Context = strings.Replace(d.AIRequests[0].Context, `"version":"manual-ai-memory-v1",`, "", 1)
		}, true},
		{"unknown-mode", func(d *agentDocument) {
			d.AIRequests[0].Context = strings.Replace(d.AIRequests[0].Context, `"mode":"selected"`, `"mode":"future"`, 1)
		}, true},
		{"selection-mismatch", func(d *agentDocument) {
			d.AIRequests[0].Context = strings.Replace(d.AIRequests[0].Context, `"mode":"selected"`, `"mode":"none"`, 1)
		}, true},
		{"unknown-field", func(d *agentDocument) {
			d.AIRequests[0].Context = strings.Replace(d.AIRequests[0].Context, `"version":`, `"unrecognized":true,"version":`, 1)
		}, true},
		{"duplicate-key", func(d *agentDocument) {
			d.AIRequests[0].Context = strings.Replace(d.AIRequests[0].Context, `"mode":"selected"`, `"mode":"none","mode":"selected"`, 1)
		}, true},
		{"limits", func(d *agentDocument) { d.AIRequests[0].Limits.DeadlineSeconds = 99 }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := fixtureStore(t)
			v, _ := selectionFixture(t, s, "selected")
			id := v.AIRequests[0].ID
			rewriteReminderFixture(t, s, func(d *agentDocument) {
				tc.change(d)
				if tc.resign && len(d.AIRequests) > 0 {
					q := &d.AIRequests[0]
					wire, _ := aiWire(*q)
					q.Disclosure = string(wire)
					q.Digest = aiDigest(*q)
				}
			})
			before, rev, _ := readAgents(t.Context(), s.db)
			if _, err := s.AgentAIRequestDraft(t.Context(), v.ProfileID, id); err == nil {
				t.Fatal("invalid source accepted")
			}
			after, next, _ := readAgents(t.Context(), s.db)
			if rev != next || !reflect.DeepEqual(before, after) {
				t.Fatal("refusal wrote state")
			}
		})
	}
}

func TestAgentAIRequestDraftStaleMemoryAndHolds(t *testing.T) {
	for _, kind := range []string{"deleted", "expired", "foreign", "ambiguous"} {
		t.Run(kind, func(t *testing.T) {
			s := fixtureStore(t)
			v, note := selectionFixture(t, s, "selected")
			q := v.AIRequests[0]
			rewriteReminderFixture(t, s, func(d *agentDocument) {
				for i, m := range d.Memories {
					if m.ID != note.ID {
						continue
					}
					switch kind {
					case "deleted":
						d.Memories = append(d.Memories[:i], d.Memories[i+1:]...)
					case "expired":
						d.Memories[i].ExpiresAt = 1
					case "foreign":
						d.Memories[i].ProfileID = "news-researcher"
					case "ambiguous":
						d.Memories = append(d.Memories, m)
					}
					break
				}
			})
			before, rev, _ := readAgents(t.Context(), s.db)
			draft, err := s.AgentAIRequestDraft(t.Context(), v.ProfileID, q.ID)
			if err != nil || draft.MemorySelection == nil || len(draft.MemorySelection.IDs) != 1 || draft.MemorySelection.IDs[0] != note.ID {
				t.Fatal("stale ID silently removed", draft, err)
			}
			after, next, _ := readAgents(t.Context(), s.db)
			if next != rev || !reflect.DeepEqual(before, after) {
				t.Fatal("stale read changed data")
			}
			selectionRefused(t, s, draftCommand(draft), false)
			draft.MemorySelection = &AIMemorySelection{Mode: "none"}
			agentApply(t, s, draftCommand(draft))
		})
	}
	for _, state := range []string{"queued", "running", "unknown", "cancelled", "completed", "failed"} {
		t.Run(state, func(t *testing.T) {
			s := fixtureStore(t)
			v := aiPrepare(t, s)
			q := v.AIRequests[0]
			rewriteReminderFixture(t, s, func(d *agentDocument) {
				d.AIRequests[0].State = state
				d.AIRequests[0].HeldMicrousd = 123
				d.AIRequests[0].Answer = "PRIVATE-ANSWER"
			})
			before, _, _ := readAgents(t.Context(), s.db)
			draft, err := s.AgentAIRequestDraft(t.Context(), v.ProfileID, q.ID)
			if err != nil {
				t.Fatal(err)
			}
			fresh := agentApply(t, s, draftCommand(draft))
			newQ := fresh.AIRequests[1]
			if !reflect.DeepEqual(fresh.AIRequests[0], before.AIRequests[0]) || newQ.HeldMicrousd != 0 || newQ.Answer != "" {
				t.Fatal("draft cleared hold/copied result")
			}
			selectionRefused(t, s, AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, ID: newQ.ID, ConsentDigest: newQ.Digest}, true)
		})
	}
}

func TestAgentAIRequestDraftNewsConnectionAndRestart(t *testing.T) {
	s, conn := admissionStore(t)
	v := newsAdd(t, s, "trading-analyst")
	source := v.NewsSources[0]
	agentApply(t, s, AgentCommand{Action: "news-fetch", ProfileID: v.ProfileID, ID: source.ID})
	calls := 0
	if err := s.advanceAgentNews(t.Context(), newsHTTPS(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/rss+xml")
		io.WriteString(w, newsRSS(s.now().Unix()))
	}), false); err != nil {
		t.Fatal(err)
	}
	v = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: v.ProfileID, Name: "Saved connected request"})
	v = agentApply(t, s, AgentCommand{Action: "ai-prepare", ProfileID: v.ProfileID, ID: v.Sessions[0].ID, Text: "Keep my saved choices", ConnectionRef: conn.Ref, Model: openAIModelPolicy().Model, NewsSourceID: source.ID, NewsMaxAgeSeconds: 300, MemorySelection: &AIMemorySelection{Mode: "none"}, Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 512, DeadlineSeconds: 3, BudgetMicrousd: 30000}})
	q := v.AIRequests[0]
	agentApply(t, s, AgentCommand{Action: "news-source-disable", ProfileID: v.ProfileID, ID: source.ID})
	profile := v.Profiles[agentProfile(agentDocument{Profiles: v.Profiles}, v.ProfileID)]
	agentApply(t, s, AgentCommand{Action: "profile-update", ProfileID: v.ProfileID, Name: profile.Name, Text: profile.Persona, Provider: "openai"})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	before, rev, _ := readAgents(t.Context(), n.db)
	draft, err := n.AgentAIRequestDraft(t.Context(), v.ProfileID, q.ID)
	if err != nil || draft.NewsSourceID != source.ID || draft.NewsMaxAgeSeconds != 300 || draft.ConnectionRef != conn.Ref || draft.Model != q.Model || draft.MemorySelection.Mode != "none" {
		t.Fatal("saved settings changed", draft, err)
	}
	after, next, _ := readAgents(t.Context(), n.db)
	if rev != next || !reflect.DeepEqual(before, after) || calls != 1 {
		t.Fatal("read fetched or wrote")
	}
	selectionRefused(t, n, draftCommand(draft), false) // locked connection never substituted
	draft.ConnectionRef = ""
	draft.Model = fixtureAIModel
	selectionRefused(t, n, draftCommand(draft), false) // disabled source never dropped/refetched
	draft.NewsSourceID = ""
	draft.NewsMaxAgeSeconds = 0
	agentApply(t, n, draftCommand(draft))
	if _, err = n.AgentAIRequestDraft(t.Context(), "news-researcher", q.ID); err == nil {
		t.Fatal("foreign source exposed")
	}
	// Historical tariff/model may be unavailable today: retain it visibly, never substitute.
	rewriteReminderFixture(t, n, func(d *agentDocument) {
		r := &d.AIRequests[0]
		r.Model = "retired-snapshot"
		r.Policy.Model = r.Model
		wire, _ := aiWire(*r)
		r.Disclosure = string(wire)
		r.Digest = aiDigest(*r)
	})
	draft, err = n.AgentAIRequestDraft(t.Context(), v.ProfileID, q.ID)
	if err != nil || draft.Model != "retired-snapshot" {
		t.Fatal("unavailable model lost", err)
	}
	draft.ConnectionRef = ""
	draft.NewsSourceID = ""
	draft.NewsMaxAgeSeconds = 0
	selectionRefused(t, n, draftCommand(draft), false)
}

func TestAgentAIRequestDraftHTTPStrict(t *testing.T) {
	s := fixtureStore(t)
	v := aiPrepare(t, s)
	token := claimFixture(t, s)
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	good := "profile=" + v.ProfileID + "&ai-draft=" + v.AIRequests[0].ID
	for _, tc := range []struct {
		query, token, method string
		status               int
	}{
		{good, token, "GET", 200}, {good, "", "GET", 401}, {"profile=news-researcher&ai-draft=" + v.AIRequests[0].ID, token, "GET", 400},
		{"ai-draft=" + v.AIRequests[0].ID, token, "GET", 400}, {"profile=trading-analyst&ai-draft=", token, "GET", 400},
		{good + "&profile=news-researcher", token, "GET", 400}, {good + "&ai-draft=other", token, "GET", 400},
		{good + "&q=search", token, "GET", 400}, {good + "&memory-search=", token, "GET", 400}, {good + "&unknown=true", token, "GET", 400},
		{good, token, "POST", 400}, {"profile=%ff&ai-draft=foo", token, "GET", 400},
	} {
		r := httptest.NewRequest(tc.method, "http://127.0.0.1:7331/api/v1/agents?"+tc.query, nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s got %d want %d: %s", tc.method, tc.query, w.Code, tc.status, w.Body)
		}
	}
}
