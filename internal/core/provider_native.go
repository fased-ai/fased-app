package core

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
)

type providerDefinition struct{ ID, Name, Host, Version string }

func providerDefinitionFor(id string) (providerDefinition, bool) {
	switch id {
	case "", "openai":
		return providerDefinition{"openai", "OpenAI", "api.openai.com", ""}, true
	case "anthropic":
		return providerDefinition{"anthropic", "Anthropic", "api.anthropic.com", "2026-10-21"}, true
	case "local":
		return providerDefinition{"local", "Local server", "local.fased.invalid", ""}, true
	case "xai":
		return providerDefinition{"xai", "xAI", "api.x.ai", ""}, true
	case "ollama-cloud":
		return providerDefinition{"ollama-cloud", "Ollama Cloud", "ollama.com", ""}, true
	case "kimi-code":
		return providerDefinition{"kimi-code", "Kimi Code", "api.kimi.ai", ""}, true
	case "moonshot-intl":
		return providerDefinition{"moonshot-intl", "Kimi International", "api.moonshot.ai", ""}, true
	case "moonshot":
		return providerDefinition{"moonshot", "Moonshot / Kimi", "api.moonshot.cn", ""}, true
	case "google":
		return providerDefinition{"google", "Google", "generativelanguage.googleapis.com", ""}, true
	case "openrouter":
		return providerDefinition{"openrouter", "OpenRouter", "openrouter.ai", ""}, true
	case "qwen":
		return providerDefinition{"qwen", "Qwen", "dashscope-intl.aliyuncs.com", ""}, true
	case "opencode":
		return providerDefinition{"opencode", "OpenCode Zen", "opencode.ai", ""}, true
	}
	return providerDefinition{}, false
}
func modelProvider(m currentModel) string {
	if m.Provider == "" {
		return "openai"
	}
	return m.Provider
}
func policyProvider(p *OpenAIModelPolicy) string {
	if p == nil || p.Provider == "" {
		return "openai"
	}
	return p.Provider
}
func providerEntries() []ProviderEntry {
	out := []ProviderEntry{}
	for _, id := range []string{"openai", "anthropic", "xai", "moonshot", "moonshot-intl", "kimi-code", "ollama-cloud", "google", "openrouter", "qwen", "opencode", "local"} {
		p, _ := providerDefinitionFor(id)
		methods := []ProviderMethod{{ID: "api-key", Name: "API key"}}
		if id == "local" {
			methods = []ProviderMethod{{ID: "local", Name: "Local server"}}
		}
		if id == "openai" || id == "openrouter" || id == "google" {
			label := "Sign in"
			if id == "openai" {
				label = "Continue with ChatGPT"
			}
			methods = append(methods, ProviderMethod{ID: "sign-in", Name: label})
		}
		entry := ProviderEntry{ID: id, Name: p.Name, Methods: methods}
		if id == "local" {
			entry.Servers = localServerPresets()
		}
		if id == "moonshot" {
			entry.Name, entry.Family, entry.Route = "Kimi", "moonshot", "China · CNY"
		}
		if id == "moonshot-intl" {
			entry.Family, entry.Route = "moonshot", "International · USD"
		}
		if id == "kimi-code" {
			entry.Family, entry.Route = "moonshot", "Code · Interactive"
		}
		if id == "qwen" {
			entry.Route = "ModelStudio · Singapore"
		}
		out = append(out, entry)
	}
	return out
}
func providerOrigin(id string) string {
	p, ok := providerDefinitionFor(id)
	if !ok {
		return ""
	}
	return "https://" + p.Host
}
func providerRequestAllowed(id string, r *http.Request) bool {
	p, ok := providerDefinitionFor(id)
	if !ok || r == nil || r.URL == nil || r.URL.Scheme != "https" || r.URL.Host != p.Host || r.URL.User != nil || r.URL.Opaque != "" || r.URL.RawPath != "" || r.URL.Fragment != "" || r.URL.ForceQuery || (r.Host != "" && r.Host != p.Host) {
		return false
	}
	if compatibleProvider(id) {
		return compatibleRequestAllowed(id, r)
	}
	q := r.URL.Query()
	if len(q) > 0 {
		if id != "anthropic" || r.Method != "GET" || r.URL.Path != "/v1/models" || q.Get("limit") != "100" || len(q["limit"]) != 1 || len(q) > 2 || r.URL.RawQuery != q.Encode() {
			return false
		}
		if _, exists := q["after_id"]; exists && (len(q["after_id"]) != 1 || !validProviderModelID(q.Get("after_id"))) {
			return false
		}
	}
	path := r.URL.Path
	if r.Method == "GET" {
		if path == "/v1/models" {
			return true
		}
		if strings.HasPrefix(path, "/v1/models/") {
			m, found := currentProviderModel(strings.TrimPrefix(path, "/v1/models/"))
			return found && modelProvider(m) == p.ID
		}
		return false
	}
	if r.Method != "POST" || len(q) > 0 {
		return false
	}
	if p.ID == "anthropic" {
		return path == "/v1/messages" || path == "/v1/messages/count_tokens"
	}
	if p.ID == "xai" {
		return path == "/v1/responses" || path == "/v1/tokenize-text"
	}
	return path == "/v1/responses" || path == "/v1/responses/input_tokens"
}
func providerGenerationURL(p *OpenAIModelPolicy) string {
	id := policyProvider(p)
	if compatibleProvider(id) {
		return providerOrigin(id) + providerBasePath(id) + "/chat/completions"
	}
	path := "/v1/responses"
	if id == "anthropic" {
		path = "/v1/messages"
	}
	return providerOrigin(id) + path
}

