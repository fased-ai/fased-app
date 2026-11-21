package core

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Reviewed source shipped with the binary, never downloaded as executable policy.
//
//go:embed provider_models.json
var providerModelSource []byte

type currentModel struct {
	CacheWrite           int64  `json:"cacheWrite,omitempty"`
	Contract             string `json:"contract,omitempty"`
	Provider             string `json:"provider,omitempty"`
	Line                 string `json:"line,omitempty"`
	BillableOutputTokens int64  `json:"billableOutputTokens,omitempty"`
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Tier                 string `json:"tier"`
	Input                int64  `json:"input"`
	Cached               int64  `json:"cached"`
	Output               int64  `json:"output"`
	ContextTokens        int64  `json:"contextTokens"`
	OutputTokens         int64  `json:"outputTokens"`
	Source               string `json:"source"`
	Reasoning            string `json:"reasoning"`
	Currency             string `json:"currency,omitempty"`
}
type modelRegistry struct {
	Version    int               `json:"version"`
	Policy     string            `json:"policy"`
	Provider   string            `json:"provider"`
	ReviewedAt int64             `json:"reviewedAt"`
	ExpiresAt  int64             `json:"expiresAt"`
	Sources    map[string]string `json:"sources"`
	Models     []currentModel    `json:"models"`
}

var currentModels = func() modelRegistry {
	var r modelRegistry
	if json.Unmarshal(providerModelSource, &r) != nil || r.Version != 1 || r.Policy != "latest-only" || r.Provider != "openai" || len(r.Models) == 0 || len(r.Models) > 16 || r.ExpiresAt <= r.ReviewedAt {
		panic("invalid compiled model registry")
	}
	seen := map[string]bool{}
	for _, m := range r.Models {
		if !validProviderModelID(m.ID) || seen[m.ID] || m.Input <= 0 || m.Output <= 0 || m.Cached < 0 || m.Cached > m.Input || m.ContextTokens < 32000 || m.OutputTokens < 1024 || (m.Reasoning != "none" && m.Reasoning != "low") {
			panic("invalid compiled model policy")
		}
		seen[m.ID] = true
	}
	return r
}()

func currentProviderModel(id string) (currentModel, bool) {
	for _, m := range allProviderModels() {
		if m.ID == id {
			return m, true
		}
	}
	return currentModel{}, false
}
func validProviderModelID(id string) bool {
	if len(id) < 1 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_:/.", c)) {
			return false
		}
	}
	return true
}

type discoveredModel struct {
	Line     string  `json:"line,omitempty"`
	ID       string  `json:"id"`
	Name     string  `json:"name,omitempty"`
	Object   string  `json:"object"`
	Created  int64   `json:"created"`
	OwnedBy  string  `json:"owned_by"`
	Shutdown *string `json:"shutdown_date"`
}
type modelDiscovery struct {
	State     string            `json:"state"`
	FetchedAt int64             `json:"fetchedAt,omitempty"`
	ExpiresAt int64             `json:"expiresAt,omitempty"`
	SHA256    string            `json:"sha256,omitempty"`
	Source    string            `json:"source,omitempty"`
	Models    []discoveredModel `json:"-"`
}

