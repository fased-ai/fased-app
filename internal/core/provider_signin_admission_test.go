package core

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// All state is the existing reconstructible fixture database. Both official
// origins are physically routed to trusted loopback TLS by private test wires;
// no application endpoint override or production activation is involved.
type ordinaryPlanHTTPS struct {
	mu       sync.Mutex
	mode     string
	wire     *openAITransport
	paths    []string
	expected string
	started  chan struct{}
}

func (f *ordinaryPlanHTTPS) set(mode, expected string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode, f.expected, f.paths = mode, expected, nil
}
func (f *ordinaryPlanHTTPS) operations() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}
func ordinaryPlanEvent(w io.Writer, event string, value any) {
	raw, _ := json.Marshal(value)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
}
func ordinaryPlanFixture(t *testing.T) (*Store, AIConnectionView, *ordinaryPlanHTTPS) {
	t.Helper()
	if providerActivationEnabled {
		t.Fatal("ordinary provider activation changed")
	}
	s := fixtureStore(t)
	s.providerFixture, s.provider.apiCredentials = false, true
	var issuer http.RoundTripper
	authServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "auth.openai.com" || issuer == nil {
			t.Error("auth fixture lost official origin")
			w.WriteHeader(400)
			return
		}
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Scheme, u.Host = "https", "auth.openai.com"
		copy.URL = &u
		response, err := issuer.RoundTrip(copy)
		if err != nil {
			t.Error("controlled auth refused", err)
			w.WriteHeader(400)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
	}))
	t.Cleanup(authServer.Close)
	target, _ := url.Parse(authServer.URL)
	s.provider.signInTransport = aiTestTransport(func(r *http.Request) (*http.Response, error) {
		if !signInEndpoint(r.URL.String()) {
			return nil, errors.New("unexpected auth origin")
		}
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Scheme, u.Host = target.Scheme, target.Host
		copy.URL, copy.Host = &u, "auth.openai.com"
		return authServer.Client().Transport.RoundTrip(copy)
	})
	start, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "openai", Remote: true, Name: "Ordinary ChatGPT"})
	if err != nil || start.SignIn == nil {
		t.Fatal("ordinary controlled sign-in start", err)
	}
	a := s.provider.signIn
	fixture, err := newProviderSignInFixture(a, s.now)
	if err != nil {
		t.Fatal(err)
	}
	issuer = fixture
	callback := a.redirect + "?" + url.Values{"code": {"fixture-authorization-code"}, "state": {a.state}, "client_id": {fixture.clientID}}.Encode()
	account, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-complete", SignInID: start.SignIn.ID, Callback: callback, Passphrase: testVaultPassword})
	if err != nil || account.Ref == "" || account.TestOnly || account.AuthMethod != "sign-in" || account.LiveCallsEnabled {
		t.Fatal("ordinary sealed account", err, account.AuthMethod)
	}
	// Remove the auth injection after registration. Generation must exercise the
	// ordinary plan branch, not the former fixture/auth-injection exception.
	s.provider.signInTransport = nil
	f := &ordinaryPlanHTTPS{}
	f.wire = nativeHTTPSFixture(t, "openai", func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "api.openai.com" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer fased-test-signin-access-") || r.Header.Get("X-Api-Key") != "" {
			t.Error("wrong inference origin/account/method")
			w.WriteHeader(400)
			return
		}
		f.mu.Lock()
		f.paths = append(f.paths, r.Method+" "+r.URL.Path)
		mode, expected, started := f.mode, f.expected, f.started
		f.mu.Unlock()
		if r.Method == "GET" && r.URL.Path == "/v1/models" {
			models := []any{}
			for _, m := range registryForProvider("openai").Models {
				models = append(models, map[string]string{"slug": m.ID, "display_name": m.Name, "visibility": "list"})
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"models": models})
			return
		}
		if r.Method != "POST" || r.URL.Path != "/v1/responses" {
			t.Error("plan fell back to API-key/count route", r.Method, r.URL.Path)
			w.WriteHeader(400)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil || expected == "" || string(raw) != expected {
			t.Error("wire changed after consent", err)
			w.WriteHeader(400)
			return
		}
		var fields map[string]json.RawMessage
		json.Unmarshal(raw, &fields)
		if string(fields["store"]) != "false" || string(fields["stream"]) != "true" || !bytes.HasPrefix(fields["input"], []byte("[")) {
			t.Error("plan wire contract lost")
		}
		for _, key := range []string{"background", "conversation", "max_output_tokens", "service_tier", "temperature", "truncation", "previous_response_id"} {
			if _, present := fields[key]; present {
				t.Error("API-only field in plan request", key)
			}
		}
		if started != nil {
			select {
			case started <- struct{}{}:
			default:
			}
		}
		w.Header().Set("X-Request-Id", "req_fased_controlled")
		if mode == "interrupted" {
			w.Header().Set("Content-Type", "text/event-stream")
			ordinaryPlanEvent(w, "response.created", map[string]any{"type": "response.created", "sequence_number": 0, "response": map[string]string{"id": "controlled-response", "model": admittedOpenAIModel}})
			return
		}
		if mode == "wait" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		if mode != "" && !strings.HasPrefix(mode, "late-") && !strings.HasPrefix(mode, "terminal-") {
			status, code := 503, "subscription_sharing_usage_unavailable"
			switch mode {
			case "quota":
				status, code = 429, "subscription_sharing_usage_limit_exceeded"
			case "permission":
				status, code = 403, "chatpass_v2_scope_not_authorized"
			case "invalid-user":
				status, code = 401, "subscription_sharing_invalid_user"
			case "detail":
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(403)
				json.NewEncoder(w).Encode(map[string]string{"detail": "diagnostic text with fased-test-signin-access-private"})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "param": "model", "type": "permission_error", "message": "diagnostic text with fased-test-signin-access-private"}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		ordinaryPlanEvent(w, "response.created", map[string]any{"type": "response.created", "sequence_number": 0, "response": map[string]string{"id": "controlled-response", "model": admittedOpenAIModel}})
		if strings.HasPrefix(mode, "terminal-") {
			status := "failed"
			if strings.Contains(mode, "incomplete") {
				status = "incomplete"
			}
			response := map[string]any{"id": "controlled-response", "model": admittedOpenAIModel, "status": status, "usage": map[string]any{"input_tokens": 32, "output_tokens": 20, "total_tokens": 52}, "output": []any{}}
			if status == "failed" {
				response["error"] = map[string]string{"code": "subscription_sharing_usage_limit_exceeded", "param": "model"}
			} else {
				response["incomplete_details"] = map[string]string{"reason": "max_output_tokens"}
			}
			if strings.HasSuffix(mode, "-missing") {
				delete(response, "usage")
			}
			if strings.HasSuffix(mode, "-invalid-total") {
				response["usage"].(map[string]any)["total_tokens"] = 53
			}
			if strings.HasSuffix(mode, "-invalid-cache") {
				response["usage"].(map[string]any)["input_tokens_details"] = map[string]int{"cached_tokens": 33}
			}
			if strings.HasSuffix(mode, "-wrong-id") {
				response["id"] = "different-response"
			}
			if strings.HasSuffix(mode, "-wrong-model") {
				response["model"] = "different-model"
			}
			ordinaryPlanEvent(w, "response."+status, map[string]any{"type": "response." + status, "sequence_number": 1, "response": response})
			return
		}
		if strings.HasPrefix(mode, "late-") {
			code := "subscription_sharing_usage_limit_exceeded"
			if mode == "late-transient" {
				code = "subscription_sharing_usage_unavailable"
			}
			ordinaryPlanEvent(w, "response.failed", map[string]any{"type": "response.failed", "sequence_number": 1, "response": map[string]any{"id": "controlled-response", "model": admittedOpenAIModel, "status": "failed", "error": map[string]string{"code": code, "param": "model", "message": "private diagnostic"}}})
			return
		}
		answer := `{"answer":"The fictional market had no new events.","cards":[]}`
		ordinaryPlanEvent(w, "response.output_text.delta", map[string]any{"type": "response.output_text.delta", "sequence_number": 1, "delta": answer})
		ordinaryPlanEvent(w, "response.output_text.done", map[string]any{"type": "response.output_text.done", "sequence_number": 2, "text": answer})
		response := map[string]any{"id": "controlled-response", "model": admittedOpenAIModel, "status": "completed", "usage": map[string]int{"input_tokens": 32, "output_tokens": 20, "total_tokens": 52}, "output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]string{"type": "output_text", "text": answer}}}}}
		ordinaryPlanEvent(w, "response.completed", map[string]any{"type": "response.completed", "sequence_number": 3, "response": response})
	})
	account, err = s.refreshProviderModels(t.Context(), account.Ref, f.wire)
	if err != nil || account.Locked || !account.PlanUse {
		t.Fatal("ordinary account model discovery", err)
	}
	view, err := s.Agents(t.Context(), "", "")
	if err != nil || len(view.AIRequests) != 0 || len(view.Tasks) != 0 {
		t.Fatal("connection/discovery generated work", err)
	}
	f.set("", "")
	return s, account, f
}

