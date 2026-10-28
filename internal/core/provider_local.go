package core

// Local model connections share provider accounts and permissions. Their endpoint
// and optional key are encrypted together; a local server grants no wallet access.
import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/fased-ai/fased-app/internal/localprovider"
)

const localWire = "local-responses-v1"
const localUsageCostBasis = "local-reported-usage"

func localPolicy(p *OpenAIModelPolicy) bool {
	return p != nil && p.Provider == "local" && p.WireVersion == localWire
}
func localRequest(q AgentAIRequest) bool        { return localPolicy(q.Policy) }
func unmeteredPolicy(p *OpenAIModelPolicy) bool { return localPolicy(p) || planSignInPolicy(p) }
func unmeteredRequest(q AgentAIRequest) bool    { return unmeteredPolicy(q.Policy) }
func reportedUsageBasis(q AgentAIRequest) string {
	if localRequest(q) {
		return localUsageCostBasis
	}
	return planUsageCostBasis
}

//go:embed provider_local.json
var localProviderSource []byte

var currentLocalModels = func() modelRegistry {
	var r modelRegistry
	if json.Unmarshal(localProviderSource, &r) != nil || r.Version != 1 || r.Provider != "local" || r.Policy != "latest-only" || r.ExpiresAt <= r.ReviewedAt || len(r.Models) < 1 || len(r.Models) > 16 {
		panic("invalid local model registry")
	}
	seen := map[string]bool{}
	for _, m := range r.Models {
		if !validProviderModelID(m.ID) || seen[m.ID] || m.Provider != "local" || m.Input != 0 || m.Output != 0 || m.Cached != 0 || m.ContextTokens < 32000 || m.OutputTokens < 1024 || m.Reasoning != "none" {
			panic("invalid local model policy")
		}
		seen[m.ID] = true
	}
	return r
}()

func localRegistry() modelRegistry { return currentLocalModels }
func localModelPolicy(model string) OpenAIModelPolicy {
	r := localRegistry()
	for _, m := range r.Models {
		if m.ID == model {
			return OpenAIModelPolicy{Provider: "local", WireVersion: localWire, ID: "local-current-v1:" + model, Model: model, ReviewedAt: r.ReviewedAt, ExpiresAt: r.ExpiresAt, ContextTokens: m.ContextTokens, OutputTokens: m.OutputTokens, ModelSource: m.Source, TariffSource: r.Sources["pricing"], TokenSource: r.Sources["discovery"], Reasoning: "none"}
		}
	}
	return OpenAIModelPolicy{}
}
func boundLocalAdmission(rt http.RoundTripper, q AgentAIRequest) bool {
	switch a := rt.(type) {
	case aiMemoryReviewTransport:
		return boundLocalAdmission(a.next, q)
	case authenticatedAITransport:
		if a.store == nil || a.request == nil || a.ref != q.ConnectionRef || a.request.ID != q.ID || a.request.Digest != q.Digest {
			return false
		}
		s := a.store
		s.provider.mu.Lock()
		defer s.provider.mu.Unlock()
		v, err := s.aiConnectionLocked(a.ref)
		state := s.credentialLocked(a.ref)
		if _, e := localprovider.ParseCredential(state.key); err != nil || e != nil || v.Provider != "local" || v.CredentialMode != "local" || v.Ref != q.ConnectionRef || !(s.providerFixture || s.provider.apiCredentials) {
			return false
		}
		if q.TaskID != "" {
			return s.taskAccessMatches(q, state.taskAccess, q.taskAccessID != "", state)
		}
		return !v.Locked
	}
	return false
}
func admitLocal(ctx context.Context, q *AgentAIRequest, rt http.RoundTripper, now func() time.Time) bool {
	wire, err := aiWire(*q)
	a := &OpenAIAdmission{State: "checking", WireSHA256: newsHash(wire)}
	q.Admission = a
	refuse := func() bool {
		a.State = "refused"
		q.State = "failed"
		q.HeldMicrousd = 0
		q.Failure = "Local model access changed or model unavailable; review the selected connection"
		return false
	}
	if err != nil || !localRequest(*q) || validateAIModelPolicy(*q, now().Unix()) != nil || !boundLocalAdmission(rt, *q) || int64(len(wire)) > q.Limits.MaxInputTokens || ctx.Err() != nil {
		return refuse()
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", localprovider.Origin+"/v1/models", nil)
	client := &http.Client{Transport: rt, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("local redirect refused") }}
	res, err := client.Do(req)
	if err != nil {
		return refuse()
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 262145))
	kind, _, mediaErr := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || len(raw) > 262144 || res.StatusCode != 200 || kind != "application/json" || mediaErr != nil || res.Header.Get("Content-Encoding") != "" || !uniqueAIJSON(raw, 32768) {
		return refuse()
	}
	var object string
	var rows []discoveredModel
	if parseProviderModelList("local", raw, &object, &rows) != nil || object != "list" {
		return refuse()
	}
	seen := map[string]bool{}
	found := false
	for _, m := range rows {
		if !validProviderModelID(m.ID) || seen[m.ID] || newerProviderRecordForID(q.Model, m, rows) {
			return refuse()
		}
		seen[m.ID] = true
		if m.ID == q.Model && (m.OwnedBy == "local" || m.OwnedBy == "system") {
			found = true
		}
	}
	if !found || !boundLocalAdmission(rt, *q) || validateAIModelPolicy(*q, now().Unix()) != nil || ctx.Err() != nil {
		return refuse()
	}
	a.AccessAt, a.ModelRecordSHA256, a.CountPayloadSHA256 = now().Unix(), newsHash(raw), newsHash(wire)
	a.State, a.GenerationAttempted = "admitted", true
	return true
}
func localFixtureResponse(r *http.Request) (*http.Response, error) {
	if r.Method == "GET" && r.URL.Path == "/v1/models" {
		rows := []map[string]any{}
		for _, m := range localRegistry().Models {
			rows = append(rows, map[string]any{"id": m.ID, "object": "model", "owned_by": "local", "created": int64(1)})
		}
		return fixtureAdmissionResponse(r, map[string]any{"object": "list", "data": rows}), nil
	}
	if r.Method != "POST" || r.URL.Path != "/v1/responses" {
		return nil, errors.New("local fixture route refused")
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if err != nil || len(b) > 65536 {
		return nil, errors.New("local fixture wire bound")
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(b, &body) != nil {
		return nil, errors.New("local fixture wire")
	}
	var model string
	json.Unmarshal(body["model"], &model)
	if localModelPolicy(model).Model == "" {
		return nil, errors.New("local fixture model refused")
	}
	// Reuse the existing stateless Responses fixture without adding another worker.
	copy := r.Clone(r.Context())
	u := *r.URL
	u.Host = "api.openai.com"
	copy.URL = &u
	copy.Body = io.NopCloser(bytes.NewReader(b))
	return (fixtureAITransport{}).RoundTrip(copy)
}
func migrateLocalProviders(ctx context.Context, db *sql.DB) error {
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
		return errors.New("finish schema26 recovery before local provider migration")
	}
	if _, _, err = readAgents(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA user_version=27"); err != nil {
		return err
	}
	return tx.Commit()
}
