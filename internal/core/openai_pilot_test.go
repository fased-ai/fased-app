package core

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenAIPilotOrdinaryHold(t *testing.T) {
	s := fixtureStore(t)
	if s.pilot.target != nil || providerActivationEnabled || newOpenAITransport().enabled {
		t.Fatal("ordinary runtime admitted a pilot")
	}
	v, _ := s.Agents(t.Context(), "", "")
	for _, action := range []string{"pilot-review", "pilot-allow"} {
		if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: action, ProfileID: v.ProfileID, Revision: v.Revision}); err == nil {
			t.Fatal("ordinary owner command activated a pilot")
		}
	}
}

func pilotStore(t *testing.T) (*Store, AIConnectionView, OpenAIPilotAllowance) {
	t.Helper()
	s := fixtureStore(t)
	s.now = func() time.Time { return time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC) }
	connection := admitDisposableAPI(t, s, "")
	s.pilot.target = &openAIPilotTarget{s.instanceID, connection.Ref, "trading-analyst", admittedOpenAIModel}
	s.provider.apiCredentials, s.providerFixture = false, false
	v, _ := s.Agents(t.Context(), "trading-analyst", "")
	r, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "pilot-review", Ref: connection.Ref, ProfileID: v.ProfileID, Revision: v.Revision})
	if e != nil {
		t.Fatal(e)
	}
	return s, connection, *r.PilotReview
}
func allowPilot(t *testing.T, s *Store, p OpenAIPilotAllowance) {
	t.Helper()
	v, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "pilot-allow", Ref: p.ConnectionRef, Pilot: &p, ConsentDigest: p.ID, Passphrase: testVaultPassword})
	if e != nil || v.Pilot == nil || v.PilotState != "active" || !s.aiExecutionAvailable(false) || v.LiveCallsEnabled {
		t.Fatal("scoped allowance", e)
	}
}
func pilotChat(t *testing.T, s *Store, p OpenAIPilotAllowance) (AgentView, ChatPermission) {
	t.Helper()
	ref := p.ConnectionRef
	agentApply(t, s, AgentCommand{Action: "profile-update", ProfileID: p.ProfileID, Name: "Pilot", Provider: "openai", Model: p.Model, AccountRef: &ref})
	v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: p.ProfileID, Name: "Synthetic pilot"})
	v = agentApply(t, s, AgentCommand{Action: "chat-review", Model: p.Model, ConnectionRef: p.ConnectionRef, ProfileID: p.ProfileID, ID: v.Sessions[len(v.Sessions)-1].ID, Limits: &p.Limits, MemorySelection: &AIMemorySelection{Mode: "relevant"}, MaxRuns: 3, AuthorizationSeconds: 600, TotalBudgetMicrousd: p.LaneBudgetMicrousd})
	permission := *v.ChatReview
	v = agentApply(t, s, AgentCommand{Action: "chat-allow", ProfileID: p.ProfileID, ID: permission.SessionID, ChatPermission: &permission, ConsentDigest: permission.ID})
	return v, permission
}
func pilotHTTPS(t *testing.T, calls *atomic.Int32) http.RoundTripper {
	t.Helper()
	return httpsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+disposableAPIInput {
			t.Error("wrong credential")
		}
		switch r.URL.Path {
		case "/v1/models":
			admissionReply(w, map[string]any{"object": "list", "data": []any{map[string]any{"id": admittedOpenAIModel, "object": "model", "created": int64(1744588800), "owned_by": "system"}}})
		case "/v1/models/" + admittedOpenAIModel:
			admissionReply(w, map[string]any{"id": admittedOpenAIModel, "object": "model", "created": 1744588800, "owned_by": "system", "shutdown_date": nil})
		case "/v1/responses/input_tokens":
			admissionReply(w, map[string]any{"object": "response.input_tokens", "input_tokens": 1000})
		case "/v1/responses":
			var request struct {
				Stream bool `json:"stream"`
			}
			_ = json.NewDecoder(r.Body).Decode(&request)
			result := admittedResult(1000, 0)
			if request.Stream {
				result["id"] = "pilot-fixture-response"
				body, _ := json.Marshal(result)
				response := fixtureAIStream(r, body, []byte(`{"answer":"Controlled admitted answer","cards":[]}`))
				defer response.Body.Close()
				w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
				_, _ = io.Copy(w, response.Body)
			} else {
				admissionReply(w, result)
			}
		default:
			t.Error("unexpected path", r.URL.Path)
		}
	}).(testOpenAITransport).next
}

