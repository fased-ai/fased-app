package core

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
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

func TestDynamicDiscoveryAnthropicGoogleOpenRouterAndLocalNames(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)

	// 1. Anthropic pagination preserves display_name on dynamically discovered models.
	ant := addNativeAccount(t, s, "anthropic", false)
	antRT := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		return fixtureAdmissionResponse(r, map[string]any{
			"data": []map[string]any{
				{
					"id":           "claude-sonnet-6-0",
					"type":         "model",
					"lifecycle":    "active",
					"line":         "sonnet",
					"display_name": "Claude Sonnet 6.0",
					"created_at":   "2026-10-08T00:00:00Z",
				},
			},
			"has_more": false,
			"last_id":  "claude-sonnet-6-0",
		}), nil
	})
	antView, err := s.refreshProviderModels(t.Context(), ant.Ref, antRT)
	if err != nil {
		t.Fatal(err)
	}
	if m := modelChoice(antView, "claude-sonnet-6-0"); !m.Selectable || m.Name != "Claude Sonnet 6.0" || !isDiscoveredPolicy(m.Policy) {
		t.Fatalf("anthropic display_name discovery mismatch: %+v", m)
	}

	// 2. Google strips models/ prefix and preserves display_name / name.
	goo := addNativeAccount(t, s, "google", false)
	gooRT := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		return fixtureAdmissionResponse(r, map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "models/gemini-4.0-pro", "display_name": "Gemini 4.0 Pro", "created": 1744588800},
			},
		}), nil
	})
	gooView, err := s.refreshProviderModels(t.Context(), goo.Ref, gooRT)
	if err != nil {
		t.Fatal(err)
	}
	if m := modelChoice(gooView, "gemini-4.0-pro"); !m.Selectable || m.Name != "Gemini 4.0 Pro" || !isDiscoveredPolicy(m.Policy) {
		t.Fatalf("google display_name discovery mismatch: %+v", m)
	}

	// 3. OpenRouter preserves model name.
	or := addNativeAccount(t, s, "openrouter", false)
	orRT := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		return fixtureAdmissionResponse(r, map[string]any{
			"data": []map[string]any{
				{"id": "anthropic/claude-9-opus", "name": "Anthropic: Claude 9 Opus", "created": 1744588800},
			},
		}), nil
	})
	orView, err := s.refreshProviderModels(t.Context(), or.Ref, orRT)
	if err != nil {
		t.Fatal(err)
	}
	if m := modelChoice(orView, "anthropic/claude-9-opus"); !m.Selectable || m.Name != "Anthropic: Claude 9 Opus" || !isDiscoveredPolicy(m.Policy) {
		t.Fatalf("openrouter name discovery mismatch: %+v", m)
	}

	// 4. Local Ollama accepts omitted "object" field and preserves display name, while rejecting invalid "object".
	loc := addLocalFixtureAccount(t, s)
	locRT := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		return fixtureAdmissionResponse(r, map[string]any{
			"data": []map[string]any{
				{"id": "qwen3:32b", "name": "Qwen 3 32B", "owned_by": "library", "created": 1},
			},
		}), nil
	})
	locView, err := s.refreshProviderModels(t.Context(), loc.Ref, locRT)
	if err != nil {
		t.Fatal(err)
	}
	if m := modelChoice(locView, "qwen3:32b"); !m.Selectable || m.Name != "Qwen 3 32B" {
		t.Fatalf("local omitted-object discovery mismatch: %+v", m)
	}

	badLocRT := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		return fixtureAdmissionResponse(r, map[string]any{
			"object": "response",
			"data":   []map[string]any{{"id": "qwen3:32b", "object": "model", "owned_by": "local"}},
		}), nil
	})
	badLocView, err := s.refreshProviderModels(t.Context(), loc.Ref, badLocRT)
	if err != nil || badLocView.Catalog.State != "failed" || modelChoice(badLocView, "qwen3:32b").Selectable {
		t.Fatalf("invalid local object field accepted: err=%v catalog=%+v", err, badLocView.Catalog)
	}
}

