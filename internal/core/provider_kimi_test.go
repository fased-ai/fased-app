package core

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestKimiInternationalPolicyAndRegionIsolation(t *testing.T) {
	p, ok := providerDefinitionFor("moonshot-intl")
	if !ok || p.Host != "api.moonshot.ai" {
		t.Fatal("missing explicit international API identity")
	}
	m := registryForProvider(p.ID).Models[0]
	policy := openAIModelPolicy(m.ID)
	if m.ID != "moonshot-intl/kimi-k3.1-pro" || m.Currency != "USD" || policy.Provider != p.ID || policy.InputMicrousdPerMillion != 2000000 || policy.CachedMicrousdPerMillion != 200000 || policy.OutputMicrousdPerMillion != 10000000 || policy.CacheMode != "kimi-implicit-5m" {
		t.Fatal("USD tariff/identity missing", m, policy)
	}
	if old := openAIModelPolicy("kimi-k3.1-pro"); old.Model != "" {
		t.Fatal("mainland CNY tariff was relabelled as USD")
	}
	for _, method := range []string{"GET", "POST"} {
		path := providerModelsPath(p.ID)
		if method == "POST" {
			path = "/v1/chat/completions"
		}
		r, _ := http.NewRequest(method, "https://api.moonshot.ai"+path, nil)
		if !providerRequestAllowed(p.ID, r) || providerRequestAllowed("moonshot", r) {
			t.Fatal("region route crossed account identity")
		}
	}
	q := AgentAIRequest{Model: m.ID, Policy: &policy, Context: "synthetic", Limits: AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024}}
	wire, err := aiWire(q)
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	json.Unmarshal(wire, &sent)
	cache, _ := sent["prompt_cache_options"].(map[string]any)
	if sent["model"] != "kimi-k3.1-pro" || sent["reasoning_effort"] != "low" || cache["ttl"] != "5m" || cache["mode"] != "implicit" {
		t.Fatal("unreviewed regional wire", string(wire))
	}
	if aiUsageCost(q, 1000, 200, 100) != 2640 {
		t.Fatal("USD/cache arithmetic differs from reviewed tariff")
	}
}

func TestKimiInternationalCatalogPreservesMainlandAccount(t *testing.T) {
	s := fixtureStore(t)
	old := addNativeAccount(t, s, "moonshot", false)
	before, _ := s.readAIVault(old.Ref)
	beforeBytes, _ := json.Marshal(before)
	current := addNativeAccount(t, s, "moonshot-intl", false)
	if len(current.Catalog.Models) != 1 || !current.Catalog.Models[0].Selectable || current.Provider == old.Provider {
		t.Fatal("international account unavailable or region conflated")
	}
	after, _ := s.readAIVault(old.Ref)
	afterBytes, _ := json.Marshal(after)
	legacy, _ := s.AIConnection(old.Ref)
	if !bytes.Equal(beforeBytes, afterBytes) || legacy.Catalog.Models[0].Selectable || legacy.Catalog.Models[0].Availability != "currency-unsupported" {
		t.Fatal("legacy CNY state changed")
	}
	if len(current.Catalog.Providers) != 12 {
		t.Fatal("regional identity missing from shared catalog")
	}
	bad := before
	bad.Provider = "moonshot-intl"
	if _, err := s.openAIVault(bad, testVaultPassword); err == nil {
		t.Fatal("region alteration bypassed encrypted account binding")
	}
	_, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Ref: old.Ref, Provider: "moonshot-intl", Key: "fased-test-no-region-retarget", Passphrase: testVaultPassword})
	if err == nil {
		t.Fatal("existing mainland account silently changed region")
	}

}

func TestKimiCacheUsageAndLatestGeneration(t *testing.T) {
	policy := openAIModelPolicy("moonshot-intl/kimi-k3.1-pro")
	q := AgentAIRequest{Model: policy.Model, Policy: &policy, Limits: AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024}}
	for _, mode := range []string{"mixed", "missing-write", "negative-write", "overlap", "wrong-model"} {
		t.Run(mode, func(t *testing.T) {
			details := map[string]any{"cached_tokens": 200, "cache_write_tokens": 300}
			model := "kimi-k3.1-pro"
			switch mode {
			case "missing-write":
				delete(details, "cache_write_tokens")
			case "negative-write":
				details["cache_write_tokens"] = -1
			case "overlap":
				details["cache_write_tokens"] = 801
			case "wrong-model":
				model = "kimi-k2.6"
			}
			usage := map[string]any{"prompt_tokens": 1000, "completion_tokens": 100, "total_tokens": 1100, "prompt_tokens_details": details}
			body, _ := json.Marshal(map[string]any{"id": "disposable-kimi-receipt", "object": "chat.completion", "model": model, "choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": "synthetic"}}}, "usage": usage})
			normalized, err := normalizeCompatible(q, body)
			if (err == nil) != (mode == "mixed") {
				t.Fatal("cache identity/usage guard", err)
			}
			if mode == "mixed" && !bytes.Contains(normalized, []byte(`"cached_tokens":200`)) {
				t.Fatal("cache read receipt lost")
			}
		})
	}
	if !newerCompatibleGeneration("moonshot-intl/kimi-k3.2-pro", q.Model) || newerCompatibleGeneration("moonshot-intl/kimi-k2.6", q.Model) {
		t.Fatal("namespace bypassed latest-only check")
	}
}

func TestKimiMalformedUsageKeepsHold(t *testing.T) {
	s := fixtureStore(t)
	a := addNativeAccount(t, s, "moonshot-intl", true)
	v, p := nativePermission(t, s, a, "moonshot-intl/kimi-k3.1-pro")
	v = chatSend(t, s, v, p, "Synthetic interrupted receipt")
	posts := 0
	rt := nativeHTTPSFixture(t, "moonshot-intl", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+disposableAPIInput {
			t.Error("credential binding changed")
		}
		res, err := compatibleFixtureResponse(r, "moonshot-intl")
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		if r.Method == "POST" {
			posts++
			body = []byte(strings.ReplaceAll(string(body), `"cache_write_tokens":0`, `"cache_write_tokens":null`))
		}
		w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
		w.Write(body)
	})
	if err := s.advanceAgentAI(t.Context(), rt, false); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	q := v.AIRequests[len(v.AIRequests)-1]
	if posts != 1 || q.State != "unknown" || q.HeldMicrousd <= 0 || q.SpentMicrousd != 0 {
		t.Fatal("unverified Kimi usage released hold", q.State, q.Failure)
	}
	vault, _ := ReadPrivateFile(s.aiVaultPath())
	dir := s.dir
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after, _ := ReadPrivateFile(reopened.aiVaultPath())
	retained, err := reopened.Agents(t.Context(), v.ProfileID, "")
	last := retained.AIRequests[len(retained.AIRequests)-1]
	if err != nil || len(retained.AIRequests) != 1 || last.State != "unknown" || last.HeldMicrousd != q.HeldMicrousd || !bytes.Equal(vault, after) {
		t.Fatal("restart lost unknown hold or key", err)
	}
}
