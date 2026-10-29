package core

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestOllamaCloudSupportedAccountContract(t *testing.T) {
	p, ok := providerDefinitionFor("ollama-cloud")
	if !ok || p.Host != "ollama.com" {
		t.Fatal("documented direct Ollama Cloud key route is missing")
	}
	policy := openAIModelPolicy("ollama-cloud/gemma4:31b")
	if !unmeteredPolicy(&policy) || policy.InputMicrousdPerMillion != 0 || policy.OutputMicrousdPerMillion != 0 {
		t.Fatal("cloud quota cannot become an invented API tariff")
	}
}

func TestOllamaCloudAccountChatTasksMemoryAndRestart(t *testing.T) {
	for _, provider := range []string{"ollama-cloud"} {
		t.Run(provider, func(t *testing.T) {
			s := fixtureStore(t)
			old := saveTestConnection(t, s)
			a := addNativeAccount(t, s, provider, true)
			calls := 0
			transport := nativeHTTPSFixture(t, provider, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method == "POST" {
					raw, _ := io.ReadAll(r.Body)
					var body map[string]any
					json.Unmarshal(raw, &body)
					if _, ok := body["response_format"]; ok {
						t.Error("unsupported cloud structured-output option")
					}
					r.Body = io.NopCloser(bytes.NewReader(raw))
				}
				p, _ := providerDefinitionFor(provider)
				if r.Host != p.Host {
					t.Error("wrong Host")
				}
				if provider == "anthropic" {
					if r.Header.Get("X-Api-Key") != disposableAPIInput || r.Header.Get("Anthropic-Version") != p.Version || r.Header.Get("Authorization") != "" {
						t.Error("wrong Messages credentials")
					}
				} else if r.Header.Get("Authorization") != "Bearer "+disposableAPIInput || r.Header.Get("X-Api-Key") != "" {
					t.Error("wrong Bearer credentials")
				}
				copy := r.Clone(r.Context())
				u := *r.URL
				u.Scheme, u.Host = "https", p.Host
				copy.URL = &u
				var res *http.Response
				var err error
				if compatibleProvider(provider) {
					res, err = compatibleFixtureResponse(copy, provider)
				} else {
					res, err = nativeFixtureResponse(copy, provider)
				}
				if err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				defer res.Body.Close()
				w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
				io.Copy(w, res.Body)
			})
			if _, err := s.refreshProviderModels(t.Context(), a.Ref, transport); err != nil {
				t.Fatal(err)
			}
			a, _ = s.AIConnection(a.Ref)
			model := registryForProvider(provider).Models[0].ID
			v, p := nativePermission(t, s, a, model)
			v = chatSend(t, s, v, p, "Native HTTPS fixture")
			if err := s.advanceAgentAI(t.Context(), transport, false); err != nil {
				t.Fatal(err)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			expectedCalls := 4
			if compatibleProvider(provider) {
				expectedCalls = 3
			}
			if calls != expectedCalls || v.AIRequests[0].State != "completed" {
				t.Fatal("controlled native completion", calls, v.AIRequests[0].State, v.AIRequests[0].Failure)
			}
			// The same account/policy and core task worker execute after interactive expiry.
			v = agentApply(t, s, AgentCommand{Action: "task-ai-prepare", ProfileID: v.ProfileID, ID: p.SessionID, UseAgentDefaults: true, Name: "Bounded native task", Text: "Summarize the fixture", IntervalSeconds: 3600, MaxRuns: 1, Limits: &p.Limits, AuthorizationSeconds: 7200, TotalBudgetMicrousd: p.Limits.BudgetMicrousd})
			task := v.Tasks[len(v.Tasks)-1]
			v = agentApply(t, s, AgentCommand{Action: "task-ai-authorize", ProfileID: v.ProfileID, ID: task.ID, ConsentDigest: task.AI.Digest})
			grant, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: a.Ref, Revision: v.Revision, ProfileID: v.ProfileID, TaskIDs: []string{task.ID}, AuthorizationSeconds: 5400})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: a.Ref, Allowance: grant.AccessReview, ConsentDigest: grant.AccessReview.ID, Passphrase: testVaultPassword}); err != nil {
				t.Fatal(err)
			}
			later := s.now().Add(61 * time.Minute)
			s.now = func() time.Time { return later }
			if err = s.runAgentTasks(t.Context(), false); err != nil {
				t.Fatal(err)
			}
			if err = s.advanceAgentAI(t.Context(), transport, false); err != nil {
				t.Fatal(err)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			last := v.AIRequests[len(v.AIRequests)-1]
			if last.State != "completed" || last.TaskID != task.ID || last.ConnectionRef != a.Ref || len(v.Runs) != 1 {
				t.Fatal("native task failed", last.State, last.Failure, len(v.Runs))
			}
			status, _ := s.AIConnection(a.Ref)
			primary, _ := s.AIConnection(old.Ref)
			if status.TaskAccess == nil || status.TaskAccess.UsedRequests != 1 || primary.TaskAccess != nil {
				t.Fatal("grant crossed accounts")
			}
			if provider == "ollama-cloud" {
				if last.Policy.Provider != provider || last.SpentMicrousd != aiUsageCost(last, last.InputTokens, last.CachedInputTokens, last.OutputTokens) || last.SpentMicrousd != 0 || last.CostBasis != "ollama-cloud-reported-usage" {
					t.Fatal("cloud quota result disconnected")
				}
				v = agentApply(t, s, AgentCommand{Action: "task-run-explain", ProfileID: v.ProfileID, ID: v.Runs[0].ID})
				v = agentApply(t, s, AgentCommand{Action: "memory-chat-save", ProfileID: v.ProfileID, Source: "message", ID: v.Messages[len(v.Messages)-1].ID, Text: "Keep this synthetic Ollama Cloud result."})
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
				locked, _ := reopened.AIConnection(a.Ref)
				after, _ := ReadPrivateFile(reopened.aiVaultPath())
				restored, err := reopened.Agents(t.Context(), v.ProfileID, "")
				if err != nil || !locked.Locked || !bytes.Equal(vault, after) || len(restored.Memories) != 1 || len(restored.Runs) != 1 || restored.AIRequests[len(restored.AIRequests)-1].Model != model {
					t.Fatal("restart lost cloud account/results/memory", err)
				}
			}
		})
	}
}

func TestOllamaServerCloudReusesLocalEngine(t *testing.T) {
	localProviderJoinedJourney(t, "ollama", "gemma4:cloud")
	p := localModelPolicy("gemma4:cloud")
	if !ollamaRelayPolicy(&p) || reportedUsageBasis(AgentAIRequest{Policy: &p}) != "ollama-cloud-reported-usage" {
		t.Fatal("server cloud quota mislabelled")
	}
	if !newerProviderRecordForID(p.Model, discoveredModel{ID: "gemma5:cloud"}, nil) {
		t.Fatal("server cloud alias bypassed latest family")
	}
}
