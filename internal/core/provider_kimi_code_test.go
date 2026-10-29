package core

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestKimiCodeInteractiveContract(t *testing.T) {
	p, ok := providerDefinitionFor("kimi-code")
	if !ok || p.Host != "api.kimi.ai" {
		t.Fatal("missing distinct Kimi Code membership connector")
	}
	for _, id := range []string{"kimi-code/k3.1-pro", "kimi-code/k3.1-pro-highspeed"} {
		policy := openAIModelPolicy(id)
		if policy.Model != id || !unmeteredPolicy(&policy) || policy.InputMicrousdPerMillion != 0 || policy.OutputMicrousdPerMillion != 0 {
			t.Fatal("membership quota must not be treated as an API dollar tariff")
		}
		d := agentDocument{}
		err := changeTaskAI(&d, AgentCommand{Action: "task-ai-prepare", Model: id}, "trading-analyst", "disposable", policy.ReviewedAt, true)
		if err == nil || !strings.Contains(err.Error(), "interactive") {
			t.Fatal("unattended task must explain membership scope", err)
		}
	}
	if validAICredential("kimi-code", disposableAPIInput) || !validAICredential("kimi-code", "sk-kimi-disposable-fixture-key-only") || validAICredential("moonshot-intl", "sk-kimi-disposable-fixture-key-only") {
		t.Fatal("membership and platform keys must remain separate")
	}
}

func TestKimiCodeAccountChatAndTaskBoundary(t *testing.T) {
	for _, stream := range []bool{true} {
		t.Run("stream", func(t *testing.T) {
			s := fixtureStore(t)
			old := addNativeAccount(t, s, "moonshot-intl", false)
			before, _ := s.readAIVault(old.Ref)
			preserved, _ := json.Marshal(before)
			original, _, _ := readAgents(t.Context(), s.db)
			originalBytes, _ := json.Marshal(original)
			s.provider.apiCredentials = true
			key := "sk-kimi-disposable-fixture-key-only"
			account, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "add", Provider: "kimi-code", Name: "Personal Kimi Code", CredentialMode: "kimi-code", Key: key, Passphrase: testVaultPassword})
			if err != nil || account.Locked || account.LiveCallsEnabled || account.AuthMethod != "api-key" || len(account.Catalog.Models) != 2 {
				t.Fatal("membership account", account, err)
			}
			afterSetup, _, _ := readAgents(t.Context(), s.db)
			setupBytes, _ := json.Marshal(afterSetup)
			if !bytes.Equal(originalBytes, setupBytes) {
				t.Fatal("setup changed work records or generated a reply")
			}
			for _, m := range account.Catalog.Models {
				if !m.Selectable || m.Policy == nil || !unmeteredPolicy(m.Policy) {
					t.Fatal("account discovery/policy missing", m)
				}
			}
			model := "kimi-code/k3.1-pro"
			v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "Personal Kimi conversation"})
			session := v.Sessions[len(v.Sessions)-1].ID
			v = agentApply(t, s, AgentCommand{Action: "profile-update", ProfileID: v.ProfileID, Name: "Analyst", Provider: "kimi-code", Model: model, AccountRef: &account.Ref})
			limits := &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 5, BudgetMicrousd: 0}
			v = agentApply(t, s, AgentCommand{Action: "chat-review", ProfileID: v.ProfileID, ID: session, UseAgentDefaults: true, Limits: limits, MemorySelection: &AIMemorySelection{Mode: "relevant"}, MaxRuns: 2, AuthorizationSeconds: 900, TotalBudgetMicrousd: 0})
			if v.ChatReview == nil {
				t.Fatal("no reviewed Chat permission")
			}
			permission := *v.ChatReview
			v = agentApply(t, s, AgentCommand{Action: "chat-allow", ProfileID: v.ProfileID, ID: session, ChatPermission: &permission, ConsentDigest: permission.ID})
			v = chatSend(t, s, v, permission, "Summarize this synthetic note")
			posts := 0
			rt := nativeHTTPSFixture(t, "kimi-code", func(w http.ResponseWriter, r *http.Request) {
				if r.Host != "api.kimi.ai" || r.Header.Get("Authorization") != "Bearer "+key || r.UserAgent() != "Fased" {
					t.Error("membership route or true client identity changed")
				}
				if r.Method == "POST" {
					posts++
					raw, _ := io.ReadAll(r.Body)
					var wire map[string]any
					json.Unmarshal(raw, &wire)
					if wire["model"] != "k3.1-pro" || wire["reasoning_effort"] != "low" {
						t.Error("wrong model or thinking disabled", string(raw))
					}
					r.Body = io.NopCloser(bytes.NewReader(raw))
				}
				response, e := compatibleFixtureResponse(r, "kimi-code")
				if e != nil {
					t.Error(e)
					w.WriteHeader(400)
					return
				}
				defer response.Body.Close()
				w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
				io.Copy(w, response.Body)
			})
			if err = s.advanceAgentAI(t.Context(), rt, true); err != nil {
				t.Fatal(err)
			}
			v, err = s.Agents(t.Context(), v.ProfileID, "")
			q := v.AIRequests[len(v.AIRequests)-1]
			if err != nil || posts != 1 || q.State != "completed" || q.Model != model || q.ConnectionRef != account.Ref || q.Answer == "" || q.SpentMicrousd != 0 || q.HeldMicrousd != 0 || q.CostBasis != "kimi-code-reported-usage" || q.Stream != stream {
				t.Fatal("shared Chat completion", q.State, q.Failure, q.CostBasis, err)
			}
			snapshot, _ := json.Marshal(v)
			_, err = s.changeAgents(t.Context(), AgentCommand{Action: "task-ai-prepare", Revision: v.Revision, ProfileID: v.ProfileID, ID: session, UseAgentDefaults: true, Name: "Unattended digest", Text: "Synthetic", IntervalSeconds: 60, MaxRuns: 1, Limits: limits, AuthorizationSeconds: 3600}, true)
			if err == nil || !strings.Contains(err.Error(), "interactive") {
				t.Fatal("agent defaults bypassed task restriction", err)
			}
			after, _ := s.Agents(t.Context(), v.ProfileID, "")
			afterBytes, _ := json.Marshal(after)
			if !bytes.Equal(snapshot, afterBytes) {
				t.Fatal("refused task changed records")
			}
			forged := q
			forged.TaskID = "forged-task"
			forged.TaskDigest = "forged-digest"
			if admitCompatible(t.Context(), &forged, rt, s.now) || posts != 1 {
				t.Fatal("dispatch accepted unattended membership request")
			}
			afterOld, _ := s.readAIVault(old.Ref)
			retained, _ := json.Marshal(afterOld)
			if !bytes.Equal(preserved, retained) {
				t.Fatal("platform account changed")
			}
			vault, _ := ReadPrivateFile(s.aiVaultPath())
			if bytes.Contains(vault, []byte(key)) {
				t.Fatal("key stored as plaintext")
			}
			dir := s.dir
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenStore(dir)
			if err != nil {
				t.Fatal("restart", err)
			}
			defer reopened.Close()
			locked, _ := reopened.AIConnection(account.Ref)
			if !locked.Locked || locked.LiveCallsEnabled {
				t.Fatal("restart retained authority")
			}
			saved, err := reopened.Agents(t.Context(), v.ProfileID, "")
			if err != nil || len(saved.AIRequests) != len(v.AIRequests) || saved.AIRequests[0].Answer != q.Answer {
				t.Fatal("restart lost work", err)
			}
			reopened.providerFixture = true
			reopened.provider.apiCredentials = true
			unlocked, err := reopened.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: account.Ref, Passphrase: testVaultPassword})
			if err != nil || unlocked.Locked || !modelChoice(unlocked, model).Selectable {
				t.Fatal("reconnect", err)
			}
			_, err = reopened.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Ref: account.Ref, Provider: "moonshot-intl", CredentialMode: "moonshot-intl", Key: disposableAPIInput, Passphrase: testVaultPassword})
			if err == nil {
				t.Fatal("membership silently migrated to metered API")
			}
		})
	}
}