func TestOrdinarySignInTerminalUsageIsSourceMatched(t *testing.T) {
	for _, tc := range []struct {
		mode  string
		known bool
		lock  bool
	}{
		{"terminal-failed-valid", true, false},
		{"terminal-incomplete-valid", true, false},
		{"terminal-failed-valid", true, true},
		{"terminal-failed-missing", false, false},
		{"terminal-incomplete-missing", false, false},
		{"terminal-failed-invalid-total", false, false},
		{"terminal-failed-invalid-cache", false, false},
		{"terminal-failed-wrong-id", false, false},
		{"terminal-failed-wrong-model", false, false},
	} {
		name := tc.mode
		if tc.lock {
			name += "-then-lock"
		}
		t.Run(name, func(t *testing.T) {
			s, account, fixture := ordinaryPlanFixture(t)
			v, p := ordinaryPlanChat(t, s, account)
			v = ordinaryPlanSend(t, s, v, p, "A fictional input for terminal usage.")
			q := v.AIRequests[len(v.AIRequests)-1]
			fixture.set(tc.mode, q.Disclosure)
			var transport http.RoundTripper = fixture.wire
			if tc.lock {
				transport = aiTestTransport(func(r *http.Request) (*http.Response, error) {
					response, err := fixture.wire.RoundTrip(r)
					if err == nil && r.Method == "POST" {
						response.Body = &ordinaryPlanCloseHook{ReadCloser: response.Body, hook: func() {
							if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "lock", Ref: account.Ref}); err != nil {
								t.Error(err)
							}
						}}
					}
					return response, err
				})
			}
			if err := s.advanceAgentAI(t.Context(), transport, false); err != nil {
				t.Fatal(err)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			q = v.AIRequests[len(v.AIRequests)-1]
			if q.Answer != "" || q.PartialAnswer != "" || len(q.Cards) != 0 || q.HeldMicrousd != 0 || q.SpentMicrousd != 0 {
				t.Fatal("terminal failure accepted output or invented money", q.State)
			}
			if tc.known {
				if q.State != "failed" || aiHasUsageHold(q) || q.InputTokens != 32 || q.OutputTokens != 20 || q.CostBasis != "chatgpt-plan-reported-usage" {
					t.Fatal("verified terminal usage discarded or remained uncertain", q.State, q.InputTokens, q.OutputTokens, q.CostBasis, q.Failure)
				}
			} else if q.State != "unknown" || !aiHasUsageHold(q) || q.InputTokens != 0 || q.OutputTokens != 0 {
				t.Fatal("unverified terminal usage was settled", q.State, q.Failure)
			}
			paths := fixture.operations()
			if err := s.advanceAgentAI(t.Context(), fixture.wire, false); err != nil || !reflect.DeepEqual(paths, fixture.operations()) {
				t.Fatal("terminal response replayed", err)
			}
		})
	}
}