// Refuse a changed native capability contract before creating a message.
func anthropicCapabilities(record map[string]json.RawMessage, limits AILimits) bool {
	var info struct {
		Input        int64 `json:"max_input_tokens"`
		Output       int64 `json:"max_tokens"`
		Capabilities struct {
			Structured struct{ Supported bool }               `json:"structured_outputs"`
			Effort     struct{ Low struct{ Supported bool } } `json:"effort"`
			Thinking   struct {
				Types struct{ Adaptive struct{ Supported bool } }
			} `json:"thinking"`
		} `json:"capabilities"`
	}
	raw, err := json.Marshal(record)
	return err == nil && json.Unmarshal(raw, &info) == nil && info.Input >= limits.MaxInputTokens && info.Output >= limits.MaxOutputTokens && info.Capabilities.Structured.Supported && info.Capabilities.Effort.Low.Supported && info.Capabilities.Thinking.Types.Adaptive.Supported
}

func nativeRequestWire(q AgentAIRequest, wire map[string]any) ([]byte, error) {
	p := q.Policy
	if p == nil {
		return nil, errors.New("native policy missing")
	}
	id := policyProvider(p)
	if compatibleProvider(id) {
		return compatibleWire(q, wire)
	}
	if id == "local" {
		schema, err := json.Marshal(wire["text"])
		if err != nil {
			return nil, errors.New("local output contract unavailable")
		}
		instruction, _ := wire["instructions"].(string)
		wire["instructions"] = instruction + " Return exactly one JSON object matching this output contract: " + string(schema) + ". Include answer and cards; no markdown fences or surrounding prose."
		for _, key := range []string{"background", "service_tier", "text", "prompt_cache_options"} {
			delete(wire, key)
		}
		return json.Marshal(wire)
	}
	if id == "xai" {
		wire["reasoning"] = map[string]string{"effort": p.Reasoning}
		return json.Marshal(wire)
	}
	if id != "anthropic" || p.WireVersion != "anthropic-messages-v1" {
		return nil, errors.New("native wire unavailable")
	}
	schema := wire["text"].(map[string]any)["format"].(map[string]any)["schema"]
	body := map[string]any{"model": q.Model, "max_tokens": q.Limits.MaxOutputTokens, "system": wire["instructions"], "messages": []any{map[string]any{"role": "user", "content": q.Context}}, "thinking": map[string]string{"type": "adaptive"}, "output_config": map[string]any{"effort": p.Reasoning, "format": map[string]any{"type": "json_schema", "schema": schema}}, "service_tier": "standard_only"}
	if q.Stream {
		body["stream"] = true
	}
	return json.Marshal(body)
}
func providerJSON(ctx context.Context, rt http.RoundTripper, provider, method, path string, body []byte, target any, bound int64) (string, error) {
	if rt == nil {
		return "", errors.New("provider transport unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, method, providerOrigin(provider)+path, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Transport: rt, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("provider redirect refused") }}
	res, err := client.Do(req)
	if err != nil {
		return "", errors.New("provider request unavailable")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, bound+1))
	kind, _, mediaErr := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || int64(len(raw)) > bound || res.StatusCode != 200 || mediaErr != nil || kind != "application/json" || res.Header.Get("Content-Encoding") != "" || !uniqueAIJSON(raw, providerJSONNodes(provider)) {
		return "", errors.New("provider JSON refused")
	}
	if json.Unmarshal(raw, target) != nil {
		return "", errors.New("provider JSON malformed")
	}
	return newsHash(raw), nil
}
func admitNativeProvider(ctx context.Context, q *AgentAIRequest, rt http.RoundTripper, now func() time.Time) bool {
	if compatibleProvider(policyProvider(q.Policy)) {
		return admitCompatible(ctx, q, rt, now)
	}
	wire, _ := aiWire(*q)
	p := q.Policy
	provider := policyProvider(p)
	a := &OpenAIAdmission{State: "checking", WireSHA256: newsHash(wire)}
	q.Admission = a
	refuse := func() bool {
		a.State = "refused"
		q.State = "failed"
		q.HeldMicrousd = 0
		q.Failure = "Provider/model/token admission refused; no generation attempted."
		return false
	}
	if !currentModelRequest(*q) || validateAIModelPolicy(*q, now().Unix()) != nil {
		return refuse()
	}
	var record map[string]json.RawMessage
	hash, err := providerJSON(ctx, rt, provider, "GET", "/v1/models/"+q.Model, nil, &record, 16384)
	if err != nil {
		return refuse()
	}
	var id string
	json.Unmarshal(record["id"], &id)
	if id != q.Model {
		return refuse()
	}
	if provider == "anthropic" {
		var lifecycle, kind string
		json.Unmarshal(record["lifecycle"], &lifecycle)
		json.Unmarshal(record["type"], &kind)
		if lifecycle != "active" || kind != "model" || !anthropicCapabilities(record, q.Limits) {
			return refuse()
		}
	} else {
		var kind string
		json.Unmarshal(record["object"], &kind)
		if kind != "model" {
			return refuse()
		}
	}
	a.AccessAt = now().Unix()
	a.ModelRecordSHA256 = hash
	var count int64
	var countedHash string
	if provider == "anthropic" {
		var body map[string]json.RawMessage
		json.Unmarshal(wire, &body)
		delete(body, "max_tokens")
		delete(body, "stream")
		delete(body, "service_tier")
		counted, _ := json.Marshal(body)
		a.CountPayloadSHA256 = newsHash(counted)
		var response struct {
			Input *int64 `json:"input_tokens"`
		}
		countedHash, err = providerJSON(ctx, rt, provider, "POST", "/v1/messages/count_tokens", counted, &response, 8192)
		if response.Input != nil {
			count = *response.Input
		}
	} else {
		// This API tokenizes text, not a Responses envelope. Retain exactly the full
		// submitted JSON as its input and treat the result only as an estimate.
		counted, _ := json.Marshal(map[string]string{"model": q.Model, "text": string(wire)})
		a.CountPayloadSHA256 = newsHash(counted)
		var response struct {
			Tokens []struct {
				ID *int64 `json:"token_id"`
			} `json:"token_ids"`
		}
		countedHash, err = providerJSON(ctx, rt, provider, "POST", "/v1/tokenize-text", counted, &response, 262144)
		if response.Tokens == nil || len(response.Tokens) > 32000 {
			return refuse()
		}
		for _, t := range response.Tokens {
			if t.ID == nil || *t.ID < 0 {
				return refuse()
			}
		}
		count = int64(len(response.Tokens))
		if int64(len(wire))+512 > q.Limits.MaxInputTokens {
			return refuse()
		}
	}
	if err != nil || count <= 0 || count > q.Limits.MaxInputTokens || ctx.Err() != nil || aiRequestReservation(*q) > q.Limits.BudgetMicrousd {
		return refuse()
	}
	a.InputTokens = count
	a.CountedAt = now().Unix()
	a.CountResponseSHA256 = countedHash
	a.State = "admitted"
	a.GenerationAttempted = true
	return true
}

func normalizeNativeResponse(provider string, q AgentAIRequest, raw []byte) ([]byte, error) {
	if compatibleProvider(provider) {
		return normalizeCompatible(q, raw)
	}
	if q.Policy == nil || !uniqueAIJSON(raw, 32768) {
		return nil, errors.New("ambiguous native response")
	}
	if provider == "xai" {
		var body map[string]json.RawMessage
		if json.Unmarshal(raw, &body) != nil {
			return nil, errors.New("native response refused")
		}
		var usage struct {
			Input     *int64           `json:"input_tokens"`
			Output    *int64           `json:"output_tokens"`
			Total     *int64           `json:"total_tokens"`
			Tools     *int64           `json:"num_server_side_tools_used"`
			Sources   *int64           `json:"num_sources_used"`
			ToolUsage map[string]int64 `json:"server_side_tool_usage_details"`
			Details   struct {
				Reasoning *int64 `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		}
		var u map[string]json.RawMessage
		if json.Unmarshal(body["usage"], &u) != nil || json.Unmarshal(body["usage"], &usage) != nil {
			return nil, errors.New("native usage missing")
		}
		if usage.Input == nil || usage.Output == nil || usage.Total == nil || usage.Details.Reasoning == nil || *usage.Input < 0 || *usage.Input > q.Limits.MaxInputTokens || *usage.Output < 0 || *usage.Output > q.Limits.MaxOutputTokens || *usage.Details.Reasoning < 0 || *usage.Details.Reasoning > q.Policy.BillableOutputTokens || *usage.Output+*usage.Details.Reasoning > q.Policy.BillableOutputTokens || *usage.Total != *usage.Input+*usage.Output+*usage.Details.Reasoning {
			return nil, errors.New("native billable usage unverified")
		}
		if usage.Tools != nil && *usage.Tools != 0 || usage.Sources != nil && *usage.Sources != 0 {
			return nil, errors.New("unexpected provider tools")
		}
		for _, n := range usage.ToolUsage {
			if n != 0 {
				return nil, errors.New("unexpected provider tools")
			}
		}
		u["output_tokens"], _ = json.Marshal(*usage.Output + *usage.Details.Reasoning)
		body["usage"], _ = json.Marshal(u)
		return json.Marshal(body)
	}
	var message struct {
		ID, Type, Role, Model string
		Stop                  string `json:"stop_reason"`
		Content               []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage *struct {
			Tier    string           `json:"service_tier"`
			Input   *int64           `json:"input_tokens"`
			Output  *int64           `json:"output_tokens"`
			Cached  *int64           `json:"cache_read_input_tokens"`
			Created *int64           `json:"cache_creation_input_tokens"`
			Tools   map[string]int64 `json:"server_tool_use"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &message) != nil || message.Type != "message" || message.Role != "assistant" || message.Model != q.Model || message.ID == "" || message.Usage == nil || message.Usage.Input == nil || message.Usage.Output == nil || *message.Usage.Input < 0 || *message.Usage.Input > q.Limits.MaxInputTokens || *message.Usage.Output < 0 || *message.Usage.Output > q.Limits.MaxOutputTokens || message.Usage.Tier != "standard" {
		return nil, errors.New("native identity/usage unverified")
	}
	cached := int64(0)
	if message.Usage.Cached != nil {
		cached = *message.Usage.Cached
	}
	if cached < 0 || cached > q.Limits.MaxInputTokens || cached+*message.Usage.Input > q.Limits.MaxInputTokens || message.Usage.Created != nil && *message.Usage.Created != 0 {
		return nil, errors.New("unreviewed cache usage")
	}
	for _, n := range message.Usage.Tools {
		if n != 0 {
			return nil, errors.New("unexpected provider tools")
		}
	}
	text := ""
	messages := 0
	for _, c := range message.Content {
		switch c.Type {
		case "text":
			text = c.Text
			messages++
		case "thinking", "redacted_thinking":
		default:
			return nil, errors.New("unapproved native content")
		}
	}
	status := "incomplete"
	if message.Stop == "end_turn" && messages == 1 {
		status = "completed"
	}
	input := *message.Usage.Input + cached
	output := *message.Usage.Output
	return json.Marshal(map[string]any{"id": message.ID, "model": message.Model, "status": status, "service_tier": "default", "usage": map[string]any{"input_tokens": input, "output_tokens": output, "total_tokens": input + output, "input_tokens_details": map[string]int64{"cached_tokens": cached}}, "output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]string{"type": "output_text", "text": text}}}}})
}

// Anthropic SSE is aggregated into its native final message for the same result
// validator. Hidden thinking/signatures are never forwarded to Chat or memory.
func readAnthropicStream(body io.Reader, onDelta func(string)) ([]byte, error) {
	scanner := bufio.NewScanner(io.LimitReader(body, 1048577))
	scanner.Buffer(make([]byte, 4096), 262144)
	var message map[string]json.RawMessage
	var content []map[string]any
	indices := map[int]string{}
	var text strings.Builder
	started, stopped, finalized := false, false, false
	event := ""
	data := ""
	frames := 0
	total := 0
	flush := func() bool {
		if data == "" {
			event = ""
			return true
		}
		frames++
		if frames > 4096 || !uniqueAIJSON([]byte(data), 32768) || stopped {
			return false
		}
		var f struct {
			Type    string
			Message json.RawMessage
			Index   int
			Block   map[string]any `json:"content_block"`
			Delta   map[string]json.RawMessage
			Usage   map[string]json.RawMessage
		}
		if json.Unmarshal([]byte(data), &f) != nil || f.Type != event {
			return false
		}
		switch f.Type {
		case "ping":
		case "message_start":
			if started || json.Unmarshal(f.Message, &message) != nil || message == nil {
				return false
			}
			var usage map[string]json.RawMessage
			if json.Unmarshal(message["usage"], &usage) != nil || usage == nil {
				return false
			}
			started = true
		case "content_block_start":
			if !started || finalized || f.Index != len(content) {
				return false
			}
			kind, _ := f.Block["type"].(string)
			if kind != "text" && kind != "thinking" && kind != "redacted_thinking" {
				return false
			}
			content = append(content, f.Block)
			indices[f.Index] = kind
		case "content_block_delta":
			kind, ok := indices[f.Index]
			if !ok {
				return false
			}
			var dtype string
			json.Unmarshal(f.Delta["type"], &dtype)
			if kind == "text" {
				if dtype != "text_delta" {
					return false
				}
				var part string
				if json.Unmarshal(f.Delta["text"], &part) != nil {
					return false
				}
				text.WriteString(part)
				if text.Len() > 16384 {
					return false
				}
				content[f.Index]["text"] = text.String()
				if onDelta != nil {
					onDelta(partialAIAnswer(text.String()))
				}
			}
			if kind != "text" && dtype != "thinking_delta" && dtype != "signature_delta" {
				return false
			}
		case "content_block_stop":
			if _, ok := indices[f.Index]; !ok {
				return false
			}
			delete(indices, f.Index)
		case "message_delta":
			if !started || finalized || len(indices) != 0 {
				return false
			}
			for k, v := range f.Delta {
				if k == "stop_details" {
					continue
				}
				if k == "container" && bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
					continue
				}
				if k != "stop_reason" && k != "stop_sequence" {
					return false
				}
				message[k] = v
			}
			var usage map[string]json.RawMessage
			if json.Unmarshal(message["usage"], &usage) != nil || usage == nil || f.Usage["output_tokens"] == nil {
				return false
			}
			for k, v := range f.Usage {
				// Native cumulative input/cache fields may be null in the final
				// delta. They do not erase verified counts from message_start.
				if k != "output_tokens" && bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
					continue
				}
				usage[k] = v
			}
			message["usage"], _ = json.Marshal(usage)
			finalized = true
		case "message_stop":
			if !started || !finalized || len(indices) != 0 {
				return false
			}
			stopped = true
		default:
			return false
		}
		event = ""
		data = ""
		return true
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		total += len(line) + 1
		if total > 1048576 {
			return nil, errors.New("native stream bound")
		}
		if line == "" {
			if !flush() {
				return nil, errors.New("native stream refused")
			}
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			if event != "" {
				return nil, errors.New("native stream event")
			}
			event = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			if data != "" {
				return nil, errors.New("native stream frame")
			}
			data = strings.TrimPrefix(line, "data: ")
		} else {
			return nil, errors.New("native stream line")
		}
	}
	if scanner.Err() != nil || !stopped || data != "" {
		return nil, errors.New("native stream interrupted")
	}
	message["content"], _ = json.Marshal(content)
	return json.Marshal(message)
}
func nativeFixtureResponse(r *http.Request, provider string) (*http.Response, error) {
	if r.Method == "GET" {
		id := strings.TrimPrefix(r.URL.Path, "/v1/models/")
		rows := []map[string]any{}
		for _, m := range allProviderModels() {
			if modelProvider(m) != provider {
				continue
			}
			if r.URL.Path != "/v1/models" && m.ID != id {
				continue
			}
			row := map[string]any{"id": m.ID, "object": "model", "owned_by": provider, "created": 1744588800}
			if provider == "anthropic" {
				row = map[string]any{"id": m.ID, "type": "model", "created_at": "2026-10-07T00:00:00Z", "lifecycle": "active", "line": m.Line, "display_name": m.Name, "max_input_tokens": m.ContextTokens, "max_tokens": m.OutputTokens, "capabilities": map[string]any{"structured_outputs": map[string]bool{"supported": true}, "effort": map[string]any{"low": map[string]bool{"supported": true}}, "thinking": map[string]any{"types": map[string]any{"adaptive": map[string]bool{"supported": true}}}}}
			}
			rows = append(rows, row)
		}
		if r.URL.Path != "/v1/models" {
			if len(rows) != 1 {
				return nil, errors.New("fixture model unavailable")
			}
			return fixtureAdmissionResponse(r, rows[0]), nil
		}
		if provider == "anthropic" {
			return fixtureAdmissionResponse(r, map[string]any{"data": rows, "has_more": false, "first_id": nil, "last_id": nil}), nil
		}
		return fixtureAdmissionResponse(r, map[string]any{"object": "list", "data": rows}), nil
	}
	if provider == "anthropic" && r.URL.Path == "/v1/messages/count_tokens" {
		return fixtureAdmissionResponse(r, map[string]int{"input_tokens": 256}), nil
	}
	if provider == "xai" && r.URL.Path == "/v1/tokenize-text" {
		tokens := []map[string]int{}
		for i := 0; i < 256; i++ {
			tokens = append(tokens, map[string]int{"token_id": i})
		}
		return fixtureAdmissionResponse(r, map[string]any{"token_ids": tokens}), nil
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, errors.New("fixture body bound")
	}
	var request struct {
		Model  string
		Stream bool
	}
	if json.Unmarshal(raw, &request) != nil {
		return nil, errors.New("fixture request")
	}
	m, ok := currentProviderModel(request.Model)
	if !ok || modelProvider(m) != provider {
		return nil, errors.New("fixture provider/model mismatch")
	}
	answer := `{"answer":"Development fixture response. No provider or wallet was contacted.","cards":[]}`
	if provider == "xai" {
		output := map[string]any{"id": "fixture-xai", "model": request.Model, "status": "completed", "service_tier": "default", "usage": map[string]any{"input_tokens": 256, "output_tokens": 128, "total_tokens": 400, "input_tokens_details": map[string]int{"cached_tokens": 0}, "output_tokens_details": map[string]int{"reasoning_tokens": 16}, "num_server_side_tools_used": 0}, "output": []any{map[string]any{"type": "reasoning", "encrypted_content": "opaque-fixture"}, map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]string{"type": "output_text", "text": answer}}}}}
		body, _ := json.Marshal(output)
		if request.Stream {
			return fixtureAIStream(r, body, []byte(answer)), nil
		}
		return fixtureAdmissionResponse(r, output), nil
	}
	msg := map[string]any{"id": "fixture-anthropic", "type": "message", "role": "assistant", "model": request.Model, "stop_reason": "end_turn", "content": []any{map[string]string{"type": "text", "text": answer}}, "usage": map[string]any{"input_tokens": 256, "output_tokens": 128, "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0, "service_tier": "standard"}}
	if !request.Stream {
		return fixtureAdmissionResponse(r, msg), nil
	}
	frame := func(kind string, value any) string {
		raw, _ := json.Marshal(value)
		return "event: " + kind + "\ndata: " + string(raw) + "\n\n"
	}
	start := map[string]any{}
	for k, v := range msg {
		start[k] = v
	}
	start["stop_reason"] = nil
	start["content"] = []any{}
	start["usage"] = map[string]any{"input_tokens": 256, "output_tokens": 0, "service_tier": "standard"}
	stream := frame("message_start", map[string]any{"type": "message_start", "message": start}) + frame("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]string{"type": "text", "text": ""}})
	for _, part := range []string{answer[:24], answer[24:]} {
		stream += frame("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": part}})
	}
	stream += frame("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}) + frame("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "container": nil, "stop_details": nil}, "usage": map[string]any{"output_tokens": 128, "input_tokens": nil, "cache_read_input_tokens": nil, "cache_creation_input_tokens": nil}}) + frame("message_stop", map[string]string{"type": "message_stop"})
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream)), Request: r}, nil
}

