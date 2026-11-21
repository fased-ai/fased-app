package core

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestDynamicDiscoveryOpenAISignInFormatsAndChatAdmission(t *testing.T) {
	t.Parallel()
	const discoveredModelID = "gpt-5.4"

	t.Run("chatgpt-models-shape-end-to-end", func(t *testing.T) {
		t.Parallel()
		s, account, _ := ordinaryPlanFixture(t)
		var wireSent string
		rt := nativeHTTPSFixture(t, "openai", func(w http.ResponseWriter, r *http.Request) {
			if r.Host != "api.openai.com" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer fased-test-signin-access-") {
				t.Error("unexpected origin or auth")
				w.WriteHeader(400)
				return
			}
			if r.Method == "GET" && r.URL.Path == "/v1/models" {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"models": []any{
						map[string]any{"slug": discoveredModelID, "display_name": "GPT-5.4", "visibility": "list"},
						map[string]any{"slug": "hidden-internal", "display_name": "Hidden", "visibility": "hide"},
					},
				})
				return
			}
			if r.Method != "POST" || r.URL.Path != "/v1/responses" {
				t.Error("unexpected plan route", r.Method, r.URL.Path)
				w.WriteHeader(400)
				return
			}
			body := make([]byte, 65536)
			n, _ := r.Body.Read(body)
			if string(body[:n]) != wireSent {
				t.Error("disclosed wire changed")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("X-Request-Id", "req_dynamic_signin")
			text := `{"answer":"GPT-5.4 live discovered reply.","cards":[]}`
			ordinaryPlanEvent(w, "response.created", map[string]any{"type": "response.created", "sequence_number": 0, "response": map[string]any{"id": "resp_dyn", "model": discoveredModelID}})
			ordinaryPlanEvent(w, "response.output_text.delta", map[string]any{"type": "response.output_text.delta", "sequence_number": 1, "output_index": 0, "content_index": 0, "delta": text})
			ordinaryPlanEvent(w, "response.output_text.done", map[string]any{"type": "response.output_text.done", "sequence_number": 2, "output_index": 0, "content_index": 0, "text": text})
			ordinaryPlanEvent(w, "response.completed", map[string]any{
				"type":            "response.completed",
				"sequence_number": 3,
				"response": map[string]any{
					"id":     "resp_dyn",
					"model":  discoveredModelID,
					"status": "completed",
					"usage":  map[string]any{"input_tokens": 120, "output_tokens": 18, "total_tokens": 138, "input_tokens_details": map[string]any{"cached_tokens": 0}},
					"output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text}}}},
				},
			})
		})
		v, err := s.refreshProviderModels(t.Context(), account.Ref, rt)
		if err != nil {
			t.Fatal(err)
		}
		if v.Catalog.State != "ready" {
			t.Fatalf("catalog not ready: %+v", v.Catalog)
		}
		m := modelChoice(v, discoveredModelID)
		if !m.Selectable || m.Name != "GPT-5.4" || m.Policy == nil || m.Policy.WireVersion != planSignInWire || m.Policy.ExpiresAt != 0 || !isDiscoveredPolicy(m.Policy) {
			t.Fatalf("discovered sign-in model metadata mismatch: %+v", m)
		}
		if hidden := modelChoice(v, "hidden-internal"); hidden.ID != "" {
			t.Fatalf("hidden model exposed in catalog: %+v", hidden)
		}
		if _, err := s.ChangeAgents(t.Context(), AgentCommand{Action: "profile-update", ProfileID: "wen-operator", Name: "Operator", Provider: "openai", AccountRef: &account.Ref, Model: "gpt-not-listed"}); err == nil {
			t.Fatal("unlisted model selected on profile")
		}
		agents := agentApply(t, s, AgentCommand{Action: "profile-update", ProfileID: "wen-operator", Name: "Operator", Provider: "openai", AccountRef: &account.Ref, Model: discoveredModelID})
		agents = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: agents.ProfileID, Name: "Dynamic GPT-5.4 Chat"})
		sessionID := agents.Sessions[len(agents.Sessions)-1].ID
		limits := &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 60}
		reviewed := agentApply(t, s, AgentCommand{Action: "chat-review", ProfileID: agents.ProfileID, ID: sessionID, UseAgentDefaults: true, Limits: limits, MemorySelection: &AIMemorySelection{Mode: "none"}, MaxRuns: 1, AuthorizationSeconds: 900})
		perm := *reviewed.ChatReview
		agents = agentApply(t, s, AgentCommand{Action: "chat-allow", ProfileID: agents.ProfileID, ID: sessionID, ChatPermission: &perm, ConsentDigest: perm.ID})
		agents, err = s.changeAgents(t.Context(), AgentCommand{Action: "chat-send", Revision: agents.Revision, ProfileID: agents.ProfileID, ID: sessionID, Text: "Ping dynamic model", ConsentDigest: perm.ID}, true)
		if err != nil {
			t.Fatal(err)
		}
		wireSent = agents.AIRequests[len(agents.AIRequests)-1].Disclosure
		if err := s.advanceAgentAI(t.Context(), rt, false); err != nil {
			t.Fatal(err)
		}
		done, err := s.Agents(t.Context(), agents.ProfileID, "")
		if err != nil {
			t.Fatal(err)
		}
		q := done.AIRequests[len(done.AIRequests)-1]
		if q.State != "completed" || q.Answer != "GPT-5.4 live discovered reply." || q.Model != discoveredModelID {
			t.Fatalf("dynamic sign-in chat execution failed: %+v", q)
		}
	})

	t.Run("openai-data-list-fallback-and-cny-isolation", func(t *testing.T) {
		t.Parallel()
		s, account, _ := ordinaryPlanFixture(t)
		rt := nativeHTTPSFixture(t, "openai", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []any{
					map[string]any{"id": discoveredModelID, "object": "model", "created": 1740000000, "owned_by": "openai"},
				},
			})
		})
		v, err := s.refreshProviderModels(t.Context(), account.Ref, rt)
		if err != nil || v.Catalog.State != "ready" {
			t.Fatal("sign-in data[] fallback failed", err, v.Catalog)
		}
		m := modelChoice(v, discoveredModelID)
		if !m.Selectable || m.Policy == nil || m.Policy.WireVersion != planSignInWire || m.Policy.ExpiresAt != 0 {
			t.Fatalf("sign-in data[] model not selectable: %+v", m)
		}

		moonshot := addNativeAccount(t, s, "moonshot", false)
		moonshotRT := aiTestTransport(func(r *http.Request) (*http.Response, error) {
			return fixtureAdmissionResponse(r, map[string]any{"data": []map[string]any{{"id": "kimi-k9-cny", "created": 1740000000}}}), nil
		})
		mv, err := s.refreshProviderModels(t.Context(), moonshot.Ref, moonshotRT)
		if err != nil {
			t.Fatal(err)
		}
		if dm := modelChoice(mv, "kimi-k9-cny"); dm.Selectable || dm.Availability != "currency-unsupported" || dm.Policy != nil {
			t.Fatalf("moonshot CNY model became selectable: %+v", dm)
		}
	})
}