func TestOrdinarySignInFailedTaskRetainsReportedUsage(t *testing.T) {
	s, account, fixture := ordinaryPlanFixture(t)
	v, p := ordinaryPlanChat(t, s, account)
	v = agentApply(t, s, AgentCommand{Action: "task-ai-prepare", ProfileID: v.ProfileID, ID: p.SessionID, UseAgentDefaults: true, Name: "Known failed plan task", Text: "A fictional bounded summary", IntervalSeconds: 60, MaxRuns: 1, AuthorizationSeconds: 900, Limits: &p.Limits})
	task := v.Tasks[len(v.Tasks)-1]
	v = agentApply(t, s, AgentCommand{Action: "task-ai-authorize", ProfileID: v.ProfileID, ID: task.ID, ConsentDigest: task.AI.Digest})
	grant, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: account.Ref, ProfileID: v.ProfileID, Revision: v.Revision, TaskIDs: []string{task.ID}, AuthorizationSeconds: 900})
	if err != nil || grant.AccessReview == nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: account.Ref, Allowance: grant.AccessReview, ConsentDigest: grant.AccessReview.ID, Passphrase: testVaultPassword}); err != nil {
		t.Fatal(err)
	}
	v = agentApply(t, s, AgentCommand{Action: "task-run", ProfileID: v.ProfileID, ID: task.ID})
	q := v.AIRequests[len(v.AIRequests)-1]
	fixture.set("terminal-failed-valid", q.Disclosure)
	if err = s.advanceAgentAI(t.Context(), fixture.wire, false); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	q = v.AIRequests[len(v.AIRequests)-1]
	if q.State != "failed" || q.InputTokens != 32 || q.OutputTokens != 20 || q.CostBasis != "chatgpt-plan-reported-usage" || aiHasUsageHold(q) || q.Answer != "" || len(q.Cards) != 0 || q.SpentMicrousd != 0 || q.HeldMicrousd != 0 || len(v.Runs) != 1 || v.Runs[0].Outcome != "ai-failed" || v.Tasks[len(v.Tasks)-1].State != "held" {
		t.Fatal("failed task lost verified usage or executed its dependent result", q.State, q.Failure, v.Tasks)
	}
}

func TestOrdinarySignInCancelledReceiptRetainsKnownUsage(t *testing.T) {
	for _, mode := range []string{"completed", "terminal-failed-valid"} {
		t.Run(mode, func(t *testing.T) {
			s, account, fixture := ordinaryPlanFixture(t)
			v := agentApply(t, s, AgentCommand{Action: "profile-create", Name: "Cancelled known receipt", Provider: "openai", AccountRef: &account.Ref, Model: admittedOpenAIModel})
			v = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: v.ProfileID, Name: "Stop keeps reported usage"})
			v = agentApply(t, s, AgentCommand{Action: "ai-prepare", ProfileID: v.ProfileID, ID: v.Sessions[0].ID, UseAgentDefaults: true, Text: "Synthetic input", MemorySelection: &AIMemorySelection{Mode: "none"}, Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 5}})
			q := v.AIRequests[len(v.AIRequests)-1]
			v = agentApply(t, s, AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, ID: q.ID, ConsentDigest: q.Digest})
			fixtureMode := mode
			if mode == "completed" {
				fixtureMode = ""
			}
			fixture.set(fixtureMode, q.Disclosure)
			transport := aiTestTransport(func(r *http.Request) (*http.Response, error) {
				response, err := fixture.wire.RoundTrip(r)
				if err == nil && r.Method == "POST" {
					response.Body = &ordinaryPlanCloseHook{ReadCloser: response.Body, hook: func() { agentApply(t, s, AgentCommand{Action: "ai-cancel", ProfileID: v.ProfileID, ID: q.ID}) }}
				}
				return response, err
			})
			if err := s.advanceAgentAI(t.Context(), transport, false); err != nil {
				t.Fatal(err)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			q = v.AIRequests[len(v.AIRequests)-1]
			if q.State != "cancelled" || q.Answer != "" || q.PartialAnswer != "" || len(q.Cards) != 0 || len(v.Runs) != 0 || q.InputTokens != 32 || q.OutputTokens != 20 || q.CostBasis != "chatgpt-plan-reported-usage" || q.HeldMicrousd != 0 || q.SpentMicrousd != 0 || aiHasUsageHold(q) {
				t.Fatal("owner Stop lost verified usage or accepted output", q.State, q.InputTokens, q.OutputTokens, q.CostBasis, q.Failure)
			}
			paths := fixture.operations()
			if err := s.advanceAgentAI(t.Context(), fixture.wire, false); err != nil || !reflect.DeepEqual(paths, fixture.operations()) {
				t.Fatal("stopped request replayed", err)
			}
		})
	}
}