func TestKimiCodeReceiptAndDispatchRefusals(t *testing.T) {
	policy := openAIModelPolicy("kimi-code/k3.1-pro")
	q := AgentAIRequest{Model: policy.Model, Policy: &policy, ChatPermissionID: "disposable-explicit-chat", ConnectionRef: "disposable", State: "queued", Context: "synthetic", Limits: AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 5}, CreatedAt: policy.ReviewedAt}
	for _, mode := range []string{"missing-usage", "wrong-model", "over-limit", "quota"} {
		t.Run(mode, func(t *testing.T) {
			posts := 0
			rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
				res, err := compatibleFixtureResponse(r, "kimi-code")
				if err != nil {
					return res, err
				}
				if r.Method != "POST" {
					return res, nil
				}
				posts++
				if mode == "quota" {
					res.Body.Close()
					res.StatusCode = 429
					res.Header.Set("Content-Type", "application/json")
					res.Body = io.NopCloser(strings.NewReader(`{"error":{"type":"rate_limit_error","message":"quota reached"}}`))
					return res, nil
				}
				b, _ := io.ReadAll(res.Body)
				res.Body.Close()
				switch mode {
				case "missing-usage":
					b = []byte(strings.ReplaceAll(string(b), `"total_tokens":384`, `"total_tokens":null`))
				case "wrong-model":
					b = []byte(strings.ReplaceAll(string(b), "k3.1-pro", "k3"))
				case "over-limit":
					b = []byte(strings.ReplaceAll(string(b), `"completion_tokens":128`, `"completion_tokens":1025`))
				}
				res.Body = io.NopCloser(bytes.NewReader(b))
				return res, nil
			})
			result, err := callReviewedAgentAIStreamAt(t.Context(), q, rt, func() time.Time { return time.Unix(policy.ReviewedAt+1, 0) }, nil)
			if posts != 1 || result.State == "completed" || result.SpentMicrousd != 0 {
				t.Fatal("bad receipt accepted or retried", result, err)
			}
			if mode != "quota" && !aiHasUsageHold(result) {
				t.Fatal("uncertain membership use became replayable", result.State, result.Failure, err)
			}
		})
	}
	if !newerCompatibleGeneration("kimi-code/k3.2-pro", q.Model) || newerCompatibleGeneration("kimi-code/k3-pro", q.Model) {
		t.Fatal("latest model guard lost namespace")
	}
	noConsent := q
	noConsent.ChatPermissionID = ""
	if admitCompatible(t.Context(), &noConsent, aiTestTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("unconsented request reached network")
		return nil, nil
	}), func() time.Time { return time.Unix(policy.ReviewedAt+1, 0) }) {
		t.Fatal("explicit interactive authority missing")
	}
}