//go:embed provider_anthropic.json
var anthropicModelSource []byte

//go:embed provider_xai.json
var xaiModelSource []byte

//go:embed provider_moonshot.json
var moonshotModelSource []byte

//go:embed provider_moonshot_intl.json
var moonshotIntlModelSource []byte

//go:embed provider_google.json
var googleModelSource []byte

//go:embed provider_openrouter.json
var openrouterModelSource []byte

//go:embed provider_qwen.json
var qwenModelSource []byte

//go:embed provider_opencode.json
var opencodeModelSource []byte

var nativeRegistries = func() map[string]modelRegistry {
	out := map[string]modelRegistry{}
	for id, raw := range map[string][]byte{"anthropic": anthropicModelSource, "xai": xaiModelSource, "moonshot": moonshotModelSource, "moonshot-intl": moonshotIntlModelSource, "google": googleModelSource, "openrouter": openrouterModelSource, "qwen": qwenModelSource, "opencode": opencodeModelSource} {
		var r modelRegistry
		if json.Unmarshal(raw, &r) != nil || r.Version != 1 || r.Provider != id || r.Policy != "latest-only" || len(r.Models) == 0 || len(r.Models) > 16 || r.ExpiresAt <= r.ReviewedAt {
			panic("invalid native registry")
		}
		for _, m := range r.Models {
			if id == "moonshot-intl" && (m.Currency != "USD" || m.CacheWrite != m.Input || !strings.HasPrefix(m.ID, "moonshot-intl/")) {
				panic("invalid Kimi region/cache policy")
			}
			if modelProvider(m) != id || !validProviderModelID(m.ID) || m.Input <= 0 || m.Output <= 0 || m.Cached < 0 || m.Cached > m.Input || m.ContextTokens < 32000 || m.OutputTokens < 1024 || m.BillableOutputTokens > m.ContextTokens || compatibleProvider(id) && (m.Currency != "USD" && m.Currency != "CNY") || id == "opencode" && !strings.HasPrefix(m.ID, "opencode/") {
				panic("invalid native model policy")
			}
		}
		out[id] = r
	}
	return out
}()