func ordinaryPlanChat(t *testing.T, s *Store, account AIConnectionView) (AgentView, ChatPermission) {
	t.Helper()
	v := agentApply(t, s, AgentCommand{Action: "profile-create", Name: "Ordinary plan analyst", Provider: "openai", AccountRef: &account.Ref, Model: admittedOpenAIModel})
	v = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: v.ProfileID, Name: "Controlled ordinary plan"})
	v = agentApply(t, s, AgentCommand{Action: "chat-review", ProfileID: v.ProfileID, ID: v.Sessions[0].ID, UseAgentDefaults: true, Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 5}, MemorySelection: &AIMemorySelection{Mode: "none"}, MaxRuns: 3, AuthorizationSeconds: 900})
	if v.ChatReview == nil || v.ChatReview.BudgetMicrousd != 0 || v.ChatReview.Limits.BudgetMicrousd != 0 {
		t.Fatal("plan review required monetary budget")
	}
	p := *v.ChatReview
	v = agentApply(t, s, AgentCommand{Action: "chat-allow", ProfileID: v.ProfileID, ID: p.SessionID, ChatPermission: &p, ConsentDigest: p.ID})
	return v, p
}

func ordinaryPlanSend(t *testing.T, s *Store, v AgentView, p ChatPermission, text string) AgentView {
	t.Helper()
	return agentApply(t, s, AgentCommand{Action: "chat-send", ProfileID: v.ProfileID, ID: p.SessionID, Text: text, ConsentDigest: p.ID})
}

func TestOrdinarySignInChatTaskResultMemory(t *testing.T) {
	s, account, fixture := ordinaryPlanFixture(t)
	v, permission := ordinaryPlanChat(t, s, account)
	v = ordinaryPlanSend(t, s, v, permission, "Summarize one fictional event.")
	q := v.AIRequests[len(v.AIRequests)-1]
	if q.Policy == nil || q.Policy.WireVersion != "openai-signin-v2" || q.ConnectionRef != account.Ref || q.Digest != aiDigest(q) || q.HeldMicrousd != 0 || q.State != "queued" {
		t.Fatal("ordinary plan queue lost method/identity or charged API money")
	}
	fixture.set("", q.Disclosure)
	if err := s.advanceAgentAI(t.Context(), fixture.wire, false); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	q = v.AIRequests[len(v.AIRequests)-1]
	if q.State != "completed" || q.Answer == "" || q.CostBasis != "chatgpt-plan-reported-usage" || q.SpentMicrousd != 0 || q.HeldMicrousd != 0 || q.InputTokens != 32 || q.OutputTokens != 20 || q.Admission == nil || !q.Admission.GenerationAttempted {
		t.Fatal("ordinary plan response did not retain reported zero-money usage", q.State, q.Failure, q.CostBasis)
	}
	if !reflect.DeepEqual(fixture.operations(), []string{"GET /v1/models", "POST /v1/responses"}) {
		t.Fatal("wrong plan admission route", fixture.operations())
	}
	v = agentApply(t, s, AgentCommand{Action: "task-ai-prepare", ProfileID: v.ProfileID, ID: permission.SessionID, UseAgentDefaults: true, Name: "Plan summary", Text: "Summarize fictional news", IntervalSeconds: 60, MaxRuns: 1, AuthorizationSeconds: 900, Limits: &permission.Limits})
	task := v.Tasks[len(v.Tasks)-1]
	if task.AI == nil || task.AI.BudgetMicrousd != 0 || task.AI.Policy.WireVersion != "openai-signin-v2" {
		t.Fatal("task inherited API billing")
	}
	v = agentApply(t, s, AgentCommand{Action: "task-ai-authorize", ProfileID: v.ProfileID, ID: task.ID, ConsentDigest: task.AI.Digest})
	grant, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: account.Ref, ProfileID: v.ProfileID, Revision: v.Revision, TaskIDs: []string{task.ID}, AuthorizationSeconds: 900})
	if err != nil || grant.AccessReview == nil || grant.AccessReview.BudgetMicrousd != 0 {
		t.Fatal("ordinary plan credential review", err)
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: account.Ref, Allowance: grant.AccessReview, ConsentDigest: grant.AccessReview.ID, Passphrase: testVaultPassword}); err != nil {
		t.Fatal(err)
	}
	v = agentApply(t, s, AgentCommand{Action: "task-run", ProfileID: v.ProfileID, ID: task.ID})
	taskQ := v.AIRequests[len(v.AIRequests)-1]
	fixture.set("", taskQ.Disclosure)
	if err = s.advanceAgentAI(t.Context(), fixture.wire, false); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	result := v.AIRequests[len(v.AIRequests)-1]
	if result.State != "completed" || result.Stream || result.TaskID != task.ID || result.CostBasis != "chatgpt-plan-reported-usage" || len(v.Runs) != 1 || v.Runs[0].AIRequestID != result.ID || v.Tasks[len(v.Tasks)-1].State != "completed" {
		t.Fatal("ordinary plan task/result failed", result.State, result.Failure)
	}
	v = agentApply(t, s, AgentCommand{Action: "task-run-explain", ProfileID: v.ProfileID, ID: v.Runs[0].ID})
	v = agentApply(t, s, AgentCommand{Action: "memory-chat-save", ProfileID: v.ProfileID, Source: "message", ID: v.Messages[len(v.Messages)-1].ID, Text: "A fictional event was summarized."})
	if len(v.Memories) != 1 || !strings.Contains(v.Memories[0].Source, "sha256=") {
		t.Fatal("ordinary result memory lost provenance")
	}
	foreign, err := s.Agents(t.Context(), "news-researcher", "")
	if err != nil || len(foreign.AIRequests) != 0 || len(foreign.Memories) != 0 {
		t.Fatal("ordinary plan crossed profile boundary", err)
	}
	if s.providerFixture || s.provider.signInTransport != nil || providerActivationEnabled {
		t.Fatal("ordinary acceptance borrowed development activation")
	}
}

