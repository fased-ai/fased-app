package core

// Admission is exercised with disposable credentials and injected HTTPS only.
// No catalog entry, access result or token count enables live API transport.
import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
)

const admittedOpenAIModel = "gpt-6.1-terra"
const legacyOpenAIModel = "gpt-4.1-mini-2025-04-14"

type OpenAIModelPolicy struct {
	Provider                 string `json:"provider,omitempty"`
	WireVersion              string `json:"wireVersion,omitempty"`
	APIVersion               string `json:"apiVersion,omitempty"`
	BillableOutputTokens     int64  `json:"billableOutputTokens,omitempty"`
	MaxRequestBudgetMicrousd int64  `json:"maxRequestBudgetMicrousd,omitempty"`
	Reasoning                string `json:"reasoning,omitempty"`
	CacheMode                string `json:"cacheMode,omitempty"`
	ID                       string `json:"id"`
	Model                    string `json:"model"`
	ReviewedAt               int64  `json:"reviewedAt"`
	ExpiresAt                int64  `json:"expiresAt"`
	ContextTokens            int64  `json:"contextTokens"`
	OutputTokens             int64  `json:"outputTokens"`
	InputMicrousdPerMillion  int64  `json:"inputMicrousdPerMillion"`
	CachedMicrousdPerMillion int64  `json:"cachedMicrousdPerMillion"`
	OutputMicrousdPerMillion int64  `json:"outputMicrousdPerMillion"`
	ModelSource              string `json:"modelSource"`
	TariffSource             string `json:"tariffSource"`
	TokenSource              string `json:"tokenSource"`
}

type OpenAIAdmission struct {
	State               string `json:"state"`
	WireSHA256          string `json:"wireSha256"`
	AccessAt            int64  `json:"accessAt"`
	ModelRecordSHA256   string `json:"modelRecordSha256"`
	CountedAt           int64  `json:"countedAt"`
	InputTokens         int64  `json:"inputTokens"`
	CountPayloadSHA256  string `json:"countPayloadSha256"`
	CountResponseSHA256 string `json:"countResponseSha256"`
	GenerationAttempted bool   `json:"generationAttempted"`
}

func legacyOpenAIModelPolicy() OpenAIModelPolicy {
	reviewed := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC).Unix()
	return OpenAIModelPolicy{ID: "openai-text-standard-2026-10-21-v1", Model: legacyOpenAIModel,
		ReviewedAt: reviewed, ExpiresAt: reviewed + 30*86400, ContextTokens: 1047576, OutputTokens: 32768,
		InputMicrousdPerMillion: 400000, CachedMicrousdPerMillion: 100000, OutputMicrousdPerMillion: 1600000,
		ModelSource:  "https://developers.openai.com/api/docs/models/gpt-4.1-mini",
		TariffSource: "https://developers.openai.com/api/docs/pricing",
		TokenSource:  "https://developers.openai.com/api/reference/resources/responses/subresources/input_tokens/methods/count"}
}

func openAIModelPolicy(ids ...string) OpenAIModelPolicy {
	id := admittedOpenAIModel
	if len(ids) > 0 {
		id = ids[0]
	}
	if id == legacyOpenAIModel {
		return legacyOpenAIModelPolicy()
	}
	m, ok := currentProviderModel(id)
	if !ok {
		return OpenAIModelPolicy{}
	}
	p := OpenAIModelPolicy{ID: fmt.Sprintf("%s-latest-v%d-%d:%s", modelProvider(m), registryForProvider(modelProvider(m)).Version, registryForProvider(modelProvider(m)).ReviewedAt, id), Model: id, Reasoning: m.Reasoning, ReviewedAt: registryForProvider(modelProvider(m)).ReviewedAt, ExpiresAt: registryForProvider(modelProvider(m)).ExpiresAt, ContextTokens: m.ContextTokens, OutputTokens: m.OutputTokens, InputMicrousdPerMillion: m.Input, CachedMicrousdPerMillion: m.Cached, OutputMicrousdPerMillion: m.Output, ModelSource: m.Source, TariffSource: registryForProvider(modelProvider(m)).Sources["pricing"]}
	if modelProvider(m) == "openai" {
		p.CacheMode = "explicit"
		p.TokenSource = "https://developers.openai.com/api/reference/resources/responses/subresources/input_tokens/methods/count"
	} else {
		p.Provider = modelProvider(m)
		p.MaxRequestBudgetMicrousd = maxModelBudget(id)
		p.WireVersion = p.Provider + "-responses-v1"
		if p.Provider == "anthropic" {
			p.WireVersion = "anthropic-messages-v1"
			p.APIVersion = "2026-10-21"
			p.TokenSource = "https://platform.claude.com/docs/en/api/messages/count_tokens"
		} else {
			p.BillableOutputTokens = m.BillableOutputTokens
			p.TokenSource = "https://docs.x.ai/developers/rest-api-reference/inference/other"
		}
	}
	return p
}
func migrateProviderModels(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var phase string
	if err = tx.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase); err != nil {
		return err
	}
	if phase == "paused" {
		return errors.New("finish schema 23 recovery before current model migration")
	}
	if _, _, err = readAgents(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA user_version=24"); err != nil {
		return err
	}
	return tx.Commit()
}

