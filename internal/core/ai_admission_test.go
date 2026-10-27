package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenAIAdmissionPolicyAndIntegerTariff(t *testing.T) {
	p := openAIModelPolicy()
	q := AgentAIRequest{Model: p.Model, Policy: &p, ConnectionRef: "disposable-connection", Limits: AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024}}
	if e := validateAIModelPolicy(q, time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC).Unix()); e != nil {
		t.Fatal(e)
	}
	if aiRequestReservation(q) != 29120 {
		t.Fatal("fractional tariff must round upward once", aiRequestReservation(q))
	}
	if aiUsageCost(q, 1, 0, 0) != 1 || aiUsageCost(q, 10, 10, 1) != 6 {
		t.Fatal("input/cache/output integer tariff")
	}
	if validateAIModelPolicy(q, p.ExpiresAt) == nil {
		t.Fatal("expired tariff admitted")
	}
	p.InputMicrousdPerMillion++
	if validateAIModelPolicy(q, p.ReviewedAt) == nil {
		t.Fatal("edited tariff admitted")
	}
}

func admissionStore(t *testing.T) (*Store, AIConnectionView) {
	s := fixtureStore(t)
	s.now = func() time.Time { return time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC) }
	return s, saveTestConnection(t, s)
}
func admissionPlan(t *testing.T, s *Store, ref string, scheduled bool) AgentView {
	t.Helper()
	v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "Admission review"})
	c := AgentCommand{Model: admittedOpenAIModel, ConnectionRef: ref, Action: "ai-prepare", ProfileID: v.ProfileID, ID: v.Sessions[len(v.Sessions)-1].ID, Text: "Compare untrusted research", Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 3, BudgetMicrousd: 30000}}
	if scheduled {
		c.Action, c.Name, c.IntervalSeconds, c.MaxRuns, c.AuthorizationSeconds, c.TotalBudgetMicrousd = "task-ai-prepare", "Admitted schedule", 60, 2, 600, 60000
	}
	return agentApply(t, s, c)
}