func TestOrdinarySignInErrorsPreserveAccountAndDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		mode, code, shape string
		status            int
		unknown, pauses   bool
	}{
		{"quota", "subscription_sharing_usage_limit_exceeded", "json-error", 429, false, true},
		{"permission", "chatpass_v2_scope_not_authorized", "json-error", 403, false, true},
		{"invalid-user", "subscription_sharing_invalid_user", "json-error", 401, false, true},
		{"detail", "", "json-detail", 403, false, true},
		{"transient", "subscription_sharing_usage_unavailable", "json-error", 503, false, false},
		{"late-quota", "subscription_sharing_usage_limit_exceeded", "sse", 200, true, true},
		{"late-transient", "subscription_sharing_usage_unavailable", "sse", 200, true, false},
		{"interrupted", "stream_unverified", "sse", 200, true, false},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			s, account, fixture := ordinaryPlanFixture(t)
			other := saveTestConnection(t, s)
			v, permission := ordinaryPlanChat(t, s, account)
			v = ordinaryPlanSend(t, s, v, permission, "Explain a synthetic input.")
			q := v.AIRequests[len(v.AIRequests)-1]
			before, err := os.ReadFile(s.aiVaultPath())
			if err != nil {
				t.Fatal(err)
			}
			fixture.set(tc.mode, q.Disclosure)
			if err = s.advanceAgentAI(t.Context(), fixture.wire, false); err != nil {
				t.Fatal(err)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			q = v.AIRequests[len(v.AIRequests)-1]
			want := "failed"
			if tc.unknown {
				want = "unknown"
			}
			if q.State != want || q.Answer != "" || q.PartialAnswer != "" || len(q.Cards) != 0 || q.SpentMicrousd != 0 || q.HeldMicrousd != 0 {
				t.Fatal("plan failure accepted an answer or invented money", q.State, q.Failure)
			}
			_, diagnostic, found := strings.Cut(q.Failure, "Diagnostics: ")
			var retained struct {
				Status     int    `json:"status"`
				Body       string `json:"body"`
				Code       string `json:"code"`
				Param      string `json:"param"`
				RequestID  string `json:"requestId"`
				BodySHA256 string `json:"bodySha256"`
				Late       bool   `json:"late"`
			}
			if !found || json.Unmarshal([]byte(diagnostic), &retained) != nil || retained.Status != tc.status || retained.Code != tc.code || retained.Body != tc.shape || retained.RequestID != "req_fased_controlled" || len(retained.BodySHA256) != 64 || retained.Late != tc.unknown {
				t.Fatal("safe diagnostic identity/shape was lost", q.Failure)
			}
			if tc.mode != "interrupted" && tc.code != "" && retained.Param != "model" {
				t.Fatal("structured error parameter lost", q.Failure)
			}
			if strings.Contains(q.Failure, "diagnostic text") || strings.Contains(q.Failure, "private diagnostic") || strings.Contains(q.Failure, "fased-test-signin-access-") {
				t.Fatal("raw provider text disclosed", q.Failure)
			}
			after, err := os.ReadFile(s.aiVaultPath())
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("request failure erased or changed credentials", err)
			}
			unrelated, err := s.AIConnection(other.Ref)
			if err != nil || unrelated.Locked {
				t.Fatal("plan failure locked another account", err)
			}
			operations := fixture.operations()
			if !reflect.DeepEqual(operations, []string{"GET /v1/models", "POST /v1/responses"}) {
				t.Fatal("failure changed billing/admission route", operations)
			}
			if err = s.advanceAgentAI(t.Context(), fixture.wire, false); err != nil || !reflect.DeepEqual(operations, fixture.operations()) {
				t.Fatal("failed/unknown work replayed", err)
			}
			command := AgentCommand{Action: "chat-send", Revision: v.Revision, ProfileID: v.ProfileID, ID: permission.SessionID, Text: "Do not retry unknown/paused plan work.", ConsentDigest: permission.ID}
			if tc.unknown || tc.pauses {
				if _, err = s.ChangeAgents(t.Context(), command); err == nil {
					t.Fatal("unknown/paused plan work allowed a new send")
				}
			}
		})
	}
}

