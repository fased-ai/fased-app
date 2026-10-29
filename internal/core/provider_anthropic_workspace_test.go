package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAnthropicWorkspaceCredentialBinding(t *testing.T) {
	s := fixtureStore(t)
	var c AIConnectionCommand
	if err := json.Unmarshal([]byte(`{"action":"add","provider":"anthropic","name":"Personal API workspace","key":"fased-test-disposable-anthropic-key","workspace":"wrkspc_01JwQvzr7rXLA5AGx3HKfFUJ"}`), &c); err != nil {
		t.Fatal(err)
	}
	c.Passphrase = testVaultPassword
	v, err := s.ChangeAIConnection(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.readAIVault(v.Ref)
	if err != nil || env.Version != 7 {
		t.Fatal("workspace binding missing from protected account envelope", env.Version, err)
	}
}

func TestAnthropicWorkspaceRejectsRetargetingAndPreservesLegacy(t *testing.T) {
	s := fixtureStore(t)
	old := addNativeAccount(t, s, "anthropic", false)
	retained, _ := ReadPrivateFile(s.aiVaultPath())
	for _, raw := range []string{
		`{"action":"add","provider":"openai","workspace":"wrkspc_01JwQvzr7rXLA5AGx3HKfFUJ"}`,
		`{"action":"add","provider":"anthropic","workspace":"wrkspc_bad\r\nX-Other: bad"}`,
		`{"action":"models-refresh","workspace":"wrkspc_01JwQvzr7rXLA5AGx3HKfFUJ"}`,
	} {
		var c AIConnectionCommand
		json.Unmarshal([]byte(raw), &c)
		if _, err := s.ChangeAIConnection(t.Context(), c); err == nil {
			t.Fatal("invalid workspace command accepted", raw)
		}
	}
	after, _ := ReadPrivateFile(s.aiVaultPath())
	if !bytes.Equal(retained, after) {
		t.Fatal("rejected workspace command changed legacy vault")
	}
	c := AIConnectionCommand{Action: "add", Provider: "anthropic", Name: "New workspace", Workspace: "wrkspc_01JwQvzr7rXLA5AGx3HKfFUJ", Key: "fased-test-disposable-workspace-account", Passphrase: testVaultPassword}
	v, err := s.ChangeAIConnection(t.Context(), c)
	if err != nil || v.Workspace != c.Workspace {
		t.Fatal(v.Workspace, err)
	}
	env, _ := s.readAIVault(v.Ref)
	original, _ := s.readAIVault(old.Ref)
	if original.Version != 4 {
		t.Fatal("legacy credential rewritten")
	}
	rawVault, _ := ReadPrivateFile(s.aiVaultPath())
	if bytes.Contains(rawVault, []byte(c.Workspace)) || bytes.Contains(rawVault, []byte(c.Key)) {
		t.Fatal("workspace/key not encrypted")
	}
	for _, field := range []string{"version", "mode", "provider", "ref"} {
		bad := env
		switch field {
		case "version":
			bad.Version = 4
		case "mode":
			bad.Mode = "anthropic"
		case "provider":
			bad.Provider = "xai"
		case "ref":
			bad.Ref = old.Ref
		}
		if _, err := s.openAIVault(bad, testVaultPassword); err == nil {
			t.Fatal("workspace credential lost AAD binding", field)
		}
	}
	for _, field := range []string{"workspace", "extra", "duplicate"} {
		plain, _ := s.openAIVault(env, testVaultPassword)
		switch field {
		case "workspace":
			plain = []byte(strings.ReplaceAll(string(plain), c.Workspace, "wrong"))
		case "extra":
			plain = []byte(strings.TrimSuffix(string(plain), "}") + `,"extra":true}`)
		case "duplicate":
			plain = []byte(strings.TrimSuffix(string(plain), "}") + `,"workspace":"` + c.Workspace + `"}`)
		}
		if _, ok := parseAnthropicWorkspace(plain, "test"); ok {
			t.Fatal("invalid workspace plaintext accepted", field)
		}
	}
	_, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Ref: v.Ref, Provider: "anthropic", Key: c.Key, Passphrase: testVaultPassword})
	if err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatal("workspace dropped on replacement", err)
	}
	if validAICredential("anthropic", "sk-ant-oat01-disposable-subscription-token") || validAICredential("anthropic", `{"apiKey":"not-a-direct-key"}`) {
		t.Fatal("unsupported token/typed credential import")
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "lock", Ref: v.Ref}); err != nil {
		t.Fatal(err)
	}
	locked, _ := s.AIConnection(v.Ref)
	if locked.Workspace != "" || !locked.Locked {
		t.Fatal("locked account exposes workspace")
	}
	unlocked, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: v.Ref, Passphrase: testVaultPassword})
	if err != nil || unlocked.Workspace != c.Workspace {
		t.Fatal("workspace unlock", err)
	}
	replacement, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Ref: v.Ref, Provider: "anthropic", Workspace: c.Workspace, Key: c.Key, Passphrase: testVaultPassword})
	if err != nil || replacement.Ref == v.Ref {
		t.Fatal("replacement retained stale authority", err)
	}
}