func TestOpenAIPilotStopRetainsAttemptAndUnknownUsage(t *testing.T) {
	for _, mode := range []string{"stop", "revoke", "lock", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			s, c, p := pilotStore(t)
			allowPilot(t, s, p)
			var clock atomic.Int64
			clock.Store(p.CreatedAt)
			s.now = func() time.Time { return time.Unix(clock.Load(), 0) }
			_, permission := pilotChat(t, s, p)
			v := agentApply(t, s, AgentCommand{Action: "chat-send", ProfileID: p.ProfileID, ID: permission.SessionID, Text: "Synthetic cancellation question.", ConsentDigest: permission.ID})
			q := v.AIRequests[len(v.AIRequests)-1]
			entered := make(chan struct{})
			release := make(chan struct{})
			rt := httpsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/models/" + p.Model:
					admissionReply(w, map[string]any{"id": p.Model, "object": "model", "created": 1744588800, "owned_by": "system", "shutdown_date": nil})
				case "/v1/responses/input_tokens":
					admissionReply(w, map[string]any{"object": "response.input_tokens", "input_tokens": 1000})
				case "/v1/responses":
					_, _ = io.Copy(io.Discard, r.Body)
					close(entered)
					select {
					case <-r.Context().Done():
					case <-release:
					}
				default:
					t.Error("unexpected exchange")
				}
			}).(testOpenAITransport).next
			t.Cleanup(func() { close(release) })
			done := make(chan error, 1)
			go func() { done <- s.advanceAgentAI(t.Context(), rt, false) }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("generation not reached")
			}
			switch mode {
			case "stop":
				agentApply(t, s, AgentCommand{Action: "ai-cancel", ProfileID: p.ProfileID, ID: q.ID})
			case "revoke":
				_, _ = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "pilot-revoke", Ref: c.Ref})
			case "lock":
				_, _ = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "lock", Ref: c.Ref})
			case "expiry":
				clock.Store(p.ExpiresAt)
			}
			select {
			case e := <-done:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("request not stopped")
			}
			v, _ = s.Agents(t.Context(), p.ProfileID, "")
			got := v.AIRequests[len(v.AIRequests)-1]
			if (got.State != "unknown" && got.State != "cancelled") || got.HeldMicrousd == 0 || s.pilot.permit.UsedRequests != 1 {
				t.Fatalf("stop erased attempt/hold: %+v", got)
			}
			if e := s.advanceAgentAI(t.Context(), aiTestTransport(func(*http.Request) (*http.Response, error) {
				t.Error("stopped request replayed")
				return nil, io.ErrUnexpectedEOF
			}), false); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestOpenAIPilotReviewIsRevisionBound(t *testing.T) {
	s, c, p := pilotStore(t)
	agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: p.ProfileID, Name: "Changed after review"})
	if _, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "pilot-allow", Ref: c.Ref, Pilot: &p, ConsentDigest: p.ID, Passphrase: testVaultPassword}); e == nil {
		t.Fatal("stale review accepted")
	}
}