func (s *Store) refreshProviderModels(ctx context.Context, ref string, rt http.RoundTripper) (AIConnectionView, error) {
	// Copy only the identity under lock; the HTTPS exchange never holds SQLite or
	// the credential mutex. A later completion cannot overwrite a newer refresh.
	s.provider.mu.Lock()
	v, err := s.aiConnectionLocked(ref)
	if err != nil || v.Ref != ref || v.Locked || v.AuthMethod == "sign-in" && !v.PlanUse {
		s.provider.mu.Unlock()
		return v, errors.New("unlock this account before refreshing models")
	}
	state := s.credentialLocked(ref)
	state.modelRefresh++
	refresh, epoch, digest := state.modelRefresh, state.epoch, state.digest
	state.discovery = modelDiscovery{State: "loading"}
	s.provider.mu.Unlock()

	result := modelDiscovery{State: "failed"}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	provider := v.Provider
	client := http.Client{Transport: authenticatedAITransport{store: s, ref: ref, next: rt, modelsOnly: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("provider redirect refused") }}
	result = fetchProviderModelList(ctx, &client, provider, s.now(), v.AuthMethod)

	if ctx.Err() != nil {
		result = modelDiscovery{State: "failed"}
	}
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	current, err := s.aiConnectionLocked(ref)
	state = s.provider.accounts[ref]
	if err != nil || state == nil || current.Ref != ref || current.Locked || state.epoch != epoch || state.digest != digest || state.modelRefresh != refresh {
		return current, ErrConflict
	}
	state.discovery = result
	return s.aiConnectionLocked(ref)
}

func fetchProviderModelList(ctx context.Context, client *http.Client, provider string, now time.Time, method string) modelDiscovery {
	failed := modelDiscovery{State: "failed"}
	path := providerModelsPath(provider)
	if provider == "anthropic" {
		path += "?limit=100"
	}
	if provider == "openrouter" {
		path += "?limit=500&offset=0"
	}
	if provider == "qwen" {
		path = qwenModelsPagePath(1)
	}
	qwenTotal := -1
	rows := []discoveredModel{}
	hashes := []string{}
	seen, cursors := map[string]bool{}, map[string]bool{}
	for page := 0; page < 4; page++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", providerOrigin(provider)+path, nil)
		response, err := client.Do(req)
		if err != nil {
			logProviderDiag("models-fetch", map[string]any{"provider": provider, "method": method, "endpoint": safeEndpointPath(providerOrigin(provider) + path), "err": err.Error()})
			return failed
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, providerModelBodyLimit(provider)+1))
		response.Body.Close()
		kind, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if readErr != nil || int64(len(raw)) > providerModelBodyLimit(provider) || response.StatusCode != 200 || mediaErr != nil || kind != "application/json" || response.Header.Get("Content-Encoding") != "" || !uniqueAIJSON(raw, providerJSONNodes(provider)) {
			oauthErr, oauthDesc := extractOAuthError(raw)
			logProviderDiag("models-fetch", map[string]any{
				"provider": provider, "method": method, "endpoint": safeEndpointPath(providerOrigin(provider) + path),
				"status": response.StatusCode, "contentType": response.Header.Get("Content-Type"),
				"contentEncoding": response.Header.Get("Content-Encoding"), "bodyBytes": len(raw),
				"cfRay": response.Header.Get("CF-Ray"), "requestId": response.Header.Get("x-request-id"),
				"oauthError": oauthErr, "oauthErrorDesc": oauthDesc, "reason": "response_rejected",
			})
			return failed
		}
		var records []discoveredModel
		cursor, object := "", "list"
		if method == "sign-in" {
			records, err = parseProviderSignInModels(raw, now.Unix())
		} else if provider == "anthropic" {
			records, cursor, err = parseAnthropicModelPage(raw)
		} else if provider == "qwen" {
			var total int
			records, total, err = parseQwenModelPage(raw, page+1)
			if qwenTotal >= 0 && total != qwenTotal {
				return failed
			}
			qwenTotal = total
			if (page+1)*100 < total {
				cursor = strconv.Itoa(page + 2)
			}
		} else {
			err = parseProviderModelList(provider, raw, &object, &records)
		}
		if err != nil || object != "list" || records == nil || len(rows)+len(records) > 2048 {
			errText := ""
			if err != nil {
				errText = err.Error()
			}
			logProviderDiag("models-fetch", map[string]any{"provider": provider, "method": method, "endpoint": safeEndpointPath(providerOrigin(provider) + path), "reason": "parse_error", "err": errText})
			return failed
		}
		for i := range records {
			if records[i].Object == "" {
				records[i].Object = "model"
			}
			if records[i].OwnedBy == "" {
				records[i].OwnedBy = provider
			}
			m := records[i]
			if !validProviderModelID(m.ID) || seen[m.ID] || m.Object != "model" || (m.Created <= 0 && !compatibleProvider(provider) && provider != "local" && method != "sign-in") || m.Created < 0 || m.Created > now.Unix()+300 || len(m.OwnedBy) < 1 || len(m.OwnedBy) > 128 {
				logProviderDiag("models-fetch", map[string]any{
					"provider": provider, "method": method, "endpoint": safeEndpointPath(providerOrigin(provider) + path),
					"reason": "entry_invalid", "modelId": sanitizeDiagValue(m.ID, 64), "object": sanitizeDiagValue(m.Object, 24),
					"created": m.Created, "ownedBy": sanitizeDiagValue(m.OwnedBy, 40), "duplicate": seen[m.ID],
				})
				return failed
			}
			seen[m.ID] = true
		}
		rows = append(rows, records...)
		hashes = append(hashes, newsHash(raw))
		if provider == "openrouter" && len(records) == 500 {
			cursor = strconv.Itoa(len(rows))
		}
		if cursor == "" {
			hash := hashes[0]
			if len(hashes) > 1 {
				encoded, _ := json.Marshal(hashes)
				hash = newsHash(encoded)
			}
			source := providerOrigin(provider) + providerModelsPath(provider)
			if provider == "qwen" {
				source = providerOrigin(provider) + qwenModelsPagePath(1)
			}
			return modelDiscovery{State: "ready", FetchedAt: now.Unix(), ExpiresAt: now.Add(15 * time.Minute).Unix(), SHA256: hash, Source: source, Models: rows}
		}
		if cursors[cursor] {
			return failed
		}
		cursors[cursor] = true
		path = "/v1/models?after_id=" + cursor + "&limit=100"
		if provider == "openrouter" {
			path = providerModelsPath(provider) + "?limit=500&offset=" + cursor
		}
		if provider == "qwen" {
			path = qwenModelsPagePath(page + 2)
		}
	}
	return failed
}