func validateAIModelPolicy(q AgentAIRequest, now int64) error {
	if q.Model == fixtureAIModel && q.Policy == nil {
		return nil
	}
	p := openAIModelPolicy(q.Model)
	if q.Policy != nil && q.Policy.WireVersion == "openai-signin-v1" {
		selected := legacySignInModelPolicy(q.Model)
		if selected == nil {
			return errors.New("sign-in model policy unavailable")
		}
		p = *selected
	}
	if planSignInRequest(q) {
		selected := chatModelPolicyForMethod(q.Model, "sign-in")
		if selected == nil {
			return errors.New("plan model policy unavailable")
		}
		p = *selected
	}
	if q.Policy == nil || *q.Policy != p || q.Model != p.Model || q.ConnectionRef == "" || now < p.ReviewedAt || now >= p.ExpiresAt || q.Limits.MaxInputTokens+q.Limits.MaxOutputTokens > p.ContextTokens || q.Limits.MaxOutputTokens > p.OutputTokens {
		return errors.New("model, token or tariff policy unavailable/expired; review supported admission with an unlocked test connection")
	}
	if planSignInRequest(q) && !validPolicyLimits(q.Limits, q.Policy) {
		return errors.New("plan requests require zero monetary fields and finite token/time acceptance limits")
	}
	return nil
}

// Ceiling division over bounded integers; never floating point currency.
func aiUsageCost(q AgentAIRequest, input, cached, output int64) int64 {
	if q.Policy == nil {
		return input + 4*output
	}
	p := q.Policy
	return ((input-cached)*p.InputMicrousdPerMillion + cached*p.CachedMicrousdPerMillion + output*p.OutputMicrousdPerMillion + 999999) / 1000000
}
func aiRequestReservation(q AgentAIRequest) int64 {
	output := q.Limits.MaxOutputTokens
	if q.Policy != nil && q.Policy.BillableOutputTokens > output {
		output = q.Policy.BillableOutputTokens
	}
	return aiUsageCost(q, q.Limits.MaxInputTokens, 0, output)
}
func aiWireWithinBound(q AgentAIRequest, wire []byte) bool {
	if q.Policy == nil {
		return int64(len(wire)) <= q.Limits.MaxInputTokens
	}
	// Actual token admission uses the provider counting contract, not byte count.
	return len(wire) <= 65536
}
func openAIRequestAllowed(r *http.Request) bool {
	if r.URL.Scheme != "https" || r.URL.Host != "api.openai.com" || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.Fragment != "" || r.URL.User != nil {
		return false
	}
	_, known := currentProviderModel(strings.TrimPrefix(r.URL.Path, "/v1/models/"))
	return (r.Method == "GET" && (r.URL.Path == "/v1/models" || strings.HasPrefix(r.URL.Path, "/v1/models/") && known)) ||
		(r.Method == "POST" && (r.URL.Path == "/v1/responses" || r.URL.Path == "/v1/responses/input_tokens"))
}
func openAICountWire(q AgentAIRequest) ([]byte, error) {
	b, e := aiWire(q)
	if e != nil {
		return nil, e
	}
	var wire map[string]json.RawMessage
	if e = json.Unmarshal(b, &wire); e != nil {
		return nil, e
	}
	// All token-bearing fields, including instructions, schema and empty tools,
	// remain byte-for-byte identical. No conversation/previous-response state.
	for _, key := range []string{"store", "background", "service_tier", "max_output_tokens", "stream"} {
		delete(wire, key)
	}
	return json.Marshal(wire)
}
func admissionJSON(ctx context.Context, rt http.RoundTripper, method, path string, body []byte, target any) (string, error) {
	if rt == nil {
		return "", errors.New("admission transport unavailable")
	}
	req, e := http.NewRequestWithContext(ctx, method, "https://api.openai.com"+path, bytes.NewReader(body))
	if e != nil {
		return "", e
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Transport: rt, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("admission redirect refused") }}
	res, e := client.Do(req)
	if e != nil {
		return "", errors.New("admission request unavailable")
	}
	defer res.Body.Close()
	kind, _, mediaErr := mime.ParseMediaType(res.Header.Get("Content-Type"))
	b, e := io.ReadAll(io.LimitReader(res.Body, 8193))
	if e != nil || len(b) > 8192 || res.StatusCode != 200 || mediaErr != nil || kind != "application/json" || res.Header.Get("Content-Encoding") != "" || !uniqueAIJSON(b) {
		return "", errors.New("admission response refused")
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(target); e != nil || decoder.Decode(new(any)) != io.EOF {
		return "", errors.New("admission response malformed")
	}
	return newsHash(b), nil
}