func TestOpenAIPilotQueuedRevocationReleasesOnlyUnsentHold(t *testing.T) {
	for _, mode := range []string{"revoke", "expiry", "restart"} {
		t.Run(mode, func(t *testing.T) {
			s, c, p := pilotStore(t)
			allowPilot(t, s, p)
			_, permission := pilotChat(t, s, p)
			v := agentApply(t, s, AgentCommand{Action: "chat-send", ProfileID: p.ProfileID, ID: permission.SessionID, Text: "Queued synthetic question.", ConsentDigest: permission.ID})
			if v.AIRequests[len(v.AIRequests)-1].HeldMicrousd == 0 {
				t.Fatal("missing queued hold")
			}
			switch mode {
			case "revoke":
				_, _ = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "pilot-revoke", Ref: c.Ref})
			case "expiry":
				s.now = func() time.Time { return time.Unix(p.ExpiresAt, 0) }
			case "restart":
				dir := s.dir
				_ = s.Close()
				n, e := OpenStore(dir)
				if e != nil {
					t.Fatal(e)
				}
				defer n.Close()
				s = n
			}
			if e := s.advanceAgentAI(t.Context(), aiTestTransport(func(*http.Request) (*http.Response, error) {
				t.Error("unsent request escaped")
				return nil, io.ErrUnexpectedEOF
			}), false); e != nil {
				t.Fatal(e)
			}
			after, _ := s.Agents(t.Context(), p.ProfileID, "")
			q := after.AIRequests[len(after.AIRequests)-1]
			if mode != "restart" {
				if _, e := s.ChangeAgents(t.Context(), AgentCommand{Action: "chat-send", Revision: after.Revision, ProfileID: p.ProfileID, ID: permission.SessionID, Text: "Forbidden after revoke.", ConsentDigest: permission.ID}); e == nil {
					t.Fatal("queued after permission expired")
				}
			}
			if q.State != "cancelled" || q.HeldMicrousd != 0 || q.DispatchBoot != "" {
				t.Fatalf("unsent hold stuck: %+v", q)
			}
		})
	}
}
func pilotRequest(p OpenAIPilotAllowance, id string, task bool) AgentAIRequest {
	q := AgentAIRequest{ID: id, ConnectionRef: p.ConnectionRef, ProfileID: p.ProfileID, Model: p.Model, Policy: chatModelPolicy(p.Model), CreatedAt: p.CreatedAt, Limits: p.Limits, ChatPermissionID: "synthetic-permission"}
	if task {
		q.TaskID, q.ChatPermissionID = "synthetic-task", ""
	}
	return q
}
func TestOpenAIPilotReviewScopeAndTamper(t *testing.T) {
	s, c, p := pilotStore(t)
	original, _ := os.ReadFile(s.aiVaultPath())
	for _, mutate := range []func(*OpenAIPilotAllowance){
		func(p *OpenAIPilotAllowance) { p.InstanceID = "foreign" },
		func(p *OpenAIPilotAllowance) { p.ConnectionRef = "foreign" },
		func(p *OpenAIPilotAllowance) { p.ProfileID = "news-researcher" },
		func(p *OpenAIPilotAllowance) { p.Model = legacyOpenAIModel },
		func(p *OpenAIPilotAllowance) { p.ExpiresAt++ },
		func(p *OpenAIPilotAllowance) { p.MaxRequests++ },
		func(p *OpenAIPilotAllowance) { p.BudgetMicrousd++ },
		func(p *OpenAIPilotAllowance) { p.Limits.MaxInputTokens++ },
		func(p *OpenAIPilotAllowance) { p.BootID = "foreign" },
		func(p *OpenAIPilotAllowance) { p.Revision++ },
	} {
		bad := p
		mutate(&bad)
		bad.ID = pilotDigest(bad)
		if _, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "pilot-allow", Ref: c.Ref, Pilot: &bad, ConsentDigest: bad.ID, Passphrase: testVaultPassword}); e == nil {
			t.Fatal("altered review accepted")
		}
	}
	if _, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "pilot-allow", Ref: c.Ref, Pilot: &p, ConsentDigest: p.ID, Passphrase: "incorrect-disposable-passphrase"}); e == nil {
		t.Fatal("password bypass")
	}
	allowPilot(t, s, p)
	if _, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "pilot-allow", Ref: c.Ref, Pilot: &p, ConsentDigest: p.ID, Passphrase: testVaultPassword}); e == nil {
		t.Fatal("permit replenished")
	}
	after, _ := os.ReadFile(s.aiVaultPath())
	if !bytes.Equal(original, after) {
		t.Fatal("review changed encrypted vault")
	}
}
func TestOpenAIPilotSharedQuotaAndScope(t *testing.T) {
	s, _, p := pilotStore(t)
	allowPilot(t, s, p)
	if s.providerActionAllowed(agentDocument{}, AgentCommand{Action: "ai-start"}, p.ProfileID, false) || s.providerActionAllowed(agentDocument{}, AgentCommand{Action: "ai-start"}, p.ProfileID, true) {
		t.Fatal("manual review borrowed the Chat/task-only pilot")
	}
	for _, mutate := range []func(*AgentAIRequest){
		func(q *AgentAIRequest) { q.ConnectionRef = "other" },
		func(q *AgentAIRequest) { q.ProfileID = "other" },
		func(q *AgentAIRequest) { q.Model = legacyOpenAIModel },
		func(q *AgentAIRequest) { q.ChatPermissionID = "" },
		func(q *AgentAIRequest) { q.News = &NewsEvidence{} },
		func(q *AgentAIRequest) { q.ResearchTurnID = "forbidden" },
		func(q *AgentAIRequest) { q.Limits.MaxOutputTokens++ },
	} {
		q := pilotRequest(p, "bad", false)
		mutate(&q)
		if s.reservePilot(&q) {
			t.Fatal("wrong scope admitted")
		}
	}
	for i, id := range []string{"chat-a", "chat-b", "chat-c", "task"} {
		q := pilotRequest(p, id, i == 3)
		if !s.reservePilot(&q) || s.reservePilot(&q) {
			t.Fatal("quota or request replay")
		}
	}
	q := pilotRequest(p, "fifth", false)
	if s.reservePilot(&q) {
		t.Fatal("fifth attempt admitted")
	}
	if s.pilot.permit.UsedRequests != 4 || s.pilot.permit.AllocatedMicrousd <= 0 {
		t.Fatal("missing shared accounting")
	}
	// A smaller already-reviewed ceiling cannot be bypassed by either lane.
	s2, _, p2 := pilotStore(t)
	allowPilot(t, s2, p2)
	q = pilotRequest(p2, "cost", false)
	s2.pilot.permit.AllocatedMicrousd = p2.BudgetMicrousd - aiRequestReservation(q) + 1
	if s2.reservePilot(&q) {
		t.Fatal("shared cost ceiling bypassed")
	}
}
func TestOpenAIPilotExpiryLockAndRevocation(t *testing.T) {
	for _, mode := range []string{"expiry", "rollback", "lock", "logout", "revoke", "foreign-instance", "foreign-boot"} {
		t.Run(mode, func(t *testing.T) {
			s, c, p := pilotStore(t)
			allowPilot(t, s, p)
			q := pilotRequest(p, "attempt", false)
			if !s.reservePilot(&q) {
				t.Fatal("reserve")
			}
			switch mode {
			case "expiry":
				s.now = func() time.Time { return time.Unix(p.ExpiresAt, 0) }
			case "rollback":
				s.now = func() time.Time { return time.Unix(p.CreatedAt-1, 0) }
			case "lock":
				_, _ = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "lock", Ref: c.Ref})
			case "logout":
				s.lockAIConnection()
			case "revoke":
				if _, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "pilot-revoke", Ref: c.Ref}); e != nil {
					t.Fatal(e)
				}
			case "foreign-instance":
				s.instanceID = "other"
			case "foreign-boot":
				s.bootID = "other"
			}
			s.provider.mu.Lock()
			valid := s.pilotRequestLocked(q, true)
			s.provider.mu.Unlock()
			if valid {
				t.Fatal("revoked request still usable")
			}
			if s.pilot.permit.UsedRequests != 1 || s.pilot.permit.AllocatedMicrousd == 0 {
				t.Fatal("revocation erased accounting")
			}
		})
	}
}
func TestOpenAIPilotBoundedHTTPSAndNoReplay(t *testing.T) {
	s, c, p := pilotStore(t)
	allowPilot(t, s, p)
	var calls atomic.Int32
	next := pilotHTTPS(t, &calls)
	listing := authenticatedAITransport{modelsOnly: true, store: s, ref: c.Ref, next: next}
	request, _ := http.NewRequest("GET", "https://api.openai.com/v1/models", nil)
	res, e := listing.RoundTrip(request)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if _, e = listing.RoundTrip(request); e == nil {
		t.Fatal("listing replay")
	}
	q := pilotRequest(p, "attempt", false)
	if !s.reservePilot(&q) {
		t.Fatal("reserve")
	}
	rt := authenticatedAITransport{store: s, ref: c.Ref, next: next, request: &q}
	for i, path := range []string{"/v1/models/" + p.Model, "/v1/responses/input_tokens", "/v1/responses"} {
		method := "POST"
		if i == 0 {
			method = "GET"
		}
		r, _ := http.NewRequest(method, "https://api.openai.com"+path, nil)
		res, e = rt.RoundTrip(r)
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		if _, e = rt.RoundTrip(r); e == nil {
			t.Fatal("exchange replay")
		}
	}
	_, _ = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "pilot-revoke", Ref: c.Ref})
	if _, e = listing.RoundTrip(request); e == nil || calls.Load() != 4 {
		t.Fatal("network after revocation", calls.Load())
	}
}
func TestOpenAIPilotJoinedChatTaskMemoryAndRestart(t *testing.T) {
	s, _, p := pilotStore(t)
	allowPilot(t, s, p)
	v, permission := pilotChat(t, s, p)
	v = agentApply(t, s, AgentCommand{Action: "chat-send", ProfileID: p.ProfileID, ID: permission.SessionID, Text: "A fictional market is quiet.", ConsentDigest: permission.ID})
	var calls atomic.Int32
	if e := s.advanceAgentAI(t.Context(), pilotHTTPS(t, &calls), false); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), p.ProfileID, "")
	q := v.AIRequests[len(v.AIRequests)-1]
	if q.State != "completed" || q.SpentMicrousd <= 0 || q.HeldMicrousd != 0 || calls.Load() != 3 {
		t.Fatalf("joined chat: %+v / %d", q, calls.Load())
	}
	v = agentApply(t, s, AgentCommand{Action: "task-ai-prepare", Model: p.Model, ConnectionRef: p.ConnectionRef, ProfileID: p.ProfileID, ID: permission.SessionID, Name: "Synthetic note", Text: "Summarize the fictional note.", IntervalSeconds: 60, MaxRuns: 1, AuthorizationSeconds: 300, TotalBudgetMicrousd: 100000, Limits: &p.Limits})
	v = taskAIAuthorize(t, s, v)
	task := v.Tasks[len(v.Tasks)-1]
	review, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: p.ConnectionRef, ProfileID: p.ProfileID, Revision: v.Revision, TaskIDs: []string{task.ID}, AuthorizationSeconds: 300})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: p.ConnectionRef, Allowance: review.AccessReview, ConsentDigest: review.AccessReview.ID, Passphrase: testVaultPassword}); e != nil {
		t.Fatal(e)
	}
	v = taskAIRun(t, s, v)
	if e = s.advanceAgentAI(t.Context(), pilotHTTPS(t, &calls), false); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), p.ProfileID, "")
	q = v.AIRequests[len(v.AIRequests)-1]
	if q.State != "completed" || v.Tasks[len(v.Tasks)-1].State != "completed" || s.pilot.permit.UsedRequests != 2 {
		t.Fatalf("joined task %+v", q)
	}
	// Discuss the saved task run, then explicitly save its source-bound note.
	v = agentApply(t, s, AgentCommand{Action: "task-run-explain", ProfileID: p.ProfileID, ID: v.Runs[len(v.Runs)-1].ID})
	v = agentApply(t, s, AgentCommand{Action: "memory-chat-save", ProfileID: p.ProfileID, ID: v.Messages[len(v.Messages)-1].ID, Source: "message", Text: "The fictional market was quiet."})
	if len(v.Memories) == 0 {
		t.Fatal("missing source-bound memory")
	}
	// Unknown usage remains durable across restart; no target/grant is restored.
	v = agentApply(t, s, AgentCommand{Action: "chat-send", ProfileID: p.ProfileID, ID: permission.SessionID, Text: "What did we retain about the fictional market?", ConsentDigest: permission.ID})
	working := pilotHTTPS(t, &calls)
	if e = s.advanceAgentAI(t.Context(), aiTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/v1/responses" {
			return nil, io.ErrUnexpectedEOF
		}
		return working.RoundTrip(r)
	}), false); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), p.ProfileID, "")
	last := v.AIRequests[len(v.AIRequests)-1]
	if last.State != "unknown" || last.HeldMicrousd == 0 {
		t.Fatalf("lost hold: %+v", last)
	}
	dir := s.dir
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	n, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer n.Close()
	if e = n.advanceAgentAI(t.Context(), aiTestTransport(func(*http.Request) (*http.Response, error) {
		t.Error("restart replay")
		return nil, io.ErrUnexpectedEOF
	}), false); e != nil {
		t.Fatal(e)
	}
	after, _ := n.Agents(t.Context(), p.ProfileID, "")
	got := after.AIRequests[len(after.AIRequests)-1]
	if n.pilot.target != nil || n.aiExecutionAvailable(false) || got.HeldMicrousd != last.HeldMicrousd || got.State != "unknown" || len(after.Memories) != len(v.Memories) {
		t.Fatal("restart lost state or revived authority")
	}
}

func TestOpenAIPilotAuthenticatedHTTPReview(t *testing.T) {
	s, c, p := pilotStore(t)
	token := claimFixture(t, s)
	h, e := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	command := AIConnectionCommand{Action: "pilot-allow", Ref: c.Ref, Pilot: &p, ConsentDigest: p.ID, Passphrase: testVaultPassword}
	body, _ := json.Marshal(command)
	for _, auth := range []bool{false, true} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:7331/api/v1/ai/connection", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("X-CSRF-Token", csrfToken(token))
		if auth {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if (!auth && w.Code != 401) || (auth && w.Code != 200) {
			t.Fatal("owner boundary", auth, w.Code, w.Body.String())
		}
	}
	if !s.aiExecutionAvailable(false) {
		t.Fatal("authenticated allowance missing")
	}
}