func TestUnsupportedLegacySignInPreservesAccountsAndPendingFlow(t *testing.T) {
	s := fixtureStore(t)
	addNativeAccount(t, s, "anthropic", false)
	before, _ := ReadPrivateFile(s.aiVaultPath())
	d, _, _ := readAgents(t.Context(), s.db)
	records, _ := json.Marshal(d)
	pending := &providerSignInAttempt{view: ProviderSignInView{ID: "disposable-existing-google", Provider: "google", ExpiresAt: s.now().Unix() + 60}}
	s.provider.signIn = pending
	s.provider.signInTransport = aiTestTransport(func(*http.Request) (*http.Response, error) {
		t.Error("unsupported login contacted vendor")
		return nil, errors.New("no network")
	})
	for _, provider := range []string{"anthropic", "moonshot", "moonshot-intl", "kimi-code"} {
		for _, remote := range []bool{false, true} {
			_, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: provider, Remote: remote})
			if err == nil || !strings.Contains(err.Error(), "key") || s.provider.signIn != pending {
				t.Fatal("unsupported login not precise/nonmutating", provider, err)
			}
		}
	}
	after, _ := ReadPrivateFile(s.aiVaultPath())
	d, _, _ = readAgents(t.Context(), s.db)
	afterRecords, _ := json.Marshal(d)
	if !bytes.Equal(before, after) || !bytes.Equal(records, afterRecords) {
		t.Fatal("unsupported login changed work")
	}
	s.provider.mu.Lock()
	s.cancelProviderSignInLocked()
	s.provider.mu.Unlock()
}

func TestAnthropicWorkspaceControlledHTTPSChatTaskAndRestart(t *testing.T) {
	for _, provider := range []string{"anthropic"} {
		t.Run(provider, func(t *testing.T) {
			s := fixtureStore(t)
			old := saveTestConnection(t, s)
			s.provider.apiCredentials = true
			workspace := "wrkspc_01JwQvzr7rXLA5AGx3HKfFUJ"
			a, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "add", Provider: provider, Workspace: workspace, CredentialMode: provider, Key: disposableAPIInput, Passphrase: testVaultPassword})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			transport := nativeHTTPSFixture(t, provider, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Anthropic-Workspace-Id") != workspace {
					t.Error("workspace was dropped or changed", r.URL.Path)
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
			if provider == "anthropic" {
				if last.Policy.Provider != provider || last.SpentMicrousd != aiUsageCost(last, last.InputTokens, last.CachedInputTokens, last.OutputTokens) || last.SpentMicrousd <= 0 {
					t.Fatal("workspace result disconnected")
				}
				v = agentApply(t, s, AgentCommand{Action: "task-run-explain", ProfileID: v.ProfileID, ID: v.Runs[0].ID})
				v = agentApply(t, s, AgentCommand{Action: "memory-chat-save", ProfileID: v.ProfileID, Source: "message", ID: v.Messages[len(v.Messages)-1].ID, Text: "Keep this synthetic Anthropic result."})
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
					t.Fatal("restart lost workspace account/results/memory", err)
				}
			}
		})
	}
}

func TestAnthropicWorkspaceHeaderCannotBeInjected(t *testing.T) {
	calls := 0
	rt := nativeHTTPSFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("anthropic-workspace-id") != "" {
			t.Error("ambient workspace header survived sanitizer")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	})
	r, _ := http.NewRequest("GET", "https://api.anthropic.com/v1/models", nil)
	r.Header.Set("x-api-key", disposableAPIInput)
	r.Header.Set("anthropic-version", "2026-10-21")
	r.Header.Set("anthropic-workspace-id", "wrkspc_01JwQvzr7rXLA5AGx3HKfFUJ")
	res, err := rt.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	r = r.WithContext(context.WithValue(r.Context(), anthropicWorkspaceContextKey{}, "wrkspc_01JwQvzr7rXLA5AGx3HKfFUJ"))
	r.Header.Set("anthropic-workspace-id", "wrkspc_01JwQvzr7rXLA5AGx3HKfFUK")
	if _, err = rt.RoundTrip(r); err == nil || calls != 1 {
		t.Fatal("changed protected workspace reached network", err, calls)
	}
}