func providerOwnsModel(provider, ownedBy string) bool {
	return ownedBy == provider || ownedBy == "system" || (provider == "openai" && (strings.HasPrefix(ownedBy, "openai-") || ownedBy == "chatgpt"))
}

func discoveredProviderCatalog(v AIConnectionView, d modelDiscovery, now int64) ProviderCatalog {
	provider := v.Provider
	if provider == "" {
		provider = "openai"
	}
	registry := registryForProvider(provider)
	catalog := ProviderCatalog{Version: 1, Policy: "latest-only", State: d.State, FetchedAt: d.FetchedAt, ExpiresAt: d.ExpiresAt, ReviewStale: now >= registry.ExpiresAt, Source: d.Source, SHA256: d.SHA256, Providers: providerEntries(), Models: []ProviderModel{}}
	records := map[string]discoveredModel{}
	for _, r := range d.Models {
		records[r.ID] = r
	}
	curated := map[string]bool{}
	for _, entry := range registry.Models {
		curated[entry.ID] = true
		m := ProviderModel{ID: entry.ID, Name: entry.Name, Provider: provider, Method: "api-key", ConnectionRef: v.Ref, Recommended: true, Tier: entry.Tier, Policy: chatModelPolicy(entry.ID)}
		if v.AuthMethod == "oauth" {
			m.Method = "oauth"
		}
		if v.AuthMethod == "sign-in" {
			m.Method = "sign-in"
			m.Policy = chatModelPolicyForMethod(entry.ID, "sign-in")
		}
		record, listed := records[entry.ID]
		switch {
		case entry.Currency != "" && entry.Currency != "USD":
			m.Policy = nil
			m.Availability = "currency-unsupported"
		case v.AuthMethod == "sign-in" && !v.PlanUse && !v.Locked:
			m.Availability = "plan-access-required"
		case now < registry.ReviewedAt:
			m.Availability = "expired"
		case v.Ref == "":
			m.Availability = "disconnected"
		case v.Locked:
			m.Availability = "locked"
		case d.State == "":
			m.Availability = "unverified"
		case d.State != "ready":
			m.Availability = d.State
		case now < d.FetchedAt || now >= d.ExpiresAt:
			m.Availability = "stale"
		case !listed:
			m.Availability = "unavailable"
		case record.Shutdown != nil:
			m.Availability = "retired"
		case !providerOwnsModel(provider, record.OwnedBy):
			m.Availability = "unsupported"
		case v.TestOnly:
			m.Availability = "fixture"
			m.Selectable = true
		case !v.LiveCallsEnabled:
			m.Availability = "disabled"
			m.Selectable = true // Selection is review metadata; transport remains independently held.
		default:
			m.Availability = "available"
			m.Selectable = true
		}
		catalog.Models = append(catalog.Models, m)
	}
	method := v.AuthMethod
	if method == "" {
		method = "api-key"
	}
	ids := []string{}
	for id := range records {
		if !curated[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		rec := records[id]
		name := rec.Name
		if name == "" {
			name = id
		}
		pol := dynamicModelPolicy(provider, id, method)
		m := ProviderModel{ID: id, Name: name, Provider: provider, Method: method, ConnectionRef: v.Ref, Policy: pol}
		switch {
		case pol == nil:
			m.Availability = "currency-unsupported"
		case v.AuthMethod == "sign-in" && !v.PlanUse && !v.Locked:
			m.Availability = "plan-access-required"
		case v.Ref == "":
			m.Availability = "disconnected"
		case v.Locked:
			m.Availability = "locked"
		case d.State == "":
			m.Availability = "unverified"
		case d.State != "ready":
			m.Availability = d.State
		case now < d.FetchedAt || now >= d.ExpiresAt:
			m.Availability = "stale"
		case rec.Shutdown != nil:
			m.Availability = "retired"
		case !providerOwnsModel(provider, rec.OwnedBy):
			m.Availability = "unsupported"
		case v.TestOnly:
			m.Availability = "fixture"
			m.Selectable = true
		case !v.LiveCallsEnabled:
			m.Availability = "disabled"
			m.Selectable = true
		default:
			m.Availability = "available"
			m.Selectable = true
		}
		catalog.Models = append(catalog.Models, m)
	}
	if catalog.State == "" {
		catalog.State = "unverified"
	}
	return catalog
}

// New model policies must never reach a reader that can erase/rewrite them.
func currentModelRequest(q AgentAIRequest) bool {
	if q.Policy == nil {
		return false
	}
	if _, ok := currentProviderModel(q.Model); ok {
		return true
	}
	return isDiscoveredPolicy(q.Policy)
}

func newerProviderGeneration(candidate, current string) bool {
	a, b := strings.Split(candidate, "-"), strings.Split(current, "-")
	if len(a) != 3 || len(b) != 3 || a[0] != "gpt" || b[0] != "gpt" || a[2] != b[2] {
		return false
	}
	av, bv := strings.Split(a[1], "."), strings.Split(b[1], ".")
	if len(av) > 2 || len(bv) > 2 {
		return false
	}
	version := func(parts []string) (int, int, bool) {
		major, e := strconv.Atoi(parts[0])
		minor := 0
		if e != nil {
			return 0, 0, false
		}
		if len(parts) == 2 {
			minor, e = strconv.Atoi(parts[1])
		}
		return major, minor, e == nil && major >= 0 && minor >= 0
	}
	am, an, ok := version(av)
	bm, bn, ok2 := version(bv)
	return ok && ok2 && (am > bm || am == bm && an > bn)
}
func (s *Store) providerModelSuperseded(ref, model string) bool {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	if a := s.provider.accounts[ref]; a != nil {
		if a.superseded[model] {
			return true
		}
		for _, r := range a.discovery.Models {
			if r.Shutdown == nil && newerProviderRecordForID(model, r, a.discovery.Models) {
				return true
			}
		}
	}
	return false
}