func registryForProvider(id string) modelRegistry {
	if id == "ollama-cloud" {
		return currentOllamaCloudModels
	}
	if id == "kimi-code" {
		return currentKimiCodeModels
	}
	if id == "local" {
		return localRegistry()
	}
	if id == "" || id == "openai" {
		return currentModels
	}
	return nativeRegistries[id]
}
func allProviderModels() []currentModel {
	out := append([]currentModel(nil), currentModels.Models...)
	for _, id := range []string{"anthropic", "xai", "moonshot", "moonshot-intl", "kimi-code", "ollama-cloud", "google", "openrouter", "qwen", "opencode"} {
		out = append(out, registryForProvider(id).Models...)
	}
	out = append(out, localRegistry().Models...)
	return out
}
func parseProviderModelList(provider string, raw []byte, object *string, rows *[]discoveredModel) error {
	if compatibleProvider(provider) {
		return parseCompatibleModels(provider, raw, object, rows)
	}
	if provider == "openai" || provider == "local" {
		var v struct {
			Object string
			Data   []discoveredModel
		}
		if json.Unmarshal(raw, &v) != nil {
			return errors.New("list refused")
		}
		*object = v.Object
		if provider == "local" {
			for i := range v.Data {
				if v.Data[i].OwnedBy == "library" || v.Data[i].OwnedBy == "local" || v.Data[i].OwnedBy == "vllm" || v.Data[i].OwnedBy == "organization_owner" || v.Data[i].OwnedBy == "lmstudio" || v.Data[i].OwnedBy == "lmstudio-community" {
					v.Data[i].OwnedBy = "local"
				}
			}
		}
		*rows = v.Data
		return nil
	}
	if provider == "xai" {
		var v struct{ Data []discoveredModel }
		if json.Unmarshal(raw, &v) != nil || v.Data == nil {
			return errors.New("list refused")
		}
		for i := range v.Data {
			if v.Data[i].Object != "" && v.Data[i].Object != "model" {
				return errors.New("list type refused")
			}
			v.Data[i].Object = "model"
			v.Data[i].OwnedBy = "xai"
		}
		*object = "list"
		*rows = v.Data
		return nil
	}
	page, cursor, err := parseAnthropicModelPage(raw)
	if err != nil || cursor != "" {
		return errors.New("model list incomplete")
	}
	*object, *rows = "list", page
	return nil
}
func parseAnthropicModelPage(raw []byte) ([]discoveredModel, string, error) {
	var native struct {
		Data []struct {
			ID        string `json:"id"`
			Type      string `json:"type"`
			Created   string `json:"created_at"`
			Lifecycle string `json:"lifecycle"`
			Line      string `json:"line"`
		}
		More *bool  `json:"has_more"`
		Last string `json:"last_id"`
	}
	if json.Unmarshal(raw, &native) != nil || native.Data == nil || native.More == nil {
		return nil, "", errors.New("model list incomplete")
	}
	out := []discoveredModel{}
	for _, m := range native.Data {
		stamp, err := time.Parse(time.RFC3339, m.Created)
		if err != nil || m.Type != "model" || m.Lifecycle != "active" && m.Lifecycle != "deprecated" && m.Lifecycle != "retired" {
			return nil, "", errors.New("model record refused")
		}
		r := discoveredModel{ID: m.ID, Object: "model", Created: stamp.Unix(), OwnedBy: "anthropic", Line: m.Line}
		if m.Lifecycle != "active" {
			retired := m.Lifecycle
			r.Shutdown = &retired
		}
		out = append(out, r)
	}
	cursor := ""
	if *native.More {
		if len(out) == 0 || !validProviderModelID(native.Last) || native.Last != out[len(out)-1].ID {
			return nil, "", errors.New("model cursor refused")
		}
		cursor = native.Last
	}
	return out, cursor, nil
}