func TestCanonicalProviderOrderPlacesTopFourFirstAndLocalLast(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	s.provider.apiCredentials = true
	v, err := s.AIConnection()
	if err != nil {
		t.Fatal(err)
	}
	wantPrefix := []string{"openai", "anthropic", "google", "xai", "openrouter"}
	if len(v.Catalog.Providers) != len(canonicalProviderOrder) {
		t.Fatalf("provider count = %d, want %d", len(v.Catalog.Providers), len(canonicalProviderOrder))
	}
	for i, id := range wantPrefix {
		if v.Catalog.Providers[i].ID != id {
			t.Fatalf("provider[%d] = %q, want %q", i, v.Catalog.Providers[i].ID, id)
		}
	}
	last := v.Catalog.Providers[len(v.Catalog.Providers)-1]
	if last.ID != "local" {
		t.Fatalf("last provider = %q, want local", last.ID)
	}
	for i, id := range canonicalProviderOrder {
		if v.Catalog.Providers[i].ID != id || v.CredentialModes[i] != id {
			t.Fatalf("order mismatch at %d: catalog=%q mode=%q want=%q", i, v.Catalog.Providers[i].ID, v.CredentialModes[i], id)
		}
	}
}

func TestPersistentProviderModelCacheSurvivesRestart(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	dir := s.dir
	acc := addNativeAccount(t, s, "anthropic", true)
	rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		return fixtureAdmissionResponse(r, map[string]any{
			"data": []map[string]any{
				{
					"id":           "claude-sonnet-6-5",
					"type":         "model",
					"lifecycle":    "active",
					"line":         "sonnet",
					"display_name": "Claude Sonnet 6.5",
					"created_at":   "2026-11-01T00:00:00Z",
				},
			},
			"has_more": false,
			"last_id":  "claude-sonnet-6-5",
		}), nil
	})
	refreshed, err := s.refreshProviderModels(t.Context(), acc.Ref, rt)
	if err != nil {
		t.Fatal(err)
	}
	if m := modelChoice(refreshed, "claude-sonnet-6-5"); !m.Selectable || m.Name != "Claude Sonnet 6.5" {
		t.Fatalf("refreshed dynamic model missing: %+v", m)
	}
	if _, err := s.ensureAgentV2(t.Context()); err != nil {
		t.Fatalf("ensureAgentV2 after cache write: %v", err)
	}

	reopened := reopenFixtureStore(t, dir)
	reopened.provider.apiCredentials = true
	unlocked, err := reopened.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:     "unlock",
		Ref:        acc.Ref,
		Passphrase: testVaultPassword,
	})
	if err != nil || unlocked.Locked {
		t.Fatalf("unlock reopened store failed: view=%+v err=%v", unlocked, err)
	}
	afterRestart, err := reopened.AIConnection(acc.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if afterRestart.Catalog.State != "ready" {
		t.Fatalf("cached catalog state = %q, want ready", afterRestart.Catalog.State)
	}
	if m := modelChoice(afterRestart, "claude-sonnet-6-5"); !m.Selectable || m.Name != "Claude Sonnet 6.5" {
		t.Fatalf("cached dynamic model did not survive restart: %+v", m)
	}

	if _, err := reopened.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:     "remove",
		Ref:        acc.Ref,
		Passphrase: testVaultPassword,
	}); err != nil {
		t.Fatalf("remove account failed: %v", err)
	}
	if cacheMap := reopened.loadProviderModelCacheMap(t.Context()); len(cacheMap) != 0 {
		t.Fatalf("cache entry survived remove: %+v", cacheMap)
	}
}

