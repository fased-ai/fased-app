package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestCompatibleProviderDefinitions(t *testing.T) {
	for id, name := range map[string]string{"moonshot": "Moonshot / Kimi", "google": "Google", "openrouter": "OpenRouter", "qwen": "Qwen", "opencode": "OpenCode Zen"} {
		t.Run(id, func(t *testing.T) {
			p, ok := providerDefinitionFor(id)
			if !ok || p.ID != id || p.Name != name {
				t.Fatalf("missing provider definition %s: %+v", id, p)
			}
		})
	}
}

func TestCompatibleAccountsUseReviewedModelsAndSharedChat(t *testing.T) {
	for _, provider := range []string{"moonshot", "google", "openrouter", "qwen", "opencode"} {
		t.Run(provider, func(t *testing.T) {
			s := fixtureStore(t)
			a := addNativeAccount(t, s, provider, false)
			if len(a.Catalog.Models) != 1 || a.Provider != provider {
				t.Fatal("account catalog", a.Catalog)
			}
			m := a.Catalog.Models[0]
			if provider == "moonshot" {
				if m.Selectable || m.Policy != nil || m.Availability != "currency-unsupported" {
					t.Fatal("CNY tariff was presented as USD", m)
				}
				return
			}
			if !m.Selectable || m.Policy == nil {
				t.Fatal("model unavailable", m)
			}
			v, p := nativePermission(t, s, a, m.ID)
			v = chatSend(t, s, v, p, "Summarize this synthetic note")
			if err := s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
				t.Fatal(err)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			q := v.AIRequests[len(v.AIRequests)-1]
			if q.State != "completed" || q.Model != m.ID || q.ConnectionRef != a.Ref || q.HeldMicrousd != 0 || q.Answer == "" || q.Admission == nil || !q.Admission.GenerationAttempted {
				t.Fatalf("shared completion failed: %s %s", q.State, q.Failure)
			}
		})
	}
}

func TestCompatibleRoutesAreFixedAndAccountScoped(t *testing.T) {
	for _, id := range []string{"moonshot", "google", "openrouter", "qwen", "opencode"} {
		p, _ := providerDefinitionFor(id)
		for _, path := range []string{providerModelsPath(id), providerBasePath(id) + "/chat/completions"} {
			method := "POST"
			if path == providerModelsPath(id) {
				method = "GET"
			}
			target := path
			if id == "qwen" && method == "GET" {
				target = qwenModelsPagePath(1)
			}
			r, _ := http.NewRequest(method, "https://"+p.Host+target, nil)
			if !providerRequestAllowed(id, r) {
				t.Fatal("reviewed route rejected", id, path)
			}
			for _, target := range []string{"http://" + p.Host + path, "https://foreign.invalid" + path, "https://" + p.Host + path + "?forward=true", "https://" + p.Host + "/v1/responses", "https://" + p.Host + path + "/extra"} {
				bad, _ := http.NewRequest(method, target, nil)
				if providerRequestAllowed(id, bad) {
					t.Fatal("unreviewed route accepted", id, target)
				}
			}
		}
	}
	if providerModelsPath("opencode") != "/inference/v1/models" || providerModelsPath("openrouter") != "/api/v1/models/user" {
		t.Fatal("discovery drift")
	}
}