func newerProviderRecordForID(id string, r discoveredModel, all []discoveredModel) bool {
	m, ok := currentProviderModel(id)
	return ok && newerProviderRecord(m, r, all)
}
func newerProviderRecord(m currentModel, r discoveredModel, all []discoveredModel) bool {
	if compatibleProvider(modelProvider(m)) {
		return newerCompatibleGeneration(r.ID, m.ID)
	}
	switch modelProvider(m) {
	case "local":
		// Same local Gemma family across documented Ollama/LM Studio/HF IDs.
		family := func(id string) string {
			id = strings.ToLower(strings.TrimPrefix(id, "google/"))
			id = strings.ReplaceAll(id, "gemma-", "gemma")
			end := strings.IndexAny(strings.TrimPrefix(id, "gemma"), "-:")
			if !strings.HasPrefix(id, "gemma") || end < 1 {
				return ""
			}
			return "gpt-" + strings.TrimPrefix(id, "gemma")[:end] + "-gemma"
		}
		x, y := family(r.ID), family(m.ID)
		return x != "" && y != "" && newerProviderGeneration(x, y)
	case "anthropic":
		if r.Line == "" || r.Line != m.Line || r.ID == m.ID {
			return false
		}
		for _, old := range all {
			if old.ID == m.ID {
				return r.Created > old.Created
			}
		}
		return false
	case "xai":
		a, b := strings.TrimPrefix(r.ID, "grok-"), strings.TrimPrefix(m.ID, "grok-")
		// Only stable numeric releases supersede; preview/vision variants do not.
		return newerProviderGeneration("gpt-"+a+"-grok", "gpt-"+b+"-grok")
	default:
		return newerProviderGeneration(r.ID, m.ID)
	}
}
func maxModelBudget(id string) int64 {
	if m, ok := currentProviderModel(id); ok && modelProvider(m) == "xai" {
		return 10000000
	}
	return 1000000
}

func migrateNativeProviders(ctx context.Context, db *sql.DB) error {
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
		return errors.New("finish schema 24 recovery before provider migration")
	}
	if _, _, err = readAgents(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA user_version=25"); err != nil {
		return err
	}
	return tx.Commit()
}