func TestOrdinarySignInQueuedStopRevokeLockRestart(t *testing.T) {
	for _, action := range []string{"stop", "revoke", "lock", "restart"} {
		t.Run(action, func(t *testing.T) {
			s, account, fixture := ordinaryPlanFixture(t)
			v, permission := ordinaryPlanChat(t, s, account)
			v = ordinaryPlanSend(t, s, v, permission, "Synthetic request still unsent.")
			q := v.AIRequests[len(v.AIRequests)-1]
			fixture.set("", q.Disclosure)
			switch action {
			case "stop":
				v = agentApply(t, s, AgentCommand{Action: "ai-cancel", ProfileID: v.ProfileID, ID: q.ID})
			case "revoke":
				v = agentApply(t, s, AgentCommand{Action: "chat-revoke", ProfileID: v.ProfileID, ID: permission.SessionID, ConsentDigest: permission.ID})
			case "lock":
				if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "lock", Ref: account.Ref}); err != nil {
					t.Fatal(err)
				}
			case "restart":
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				restarted, err := OpenStore(s.dir)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { restarted.Close() })
				s = restarted
				s.provider.apiCredentials = true
			}
			if err := s.advanceAgentAI(t.Context(), fixture.wire, false); err != nil {
				t.Fatal(err)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			q = v.AIRequests[len(v.AIRequests)-1]
			if q.State != "cancelled" || q.HeldMicrousd != 0 || q.Answer != "" || len(fixture.operations()) != 0 {
				t.Fatal("queued plan work survived stop/revoke/lock/restart", q.State, fixture.operations())
			}
		})
	}
}

func TestOrdinarySignInRejectsLegacyTariffAndAPIFallback(t *testing.T) {
	s, account, fixture := ordinaryPlanFixture(t)
	v, permission := ordinaryPlanChat(t, s, account)
	v = ordinaryPlanSend(t, s, v, permission, "A current plan request.")
	q := v.AIRequests[len(v.AIRequests)-1]
	legacy := q
	old := openAIModelPolicy(q.Model)
	old.ID += ":sign-in-fixture"
	old.WireVersion, old.CacheMode = "openai-signin-v1", ""
	old.TariffSource, old.TokenSource = "local:synthetic-fixture-allocation", "local:conservative-signin-fixture-bound"
	legacy.Policy = &old
	if s.providerRequestAllowed(legacy, false) {
		t.Fatal("legacy synthetic approval enabled ordinary execution")
	}
	if legacy.Policy.WireVersion != "openai-signin-v1" || legacy.Policy.InputMicrousdPerMillion == 0 {
		t.Fatal("legacy retained record mutated")
	}
	api := saveTestConnection(t, s)
	copy := q
	copy.ConnectionRef = api.Ref
	request, _ := http.NewRequestWithContext(context.Background(), "POST", "https://api.openai.com/v1/responses", strings.NewReader(copy.Disclosure))
	transport := authenticatedAITransport{store: s, ref: api.Ref, request: &copy, next: fixture.wire}
	fixture.set("", copy.Disclosure)
	if _, err := transport.RoundTrip(request); err == nil || len(fixture.operations()) != 0 {
		t.Fatal("plan request silently used API-key account")
	}
	for _, path := range []string{"/v1/responses/input_tokens", "/v1/models/" + admittedOpenAIModel} {
		request, _ := http.NewRequestWithContext(context.Background(), "POST", "https://api.openai.com"+path, strings.NewReader("{}"))
		transport = authenticatedAITransport{store: s, ref: account.Ref, request: &q, next: fixture.wire}
		if _, err := transport.RoundTrip(request); err == nil || len(fixture.operations()) != 0 {
			t.Fatal("plan request reached API-key admission route", path)
		}
	}
}

func TestOrdinarySignInRejectsMonetaryPlanConsent(t *testing.T) {
	s, account, _ := ordinaryPlanFixture(t)
	v := agentApply(t, s, AgentCommand{Action: "profile-create", Name: "Plan money isolation", Provider: "openai", AccountRef: &account.Ref, Model: admittedOpenAIModel})
	v = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: v.ProfileID, Name: "Plan review"})
	for _, action := range []string{"ai-prepare", "chat-review", "task-ai-prepare"} {
		limits := &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 5}
		c := AgentCommand{Action: action, Revision: v.Revision, ProfileID: v.ProfileID, ID: v.Sessions[0].ID, Name: "Refuse API price", Text: "Only synthetic text", UseAgentDefaults: true, Limits: limits, MaxRuns: 1, AuthorizationSeconds: 900, IntervalSeconds: 60}
		if action == "ai-prepare" {
			limits.BudgetMicrousd = 1
		} else {
			c.TotalBudgetMicrousd = 1
		}
		if action != "task-ai-prepare" {
			c.MemorySelection = &AIMemorySelection{Mode: "none"}
		}
		before, revision, err := readAgents(t.Context(), s.db)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.ChangeAgents(t.Context(), c); err == nil {
			t.Fatal("plan accepted API monetary consent", action)
		}
		after, next, err := readAgents(t.Context(), s.db)
		if err != nil || next != revision || !reflect.DeepEqual(before, after) {
			t.Fatal("refused monetary consent changed retained records", action, err)
		}
	}
}

