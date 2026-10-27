package core

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func addNativeAccount(t *testing.T, s *Store, provider string, api bool) AIConnectionView {
	t.Helper()
	mode := "test"
	key := "fased-test-" + provider + "-disposable-account"
	if api {
		s.provider.apiCredentials = true
		mode = provider
		key = disposableAPIInput
	}
	v, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "add", Provider: provider, Name: provider, CredentialMode: mode, Key: key, Passphrase: testVaultPassword})
	if err != nil || v.Provider != provider || v.Locked || v.LiveCallsEnabled {
		t.Fatal("native account", v.Provider, err)
	}
	return v
}

// The production constructor never accepts an endpoint or trust override. This
// test-only dialer preserves the official URL, Host, headers and TLS validation.
func nativeHTTPSFixture(t *testing.T, provider string, h http.HandlerFunc) *openAITransport {
	t.Helper()
	server := httptest.NewTLSServer(h)
	t.Cleanup(server.Close)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	u, _ := url.Parse(server.URL)
	p, _ := providerDefinitionFor(provider)
	tr := newNativeProviderTransport(provider)
	tr.enabled = true
	tr.wire.TLSClientConfig.RootCAs = roots
	tr.wire.TLSClientConfig.ServerName = u.Hostname()
	tr.wire.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != p.Host+":443" {
			return nil, errors.New("unexpected test origin")
		}
		return (&net.Dialer{}).DialContext(ctx, network, u.Host)
	}
	return tr
}
func nativePermission(t *testing.T, s *Store, account AIConnectionView, model string) (AgentView, ChatPermission) {
	t.Helper()
	v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "Native Chat"})
	v = agentApply(t, s, AgentCommand{Action: "profile-update", ProfileID: v.ProfileID, Name: "Analyst", Provider: account.Provider, Model: model, AccountRef: &account.Ref})
	budget := int64(500000)
	if account.Provider == "xai" {
		budget = 3100000
	}
	limits := &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 5, BudgetMicrousd: budget}
	v = agentApply(t, s, AgentCommand{Action: "chat-review", ProfileID: v.ProfileID, ID: v.Sessions[len(v.Sessions)-1].ID, UseAgentDefaults: true, Limits: limits, MemorySelection: &AIMemorySelection{Mode: "relevant"}, MaxRuns: 2, AuthorizationSeconds: 900, TotalBudgetMicrousd: 2 * budget})
	if v.ChatReview == nil {
		t.Fatal("no chat review")
	}
	p := *v.ChatReview
	v = agentApply(t, s, AgentCommand{Action: "chat-allow", ProfileID: v.ProfileID, ID: p.SessionID, ChatPermission: &p, ConsentDigest: p.ID})
	return v, p
}
func TestNativeProvidersEveryCurrentModelUsesSharedChat(t *testing.T) {
	for _, provider := range []string{"anthropic", "xai"} {
		for _, model := range registryForProvider(provider).Models {
			t.Run(model.ID, func(t *testing.T) {
				s := fixtureStore(t)
				a := addNativeAccount(t, s, provider, false)
				if !modelChoice(a, model.ID).Selectable || len(a.Catalog.Providers) != 3 {
					t.Fatal("native discovery absent", a.Catalog)
				}
				v, p := nativePermission(t, s, a, model.ID)
				v = chatSend(t, s, v, p, "Summarize my saved fixture note")
				if err := s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
					t.Fatal(err)
				}
				v, _ = s.Agents(t.Context(), v.ProfileID, "")
				q := v.AIRequests[len(v.AIRequests)-1]
				if q.State != "completed" || !q.Stream || q.ConnectionRef != a.Ref || q.Model != model.ID || q.Admission == nil || !q.Admission.GenerationAttempted || q.SpentMicrousd <= 0 || q.HeldMicrousd != 0 || q.Answer == "" {
					t.Fatal("native chat failed", q.State, q.Failure)
				}
				if strings.Contains(q.Answer, "opaque-fixture") {
					t.Fatal("reasoning leaked")
				}
				raw, _ := aiWire(q)
				if provider == "anthropic" && (!bytes.Contains(raw, []byte(`"service_tier":"standard_only"`)) || bytes.Contains(raw, []byte(`"input":`))) {
					t.Fatal("wrong Messages wire")
				}
				if provider == "xai" && (bytes.Contains(raw, []byte("prompt_cache_options")) || aiRequestReservation(q) < 3000000 || q.Policy.BillableOutputTokens != 500000) {
					t.Fatal("reasoning not reserved")
				}
			})
		}
	}
}
func TestNativeProvidersControlledHTTPSAndTaskGrant(t *testing.T) {
	for _, provider := range []string{"anthropic", "xai"} {
		t.Run(provider, func(t *testing.T) {
			s := fixtureStore(t)
			old := saveTestConnection(t, s)
			a := addNativeAccount(t, s, provider, true)
			calls := 0
			transport := nativeHTTPSFixture(t, provider, func(w http.ResponseWriter, r *http.Request) {
				calls++
				p, _ := providerDefinitionFor(provider)
				if r.Host != p.Host {
					t.Error("wrong Host")
				}
				if provider == "anthropic" {
					if r.Header.Get("X-Api-Key") != disposableAPIInput || r.Header.Get("Anthropic-Version") != p.Version || r.Header.Get("Authorization") != "" {
						t.Error("wrong Messages credentials")
					}
				} else if r.Header.Get("Authorization") != "Bearer "+disposableAPIInput || r.Header.Get("X-Api-Key") != "" {
					t.Error("wrong xAI credentials")
				}
				copy := r.Clone(r.Context())
				u := *r.URL
				u.Scheme, u.Host = "https", p.Host
				copy.URL = &u
				res, err := nativeFixtureResponse(copy, provider)
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
			if calls != 4 || v.AIRequests[0].State != "completed" {
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
		})
	}
}
func TestNativeProvidersVaultBindingRestartAndClosedGate(t *testing.T) {
	s := fixtureStore(t)
	legacy := saveTestConnection(t, s)
	before, _ := s.readAIVault(legacy.Ref)
	beforeJSON, _ := json.Marshal(before)
	a := addNativeAccount(t, s, "anthropic", false)
	b := addNativeAccount(t, s, "xai", false)
	for _, account := range []AIConnectionView{a, b} {
		env, err := s.readAIVault(account.Ref)
		if err != nil || env.Version != 4 || env.Provider != account.Provider || env.Mode != "test" {
			t.Fatal("native envelope", err)
		}
		for _, mutate := range []func(*aiVaultEnvelope){func(v *aiVaultEnvelope) { v.Provider = "openai" }, func(v *aiVaultEnvelope) {
			if v.Provider == "xai" {
				v.Provider = "anthropic"
			} else {
				v.Provider = "xai"
			}
		}, func(v *aiVaultEnvelope) { v.Mode = v.Provider }, func(v *aiVaultEnvelope) { v.Ref = legacy.Ref }} {
			bad := env
			mutate(&bad)
			if plain, err := s.openAIVault(bad, testVaultPassword); err == nil {
				clear(plain)
				t.Fatal("native AAD tamper accepted")
			}
		}
		if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Ref: account.Ref, Provider: "openai", Key: testAPIKey, Passphrase: testVaultPassword}); err == nil {
			t.Fatal("account provider changed")
		}
		if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "add", Provider: account.Provider, CredentialMode: account.Provider, Key: disposableAPIInput, Passphrase: testVaultPassword}); err == nil {
			t.Fatal("ordinary runtime admitted live key")
		}
		tr := newNativeProviderTransport(account.Provider)
		tr.wire.DialContext = func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("disabled transport dialed")
			return nil, nil
		}
		req, _ := http.NewRequest("POST", providerGenerationURL(&account.AdmissionPolicy), nil)
		if _, err := tr.RoundTrip(req); err == nil {
			t.Fatal("native production dispatch enabled")
		}
	}
	after, _ := s.readAIVault(legacy.Ref)
	afterJSON, _ := json.Marshal(after)
	if !bytes.Equal(beforeJSON, afterJSON) {
		t.Fatal("legacy encrypted bytes changed")
	}
	v, p := nativePermission(t, s, a, "claude-sonnet-5-5")
	v = agentApply(t, s, AgentCommand{Action: "ai-prepare", ProfileID: v.ProfileID, ID: p.SessionID, Text: "Preserve draft", UseAgentDefaults: true, Limits: &p.Limits})
	draft := v.AIRequests[len(v.AIRequests)-1]
	draftJSON, _ := json.Marshal(draft)
	v = agentApply(t, s, AgentCommand{Action: "profile-update", ProfileID: v.ProfileID, Name: "Analyst", Provider: "xai", Model: "grok-4.7", AccountRef: &b.Ref})
	raw, _ := os.ReadFile(s.aiVaultPath())
	dir := s.dir
	s.Close()
	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	retained, _ := os.ReadFile(reopened.aiVaultPath())
	if !bytes.Equal(raw, retained) {
		t.Fatal("restart rewrote keys")
	}
	current, _ := reopened.Agents(t.Context(), v.ProfileID, "")
	gotJSON, _ := json.Marshal(current.AIRequests[len(current.AIRequests)-1])
	if !bytes.Equal(draftJSON, gotJSON) {
		t.Fatal("draft retargeted")
	}
	locked, _ := reopened.AIConnection(a.Ref)
	if !locked.Locked || locked.Provider != "anthropic" {
		t.Fatal("restart identity")
	}
}
func nativeResponseBytes(t *testing.T, provider string, q AgentAIRequest) []byte {
	t.Helper()
	wire, _ := aiWire(q)
	req, _ := http.NewRequest("POST", providerGenerationURL(q.Policy), bytes.NewReader(wire))
	res, err := nativeFixtureResponse(req, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return raw
}
func TestNativeProvidersUsageAndStreamRefusals(t *testing.T) {
	for _, provider := range []string{"anthropic", "xai"} {
		model := registryForProvider(provider).Models[0].ID
		policy := openAIModelPolicy(model)
		q := AgentAIRequest{Model: model, Policy: &policy, Limits: AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, BudgetMicrousd: 3100000}}
		raw := nativeResponseBytes(t, provider, q)
		if _, err := normalizeNativeResponse(provider, q, raw); err != nil {
			t.Fatal(provider, err)
		}
		for _, value := range []string{"-1", "9223372036854775808", "1.1", "null", "\"12\"", "99999999"} {
			for _, field := range []string{"input_tokens", "output_tokens"} {
				var m map[string]json.RawMessage
				json.Unmarshal(raw, &m)
				var u map[string]json.RawMessage
				json.Unmarshal(m["usage"], &u)
				u[field] = json.RawMessage(value)
				m["usage"], _ = json.Marshal(u)
				bad, _ := json.Marshal(m)
				if _, err := normalizeNativeResponse(provider, q, bad); err == nil {
					t.Fatal("bad native usage accepted", provider, field, value)
				}
			}
		}
		for _, field := range []string{"cache_creation_input_tokens", "num_server_side_tools_used", "server_tool_use"} {
			var m map[string]json.RawMessage
			json.Unmarshal(raw, &m)
			var u map[string]json.RawMessage
			json.Unmarshal(m["usage"], &u)
			if provider == "anthropic" {
				if field == "num_server_side_tools_used" {
					continue
				}
				if field == "server_tool_use" {
					u[field] = json.RawMessage(`{"web_search_requests":1}`)
				} else {
					u[field] = json.RawMessage(`1`)
				}
			} else {
				if field != "num_server_side_tools_used" {
					continue
				}
				u[field] = json.RawMessage(`1`)
			}
			m["usage"], _ = json.Marshal(u)
			bad, _ := json.Marshal(m)
			if _, err := normalizeNativeResponse(provider, q, bad); err == nil {
				t.Fatal("unreviewed usage accepted", provider, field)
			}
		}
		q.Stream = true
		wire, _ := aiWire(q)
		req, _ := http.NewRequest("POST", providerGenerationURL(q.Policy), bytes.NewReader(wire))
		res, _ := nativeFixtureResponse(req, provider)
		stream, _ := io.ReadAll(res.Body)
		res.Body.Close()
		parse := func(data []byte) ([]byte, error) {
			if provider == "anthropic" {
				return readAnthropicStream(bytes.NewReader(data), nil)
			}
			return readAIStream(bytes.NewReader(data), model, nil)
		}
		native, err := parse(stream)
		if err != nil {
			t.Fatal(provider, err)
		}
		if _, err = normalizeNativeResponse(provider, q, native); err != nil {
			t.Fatal(provider, err)
		}
		if _, err = parse(stream[:len(stream)/2]); err == nil {
			t.Fatal("truncated stream accepted", provider)
		}
		if provider == "xai" {
			for _, value := range []string{"-1", "9223372036854775808", "null", "499999"} {
				bad := bytes.Replace(raw, []byte(`"reasoning_tokens":16`), []byte(`"reasoning_tokens":`+value), 1)
				if _, err = normalizeNativeResponse(provider, q, bad); err == nil {
					t.Fatal("reasoning usage accepted", value)
				}
			}
		}
	}
}
func TestNativeProvidersSchema24ReaderAndMigration(t *testing.T) {
	s := fixtureStore(t)
	a := saveTestConnection(t, s)
	v := prepareConnected(t, s, a.Ref)
	before, _ := json.Marshal(v)
	if _, err := s.db.Exec("PRAGMA user_version=24"); err != nil {
		t.Fatal(err)
	}
	dir := s.dir
	s.Close()
	next, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := next.Agents(t.Context(), v.ProfileID, "")
	after, _ := json.Marshal(got)
	if !bytes.Equal(before, after) {
		t.Fatal("migration changed records")
	}
	next.Close()
	if reader := os.Getenv("FASED_SCHEMA24_READER"); reader != "" {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, reader, "serve", "--data-dir", dir, "--listen", "127.0.0.1:0").CombinedOutput()
		if err == nil || !strings.Contains(string(out), "schema is newer") {
			t.Fatal("old reader accepted", string(out), err)
		}
	}
	s = fixtureStore(t)
	if _, err = s.db.Exec("PRAGMA user_version=24; UPDATE update_pause SET phase='paused' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	dir = s.dir
	s.Close()
	if bad, err := OpenStore(dir); err == nil {
		bad.Close()
		t.Fatal("paused migration accepted")
	}
}