// Duplicate usage/identity keys are ambiguous evidence, even if encoding/json
// would select their final value. Bounds are independent of the provider schema.
func uniqueAIJSON(raw []byte, budgets ...int) bool {
	budget := 4096
	if len(budgets) > 0 {
		budget = budgets[0]
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	tokens := 0
	var value func(int) bool
	value = func(depth int) bool {
		tokens++
		if depth > 16 || tokens > budget {
			return false
		}
		t, e := d.Token()
		if e != nil {
			return false
		}
		open, container := t.(json.Delim)
		if !container {
			return true
		}
		keys := map[string]bool{}
		for d.More() {
			if open == '{' {
				key, err := d.Token()
				name, ok := key.(string)
				if err != nil || !ok || keys[name] {
					return false
				}
				keys[name] = true
			}
			if !value(depth + 1) {
				return false
			}
		}
		close, err := d.Token()
		return err == nil && ((open == '{' && close == json.Delim('}')) || (open == '[' && close == json.Delim(']')))
	}
	if !value(0) {
		return false
	}
	_, e := d.Token()
	return e == io.EOF
}

func admitOpenAI(ctx context.Context, q *AgentAIRequest, rt http.RoundTripper, now func() time.Time) bool {
	wire, _ := aiWire(*q)
	a := &OpenAIAdmission{State: "checking", WireSHA256: newsHash(wire)}
	q.Admission = a
	refuse := func() bool {
		a.State = "refused"
		q.State, q.HeldMicrousd = "failed", 0
		q.Failure = "Model/access/token/tariff admission refused; no response creation attempted. Review a new request; no retry or fallback."
		return false
	}
	if !currentModelRequest(*q) || validateAIModelPolicy(*q, now().Unix()) != nil {
		return refuse()
	}
	var model struct {
		ID       string  `json:"id"`
		Object   string  `json:"object"`
		Created  *int64  `json:"created"`
		OwnedBy  string  `json:"owned_by"`
		Shutdown *string `json:"shutdown_date"`
	}
	hash, e := admissionJSON(ctx, rt, "GET", "/v1/models/"+q.Model, nil, &model)
	if e != nil || model.ID != q.Model || model.Object != "model" || (model.OwnedBy != "openai" && model.OwnedBy != "system") || model.Created == nil || *model.Created <= 0 || *model.Created > now().Unix() || model.Shutdown != nil {
		return refuse()
	}
	a.AccessAt, a.ModelRecordSHA256 = now().Unix(), hash
	countWire, e := openAICountWire(*q)
	if e != nil {
		return refuse()
	}
	a.CountPayloadSHA256 = newsHash(countWire)
	var counted struct {
		Object string `json:"object"`
		Input  *int64 `json:"input_tokens"`
	}
	hash, e = admissionJSON(ctx, rt, "POST", "/v1/responses/input_tokens", countWire, &counted)
	if e != nil || counted.Object != "response.input_tokens" || counted.Input == nil || *counted.Input <= 0 || *counted.Input > q.Limits.MaxInputTokens {
		return refuse()
	}
	a.CountedAt, a.InputTokens, a.CountResponseSHA256 = now().Unix(), *counted.Input, hash
	if validateAIModelPolicy(*q, now().Unix()) != nil || a.CountedAt < a.AccessAt || a.CountedAt-a.AccessAt > 30 || ctx.Err() != nil || aiRequestReservation(*q) > q.Limits.BudgetMicrousd {
		return refuse()
	}
	a.State = "admitted"
	// Set before crossing the creation boundary. Unverified outcomes retain hold.
	a.GenerationAttempted = true
	return true
}

func signInRequest(q AgentAIRequest) bool {
	return q.Policy != nil && policyProvider(q.Policy) == "openai" && (q.Policy.WireVersion == "openai-signin-v1" || q.Policy.WireVersion == planSignInWire)
}

// Sign-in preview does not expose the API-key token-count contract. This local
// conservative bound verifies the connected fixture journey only. Production
// admission remains refused even if another release gate changes later.
func controlledSignInAdmission(rt http.RoundTripper, q AgentAIRequest) bool {
	switch value := rt.(type) {
	case aiMemoryReviewTransport:
		return controlledSignInAdmission(value.next, q)
	case authenticatedAITransport:
		if value.store == nil || value.request == nil || value.ref != q.ConnectionRef || value.request.ID != q.ID {
			return false
		}
		value.store.provider.mu.Lock()
		allowed := value.store.providerFixture || value.store.provider.signInTransport != nil
		value.store.provider.mu.Unlock()
		return allowed
	}
	return false
}

func admitSignInFixture(ctx context.Context, q *AgentAIRequest, rt http.RoundTripper, now func() time.Time) bool {
	wire, err := aiWire(*q)
	a := &OpenAIAdmission{State: "checking", WireSHA256: newsHash(wire)}
	q.Admission = a
	refuse := func() bool {
		a.State = "refused"
		q.State, q.HeldMicrousd = "failed", 0
		q.Failure = "Sign-in fixture admission unavailable; no response attempted. Live subscription admission stays held."
		return false
	}
	if err != nil || !signInRequest(*q) || planSignInRequest(*q) || !controlledSignInAdmission(rt, *q) || validateAIModelPolicy(*q, now().Unix()) != nil {
		return refuse()
	}
	client := &http.Client{Transport: rt, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("sign-in redirect refused") }}
	discovery := fetchProviderModelList(ctx, client, "openai", now(), "sign-in")
	found := false
	for _, model := range discovery.Models {
		if model.ID == q.Model && model.Shutdown == nil {
			found = true
		}
	}
	if discovery.State != "ready" || !found || discovery.ExpiresAt <= now().Unix() {
		return refuse()
	}
	a.AccessAt, a.ModelRecordSHA256 = now().Unix(), discovery.SHA256
	// Include the entire submitted JSON plus a fixed envelope allowance. Never
	// claim this synthetic estimate came from a provider token-count endpoint.
	estimate := int64(len(wire)) + 256
	if estimate > q.Limits.MaxInputTokens || ctx.Err() != nil || aiRequestReservation(*q) > q.Limits.BudgetMicrousd {
		return refuse()
	}
	a.CountedAt, a.InputTokens = now().Unix(), estimate
	a.CountPayloadSHA256 = newsHash(wire)
	a.CountResponseSHA256 = newsHash([]byte(fmt.Sprintf("sign-in-fixture-byte-bound-v1:%d", estimate)))
	if a.CountedAt < a.AccessAt || validateAIModelPolicy(*q, now().Unix()) != nil {
		return refuse()
	}
	a.State, a.GenerationAttempted = "admitted", true
	return true
}

func migrateAIAdmission(ctx context.Context, db *sql.DB) error {
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var phase string
	if e = tx.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase); e != nil {
		return e
	}
	if phase == "paused" {
		return errors.New("core metadata paused; finish recovery before admission migration")
	}
	if _, e = tx.ExecContext(ctx, "PRAGMA user_version=9"); e != nil {
		return e
	}
	return tx.Commit()
}