func TestOrdinarySignInDiagnosticCannotReflectOpaqueBearer(t *testing.T) {
	s, account, _ := ordinaryPlanFixture(t)
	opaque := "opaque-disposable-signin-secret"
	s.provider.mu.Lock()
	state := s.credentialLocked(account.Ref)
	credential, ok := parseProviderSignInCredential(state.key)
	if !ok {
		s.provider.mu.Unlock()
		t.Fatal("fixture account credential missing")
	}
	credential.AccessToken = opaque
	clear(state.key)
	state.key, _ = json.Marshal(credential)
	s.provider.mu.Unlock()
	for _, requestID := range []string{opaque, "req_" + opaque, "req_safe_controlled"} {
		request, _ := http.NewRequestWithContext(context.Background(), "GET", "https://api.openai.com/v1/models", nil)
		transport := authenticatedAITransport{store: s, ref: account.Ref, modelsOnly: true, next: aiTestTransport(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("Authorization") != "Bearer "+opaque {
				t.Error("opaque token changed billing route")
			}
			return &http.Response{StatusCode: 503, Header: http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {requestID}}, Body: io.NopCloser(strings.NewReader(`{"detail":"temporarily unavailable"}`))}, nil
		})}
		response, err := transport.RoundTrip(request)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		failure := planFailure(response.StatusCode, response.Header.Get("Content-Type"), response.Header.Get("X-Request-Id"), raw, false)
		if strings.Contains(failure.Error(), opaque) {
			t.Fatal("opaque OAuth bearer persisted through request identity")
		}
		if requestID == "req_safe_controlled" && failure.RequestID != requestID {
			t.Fatal("safe request identity was lost")
		}
	}
}

func TestOrdinarySignInOlderReaderRefusesZeroMoneyState(t *testing.T) {
	reader := os.Getenv("FASED_PLAN_OLDER_CORE")
	if reader == "" {
		t.Skip("pinned pre-change core supplied by acceptance")
	}
	for _, kind := range []string{"chat", "task"} {
		t.Run(kind, func(t *testing.T) {
			s, account, _ := ordinaryPlanFixture(t)
			if kind == "chat" {
				ordinaryPlanChat(t, s, account)
			} else {
				v := agentApply(t, s, AgentCommand{Action: "profile-create", Name: "Saved plan task", Provider: "openai", AccountRef: &account.Ref, Model: admittedOpenAIModel})
				v = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: v.ProfileID, Name: "Saved task context"})
				agentApply(t, s, AgentCommand{Action: "task-ai-prepare", ProfileID: v.ProfileID, ID: v.Sessions[0].ID, UseAgentDefaults: true, Name: "Saved plan review", Text: "Synthetic input", IntervalSeconds: 60, MaxRuns: 1, AuthorizationSeconds: 900, Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 5}})
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			// Copy the complete closed SQLite participant into another reconstructible
			// fixture directory, deliberately excluding the independent v5 vault.
			dir := fixtureDir(t)
			for _, name := range []string{"core.sqlite", "core.sqlite-wal", "core.sqlite-shm", "core.sqlite-journal"} {
				contents, err := os.ReadFile(filepath.Join(s.dir, name))
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(dir, name), contents, 0600); err != nil {
					t.Fatal(err)
				}
			}
			snapshot := func() (string, int64) {
				db, err := sql.Open("sqlite", filepath.Join(dir, "core.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				var document string
				var revision int64
				if err = db.QueryRowContext(t.Context(), "SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&document, &revision); err != nil {
					t.Fatal(err)
				}
				return document, revision
			}
			before, revision := snapshot()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, reader, "serve", "--data-dir", dir, "--listen", "127.0.0.1:0").CombinedOutput()
			if err == nil || ctx.Err() != nil || (!strings.Contains(string(out), "Chat permission") && !strings.Contains(string(out), "task") && !strings.Contains(string(out), "schema is newer")) {
				t.Fatal("older reader did not refuse new plan semantics before listen", string(out), err)
			}
			if _, err = os.Stat(filepath.Join(dir, "endpoint.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("older reader published an endpoint", err)
			}
			after, next := snapshot()
			if before != after || revision != next {
				t.Fatal("older reader mutated the logical workspace")
			}
		})
	}
}

type ordinaryPlanCloseHook struct {
	io.ReadCloser
	once sync.Once
	hook func()
}

func (r *ordinaryPlanCloseHook) Close() error {
	err := r.ReadCloser.Close()
	r.once.Do(r.hook)
	return err
}