// A connected scheduled fixture must carry the same finite credential grant as
// the product. Admission failures below still exercise the actual HTTPS path.
func authorizeAdmissionTask(t *testing.T, s *Store, v AgentView) AgentView {
	t.Helper()
	v = taskAIAuthorize(t, s, v)
	task := v.Tasks[len(v.Tasks)-1]
	reviewed, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: task.AI.ConnectionRef, Revision: v.Revision, ProfileID: v.ProfileID, TaskIDs: []string{task.ID}, AuthorizationSeconds: 600})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: task.AI.ConnectionRef, Allowance: reviewed.AccessReview, ConsentDigest: reviewed.AccessReview.ID, Passphrase: testVaultPassword}); e != nil {
		t.Fatal(e)
	}
	return v
}
func admissionReply(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
func admittedResult(input, cached int64) map[string]any {
	return map[string]any{"model": admittedOpenAIModel, "status": "completed", "service_tier": "default", "usage": map[string]any{"input_tokens": input, "output_tokens": 128, "total_tokens": input + 128, "input_tokens_details": map[string]int64{"cached_tokens": cached}}, "output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]string{"type": "output_text", "text": `{"answer":"Controlled admitted answer","cards":[]}`}}}}}
}

func TestOpenAIAdmissionHTTPSJoinedTaskAndRestart(t *testing.T) {
	s, connection := admissionStore(t)
	v := admissionPlan(t, s, connection.Ref, true)
	task := v.Tasks[len(v.Tasks)-1]
	if task.AI.Policy == nil || !strings.Contains(task.AI.Disclosure, admittedOpenAIModel) || task.AI.Authorized {
		t.Fatal("policy missing from unapproved plan")
	}
	v = taskAIRun(t, s, authorizeAdmissionTask(t, s, v))
	var paths []string
	var counted map[string]json.RawMessage
	rt := httpsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testAPIKey {
			t.Error("unbound credential")
		}
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/v1/models/" + admittedOpenAIModel:
			admissionReply(w, map[string]any{"id": admittedOpenAIModel, "object": "model", "created": 1744588800, "owned_by": "system", "shutdown_date": nil})
		case "/v1/responses/input_tokens":
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &counted)
			if counted["input"] == nil || counted["instructions"] == nil || counted["text"] == nil || counted["store"] != nil {
				t.Error("counting omitted input or added unsupported fields")
			}
			admissionReply(w, map[string]any{"object": "response.input_tokens", "input_tokens": 2001})
		case "/v1/responses":
			var body map[string]json.RawMessage
			_ = json.NewDecoder(r.Body).Decode(&body)
			for key, value := range counted {
				if string(body[key]) != string(value) {
					t.Errorf("counted field %s changed", key)
				}
			}
			admissionReply(w, admittedResult(2001, 100))
		default:
			t.Error("unexpected endpoint")
		}
	})
	if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	q := v.AIRequests[0]
	if q.State != "completed" || q.HeldMicrousd != 0 || q.SpentMicrousd != 2546 || q.CachedInputTokens != 100 || q.Admission == nil || q.Admission.InputTokens != 2001 || q.Admission.State != "admitted" || !q.Admission.GenerationAttempted || len(q.Admission.CountPayloadSHA256) != 64 || len(paths) != 3 || v.Tasks[0].Runs != 1 || v.Runs[0].AIRequestID != q.ID {
		t.Fatal("joined admission/usage/dependency", q, paths)
	}
	if e := s.advanceAgentAI(t.Context(), rt, true); e != nil || len(paths) != 3 {
		t.Fatal("replayed admission")
	}
	other, _ := s.Agents(t.Context(), "news-researcher", "")
	if len(other.AIRequests) != 0 {
		t.Fatal("cross-profile admission")
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
	if *got.AIRequests[0].Admission != *q.Admission || *got.AIRequests[0].Policy != *q.Policy || got.AIRequests[0].SpentMicrousd != 2546 {
		t.Fatal("restart lost admission provenance")
	}
	if conn, _ := n.AIConnection(); !conn.Locked || conn.LiveCallsEnabled {
		t.Fatal("restart enabled calls")
	}
}