func TestCompatibleUsageStreamAndRoutingRefuseAmbiguity(t *testing.T) {
	for _, id := range []string{"google", "openrouter", "qwen", "opencode"} {
		t.Run(id, func(t *testing.T) {
			m := registryForProvider(id).Models[0]
			policy := openAIModelPolicy(m.ID)
			q := AgentAIRequest{Model: m.ID, Policy: &policy, Stream: true, Context: "synthetic", Limits: AILimits{MaxInputTokens: 8192, MaxOutputTokens: 512}}
			wire, err := aiWire(q)
			if err != nil {
				t.Fatal(err)
			}
			var sent map[string]any
			json.Unmarshal(wire, &sent)
			if id == "openrouter" {
				routing := sent["provider"].(map[string]any)
				ceiling := routing["max_price"].(map[string]any)
				if routing["allow_fallbacks"] != false || routing["require_parameters"] != true || ceiling["prompt"] != float64(policy.InputMicrousdPerMillion)/1e6 || ceiling["request"] != float64(0) {
					t.Fatal("routing ceiling absent", routing)
				}
			}
			r, _ := http.NewRequest("POST", providerGenerationURL(q.Policy), bytes.NewReader(wire))
			res, err := compatibleFixtureResponse(r, id)
			if err != nil {
				t.Fatal(err)
			}
			stream, _ := io.ReadAll(res.Body)
			res.Body.Close()
			raw, err := readCompatibleStream(bytes.NewReader(stream), q, nil)
			if err != nil {
				t.Fatal(err)
			}
			normalized, err := normalizeCompatible(q, raw)
			if err != nil || !bytes.Contains(normalized, []byte(`"total_tokens":384`)) {
				t.Fatal("usage conversion", err, string(normalized))
			}
			for name, bad := range map[string][]byte{
				"truncated": bytes.ReplaceAll(stream, []byte("data: [DONE]"), nil),
				"identity":  bytes.ReplaceAll(stream, []byte(providerRemoteModel(id, m.ID)), []byte("wrong-model")),
				"tools":     bytes.ReplaceAll(stream, []byte(`"role":"assistant"`), []byte(`"role":"assistant","tool_calls":[{"id":"call"}]`)),
				"finish":    bytes.ReplaceAll(stream, []byte(`"finish_reason":"stop"`), []byte(`"finish_reason":"length"`)),
			} {
				if _, err := readCompatibleStream(bytes.NewReader(bad), q, nil); err == nil {
					t.Fatal("unsafe stream accepted", name)
				}
			}
			for name, bad := range map[string][]byte{
				"usage":     bytes.ReplaceAll(raw, []byte(`"total_tokens":384`), []byte(`"total_tokens":385`)),
				"duplicate": bytes.ReplaceAll(raw, []byte(`"prompt_tokens":256`), []byte(`"prompt_tokens":256,"prompt_tokens":256`)),
				"cache":     bytes.ReplaceAll(raw, []byte(`"cached_tokens":null`), []byte(`"cached_tokens":257`)),
				"identity":  bytes.ReplaceAll(raw, []byte(providerRemoteModel(id, m.ID)), []byte("wrong-model")),
			} {
				if bytes.Equal(bad, raw) {
					t.Fatal("mutation did not apply", name)
				}
				if _, err := normalizeCompatible(q, bad); err == nil {
					t.Fatal("unsafe completion accepted", name)
				}
			}
			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			q.Limits.BudgetMicrousd = 500000
			if admitCompatible(cancelled, &q, fixtureAITransport{}, time.Now) || q.Admission.GenerationAttempted || q.HeldMicrousd != 0 {
				t.Fatal("cancelled admission generated")
			}
		})
	}
}

func TestCompatibleVaultCapacityRestartAndBinding(t *testing.T) {
	s := fixtureStore(t)
	original := saveTestConnection(t, s)
	originalEnvelope, _ := s.readAIVault(original.Ref)
	originalBytes, _ := json.Marshal(originalEnvelope)
	accounts := []AIConnectionView{original}
	for i := 0; i < 15; i++ {
		id := []string{"moonshot", "google", "openrouter", "qwen", "opencode"}[i%5]
		accounts = append(accounts, addNativeAccount(t, s, id, false))
	}
	before, err := ReadPrivateFile(s.aiVaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(before, []byte("fased-test-")) {
		t.Fatal("plaintext credential persisted")
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "add", Provider: "google", Key: "fased-test-rejected-capacity-key", Passphrase: testVaultPassword}); err == nil {
		t.Fatal("capacity exceeded")
	}
	after, _ := ReadPrivateFile(s.aiVaultPath())
	if !bytes.Equal(before, after) {
		t.Fatal("failed addition changed vault")
	}
	env, _ := s.readAIVault(accounts[1].Ref)
	env.Provider = "google"
	if raw, err := s.openAIVault(env, testVaultPassword); err == nil {
		clear(raw)
		t.Fatal("provider AAD tamper accepted")
	}
	dir := s.dir
	s.Close()
	next, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	next.providerFixture = true
	view, err := next.AIConnection()
	if err != nil || len(view.Connections) != 16 {
		t.Fatal("restart lost accounts", err)
	}
	for _, a := range accounts {
		v, err := next.AIConnection(a.Ref)
		if err != nil || v.Ref != a.Ref || !v.Locked || v.Provider != a.Provider || v.LiveCallsEnabled {
			t.Fatal("restart identity", v.Ref, err)
		}
	}
	restored, _ := next.readAIVault(original.Ref)
	retained, _ := json.Marshal(restored)
	if !bytes.Equal(originalBytes, retained) {
		t.Fatal("old envelope changed")
	}
	if _, err = next.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: accounts[2].Ref, Passphrase: testVaultPassword}); err != nil {
		t.Fatal(err)
	}
}

