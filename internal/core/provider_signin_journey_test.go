package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func assertSignInJourneyDisclosure(t *testing.T, q AgentAIRequest, stream bool) {
	t.Helper()
	wire, err := aiWire(q)
	if err != nil || q.Policy == nil || q.Policy.WireVersion != "openai-signin-v2" || q.Policy.InputMicrousdPerMillion != 0 || q.Policy.OutputMicrousdPerMillion != 0 || string(wire) != q.Disclosure || q.Stream != stream {
		t.Fatal("sign-in disclosure was not frozen before consent", err, q.Stream)
	}
	var fields map[string]json.RawMessage
	var input []struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	var streaming bool
	if json.Unmarshal(wire, &fields) != nil || json.Unmarshal(fields["input"], &input) != nil || len(input) != 1 || input[0].Role != "user" || len(input[0].Content) != 1 || input[0].Content[0].Type != "input_text" || input[0].Content[0].Text != q.Context || json.Unmarshal(fields["stream"], &streaming) != nil || !streaming {
		t.Fatal("sign-in wire is not the reviewed input array and required SSE")
	}
	for _, field := range []string{"background", "service_tier", "max_output_tokens", "truncation", "prompt_cache_options"} {
		if _, exists := fields[field]; exists {
			t.Fatal("API-only field leaked into sign-in disclosure", field)
		}
	}
}

