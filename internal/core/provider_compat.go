package core

// Compatible providers share the existing vault, finite grants and worker. Only
// reviewed model IDs and these fixed HTTPS routes are allowed. No tools, router
// fallback, endpoint override or automatic generation is introduced here.
import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func compatibleProvider(id string) bool {
	switch id {
	case "moonshot", "moonshot-intl", "kimi-code", "ollama-cloud", "google", "openrouter", "qwen", "opencode":
		return true
	}
	return false
}
func providerBasePath(id string) string {
	switch id {
	case "kimi-code":
		return "/coding/v1"
	case "google":
		return "/v1beta/openai"
	case "openrouter":
		return "/api/v1"
	case "qwen":
		return "/compatible-mode/v1"
	case "opencode":
		return "/inference/openai/v1"
	}
	return "/v1"
}
func providerModelsPath(id string) string {
	if id == "qwen" {
		return "/api/v1/models"
	}
	if id == "openrouter" {
		return providerBasePath(id) + "/models/user"
	}
	if id == "opencode" {
		return "/inference/v1/models"
	}
	return providerBasePath(id) + "/models"
}
func providerModelBodyLimit(id string) int64 {
	if id == "openrouter" {
		return 2097152
	}
	return 262144
}
func providerJSONNodes(id string) int {
	if id == "openrouter" {
		return 262144
	}
	return 32768
}
func compatibleRequestAllowed(id string, r *http.Request) bool {
	if id == "qwen" && r.Method == http.MethodGet {
		q := r.URL.Query()
		return r.URL.Path == providerModelsPath(id) && len(q) == 3 && len(q["page_no"]) == 1 && len(q["page_size"]) == 1 && len(q["providers"]) == 1 && q.Get("page_size") == "100" && q.Get("providers") == "qwen" && q.Encode() == r.URL.RawQuery && (q.Get("page_no") == "1" || q.Get("page_no") == "2" || q.Get("page_no") == "3" || q.Get("page_no") == "4")
	}
	if r.URL.RawQuery != "" {
		if id != "openrouter" || r.Method != "GET" || r.URL.Path != providerModelsPath(id) {
			return false
		}
		q := r.URL.Query()
		if len(q) != 2 || len(q["limit"]) != 1 || q.Get("limit") != "500" || len(q["offset"]) != 1 || q.Encode() != r.URL.RawQuery {
			return false
		}
		switch q.Get("offset") {
		case "0", "500", "1000", "1500":
		default:
			return false
		}
	}
	if r.Method == http.MethodGet {
		return r.URL.Path == providerModelsPath(id)
	}
	return r.Method == http.MethodPost && (r.URL.Path == providerBasePath(id)+"/chat/completions" || id == "moonshot" && r.URL.Path == "/v1/tokenizers/estimate-token-count")
}
func providerRemoteModel(id, model string) string {
	if id == "ollama-cloud" {
		return strings.TrimPrefix(model, "ollama-cloud/")
	}
	if id == "kimi-code" {
		return strings.TrimPrefix(model, "kimi-code/")
	}
	if id == "moonshot-intl" {
		if !strings.HasPrefix(model, "moonshot-intl/") {
			return ""
		}
		return strings.TrimPrefix(model, "moonshot-intl/")
	}
	if id == "opencode" {
		if !strings.HasPrefix(model, "opencode/") {
			return ""
		}
		return strings.TrimPrefix(model, "opencode/")
	}
	return model
}
func compatibleWire(q AgentAIRequest, original map[string]any) ([]byte, error) {
	format, ok := original["text"].(map[string]any)
	if !ok {
		return nil, errors.New("output contract missing")
	}
	schema, err := json.Marshal(format)
	if err != nil {
		return nil, err
	}
	system, _ := original["instructions"].(string)
	system += " Return one JSON object matching this contract, without markdown: " + string(schema)
	body := map[string]any{"model": providerRemoteModel(policyProvider(q.Policy), q.Model), "messages": []any{map[string]string{"role": "system", "content": system}, map[string]string{"role": "user", "content": q.Context}}, "max_tokens": q.Limits.MaxOutputTokens, "response_format": map[string]string{"type": "json_object"}}
	if policyProvider(q.Policy) == "ollama-cloud" {
		delete(body, "response_format")
	} // Cloud has no structured-output API; validate the prompted JSON locally.
	if q.Stream {
		body["stream"] = true
		body["stream_options"] = map[string]bool{"include_usage": true}
	}
	switch policyProvider(q.Policy) {
	case "kimi-code":
		body["reasoning_effort"] = "low"
	case "moonshot-intl":
		body["reasoning_effort"] = "low"
		body["prompt_cache_options"] = map[string]string{"mode": "implicit", "ttl": "5m"}
	case "moonshot", "google":
		body["reasoning_effort"] = "low"
	case "qwen":
		body["enable_thinking"] = false
		body["enable_search"] = false
	case "opencode":
		body["thinking"] = map[string]string{"type": "disabled"}
	case "openrouter":
		body["reasoning"] = map[string]string{"effort": "low"}
		body["provider"] = map[string]any{"allow_fallbacks": false, "require_parameters": true, "max_price": map[string]any{"prompt": float64(q.Policy.InputMicrousdPerMillion) / 1e6, "completion": float64(q.Policy.OutputMicrousdPerMillion) / 1e6, "request": 0}}
		body["plugins"] = []any{}
	}
	return json.Marshal(body)
}