func TestNativeProvidersDiscoveryPaginationAndSupersession(t *testing.T) {
	s := fixtureStore(t)
	a := addNativeAccount(t, s, "anthropic", false)
	pages := 0
	rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		pages++
		row := map[string]any{"id": "claude-sonnet-5-5", "type": "model", "lifecycle": "active", "line": "sonnet", "created_at": "2026-10-07T00:00:00Z"}
		more := true
		if r.URL.Query().Get("after_id") != "" {
			row["id"] = "claude-sonnet-6-0"
			row["created_at"] = "2026-10-08T00:00:00Z"
			more = false
		}
		if r.Header.Get("X-Api-Key") != "fased-test-anthropic-disposable-account" {
			t.Error("wrong paged credential")
		}
		return fixtureAdmissionResponse(r, map[string]any{"data": []any{row}, "has_more": more, "last_id": row["id"]}), nil
	})
	got, err := s.refreshProviderModels(t.Context(), a.Ref, rt)
	if err != nil || pages != 2 || got.Catalog.State != "ready" || modelChoice(got, "claude-sonnet-5-5").Selectable || modelChoice(got, "claude-sonnet-5-5").Availability != "superseded" {
		t.Fatal("pagination/supersession", pages, err, got.Catalog)
	}
	failed := aiTestTransport(func(r *http.Request) (*http.Response, error) { return nil, errors.New("unavailable") })
	got, err = s.refreshProviderModels(t.Context(), a.Ref, failed)
	if err != nil || modelChoice(got, "claude-sonnet-5-5").Selectable || !s.providerModelSuperseded(a.Ref, "claude-sonnet-5-5") {
		t.Fatal("failure forgot supersession", err)
	}
	// Partial/repeating pages, missing lifecycle and a response of the wrong media type fail closed.
	for _, bad := range []string{`{"data":[],"has_more":true,"last_id":"cursor"}`, `{"data":[{"id":"m","type":"model","created_at":"2026-10-07T00:00:00Z"}],"has_more":false}`} {
		if _, _, err := parseAnthropicModelPage([]byte(bad)); err == nil {
			t.Fatal("incomplete page accepted")
		}
	}
	b := addNativeAccount(t, s, "xai", false)
	got, err = s.refreshProviderModels(t.Context(), b.Ref, aiTestTransport(func(r *http.Request) (*http.Response, error) {
		return fixtureAdmissionResponse(r, map[string]any{"data": []any{map[string]any{"id": "grok-4.7", "created": 1744588800}, map[string]any{"id": "grok-5.0", "created": 1744588801}}}), nil
	}))
	if err != nil || modelChoice(got, "grok-4.7").Availability != "superseded" || modelChoice(got, "grok-5.0").Selectable {
		t.Fatal("Grok latest-only", err)
	}
}
func TestNativeProvidersAdmissionAndUnknownUsageHold(t *testing.T) {
	for _, provider := range []string{"anthropic", "xai"} {
		t.Run(provider, func(t *testing.T) {
			s := fixtureStore(t)
			a := addNativeAccount(t, s, provider, false)
			model := registryForProvider(provider).Models[0].ID
			v, p := nativePermission(t, s, a, model)
			v = chatSend(t, s, v, p, "No replay on unknown usage")
			calls := 0
			rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method == "POST" && (r.URL.Path == "/v1/messages" || r.URL.Path == "/v1/responses") {
					return nil, errors.New("interrupted after dispatch")
				}
				return nativeFixtureResponse(r, provider)
			})
			if err := s.advanceAgentAI(t.Context(), rt, true); err != nil {
				t.Fatal(err)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			q := v.AIRequests[len(v.AIRequests)-1]
			if q.State != "unknown" || q.HeldMicrousd != aiRequestReservation(q) || calls != 3 {
				t.Fatal("unknown usage not held", q.State, q.HeldMicrousd, calls)
			}
			if err := s.advanceAgentAI(t.Context(), rt, true); err != nil {
				t.Fatal(err)
			}
			if calls != 3 {
				t.Fatal("unknown request retried")
			}
			attempted := false
			wrong, _ := http.NewRequest("GET", "https://api.openai.com/v1/models", nil)
			if _, err := (authenticatedAITransport{store: s, ref: a.Ref, modelsOnly: true, next: aiTestTransport(func(*http.Request) (*http.Response, error) { attempted = true; return nil, nil })}).RoundTrip(wrong); err == nil || attempted {
				t.Fatal("cross-provider credential sent")
			}
		})
	}
	policy := openAIModelPolicy("claude-sonnet-5-5")
	q := AgentAIRequest{Model: policy.Model, Policy: &policy, ConnectionRef: "disposable-admission-ref", Limits: AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, BudgetMicrousd: 1000000}}
	now := func() time.Time { return time.Unix(policy.ReviewedAt+1, 0) }
	calls := 0
	rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return fixtureAdmissionResponse(r, map[string]any{"id": q.Model, "type": "model", "lifecycle": "active", "capabilities": map[string]any{"structured_outputs": map[string]bool{"supported": false}}}), nil
	})
	if admitNativeProvider(t.Context(), &q, rt, now) || calls != 1 || q.Admission.GenerationAttempted {
		t.Fatal("unsupported Messages model generated")
	}
}