func TestProviderSignInConnectedJourney(t *testing.T) {
	s := fixtureStore(t)
	account := completeSignInFixture(t, s, true)
	v := agentApply(t, s, AgentCommand{Action: "profile-create", Name: "Plan analyst", Text: "Use only retained synthetic sources", Provider: "openai", AccountRef: &account.Ref, Model: admittedOpenAIModel})
	profile := v.ProfileID
	v = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: profile, Name: "Synthetic plan conversation"})
	session := v.Sessions[0].ID
	limits := &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 5}

	// The manual review contains the actual sign-in wire before any consent.
	v = agentApply(t, s, AgentCommand{Action: "ai-prepare", ProfileID: profile, ID: session, UseAgentDefaults: true, Text: "Review a synthetic note", MemorySelection: &AIMemorySelection{Mode: "none"}, Limits: limits})
	review := v.AIRequests[len(v.AIRequests)-1]
	assertSignInJourneyDisclosure(t, review, false)
	if review.State != "prepared" || review.Digest != aiDigest(review) || review.ConnectionRef != account.Ref || review.Model != admittedOpenAIModel || review.HeldMicrousd != 0 {
		t.Fatal("manual sign-in review changed identity or created authority")
	}
	v = agentApply(t, s, AgentCommand{Action: "ai-cancel", ProfileID: profile, ID: review.ID})

	v = agentApply(t, s, AgentCommand{Action: "chat-review", ProfileID: profile, ID: session, UseAgentDefaults: true, Limits: limits, MemorySelection: &AIMemorySelection{Mode: "relevant"}, MaxRuns: 2, AuthorizationSeconds: 900})
	permission := *v.ChatReview
	if permission.ConnectionRef != account.Ref || permission.Model != admittedOpenAIModel || permission.ID != chatDigest(permission) {
		t.Fatal("Chat review lost configured account/model")
	}
	v = agentApply(t, s, AgentCommand{Action: "chat-allow", ProfileID: profile, ID: session, ChatPermission: &permission, ConsentDigest: permission.ID})
	v = chatSend(t, s, v, permission, "A fictional market has no new events.")
	chat := v.AIRequests[len(v.AIRequests)-1]
	assertSignInJourneyDisclosure(t, chat, true)
	if chat.Digest != aiDigest(chat) || chat.ConnectionRef != account.Ref || chat.State != "queued" {
		t.Fatal("Chat queued a different route or wire")
	}
	paths := []string{}
	transport := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer fased-test-signin-access-") || r.Header.Get("X-Api-Key") != "" {
			t.Error("generation/discovery did not use the selected sign-in token")
		}
		if r.URL.Path != "/v1/models" && r.URL.Path != "/v1/responses" {
			t.Error("sign-in reached an API-key admission endpoint", r.URL.Path)
			return nil, errors.New("wrong sign-in endpoint")
		}
		if r.Method == "POST" {
			raw, err := io.ReadAll(r.Body)
			if err != nil || !bytes.Contains(raw, []byte(`"stream":true`)) || !bytes.Contains(raw, []byte(`"input":[`)) {
				t.Error("submitted wire differs from reviewed sign-in wire", err)
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
		}
		return (fixtureAITransport{}).RoundTrip(r)
	})
	if err := s.advanceAgentAI(t.Context(), transport, true); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), profile, "")
	chat = v.AIRequests[len(v.AIRequests)-1]
	if chat.State != "completed" || !chat.Stream || chat.Answer == "" || chat.PartialAnswer != "" || chat.HeldMicrousd != 0 || chat.CostBasis != "chatgpt-plan-reported-usage" || chat.Admission == nil || !chat.Admission.GenerationAttempted || chat.Admission.InputTokens != 0 || chat.Admission.CountResponseSHA256 != "" || chat.Admission.CountPayloadSHA256 != newsHash([]byte(chat.Disclosure)) || chat.InputTokens <= 0 || chat.SpentMicrousd != 0 {
		t.Fatal("sign-in Chat did not reconcile its bounded stream", chat.State, chat.Failure, chat.CostBasis)
	}
	if !reflect.DeepEqual(paths, []string{"GET /v1/models", "POST /v1/responses"}) {
		t.Fatal("sign-in used API-key access/count paths", paths)
	}

	// A scheduled task freezes the same route, but retains its non-Chat stream flag.
	v = agentApply(t, s, AgentCommand{Action: "task-ai-prepare", ProfileID: profile, ID: session, UseAgentDefaults: true, Name: "Synthetic summary", Text: "Summarize the fictional note", IntervalSeconds: 60, MaxRuns: 1, AuthorizationSeconds: 900, Limits: limits})
	task := v.Tasks[len(v.Tasks)-1]
	taskRequest := taskAIRequest(task, s.now().Unix())
	assertSignInJourneyDisclosure(t, taskRequest, false)
	if task.State != "review" || task.AI.Authorized || task.AI.Digest != taskAIDigest(task) || taskRequest.ConnectionRef != account.Ref || taskRequest.Model != admittedOpenAIModel {
		t.Fatal("task review changed route or authorized itself")
	}
	v = taskAIAuthorize(t, s, v)
	grant, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: account.Ref, ProfileID: profile, Revision: v.Revision, TaskIDs: []string{task.ID}, AuthorizationSeconds: 900})
	if err != nil || grant.AccessReview == nil {
		t.Fatal("task credential review", err)
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: account.Ref, Allowance: grant.AccessReview, ConsentDigest: grant.AccessReview.ID, Passphrase: testVaultPassword}); err != nil {
		t.Fatal(err)
	}
	v = taskAIRun(t, s, v)
	if err = s.advanceAgentAI(t.Context(), transport, true); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), profile, "")
	result := v.AIRequests[len(v.AIRequests)-1]
	if result.State != "completed" || result.Stream || result.TaskID != task.ID || result.ConnectionRef != account.Ref || result.Policy.WireVersion != "openai-signin-v2" || result.CostBasis != "chatgpt-plan-reported-usage" || result.HeldMicrousd != 0 || len(v.Runs) != 1 || v.Runs[0].AIRequestID != result.ID || v.Runs[0].AIDigest != task.AI.Digest || v.Tasks[len(v.Tasks)-1].State != "completed" {
		t.Fatal("task did not settle one source-bound result", result.State, result.Failure)
	}
	if !reflect.DeepEqual(paths, []string{"GET /v1/models", "POST /v1/responses", "GET /v1/models", "POST /v1/responses"}) {
		t.Fatal("task reached a different admission route", paths)
	}
	v = agentApply(t, s, AgentCommand{Action: "task-run-explain", ProfileID: profile, ID: v.Runs[0].ID})
	v = agentApply(t, s, AgentCommand{Action: "memory-chat-save", ProfileID: profile, Source: "message", ID: v.Messages[len(v.Messages)-1].ID, Text: "The fictional market had no new events."})
	if len(v.Memories) != 1 || !strings.Contains(v.Memories[0].Source, "sha256=") {
		t.Fatal("result did not retain inspectable memory provenance")
	}
	foreign, err := s.Agents(t.Context(), "news-researcher", "fictional")
	if err != nil || len(foreign.Messages) != 0 || len(foreign.Memories) != 0 || len(foreign.AIRequests) != 0 {
		t.Fatal("sign-in journey leaked into another agent", err)
	}

	for _, stop := range []string{"stop", "revoke", "lock"} {
		t.Run(stop, func(t *testing.T) {
			v = agentApply(t, s, AgentCommand{Action: "chat-review", ProfileID: profile, ID: session, UseAgentDefaults: true, Limits: limits, MemorySelection: &AIMemorySelection{Mode: "none"}, MaxRuns: 1, AuthorizationSeconds: 900})
			p := *v.ChatReview
			v = agentApply(t, s, AgentCommand{Action: "chat-allow", ProfileID: profile, ID: session, ChatPermission: &p, ConsentDigest: p.ID})
			v = chatSend(t, s, v, p, "Do not dispatch this synthetic reply")
			pending := v.AIRequests[len(v.AIRequests)-1]
			before, _, _ := readAgents(t.Context(), s.db)
			switch stop {
			case "stop":
				v = agentApply(t, s, AgentCommand{Action: "ai-cancel", ProfileID: profile, ID: pending.ID})
			case "revoke":
				v = agentApply(t, s, AgentCommand{Action: "chat-revoke", ProfileID: profile, ID: session, ConsentDigest: p.ID})
			case "lock":
				if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "lock", Ref: account.Ref}); err != nil {
					t.Fatal(err)
				}
			}
			calls := len(paths)
			if err := s.advanceAgentAI(t.Context(), transport, true); err != nil {
				t.Fatal(err)
			}
			after, _, _ := readAgents(t.Context(), s.db)
			last := after.AIRequests[len(after.AIRequests)-1]
			if len(paths) != calls || last.State != "cancelled" || last.HeldMicrousd != 0 || !reflect.DeepEqual(before.Messages, after.Messages) || !reflect.DeepEqual(before.Memories, after.Memories) || !reflect.DeepEqual(before.Runs, after.Runs) || !reflect.DeepEqual(before.AIRequests[:len(before.AIRequests)-1], after.AIRequests[:len(after.AIRequests)-1]) {
				t.Fatal("revoked sign-in work dispatched or changed retained sources", stop, last.State)
			}
		})
	}
}