func TestOrdinarySignInUncertaintySurvivesCompletionChange(t *testing.T) {
	for _, change := range []string{"lock", "context", "clock", "stop"} {
		t.Run(change, func(t *testing.T) {
			s, account, fixture := ordinaryPlanFixture(t)
			var clock atomic.Int64
			clock.Store(s.now().Unix())
			s.now = func() time.Time { return time.Unix(clock.Load(), 0) }
			v := agentApply(t, s, AgentCommand{Action: "profile-create", Name: "Uncertain manual result", Provider: "openai", AccountRef: &account.Ref, Model: admittedOpenAIModel})
			v = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: v.ProfileID, Name: "Preserve uncertain usage"})
			limits := &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 5}
			v = agentApply(t, s, AgentCommand{Action: "ai-prepare", ProfileID: v.ProfileID, ID: v.Sessions[0].ID, UseAgentDefaults: true, Text: "Synthetic input before uncertainty", MemorySelection: &AIMemorySelection{Mode: "none"}, Limits: limits})
			q := v.AIRequests[len(v.AIRequests)-1]
			v = agentApply(t, s, AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, ID: q.ID, ConsentDigest: q.Digest})
			fixture.set("interrupted", q.Disclosure)
			transport := aiTestTransport(func(r *http.Request) (*http.Response, error) {
				response, err := fixture.wire.RoundTrip(r)
				if err == nil && r.Method == "POST" && r.URL.Path == "/v1/responses" {
					response.Body = &ordinaryPlanCloseHook{ReadCloser: response.Body, hook: func() {
						switch change {
						case "lock":
							if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "lock", Ref: account.Ref}); err != nil {
								t.Error(err)
							}
						case "context":
							agentApply(t, s, AgentCommand{Action: "profile-update", ProfileID: v.ProfileID, Name: "Uncertain manual result", Text: "Changed after provider response", Provider: "openai", AccountRef: &account.Ref, Model: admittedOpenAIModel})
						case "clock":
							clock.Add(3600)
						case "stop":
							agentApply(t, s, AgentCommand{Action: "ai-cancel", ProfileID: v.ProfileID, ID: q.ID})
						}
					}}
				}
				return response, err
			})
			if err := s.advanceAgentAI(t.Context(), transport, false); err != nil {
				t.Fatal(err)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			q = v.AIRequests[len(v.AIRequests)-1]
			want := "unknown"
			if change == "stop" {
				want = "cancelled"
			}
			if q.State != want || !aiHasUsageHold(q) || q.HeldMicrousd != 0 || q.DispatchBoot == "" || q.Answer != "" || len(q.Cards) != 0 || finalManualAI(q) {
				t.Fatal("completion change erased zero-money uncertainty", change, q.State, q.Failure)
			}
			before := retainedRevisionState(t, s)
			if _, err := s.ChangeAgents(t.Context(), AgentCommand{Action: "session-archive", ProfileID: v.ProfileID, Revision: v.Revision, ID: v.Sessions[0].ID}); err == nil {
				t.Fatal("dispatched zero-money uncertainty archived")
			}
			if retainedRevisionState(t, s) != before {
				t.Fatal("failed archive changed uncertain journal")
			}
			if change != "clock" {
				if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: account.Ref, Passphrase: testVaultPassword}); err != nil {
					t.Fatal(err)
				}
				if _, err := s.refreshProviderModels(t.Context(), account.Ref, fixture.wire); err != nil {
					t.Fatal(err)
				}
				for _, action := range []string{"ai-prepare", "chat-review", "task-ai-prepare"} {
					v, _ = s.Agents(t.Context(), v.ProfileID, "")
					c := AgentCommand{Action: action, Revision: v.Revision, ProfileID: v.ProfileID, ID: v.Sessions[0].ID, UseAgentDefaults: true, Text: "Must not bypass uncertain usage", Name: "Blocked plan work", Limits: limits, MaxRuns: 1, AuthorizationSeconds: 900, IntervalSeconds: 60}
					if action != "task-ai-prepare" {
						c.MemorySelection = &AIMemorySelection{Mode: "none"}
					}
					prepared, err := s.ChangeAgents(t.Context(), c)
					if err != nil {
						continue
					}
					// Preparing an inert review is safe; it must not bypass the
					// outstanding hold when authorizing actual provider work.
					switch action {
					case "ai-prepare":
						request := prepared.AIRequests[len(prepared.AIRequests)-1]
						if _, err = s.ChangeAgents(t.Context(), AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, Revision: prepared.Revision, ID: request.ID, ConsentDigest: request.Digest}); err == nil {
							t.Fatal("manual start bypassed uncertain plan usage")
						}
					case "chat-review":
						p := prepared.ChatReview
						if p != nil {
							if _, err = s.ChangeAgents(t.Context(), AgentCommand{Action: "chat-allow", ProfileID: v.ProfileID, Revision: prepared.Revision, ID: p.SessionID, ChatPermission: p, ConsentDigest: p.ID}); err == nil {
								t.Fatal("Chat permission bypassed uncertain plan usage")
							}
						}
					case "task-ai-prepare":
						task := prepared.Tasks[len(prepared.Tasks)-1]
						authorized, authorizeErr := s.ChangeAgents(t.Context(), AgentCommand{Action: "task-ai-authorize", ProfileID: v.ProfileID, Revision: prepared.Revision, ID: task.ID, ConsentDigest: task.AI.Digest})
						if authorizeErr == nil {
							if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: account.Ref, ProfileID: v.ProfileID, Revision: authorized.Revision, TaskIDs: []string{task.ID}, AuthorizationSeconds: 900}); err == nil {
								t.Fatal("task credential authority bypassed uncertain plan usage")
							}
						}
					}
				}
			}
		})
	}
}

func TestOrdinarySignInPolicyHasNoAPIMoney(t *testing.T) {
	policy := chatModelPolicyForMethod(admittedOpenAIModel, "sign-in")
	if policy == nil || policy.WireVersion != "openai-signin-v2" || policy.InputMicrousdPerMillion != 0 || policy.CachedMicrousdPerMillion != 0 || policy.OutputMicrousdPerMillion != 0 || policy.BillableOutputTokens != 0 || policy.MaxRequestBudgetMicrousd != 0 {
		t.Fatal("ordinary sign-in still inherits the synthetic/API tariff policy")
	}
	api := chatModelPolicyForMethod(admittedOpenAIModel, "api-key")
	if api == nil || api.InputMicrousdPerMillion == 0 || api.OutputMicrousdPerMillion == 0 || api.WireVersion == policy.WireVersion {
		t.Fatal("plan admission changed API-key money policy or billing identity")
	}
}