func TestDynamicDiscoveryOpenRouterAndLocalNewModels(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)

	// OpenRouter discovering an arbitrary new model + refusing retired/unlisted IDs.
	router := addNativeAccount(t, s, "openrouter", false)
	const routerModel = "anthropic/claude-9-opus"
	const retiredModel = "anthropic/claude-1-retired"
	routerRT := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			return fixtureAdmissionResponse(r, map[string]any{
				"data": []map[string]any{
					{"id": routerModel, "created": 1740000000},
					{"id": retiredModel, "created": 1740000000, "shutdown_date": "2026-10-01"},
				},
			}), nil
		}
		return (fixtureAITransport{}).RoundTrip(r)
	})
	rv, err := s.refreshProviderModels(t.Context(), router.Ref, routerRT)
	if err != nil || rv.Catalog.State != "ready" {
		t.Fatal("openrouter discovery failed", err, rv.Catalog)
	}
	if m := modelChoice(rv, routerModel); !m.Selectable || m.Policy == nil || !isDiscoveredPolicy(m.Policy) {
		t.Fatalf("openrouter dynamic model not selectable: %+v", m)
	}
	if m := modelChoice(rv, retiredModel); m.Selectable || m.Availability != "retired" {
		t.Fatalf("openrouter retired model selectable: %+v", m)
	}
	agents := agentApply(t, s, AgentCommand{Action: "profile-update", ProfileID: "wen-operator", Name: "Operator", Provider: "openrouter", AccountRef: &router.Ref, Model: routerModel})
	if _, err := s.ChangeAgents(t.Context(), AgentCommand{Revision: agents.Revision, Action: "profile-update", ProfileID: agents.ProfileID, Name: "Operator", Provider: "openrouter", AccountRef: &router.Ref, Model: "openrouter/unlisted-fake"}); err == nil {
		t.Fatal("unlisted OpenRouter model accepted on profile")
	}

	// Local discovering an arbitrary new Ollama model and admitting chat through execution.
	localAcc := addLocalFixtureAccount(t, s)
	const localNewModel = "qwen3:32b"
	localRT := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" && r.URL.Path == "/v1/models" {
			return fixtureAdmissionResponse(r, map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": localNewModel, "object": "model", "owned_by": "library", "created": 1},
				},
			}), nil
		}
		if r.Method == "POST" && r.URL.Path == "/v1/responses" {
			answer := `{"answer":"Local dynamic qwen3 ready.","cards":[]}`
			body, _ := json.Marshal(map[string]any{
				"id":           "resp_local_dyn",
				"model":        localNewModel,
				"status":       "completed",
				"service_tier": "default",
				"usage":        map[string]any{"input_tokens": 90, "output_tokens": 12, "total_tokens": 102},
				"output": []any{map[string]any{
					"type":    "message",
					"role":    "assistant",
					"status":  "completed",
					"content": []any{map[string]any{"type": "output_text", "text": answer}},
				}},
			})
			return fixtureAIStream(r, body, []byte(answer)), nil
		}
		return nil, http.ErrNotSupported
	})
	lv, err := s.refreshProviderModels(t.Context(), localAcc.Ref, localRT)
	if err != nil || !modelChoice(lv, localNewModel).Selectable {
		t.Fatalf("local dynamic model not selectable: %v %+v", err, lv.Catalog)
	}
	agents = agentApply(t, s, AgentCommand{Action: "profile-create", Name: "Local Qwen", Provider: "local", AccountRef: &localAcc.Ref, Model: localNewModel})
	profileID := agents.ProfileID
	agents = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: profileID, Name: "Local Dynamic"})
	sessionID := agents.Sessions[len(agents.Sessions)-1].ID
	reviewed := agentApply(t, s, AgentCommand{Action: "chat-review", ProfileID: profileID, ID: sessionID, UseAgentDefaults: true, Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 60}, MemorySelection: &AIMemorySelection{Mode: "none"}, MaxRuns: 1, AuthorizationSeconds: 900})
	perm := *reviewed.ChatReview
	agents = agentApply(t, s, AgentCommand{Action: "chat-allow", ProfileID: profileID, ID: sessionID, ChatPermission: &perm, ConsentDigest: perm.ID})
	_ = chatSend(t, s, agents, perm, "Hello local model")
	if err := s.advanceAgentAI(t.Context(), localRT, true); err != nil {
		t.Fatal(err)
	}
	done, err := s.Agents(t.Context(), profileID, "")
	if err != nil {
		t.Fatal(err)
	}
	q := done.AIRequests[len(done.AIRequests)-1]
	if q.State != "completed" || q.Model != localNewModel || q.Answer != "Local dynamic qwen3 ready." {
		t.Fatalf("local dynamic chat failed: %+v", q)
	}
}