func TestProviderSignInAdmissionRejectsRawFixtureAndWrongMethodTransport(t *testing.T) {
	s := fixtureStore(t)
	account := completeSignInFixture(t, s, true)
	v := agentApply(t, s, AgentCommand{Action: "profile-create", Name: "Route isolation", Provider: "openai", AccountRef: &account.Ref, Model: admittedOpenAIModel})
	v = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: v.ProfileID, Name: "Isolated route"})
	v = agentApply(t, s, AgentCommand{Action: "ai-prepare", ProfileID: v.ProfileID, ID: v.Sessions[0].ID, UseAgentDefaults: true, Text: "Synthetic only", Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 5}})
	q := v.AIRequests[0]
	assertSignInJourneyDisclosure(t, q, false)
	calls := 0
	raw := aiTestTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("I/O forbidden")
	})
	copy := q
	if admitSignIn(t.Context(), &copy, raw, s.now) || calls != 0 || copy.Admission == nil || copy.Admission.GenerationAttempted {
		t.Fatal("raw transport admitted sign-in or performed I/O")
	}
	s.providerFixture = false
	copy = q
	bound := authenticatedAITransport{store: s, ref: q.ConnectionRef, request: &copy, next: raw}
	if admitSignIn(t.Context(), &copy, bound, s.now) || calls != 0 || copy.Admission == nil || copy.Admission.GenerationAttempted {
		t.Fatal("ordinary mode admitted subscription fixture or performed I/O")
	}
	s.providerFixture = true
	api := addNativeAccount(t, s, "openai", false)
	copy = q
	copy.ConnectionRef = api.Ref
	bound = authenticatedAITransport{store: s, ref: api.Ref, request: &copy, next: raw}
	if admitSignIn(t.Context(), &copy, bound, s.now) || calls != 0 || copy.Admission == nil || copy.Admission.GenerationAttempted {
		t.Fatal("sign-in wire fell back to API-key discovery/admission")
	}
	request, err := http.NewRequest("POST", "https://api.openai.com/v1/responses", strings.NewReader(copy.Disclosure))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bound.RoundTrip(request); err == nil || calls != 0 {
		t.Fatal("sign-in wire used API-key generation transport")
	}
}