func TestNativeProvidersAnthropicNullableStreamAndNullMaps(t *testing.T) {
	p := openAIModelPolicy("claude-sonnet-5-5")
	q := AgentAIRequest{Model: p.Model, Policy: &p, Stream: true, Limits: AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024}}
	wire, _ := aiWire(q)
	req, _ := http.NewRequest("POST", providerGenerationURL(q.Policy), bytes.NewReader(wire))
	res, _ := nativeFixtureResponse(req, "anthropic")
	stream, _ := io.ReadAll(res.Body)
	res.Body.Close()
	result, err := readAnthropicStream(bytes.NewReader(stream), nil)
	if err != nil {
		t.Fatal("documented nullable delta refused", err)
	}
	normalized, err := normalizeNativeResponse("anthropic", q, result)
	if err != nil || !bytes.Contains(normalized, []byte(`"input_tokens":256`)) {
		t.Fatal("initial counts lost", err)
	}
	for _, message := range []string{`null`, `{"usage":null}`, `{}`, `{"usage":[]}`} {
		frame := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":" + message + "}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
		if _, err := readAnthropicStream(strings.NewReader(frame), nil); err == nil {
			t.Fatal("null or malformed map accepted", message)
		}
	}
}

func TestNativeProvidersNoImplicitTransport(t *testing.T) {
	called := false
	original := http.DefaultTransport
	http.DefaultTransport = aiTestTransport(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unreviewed default transport")
	})
	defer func() { http.DefaultTransport = original }()
	if _, err := providerJSON(t.Context(), nil, "xai", "GET", "/v1/models", nil, new(any), 8192); err == nil || called {
		t.Fatal("native admission used implicit network transport")
	}
}