func TestCompatibleUnknownUsageRetainsHoldAndNewerModelsDisableOldChoice(t *testing.T) {
	s := fixtureStore(t)
	a := addNativeAccount(t, s, "google", false)
	v, p := nativePermission(t, s, a, "gemini-3.8-flash")
	v = chatSend(t, s, v, p, "Synthetic uncertain response")
	calls := 0
	rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		response, err := compatibleFixtureResponse(r, "google")
		if err != nil {
			return nil, err
		}
		if r.Method == "POST" {
			calls++
			raw, _ := io.ReadAll(response.Body)
			response.Body.Close()
			raw = bytes.ReplaceAll(raw, []byte(`"total_tokens":384`), []byte(`"total_tokens":385`))
			response.Body = io.NopCloser(bytes.NewReader(raw))
		}
		return response, nil
	})
	if err := s.advanceAgentAI(t.Context(), rt, true); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	q := v.AIRequests[len(v.AIRequests)-1]
	if q.State != "unknown" || q.HeldMicrousd <= 0 || calls != 1 || q.Answer != "" {
		t.Fatal("uncertain usage lost reservation", q.State, q.Failure)
	}
	if err := s.advanceAgentAI(t.Context(), rt, true); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("uncertain request was retried")
	}
	discovery := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		return fixtureAdmissionResponse(r, map[string]any{"data": []map[string]string{{"id": "gemini-3.8-flash"}, {"id": "gemini-3.9-flash"}}}), nil
	})
	a, err := s.refreshProviderModels(t.Context(), a.Ref, discovery)
	if err != nil {
		t.Fatal(err)
	}
	if modelChoice(a, "gemini-3.8-flash").Selectable {
		t.Fatal("superseded model remains selectable")
	}
	current, _ := s.Agents(t.Context(), v.ProfileID, "")
	held := current.AIRequests[len(current.AIRequests)-1]
	if held.HeldMicrousd != q.HeldMicrousd || current.Profiles[1].Model == "gemini-3.9-flash" {
		t.Fatal("refresh altered reservation or silently upgraded agent")
	}
}

func TestCompatibleRouterDiscoveryPaginationIsFiniteAndComplete(t *testing.T) {
	s := fixtureStore(t)
	a := addNativeAccount(t, s, "openrouter", false)
	calls := 0
	rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if !providerRequestAllowed("openrouter", r) {
			t.Fatal("unbounded route", r.URL)
		}
		rows := []map[string]any{}
		if r.URL.Query().Get("offset") == "0" {
			for i := 0; i < 500; i++ {
				rows = append(rows, map[string]any{"id": fmt.Sprintf("fixture/model-%d", i)})
			}
		} else if r.URL.Query().Get("offset") == "500" {
			rows = append(rows, map[string]any{"id": "google/gemini-3.8-flash"})
		} else {
			t.Fatal("unexpected offset", r.URL)
		}
		return fixtureAdmissionResponse(r, map[string]any{"data": rows}), nil
	})
	a, err := s.refreshProviderModels(t.Context(), a.Ref, rt)
	if err != nil || calls != 2 || !modelChoice(a, "google/gemini-3.8-flash").Selectable {
		t.Fatal("later page unavailable", calls, err)
	}
	for _, query := range []string{"?limit=501&offset=0", "?limit=500&offset=2000", "?limit=500&offset=0&offset=500", "?offset=0&limit=500"} {
		req, _ := http.NewRequest("GET", providerOrigin("openrouter")+providerModelsPath("openrouter")+query, nil)
		if providerRequestAllowed("openrouter", req) {
			t.Fatal("unbounded cursor accepted", query)
		}
	}
}