func TestDynamicDiscoveryFiltersNonChatModelsAndRoutesGoogleSignIn(t *testing.T) {
	t.Parallel()
	now := time.Unix(1774000000, 0)
	openaiDisc := modelDiscovery{
		State:     "ready",
		FetchedAt: now.Unix(),
		ExpiresAt: now.Add(providerModelCacheTTL).Unix(),
		Models: []discoveredModel{
			{ID: "gpt-5.4", Object: "model", OwnedBy: "openai", Created: now.Unix()},
			{ID: "gpt-5.4-chat", Name: "GPT-5.4 Chat", Object: "model", OwnedBy: "openai", Created: now.Unix()},
			{ID: "text-embedding-3-large", Object: "model", OwnedBy: "openai", Created: now.Unix()},
			{ID: "whisper-1", Object: "model", OwnedBy: "openai", Created: now.Unix()},
			{ID: "tts-1", Object: "model", OwnedBy: "openai", Created: now.Unix()},
			{ID: "dall-e-3", Object: "model", OwnedBy: "openai", Created: now.Unix()},
			{ID: "gpt-image-1", Object: "model", OwnedBy: "openai", Created: now.Unix()},
			{ID: "sora-2", Object: "model", OwnedBy: "openai", Created: now.Unix()},
			{ID: "omni-moderation-latest", Object: "model", OwnedBy: "openai", Created: now.Unix()},
			{ID: "gpt-realtime-mini", Object: "model", OwnedBy: "openai", Created: now.Unix()},
		},
	}
	openAIView := AIConnectionView{Ref: "acc_openai", Provider: "openai", AuthMethod: "api-key"}
	cat := discoveredProviderCatalog(openAIView, openaiDisc, now.Unix())
	seen := map[string]bool{}
	for _, m := range cat.Models {
		seen[m.ID] = true
	}
	if !seen["gpt-5.4-chat"] {
		t.Fatalf("expected chat model gpt-5.4-chat in catalog: %+v", cat.Models)
	}
	for _, excluded := range []string{"text-embedding-3-large", "whisper-1", "tts-1", "dall-e-3", "gpt-image-1", "sora-2", "omni-moderation-latest", "gpt-realtime-mini"} {
		if seen[excluded] {
			t.Fatalf("non-chat model %q leaked into discovered catalog", excluded)
		}
	}

	googleDisc := modelDiscovery{
		State:     "ready",
		FetchedAt: now.Unix(),
		ExpiresAt: now.Add(providerModelCacheTTL).Unix(),
		Models: []discoveredModel{
			{ID: "gemini-4.0-pro", Name: "Gemini 4.0 Pro", Object: "model", OwnedBy: "google", Created: now.Unix()},
			{ID: "imagen-4", Object: "model", OwnedBy: "google", Created: now.Unix()},
			{ID: "veo-3", Object: "model", OwnedBy: "google", Created: now.Unix()},
			{ID: "gemini-embedding-001", Object: "model", OwnedBy: "google", Created: now.Unix()},
		},
	}
	googleView := AIConnectionView{Ref: "acc_google", Provider: "google", AuthMethod: "oauth"}
	gCat := discoveredProviderCatalog(googleView, googleDisc, now.Unix())
	gSeen := map[string]bool{}
	for _, m := range gCat.Models {
		gSeen[m.ID] = true
	}
	if !gSeen["gemini-4.0-pro"] {
		t.Fatalf("expected gemini-4.0-pro in google catalog: %+v", gCat.Models)
	}
	for _, excluded := range []string{"imagen-4", "veo-3", "gemini-embedding-001"} {
		if gSeen[excluded] {
			t.Fatalf("non-chat google model %q leaked into catalog", excluded)
		}
	}

	googleSignInRT := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		return fixtureAdmissionResponse(r, map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "models/gemini-4.0-pro", "display_name": "Gemini 4.0 Pro", "created": 1744588800},
			},
		}), nil
	})
	disc := fetchProviderModelList(t.Context(), &http.Client{Transport: googleSignInRT}, "google", now, "sign-in")
	if disc.State != "ready" || len(disc.Models) != 1 || disc.Models[0].ID != "gemini-4.0-pro" {
		t.Fatalf("google sign-in model discovery routed incorrectly: %+v", disc)
	}
}
