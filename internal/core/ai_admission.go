package core

// Admission is exercised with disposable credentials and injected HTTPS only.
// No catalog entry, access result or token count enables live API transport.
import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"
)

const admittedOpenAIModel = "gpt-4.1-mini-2025-04-14"

type OpenAIModelPolicy struct {
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

func openAIModelPolicy() OpenAIModelPolicy {
	reviewed := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC).Unix()
	return OpenAIModelPolicy{ID: "openai-text-standard-2026-10-21-v1", Model: admittedOpenAIModel,
		ReviewedAt: reviewed, ExpiresAt: reviewed + 30*86400, ContextTokens: 1047576, OutputTokens: 32768,
		InputMicrousdPerMillion: 400000, CachedMicrousdPerMillion: 100000, OutputMicrousdPerMillion: 1600000,
		ModelSource:  "https://developers.openai.com/api/docs/models/gpt-4.1-mini",
		TariffSource: "https://developers.openai.com/api/docs/pricing",
		TokenSource:  "https://developers.openai.com/api/reference/resources/responses/subresources/input_tokens/methods/count"}
}

func validateAIModelPolicy(q AgentAIRequest, now int64) error {
	if q.Model == fixtureAIModel && q.Policy == nil {
		return nil
	}
	p := openAIModelPolicy()
	if q.Policy == nil || *q.Policy != p || q.Model != p.Model || q.ConnectionRef == "" || now < p.ReviewedAt || now >= p.ExpiresAt || q.Limits.MaxInputTokens+q.Limits.MaxOutputTokens > p.ContextTokens || q.Limits.MaxOutputTokens > p.OutputTokens {
		return errors.New("model, token or tariff policy unavailable/expired; review supported admission with an unlocked test connection")
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
	return aiUsageCost(q, q.Limits.MaxInputTokens, 0, q.Limits.MaxOutputTokens)
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
	return (r.Method == "GET" && r.URL.Path == "/v1/models/"+admittedOpenAIModel) ||
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
func uniqueAIJSON(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	tokens := 0
	var value func(int) bool
	value = func(depth int) bool {
		tokens++
		if depth > 16 || tokens > 4096 {
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
	if validateAIModelPolicy(*q, now().Unix()) != nil {
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