// Native Qwen discovery is paginated and is deliberately not accepted as an
// OpenAI-shaped list. Both account refresh and generation admission use it.
func qwenModelsPagePath(page int) string {
	return providerModelsPath("qwen") + "?page_no=" + strconv.Itoa(page) + "&page_size=100&providers=qwen"
}
func parseQwenModelPage(raw []byte, page int) ([]discoveredModel, int, error) {
	var response struct {
		Success bool    `json:"success"`
		Code    *string `json:"code"`
		Output  *struct {
			Total  *int `json:"total"`
			Page   int  `json:"page_no"`
			Size   int  `json:"page_size"`
			Models []struct {
				ID       string          `json:"model"`
				Provider string          `json:"provider"`
				Offline  json.RawMessage `json:"inference_offline_info"`
			} `json:"models"`
		} `json:"output"`
	}
	if !uniqueAIJSON(raw, 32768) || json.Unmarshal(raw, &response) != nil || !response.Success || response.Code != nil && *response.Code != "" || response.Output == nil || response.Output.Total == nil {
		return nil, 0, errors.New("Qwen model discovery refused")
	}
	o := response.Output
	if page < 1 || page > 4 || o.Page != page || o.Size != 100 || *o.Total < 0 || *o.Total > 400 || o.Models == nil || len(o.Models) != min(100, max(0, *o.Total-(page-1)*100)) || page > 1 && *o.Total <= (page-1)*100 {
		return nil, 0, errors.New("Qwen model page incomplete")
	}
	rows := []discoveredModel{}
	for _, m := range o.Models {
		if !strings.HasPrefix(m.ID, "qwen") || m.Provider != "" && m.Provider != "qwen" {
			return nil, 0, errors.New("Qwen model identity refused")
		}
		row := discoveredModel{ID: m.ID, Object: "model", OwnedBy: "qwen"}
		if len(m.Offline) > 0 && string(m.Offline) != "null" {
			shutdown := "offline-notice"
			row.Shutdown = &shutdown
		}
		rows = append(rows, row)
	}
	return rows, *o.Total, nil
}
func parseCompatibleModels(provider string, raw []byte, object *string, rows *[]discoveredModel) error {
	if provider == "qwen" {
		return errors.New("Qwen requires native model discovery")
	}
	var list struct {
		Object string `json:"object"`
		Data   []struct {
			ID       string  `json:"id"`
			Created  int64   `json:"created"`
			Shutdown *string `json:"shutdown_date"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &list) != nil || list.Data == nil || len(list.Data) > 2048 || list.Object != "" && list.Object != "list" {
		return errors.New("provider model list refused")
	}
	*object = "list"
	*rows = []discoveredModel{}
	for _, m := range list.Data {
		id := m.ID
		if provider == "google" {
			id = strings.TrimPrefix(id, "models/")
		}
		if provider == "opencode" {
			id = "opencode/" + id
		}
		if provider == "ollama-cloud" {
			id = "ollama-cloud/" + id
		}
		if provider == "kimi-code" {
			id = "kimi-code/" + id
		}
		if provider == "moonshot-intl" {
			id = "moonshot-intl/" + id
		}
		*rows = append(*rows, discoveredModel{ID: id, Object: "model", Created: m.Created, OwnedBy: provider, Shutdown: m.Shutdown})
	}
	return nil
}
func admitCompatible(ctx context.Context, q *AgentAIRequest, rt http.RoundTripper, now func() time.Time) bool {
	wire, wireErr := aiWire(*q)
	provider := policyProvider(q.Policy)
	a := &OpenAIAdmission{State: "checking", WireSHA256: newsHash(wire)}
	q.Admission = a
	refuse := func() bool {
		a.State = "refused"
		q.State = "failed"
		q.HeldMicrousd = 0
		q.Failure = "Provider/model/input limit changed; no generation attempted."
		return false
	}
	if kimiCodePolicy(q.Policy) && (q.TaskID != "" || q.TaskDigest != "" || q.ChatPermissionID == "") {
		return refuse()
	}
	if wireErr != nil || validateAIModelPolicy(*q, now().Unix()) != nil || !currentModelRequest(*q) || int64(len(wire))+512 > q.Limits.MaxInputTokens || ctx.Err() != nil {
		return refuse()
	}
	var rows []discoveredModel
	var hash string
	if provider == "openrouter" || provider == "qwen" {
		client := http.Client{Transport: rt, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("provider redirect refused") }}
		snapshot := fetchProviderModelList(ctx, &client, provider, now(), "api-key")
		if snapshot.State != "ready" {
			return refuse()
		}
		rows, hash = snapshot.Models, snapshot.SHA256
	} else {
		var raw json.RawMessage
		var err error
		hash, err = providerJSON(ctx, rt, provider, "GET", providerModelsPath(provider), nil, &raw, providerModelBodyLimit(provider))
		var kind string
		if err != nil || parseCompatibleModels(provider, raw, &kind, &rows) != nil {
			return refuse()
		}
	}
	found := false
	seen := map[string]bool{}
	for _, row := range rows {
		if !validProviderModelID(row.ID) || seen[row.ID] || row.Created < 0 || row.Created > now().Unix() || newerProviderRecordForID(q.Model, row, rows) {
			return refuse()
		}
		seen[row.ID] = true
		if row.ID == q.Model && row.Shutdown == nil {
			found = true
		}
	}
	if !found || aiRequestReservation(*q) > q.Limits.BudgetMicrousd || ctx.Err() != nil {
		return refuse()
	}
	// A conservative bound over the complete transmitted UTF-8 JSON plus framing;
	// this is not a provider-reported exact token count or a synthetic count receipt.
	a.AccessAt, a.ModelRecordSHA256, a.CountPayloadSHA256 = now().Unix(), hash, newsHash(wire)
	a.CountedAt, a.InputTokens = now().Unix(), int64(len(wire))+512
	a.State, a.GenerationAttempted = "admitted", true
	return true
}

type compatibleUsage struct {
	Prompt        *int64 `json:"prompt_tokens"`
	Completion    *int64 `json:"completion_tokens"`
	Total         *int64 `json:"total_tokens"`
	PromptDetails struct {
		Cached *int64 `json:"cached_tokens"`
		Write  *int64 `json:"cache_write_tokens,omitempty"`
	} `json:"prompt_tokens_details"`
	CompletionDetails struct {
		Reasoning *int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}
type compatibleMessage struct {
	Role          string          `json:"role"`
	Content       *string         `json:"content"`
	Reasoning     *string         `json:"reasoning_content"`
	ReasoningText *string         `json:"reasoning"`
	Tools         json.RawMessage `json:"tool_calls"`
	Function      json.RawMessage `json:"function_call"`
	Refusal       json.RawMessage `json:"refusal"`
}
type compatibleCompletion struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int               `json:"index"`
		Message compatibleMessage `json:"message"`
		Finish  string            `json:"finish_reason"`
	} `json:"choices"`
	Usage *compatibleUsage `json:"usage"`
}

func emptyCompatibleField(b json.RawMessage) bool {
	return len(b) == 0 || string(b) == "null" || string(b) == "[]"
}
func normalizeCompatible(q AgentAIRequest, raw []byte) ([]byte, error) {
	var c compatibleCompletion
	if !uniqueAIJSON(raw, 32768) || json.Unmarshal(raw, &c) != nil || c.ID == "" || len(c.ID) > 256 || c.Object != "chat.completion" || c.Model != providerRemoteModel(policyProvider(q.Policy), q.Model) || len(c.Choices) != 1 || c.Choices[0].Index != 0 || c.Choices[0].Finish != "stop" || c.Choices[0].Message.Role != "assistant" || c.Choices[0].Message.Content == nil || len(*c.Choices[0].Message.Content) > 8192 || c.Usage == nil {
		return nil, errors.New("completion identity or output refused")
	}
	m, u := c.Choices[0].Message, c.Usage
	if !emptyCompatibleField(m.Tools) || !emptyCompatibleField(m.Function) || !emptyCompatibleField(m.Refusal) || u.Prompt == nil || u.Completion == nil || u.Total == nil || *u.Prompt <= 0 || *u.Prompt > q.Limits.MaxInputTokens || *u.Completion < 0 || *u.Completion > q.Limits.MaxOutputTokens || *u.Total != *u.Prompt+*u.Completion {
		return nil, errors.New("completion tools or usage refused")
	}
	if policyProvider(q.Policy) == "moonshot-intl" && (u.PromptDetails.Cached == nil || u.PromptDetails.Write == nil || *u.PromptDetails.Cached < 0 || *u.PromptDetails.Write < 0 || *u.PromptDetails.Cached > *u.Prompt || *u.PromptDetails.Write > *u.Prompt-*u.PromptDetails.Cached) {
		return nil, errors.New("Kimi cache read/write usage refused")
	}
	cached := int64(0)
	if u.PromptDetails.Cached != nil {
		cached = *u.PromptDetails.Cached
	}
	if cached < 0 || cached > *u.Prompt || u.CompletionDetails.Reasoning != nil && (*u.CompletionDetails.Reasoning < 0 || *u.CompletionDetails.Reasoning > *u.Completion) {
		return nil, errors.New("completion token details refused")
	}
	result := map[string]any{"id": c.ID, "model": q.Model, "status": "completed", "service_tier": "default", "usage": map[string]any{"input_tokens": *u.Prompt, "output_tokens": *u.Completion, "total_tokens": *u.Total, "input_tokens_details": map[string]int64{"cached_tokens": cached}}, "output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]string{"type": "output_text", "text": *m.Content}}}}}
	return json.Marshal(result)
}
func readCompatibleStream(body io.Reader, q AgentAIRequest, onDelta func(string)) ([]byte, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), 32768)
	var id, model string
	var text strings.Builder
	var usage *compatibleUsage
	finished, done, role := false, false, false
	frames := 0
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			return nil, errors.New("completion stream frame refused")
		}
		value := strings.TrimPrefix(line, "data: ")
		if value == "[DONE]" {
			if done || !finished || usage == nil {
				return nil, errors.New("completion stream incomplete")
			}
			done = true
			continue
		}
		frames++
		if done || frames > 8192 || !uniqueAIJSON([]byte(value), 32768) {
			return nil, errors.New("completion stream bound")
		}
		var f struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Model   string `json:"model"`
			Choices []struct {
				Index  int               `json:"index"`
				Delta  compatibleMessage `json:"delta"`
				Finish *string           `json:"finish_reason"`
			} `json:"choices"`
			Usage *compatibleUsage `json:"usage"`
		}
		if json.Unmarshal([]byte(value), &f) != nil || f.Object != "chat.completion.chunk" || f.ID == "" || f.Model != providerRemoteModel(policyProvider(q.Policy), q.Model) || id != "" && id != f.ID {
			return nil, errors.New("completion stream identity refused")
		}
		id, model = f.ID, f.Model
		if f.Usage != nil {
			if usage != nil {
				return nil, errors.New("duplicate completion usage")
			}
			usage = f.Usage
		}
		if len(f.Choices) == 0 {
			if !finished || f.Usage == nil {
				return nil, errors.New("unexpected completion event")
			}
			continue
		}
		if len(f.Choices) != 1 || finished || f.Choices[0].Index != 0 {
			return nil, errors.New("completion choice refused")
		}
		d := f.Choices[0].Delta
		if !emptyCompatibleField(d.Tools) || !emptyCompatibleField(d.Function) || !emptyCompatibleField(d.Refusal) {
			return nil, errors.New("completion stream tools refused")
		}
		if d.Role != "" {
			if role || d.Role != "assistant" {
				return nil, errors.New("completion role refused")
			}
			role = true
		}
		if d.Content != nil {
			if !role || text.Len()+len(*d.Content) > 8192 {
				return nil, errors.New("completion text bound")
			}
			text.WriteString(*d.Content)
			if onDelta != nil {
				onDelta(text.String())
			}
		}
		if f.Choices[0].Finish != nil {
			if *f.Choices[0].Finish != "stop" {
				return nil, errors.New("completion stopped without final result")
			}
			finished = true
		}
	}
	if scanner.Err() != nil || !done || !finished || usage == nil || !role {
		return nil, errors.New("completion stream interrupted")
	}
	var c compatibleCompletion
	c.ID, c.Object, c.Model, c.Usage = id, "chat.completion", model, usage
	content := text.String()
	c.Choices = make([]struct {
		Index   int               `json:"index"`
		Message compatibleMessage `json:"message"`
		Finish  string            `json:"finish_reason"`
	}, 1)
	c.Choices[0].Message = compatibleMessage{Role: "assistant", Content: &content}
	c.Choices[0].Finish = "stop"
	return json.Marshal(c)
}
func compatibleFixtureResponse(r *http.Request, provider string) (*http.Response, error) {
	if r.Method == "GET" && r.URL.Path == providerModelsPath(provider) {
		if provider == "qwen" {
			if !providerRequestAllowed(provider, r) || r.URL.Query().Get("page_no") != "1" {
				return nil, errors.New("Qwen fixture page refused")
			}
			rows := []map[string]any{}
			for _, m := range registryForProvider(provider).Models {
				rows = append(rows, map[string]any{"model": m.ID, "provider": "qwen"})
			}
			return fixtureAdmissionResponse(r, map[string]any{"success": true, "code": nil, "output": map[string]any{"total": len(rows), "page_no": 1, "page_size": 100, "models": rows}}), nil
		}
		rows := []map[string]any{}
		for _, m := range registryForProvider(provider).Models {
			rows = append(rows, map[string]any{"id": providerRemoteModel(provider, m.ID), "object": "model", "created": int64(1), "owned_by": provider})
		}
		return fixtureAdmissionResponse(r, map[string]any{"object": "list", "data": rows}), nil
	}
	if r.Method != "POST" || r.URL.Path != providerBasePath(provider)+"/chat/completions" {
		return nil, errors.New("compatible fixture route refused")
	}
	b, e := io.ReadAll(io.LimitReader(r.Body, 65537))
	if e != nil || len(b) > 65536 {
		return nil, errors.New("compatible fixture wire bound")
	}
	var request struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if json.Unmarshal(b, &request) != nil {
		return nil, errors.New("compatible fixture wire refused")
	}
	found := false
	for _, m := range registryForProvider(provider).Models {
		if providerRemoteModel(provider, m.ID) == request.Model {
			found = true
		}
	}
	if !found {
		return nil, errors.New("compatible fixture model refused")
	}
	answer := `{"answer":"Controlled provider response. No real AI or wallet was used.","cards":[]}`
	u := &compatibleUsage{}
	if provider == "moonshot-intl" {
		zero := int64(0)
		u.PromptDetails.Cached, u.PromptDetails.Write = &zero, &zero
	}
	input, output, total := int64(256), int64(128), int64(384)
	u.Prompt, u.Completion, u.Total = &input, &output, &total
	var c compatibleCompletion
	c.ID, c.Object, c.Model, c.Usage = "fixture-compatible", "chat.completion", request.Model, u
	c.Choices = make([]struct {
		Index   int               `json:"index"`
		Message compatibleMessage `json:"message"`
		Finish  string            `json:"finish_reason"`
	}, 1)
	c.Choices[0].Message = compatibleMessage{Role: "assistant", Content: &answer}
	c.Choices[0].Finish = "stop"
	if !request.Stream {
		return fixtureAdmissionResponse(r, c), nil
	}
	frame := func(v any) string { b, _ := json.Marshal(v); return "data: " + string(b) + "\n\n" }
	event := func(delta map[string]any, finish any, usage any) map[string]any {
		choices := []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}
		if usage != nil {
			choices = []any{}
		}
		return map[string]any{"id": c.ID, "object": "chat.completion.chunk", "model": c.Model, "choices": choices, "usage": usage}
	}
	data := frame(event(map[string]any{"role": "assistant"}, nil, nil)) + frame(event(map[string]any{"content": answer[:20]}, nil, nil)) + frame(event(map[string]any{"content": answer[20:]}, "stop", nil)) + frame(event(nil, nil, u)) + "data: [DONE]\n\n"
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(bytes.NewBufferString(data)), Request: r}, nil
}

// Stable model families supersede only the same family; previews and different
// tiers do not revoke a reviewed model or silently change its binding.
func newerCompatibleGeneration(candidate, current string) bool {
	normalize := func(id string) string {
		for _, prefix := range []string{"google/", "opencode/", "moonshot-intl/", "kimi-code/", "ollama-cloud/"} {
			id = strings.TrimPrefix(id, prefix)
		}
		if strings.HasPrefix(id, "gemma") {
			parts := strings.Split(strings.TrimPrefix(id, "gemma"), ":")
			if len(parts) == 2 {
				return "gpt-" + parts[0] + "-gemma" + parts[1]
			}
		}
		for _, prefix := range []string{"gemini-", "kimi-k", "qwen", "deepseek-v", "k"} {
			if strings.HasPrefix(id, prefix) {
				s := strings.TrimPrefix(id, prefix)
				i := strings.IndexByte(s, '-')
				if i > 0 {
					return "gpt-" + s[:i] + "-" + strings.ReplaceAll(prefix+s[i+1:], "-", "")
				}
			}
		}
		return ""
	}
	a, b := normalize(candidate), normalize(current)
	return a != "" && b != "" && newerProviderGeneration(a, b)
}