func TestOpenAIAdmissionHTTPSRefusalsAndDependentIsolation(t *testing.T) {
	for _, mode := range []string{"401", "403", "429", "wrong-model", "shutdown", "missing-created", "count-null", "count-negative", "count-over", "count-fraction", "count-object", "count-duplicate", "redirect", "oversize", "reflection", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			s, c := admissionStore(t)
			v := taskAIRun(t, s, authorizeAdmissionTask(t, s, admissionPlan(t, s, c.Ref, true)))
			var calls, generations atomic.Int32
			rt := httpsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path == "/v1/responses" {
					generations.Add(1)
					t.Error("generation after rejected admission")
					return
				}
				if r.Method == "GET" {
					if mode == "401" {
						w.WriteHeader(401)
						return
					}
					if mode == "403" {
						w.WriteHeader(403)
						return
					}
					if mode == "429" {
						w.WriteHeader(429)
						return
					}
					m := map[string]any{"id": admittedOpenAIModel, "object": "model", "created": 1744588800, "owned_by": "openai"}
					if mode == "wrong-model" {
						m["id"] = "gpt-4.1-mini"
					}
					if mode == "shutdown" {
						m["shutdown_date"] = "2026-12-01"
					}
					if mode == "missing-created" {
						delete(m, "created")
					}
					admissionReply(w, m)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch mode {
				case "count-duplicate":
					io.WriteString(w, `{"object":"response.input_tokens","input_tokens":1,"input_tokens":2001}`)
				case "count-null":
					io.WriteString(w, `{"object":"response.input_tokens","input_tokens":null}`)
				case "count-negative":
					admissionReply(w, map[string]any{"object": "response.input_tokens", "input_tokens": -1})
				case "count-over":
					admissionReply(w, map[string]any{"object": "response.input_tokens", "input_tokens": 24001})
				case "count-fraction":
					io.WriteString(w, `{"object":"response.input_tokens","input_tokens":1.5}`)
				case "count-object":
					admissionReply(w, map[string]any{"object": "other", "input_tokens": 2001})
				case "redirect":
					w.Header().Set("Location", "https://elsewhere.invalid")
					w.WriteHeader(307)
				case "oversize":
					io.WriteString(w, strings.Repeat("x", 8193))
				case "reflection":
					admissionReply(w, map[string]any{"object": "response.input_tokens", "input_tokens": 2001, "secret": testAPIKey})
				case "timeout":
					_, _ = io.Copy(io.Discard, r.Body)
					select {
					case <-r.Context().Done():
					case <-time.After(4 * time.Second):
					}
				}
			})
			if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
				t.Fatal(e)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			q := v.AIRequests[0]
			if q.State != "failed" || q.Admission == nil || q.Admission.State != "refused" || q.Admission.GenerationAttempted || q.HeldMicrousd != 0 || v.Tasks[0].State != "held" || len(v.Runs) != 1 || generations.Load() != 0 {
				t.Fatal("admission refusal did not hold only dependency", q)
			}
			before := calls.Load()
			if e := s.advanceAgentAI(t.Context(), rt, true); e != nil || calls.Load() != before {
				t.Fatal("retry")
			}
			v = agentApply(t, s, AgentCommand{Action: "task-create", ProfileID: v.ProfileID, Name: "Independent", Text: "Still works", IntervalSeconds: 60, MaxRuns: 1})
			v = newsRun(t, s, v)
			if v.Tasks[len(v.Tasks)-1].State != "completed" {
				t.Fatal("admission stopped independent work")
			}
		})
	}
}

func TestOpenAIAdmissionUsageMismatchKeepsReservation(t *testing.T) {
	for _, mode := range []string{"tokens", "cache", "tier", "model", "duplicate", "lost-result"} {
		t.Run(mode, func(t *testing.T) {
			s, c := admissionStore(t)
			v := aiStart(t, s, admissionPlan(t, s, c.Ref, false))
			rt := httpsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					admissionReply(w, map[string]any{"id": admittedOpenAIModel, "object": "model", "created": 1744588800, "owned_by": "openai"})
					return
				}
				if r.URL.Path == "/v1/responses/input_tokens" {
					admissionReply(w, map[string]any{"object": "response.input_tokens", "input_tokens": 2001})
					return
				}
				m := admittedResult(2001, 0)
				switch mode {
				case "duplicate":
					w.Header().Set("Content-Type", "application/json")
					b, _ := json.Marshal(m)
					io.WriteString(w, strings.Replace(string(b), `"input_tokens":2001`, `"input_tokens":1,"input_tokens":2001`, 1))
					return
				case "tokens":
					m = admittedResult(2002, 0)
				case "cache":
					m = admittedResult(2001, 2002)
				case "tier":
					m["service_tier"] = "priority"
				case "model":
					m["model"] = "gpt-4.1-mini"
				case "lost-result":
					w.WriteHeader(503)
					return
				}
				admissionReply(w, m)
			})
			if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
				t.Fatal(e)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			q := v.AIRequests[0]
			if q.State != "unknown" || q.HeldMicrousd != 29120 || q.Admission == nil || !q.Admission.GenerationAttempted || q.SpentMicrousd != 0 {
				t.Fatal("unverified usage released hold", q)
			}
		})
	}
}

