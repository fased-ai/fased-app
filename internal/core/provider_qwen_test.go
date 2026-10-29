package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func qwenModelPage(page, total int, models ...string) map[string]any {
	rows := []map[string]any{}
	for _, id := range models {
		rows = append(rows, map[string]any{"model": id, "provider": "qwen"})
	}
	return map[string]any{"success": true, "code": nil, "output": map[string]any{"total": total, "page_no": page, "page_size": 100, "models": rows}}
}

func TestQwenDocumentedModelDiscovery(t *testing.T) {
	s := fixtureStore(t)
	calls := 0
	rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.URL.String() != "https://dashscope-intl.aliyuncs.com/api/v1/models?page_no=1&page_size=100&providers=qwen" || !providerRequestAllowed("qwen", r) {
			t.Errorf("undocumented or unbounded Qwen discovery: %s %s", r.Method, r.URL)
		}
		return fixtureAdmissionResponse(r, qwenModelPage(1, 1, "qwen3.8-flash")), nil
	})
	d := fetchProviderModelList(t.Context(), &http.Client{Transport: rt}, "qwen", s.now(), "api-key")
	if d.State != "ready" || calls != 1 || len(d.Models) != 1 || d.Models[0].ID != "qwen3.8-flash" || d.Models[0].OwnedBy != "qwen" || !strings.Contains(d.Source, "page_size=100") {
		t.Fatalf("documented Qwen response was not discovered: %+v", d)
	}
	for _, path := range []string{"/compatible-mode/v1/models", "/api/v1/models", "/api/v1/models?page_no=5&page_size=100&providers=qwen", "/api/v1/models?page_no=1&page_size=100&providers=foreign", "/api/v1/models?page_no=1&page_size=100&providers=qwen&endpoint=foreign"} {
		r, _ := http.NewRequest("GET", providerOrigin("qwen")+path, nil)
		if providerRequestAllowed("qwen", r) {
			t.Fatal("unreviewed model route accepted", path)
		}
	}
}

func TestQwenSubscriptionKeysCannotReplaceAPIAccount(t *testing.T) {
	s := fixtureStore(t)
	a := addNativeAccount(t, s, "qwen", true)
	agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "Preserved Qwen work"})
	before, _ := ReadPrivateFile(s.aiVaultPath())
	doc, _, _ := readAgents(t.Context(), s.db)
	records, _ := json.Marshal(doc)
	for _, action := range []string{"add", "save"} {
		ref := a.Ref
		if action == "add" {
			ref = ""
		}
		_, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: action, Ref: ref, Provider: "qwen", Name: "Preserve", CredentialMode: "qwen", Key: "sk-sp-disposable-subscription-key-only", Passphrase: testVaultPassword})
		if err == nil || !strings.Contains(err.Error(), "subscription") {
			t.Fatalf("subscription key needs precise refusal: %v", err)
		}
	}
	after, _ := ReadPrivateFile(s.aiVaultPath())
	doc, _, _ = readAgents(t.Context(), s.db)
	afterRecords, _ := json.Marshal(doc)
	if !bytes.Equal(before, after) || !bytes.Equal(records, afterRecords) {
		t.Fatal("refused key changed account or work")
	}
	if validAICredential("qwen", "sk-sp-disposable-subscription-key-only") {
		t.Fatal("subscription key valid for pay-as-you-go route")
	}
}

func TestQwenNativePagesFailClosed(t *testing.T) {
	first := []string{}
	for i := 0; i < 100; i++ {
		first = append(first, fmt.Sprintf("qwen-unreviewed-%03d", i))
	}
	for _, mode := range []string{"complete", "duplicate", "changed-total", "wrong-page", "truncated", "over-limit", "api-error", "old-shape", "missing-total", "offline"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			calls := 0
			rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				page, _ := strconv.Atoi(r.URL.Query().Get("page_no"))
				response := qwenModelPage(page, 101, "qwen3.8-flash")
				if page == 1 {
					response = qwenModelPage(1, 101, first...)
				}
				o := response["output"].(map[string]any)
				switch mode {
				case "duplicate":
					if page == 2 {
						response = qwenModelPage(2, 101, first[0])
					}
				case "changed-total":
					if page == 2 {
						o["total"] = 102
					}
				case "wrong-page":
					o["page_no"] = 2
				case "truncated":
					o["models"] = []any{}
				case "over-limit":
					o["total"] = 401
				case "api-error":
					response["success"] = false
				case "old-shape":
					response = map[string]any{"object": "list", "data": []any{}}
				case "missing-total":
					delete(o, "total")
				case "offline":
					if page == 2 {
						o["models"].([]map[string]any)[0]["inference_offline_info"] = map[string]string{"offline_time": "2026-10-28"}
					}
				}
				return fixtureAdmissionResponse(r, response), nil
			})
			d := fetchProviderModelList(t.Context(), &http.Client{Transport: rt}, "qwen", s.now(), "api-key")
			if mode == "complete" || mode == "offline" {
				if d.State != "ready" || calls != 2 || len(d.Models) != 101 {
					t.Fatalf("complete pages refused: %+v calls=%d", d, calls)
				}
				if mode == "offline" && d.Models[100].Shutdown == nil {
					t.Fatal("offline model became selectable")
				}
			} else if d.State != "failed" || calls > 2 {
				t.Fatalf("ambiguous model pages accepted: %s calls=%d", d.State, calls)
			}
		})
	}
}

func TestQwenMissingModelRefusesGeneration(t *testing.T) {
	s := fixtureStore(t)
	p := openAIModelPolicy("qwen3.8-flash")
	q := AgentAIRequest{Model: p.Model, Policy: &p, ConnectionRef: "disposable-qwen-account", Context: "synthetic", Limits: AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, BudgetMicrousd: 1000000}}
	calls := 0
	rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" {
			t.Fatal("generation attempted for missing model")
		}
		return fixtureAdmissionResponse(r, qwenModelPage(1, 0)), nil
	})
	if admitCompatible(t.Context(), &q, rt, s.now) || calls != 1 || q.Admission.GenerationAttempted {
		t.Fatal("missing model admitted")
	}
}

func TestQwenLegacySubscriptionKeyNeverLeavesVault(t *testing.T) {
	s := fixtureStore(t)
	s.provider.apiCredentials = true
	// Reconstruct a formerly accepted v4 record without weakening the public
	// save guard. Existing bytes remain readable; transmission is refused.
	s.provider.mu.Lock()
	a, err := s.persistAIConnectionLocked(AIConnectionCommand{Name: "Legacy plan", Key: "sk-sp-disposable-legacy-plan-key", Passphrase: testVaultPassword}, aiVaultCollection{Version: 3}, "qwen", "qwen", false)
	s.provider.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	before, _ := ReadPrivateFile(s.aiVaultPath())
	called := false
	rt := aiTestTransport(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, fmt.Errorf("must not transmit")
	})
	view, err := s.refreshProviderModels(t.Context(), a.Ref, rt)
	after, _ := ReadPrivateFile(s.aiVaultPath())
	if err != nil || called || view.Catalog.State != "failed" || !bytes.Equal(before, after) {
		t.Fatal("legacy key transmitted or rewritten", err)
	}
}