func TestOpenAIAdmissionConsentPolicyAndDefaultOff(t *testing.T) {
	s, c := admissionStore(t)
	v := admissionPlan(t, s, c.Ref, false)
	q := v.AIRequests[0]
	if _, e := s.ChangeAgents(t.Context(), AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, Revision: v.Revision, ID: q.ID, ConsentDigest: q.Digest}); e == nil {
		t.Fatal("normal build enabled generation")
	}
	for _, model := range []string{"gpt-4.1-mini", "gpt-other"} {
		if _, e := s.ChangeAgents(t.Context(), AgentCommand{Action: "ai-prepare", ProfileID: v.ProfileID, Revision: v.Revision, ID: q.SessionID, Text: q.Prompt, ConnectionRef: c.Ref, Model: model, Limits: &q.Limits}); e == nil {
			t.Fatal("alias admitted")
		}
	}
	modified := q
	p := *q.Policy
	p.OutputMicrousdPerMillion++
	modified.Policy = &p
	if aiDigest(modified) == q.Digest {
		t.Fatal("tariff missing from consent")
	}
	old := s.now
	s.now = func() time.Time { return time.Unix(q.Policy.ExpiresAt, 0) }
	if _, e := s.changeAgents(t.Context(), AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, Revision: v.Revision, ID: q.ID, ConsentDigest: q.Digest}, true); e == nil {
		t.Fatal("expired admission consent accepted")
	}
	s.now = old
	if _, e := s.db.Exec("UPDATE update_pause SET phase='paused' WHERE id=1; PRAGMA user_version=8"); e != nil {
		t.Fatal(e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	if n, e := OpenStore(s.dir); e == nil {
		n.Close()
		t.Fatal("paused schema-8 migration accepted")
	}
}

func TestOpenAIAdmissionOwnerCancelDuringCount(t *testing.T) {
	s, c := admissionStore(t)
	v := aiStart(t, s, admissionPlan(t, s, c.Ref, false))
	started := make(chan struct{})
	var generations atomic.Int32
	rt := httpsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			admissionReply(w, map[string]any{"id": admittedOpenAIModel, "object": "model", "created": 1744588800, "owned_by": "openai"})
			return
		}
		if r.URL.Path == "/v1/responses/input_tokens" {
			_, _ = io.Copy(io.Discard, r.Body)
			close(started)
			select {
			case <-r.Context().Done():
			case <-time.After(4 * time.Second):
			}
			return
		}
		generations.Add(1)
	})
	done := make(chan error, 1)
	go func() { done <- s.advanceAgentAI(context.Background(), rt, true) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("count never started")
	}
	agentApply(t, s, AgentCommand{Action: "ai-cancel", ProfileID: v.ProfileID, ID: v.AIRequests[0].ID})
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("count not cancelled")
	}
	if generations.Load() != 0 {
		t.Fatal("cancelled preflight created response")
	}
}

func TestOpenAIAdmissionPauseRecoveryWithoutReplay(t *testing.T) {
	s, c := admissionStore(t)
	v := aiStart(t, s, admissionPlan(t, s, c.Ref, false))
	started := make(chan struct{})
	var calls atomic.Int32
	rt := httpsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method == "GET" {
			admissionReply(w, map[string]any{"id": admittedOpenAIModel, "object": "model", "created": 1744588800, "owned_by": "openai"})
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(4 * time.Second):
		}
	})
	done := make(chan error, 1)
	go func() { done <- s.advanceAgentAI(t.Context(), rt, true) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("preflight never started")
	}
	if _, e := s.db.Exec("UPDATE update_pause SET phase='paused'"); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if e != ErrCorePaused {
			t.Fatal("pause fence", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pause did not stop preflight")
	}
	if _, e := s.db.Exec("UPDATE update_pause SET phase='idle'"); e != nil {
		t.Fatal(e)
	}
	if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	q := v.AIRequests[0]
	if q.State != "unknown" || q.HeldMicrousd != 29120 || calls.Load() != 2 {
		t.Fatal("interrupted admission stranded or replayed", q.State, calls.Load())
	}
}
