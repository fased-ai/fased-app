package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type resolvedAgentProvider struct {
	Ref        string
	Provider   string
	Name       string
	AuthMethod string
	Model      string
	Fixture    bool
}

type providerV2Prompt struct {
	Instructions string
	Messages     []AgentV2Message
	Tools        []AgentToolSpec
}

type providerToolCall struct {
	ItemID   string `json:"itemId,omitempty"`
	CallID   string `json:"callId"`
	Name     string `json:"name"`
	ArgsJSON string `json:"argsJson"`
}

type providerTurnResult struct {
	ResponseID   string
	Text         string
	Reasoning    string
	ToolCalls    []providerToolCall
	InputTokens  int64
	OutputTokens int64
	CachedTokens int64
	FinishReason string
}

type providerStreamCallbacks struct {
	onTextDelta     func(string)
	onThinkingDelta func(string)
}

func resolveAgentV2Provider(ctx context.Context, s *Store, explicitRef, threadRef, explicitModel, threadModel, profileID string) (resolvedAgentProvider, error) {
	profileRef, profileProvider, profileModel := "", "", ""
	if d, _, err := readAgents(ctx, s.db); err == nil {
		for _, p := range d.Profiles {
			if p.ID == profileID {
				profileRef = p.ConnectionRef
				profileProvider = p.Provider
				profileModel = p.Model
				break
			}
		}
	}

	pickModel := func(v AIConnectionView) string {
		if m := strings.TrimSpace(explicitModel); m != "" && validProviderModelID(m) {
			return m
		}
		if m := strings.TrimSpace(threadModel); m != "" && validProviderModelID(m) {
			return m
		}
		if m := strings.TrimSpace(profileModel); m != "" && validProviderModelID(m) && (profileRef == v.Ref || (profileRef == "" && profileProvider == v.Provider)) {
			return m
		}
		if v.Model != "" {
			return v.Model
		}
		for _, m := range v.Catalog.Models {
			if m.Selectable && m.ID != "" {
				return m.ID
			}
		}
		for _, m := range v.Catalog.Models {
			if m.ID != "" {
				return m.ID
			}
		}
		reg := registryForProvider(v.Provider)
		if len(reg.Models) > 0 {
			return reg.Models[0].ID
		}
		return ""
	}

	buildResolved := func(v AIConnectionView) (resolvedAgentProvider, error) {
		model := pickModel(v)
		if model == "" {
			return resolvedAgentProvider{}, fmt.Errorf("no model available for provider %s", v.Provider)
		}
		return resolvedAgentProvider{
			Ref:        v.Ref,
			Provider:   v.Provider,
			Name:       v.Name,
			AuthMethod: v.AuthMethod,
			Model:      model,
		}, nil
	}

	// Priority 1: explicit connectionRef from request.
	if ref := strings.TrimSpace(explicitRef); ref != "" {
		if !validConnectionRef(ref) {
			return resolvedAgentProvider{}, errors.New("invalid AI connection reference")
		}
		v, err := s.AIConnection(ref)
		if err != nil || v.Ref != ref || v.Locked || v.PlanPaused {
			return resolvedAgentProvider{}, errors.New("selected AI provider is locked or unavailable; unlock or reconnect it in Providers")
		}
		return buildResolved(v)
	}

	// Priority 2: thread or profile affinity if still unlocked and active.
	for _, candidateRef := range []string{strings.TrimSpace(threadRef), strings.TrimSpace(profileRef)} {
		if candidateRef == "" || !validConnectionRef(candidateRef) {
			continue
		}
		v, err := s.AIConnection(candidateRef)
		if err == nil && v.Ref == candidateRef && !v.Locked && !v.PlanPaused {
			return buildResolved(v)
		}
	}

	// Priority 3: default or first unlocked connection.
	v, err := s.AIConnection()
	if err == nil && v.Ref != "" && !v.Locked && !v.PlanPaused {
		return buildResolved(v)
	}
	if err == nil {
		for _, c := range v.Connections {
			if c.Ref != "" && !c.Locked && !c.PlanPaused {
				return buildResolved(c)
			}
		}
	}

	// Fixture server fallback when no live connection is configured.
	if s.providerFixture && s.agentV2TransportOverride == nil {
		model := strings.TrimSpace(explicitModel)
		if model == "" {
			model = strings.TrimSpace(threadModel)
		}
		if model == "" {
			model = "fixture-v2"
		}
		return resolvedAgentProvider{
			Provider:   "openai",
			Name:       "Fixture",
			AuthMethod: "fixture",
			Model:      model,
			Fixture:    true,
		}, nil
	}
	if err == nil && (v.Ref != "" || len(v.Connections) > 0) {
		return resolvedAgentProvider{}, errors.New("connected AI provider is locked or paused; unlock it in Providers")
	}
	return resolvedAgentProvider{}, errors.New("no AI provider connected; connect one in Providers first")
}

func streamProviderV2(ctx context.Context, s *Store, target resolvedAgentProvider, prompt providerV2Prompt, cb providerStreamCallbacks) (providerTurnResult, error) {
	if target.Fixture && s.agentV2TransportOverride == nil {
		return runFixtureProviderV2(ctx, prompt, cb)
	}
	if !s.aiExecutionAvailable(s.providerFixture) && s.agentV2TransportOverride == nil {
		return providerTurnResult{}, errors.New("provider calls are disabled on this instance")
	}
	baseRT := s.agentV2TransportOverride
	if baseRT == nil {
		baseRT = s.providerRuntimeTransport()
	}
	rt := authenticatedAITransport{
		store:   s,
		ref:     target.Ref,
		next:    baseRT,
		agentV2: true,
	}
	client := &http.Client{
		Transport: rt,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("provider redirect refused")
		},
	}

	switch {
	case target.Provider == "anthropic":
		return callAnthropicV2(ctx, client, target, prompt, cb)
	case compatibleProvider(target.Provider):
		return callCompatibleV2(ctx, client, target, prompt, cb)
	default:
		return callResponsesV2(ctx, client, target, prompt, cb)
	}
}

func toolOutputJSONForWire(tc AgentToolCall) string {
	if strings.TrimSpace(tc.ResultJSON) != "" {
		return tc.ResultJSON
	}
	if tc.Status == "rejected" {
		msg := tc.ErrorText
		if msg == "" {
			msg = "Owner rejected action."
		}
		b, _ := json.Marshal(map[string]any{"status": "rejected", "reason": msg})
		return string(b)
	}
	if tc.ErrorText != "" {
		b, _ := json.Marshal(map[string]any{"status": "error", "error": tc.ErrorText})
		return string(b)
	}
	return `{"status":"skipped","reason":"action did not complete"}`
}

func safeCallID(tc AgentToolCall, idx int) string {
	if strings.TrimSpace(tc.CallID) != "" {
		return tc.CallID
	}
	if strings.TrimSpace(tc.ID) != "" {
		return tc.ID
	}
	return fmt.Sprintf("call_%d", idx)
}

func callResponsesV2(ctx context.Context, client *http.Client, target resolvedAgentProvider, prompt providerV2Prompt, cb providerStreamCallbacks) (providerTurnResult, error) {
	input := make([]any, 0, len(prompt.Messages)*2)
	for _, m := range prompt.Messages {
		switch m.Role {
		case "user":
			input = append(input, map[string]any{
				"role":    "user",
				"content": []any{map[string]string{"type": "input_text", "text": m.Content}},
			})
		case "assistant":
			if strings.TrimSpace(m.Content) != "" {
				input = append(input, map[string]any{
					"type":    "message",
					"role":    "assistant",
					"content": []any{map[string]string{"type": "output_text", "text": m.Content}},
				})
			}
			for i, tc := range m.ToolCalls {
				cid := safeCallID(tc, i)
				args := strings.TrimSpace(tc.ArgsJSON)
				if args == "" {
					args = "{}"
				}
				input = append(input, map[string]any{
					"type":      "function_call",
					"call_id":   cid,
					"name":      tc.ToolName,
					"arguments": args,
				})
				input = append(input, map[string]any{
					"type":    "function_call_output",
					"call_id": cid,
					"output":  toolOutputJSONForWire(tc),
				})
			}
		case "tool":
			// Tool outputs are emitted immediately beside each parent assistant function_call.
		}
	}

	payload := map[string]any{
		"model":        target.Model,
		"instructions": prompt.Instructions,
		"input":        input,
		"store":        false,
		"stream":       true,
	}
	if len(prompt.Tools) > 0 {
		tools := make([]map[string]any, 0, len(prompt.Tools))
		for _, spec := range prompt.Tools {
			tools = append(tools, map[string]any{
				"type":        "function",
				"name":        spec.Name,
				"description": spec.Description,
				"parameters":  spec.Parameters,
			})
		}
		payload["tools"] = tools
		payload["tool_choice"] = "auto"
	}
	if target.AuthMethod != "sign-in" {
		payload["max_output_tokens"] = 4096
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return providerTurnResult{}, err
	}

	endpoint := providerOrigin(target.Provider) + "/v1/responses"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return providerTurnResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		return providerTurnResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return providerTurnResult{}, readProviderHTTPError(resp)
	}
	return parseResponsesV2SSE(resp.Body, cb)
}

func parseResponsesV2SSE(body io.Reader, cb providerStreamCallbacks) (providerTurnResult, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 8192), 2*1024*1024)

	var out providerTurnResult
	var textBuf, reasonBuf strings.Builder
	byIndex := map[int]*providerToolCall{}
	order := []int{}
	byItemID := map[string]*providerToolCall{}
	eventName := ""
	var dataLines []string

	ensureIndex := func(idx int) *providerToolCall {
		if tc, ok := byIndex[idx]; ok {
			return tc
		}
		tc := &providerToolCall{}
		byIndex[idx] = tc
		order = append(order, idx)
		return tc
	}

	flush := func() error {
		if len(dataLines) == 0 {
			eventName = ""
			return nil
		}
		rawData := strings.TrimSpace(strings.Join(dataLines, "\n"))
		ev := eventName
		eventName = ""
		dataLines = nil
		if rawData == "" || rawData == "[DONE]" {
			return nil
		}
		var frame struct {
			Type        string          `json:"type"`
			Delta       string          `json:"delta"`
			Text        string          `json:"text"`
			Arguments   string          `json:"arguments"`
			ItemID      string          `json:"item_id"`
			OutputIndex int             `json:"output_index"`
			Item        json.RawMessage `json:"item"`
			Response    json.RawMessage `json:"response"`
			Error       *struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(rawData), &frame) != nil {
			return nil
		}
		kind := frame.Type
		if kind == "" {
			kind = ev
		}
		switch kind {
		case "response.created":
			var r struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(frame.Response, &r) == nil && r.ID != "" {
				out.ResponseID = r.ID
			}
		case "response.output_text.delta":
			if frame.Delta != "" {
				textBuf.WriteString(frame.Delta)
				if cb.onTextDelta != nil {
					cb.onTextDelta(frame.Delta)
				}
			}
		case "response.output_text.done":
			if textBuf.Len() == 0 && frame.Text != "" {
				textBuf.WriteString(frame.Text)
				if cb.onTextDelta != nil {
					cb.onTextDelta(frame.Text)
				}
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if frame.Delta != "" {
				reasonBuf.WriteString(frame.Delta)
				if cb.onThinkingDelta != nil {
					cb.onThinkingDelta(frame.Delta)
				}
			}
		case "response.output_item.added", "response.output_item.done":
			var item struct {
				ID        string `json:"id"`
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}
			if json.Unmarshal(frame.Item, &item) == nil && item.Type == "function_call" {
				tc := byItemID[item.ID]
				if tc == nil {
					tc = ensureIndex(frame.OutputIndex)
				}
				if item.ID != "" {
					tc.ItemID = item.ID
					byItemID[item.ID] = tc
				}
				if item.CallID != "" {
					tc.CallID = item.CallID
				}
				if item.Name != "" {
					tc.Name = item.Name
				}
				if item.Arguments != "" && (kind == "response.output_item.done" || tc.ArgsJSON == "") {
					tc.ArgsJSON = item.Arguments
				}
			}
		case "response.function_call_arguments.delta":
			tc := byItemID[frame.ItemID]
			if tc == nil {
				tc = ensureIndex(frame.OutputIndex)
				if frame.ItemID != "" {
					tc.ItemID = frame.ItemID
					byItemID[frame.ItemID] = tc
				}
			}
			tc.ArgsJSON += frame.Delta
		case "response.function_call_arguments.done":
			tc := byItemID[frame.ItemID]
			if tc == nil {
				tc = ensureIndex(frame.OutputIndex)
			}
			if frame.Arguments != "" {
				tc.ArgsJSON = frame.Arguments
			}
		case "response.completed", "response.incomplete":
			var r struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Output []struct {
					ID        string `json:"id"`
					Type      string `json:"type"`
					CallID    string `json:"call_id"`
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
					Content   []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"output"`
				Usage *struct {
					InputTokens        int64 `json:"input_tokens"`
					OutputTokens       int64 `json:"output_tokens"`
					InputTokensDetails *struct {
						CachedTokens int64 `json:"cached_tokens"`
					} `json:"input_tokens_details"`
				} `json:"usage"`
			}
			if json.Unmarshal(frame.Response, &r) == nil {
				if r.ID != "" {
					out.ResponseID = r.ID
				}
				if r.Status != "" {
					out.FinishReason = r.Status
				}
				if r.Usage != nil {
					out.InputTokens = r.Usage.InputTokens
					out.OutputTokens = r.Usage.OutputTokens
					if r.Usage.InputTokensDetails != nil {
						out.CachedTokens = r.Usage.InputTokensDetails.CachedTokens
					}
				}
				for idx, item := range r.Output {
					if item.Type == "function_call" && item.Name != "" {
						tc := byItemID[item.ID]
						if tc == nil {
							tc = ensureIndex(idx)
						}
						if item.ID != "" {
							tc.ItemID = item.ID
							byItemID[item.ID] = tc
						}
						if item.CallID != "" {
							tc.CallID = item.CallID
						}
						tc.Name = item.Name
						if item.Arguments != "" {
							tc.ArgsJSON = item.Arguments
						}
					} else if item.Type == "message" && textBuf.Len() == 0 {
						for _, part := range item.Content {
							if part.Text != "" {
								textBuf.WriteString(part.Text)
								if cb.onTextDelta != nil {
									cb.onTextDelta(part.Text)
								}
							}
						}
					}
				}
			}
		case "response.failed":
			var r struct {
				Error *struct {
					Message string `json:"message"`
					Code    string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(frame.Response, &r)
			if r.Error != nil && r.Error.Message != "" {
				return fmt.Errorf("provider error: %s", sanitizeDiagValue(r.Error.Message, 240))
			}
			return errors.New("provider response failed")
		case "error":
			if frame.Error != nil && frame.Error.Message != "" {
				return fmt.Errorf("provider stream error: %s", sanitizeDiagValue(frame.Error.Message, 240))
			}
			return errors.New("provider stream error")
		}
		return nil
	}

	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return providerTurnResult{}, err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(line[len("event:"):])
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimPrefix(line[len("data:"):], " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return providerTurnResult{}, err
	}
	if err := flush(); err != nil {
		return providerTurnResult{}, err
	}

	out.Text = textBuf.String()
	out.Reasoning = reasonBuf.String()
	for i, idx := range order {
		tc := byIndex[idx]
		if tc == nil || tc.Name == "" {
			continue
		}
		cid := strings.TrimSpace(tc.CallID)
		if cid == "" {
			cid = tc.ItemID
		}
		if cid == "" {
			cid = fmt.Sprintf("call_%d", i+1)
		}
		args := strings.TrimSpace(tc.ArgsJSON)
		if args == "" {
			args = "{}"
		}
		out.ToolCalls = append(out.ToolCalls, providerToolCall{
			ItemID:   tc.ItemID,
			CallID:   cid,
			Name:     tc.Name,
			ArgsJSON: args,
		})
	}
	return out, nil
}

func callAnthropicV2(ctx context.Context, client *http.Client, target resolvedAgentProvider, prompt providerV2Prompt, cb providerStreamCallbacks) (providerTurnResult, error) {
	type antMsg struct {
		Role    string
		Content []map[string]any
	}
	var merged []antMsg
	appendRole := func(role string, blocks []map[string]any) {
		if len(blocks) == 0 {
			return
		}
		if len(merged) > 0 && merged[len(merged)-1].Role == role {
			merged[len(merged)-1].Content = append(merged[len(merged)-1].Content, blocks...)
			return
		}
		merged = append(merged, antMsg{Role: role, Content: blocks})
	}

	for _, m := range prompt.Messages {
		switch m.Role {
		case "user":
			appendRole("user", []map[string]any{{"type": "text", "text": m.Content}})
		case "assistant":
			blocks := []map[string]any{}
			if strings.TrimSpace(m.Content) != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
			}
			for i, tc := range m.ToolCalls {
				cid := safeCallID(tc, i)
				var inputObj any = map[string]any{}
				if err := json.Unmarshal([]byte(tc.ArgsJSON), &inputObj); err != nil || inputObj == nil {
					inputObj = map[string]any{}
				}
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    cid,
					"name":  tc.ToolName,
					"input": inputObj,
				})
			}
			appendRole("assistant", blocks)
			if len(m.ToolCalls) > 0 {
				results := make([]map[string]any, 0, len(m.ToolCalls))
				for i, tc := range m.ToolCalls {
					results = append(results, map[string]any{
						"type":        "tool_result",
						"tool_use_id": safeCallID(tc, i),
						"content":     toolOutputJSONForWire(tc),
					})
				}
				appendRole("user", results)
			}
		}
	}
	if len(merged) == 0 || merged[0].Role != "user" {
		merged = append([]antMsg{{Role: "user", Content: []map[string]any{{"type": "text", "text": "Continue."}}}}, merged...)
	}

	wireMsgs := make([]map[string]any, 0, len(merged))
	for _, mm := range merged {
		wireMsgs = append(wireMsgs, map[string]any{
			"role":    mm.Role,
			"content": mm.Content,
		})
	}
	payload := map[string]any{
		"model":      target.Model,
		"max_tokens": 4096,
		"system":     prompt.Instructions,
		"messages":   wireMsgs,
		"stream":     true,
	}
	if len(prompt.Tools) > 0 {
		tools := make([]map[string]any, 0, len(prompt.Tools))
		for _, spec := range prompt.Tools {
			tools = append(tools, map[string]any{
				"name":         spec.Name,
				"description":  spec.Description,
				"input_schema": spec.Parameters,
			})
		}
		payload["tools"] = tools
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return providerTurnResult{}, err
	}

	endpoint := providerOrigin("anthropic") + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return providerTurnResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		return providerTurnResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return providerTurnResult{}, readProviderHTTPError(resp)
	}
	return parseAnthropicV2SSE(resp.Body, cb)
}

func parseAnthropicV2SSE(body io.Reader, cb providerStreamCallbacks) (providerTurnResult, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 8192), 2*1024*1024)

	var out providerTurnResult
	var textBuf, reasonBuf strings.Builder
	blockKind := map[int]string{}
	toolsByIndex := map[int]*providerToolCall{}
	toolOrder := []int{}
	eventName := ""
	var dataLines []string

	flush := func() error {
		if len(dataLines) == 0 {
			eventName = ""
			return nil
		}
		rawData := strings.TrimSpace(strings.Join(dataLines, "\n"))
		ev := eventName
		eventName = ""
		dataLines = nil
		if rawData == "" {
			return nil
		}
		var frame struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message *struct {
				ID    string `json:"id"`
				Usage *struct {
					InputTokens  int64 `json:"input_tokens"`
					CacheRead    int64 `json:"cache_read_input_tokens"`
					OutputTokens int64 `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
			ContentBlock *struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta *struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage *struct {
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(rawData), &frame) != nil {
			return nil
		}
		kind := frame.Type
		if kind == "" {
			kind = ev
		}
		switch kind {
		case "message_start":
			if frame.Message != nil {
				out.ResponseID = frame.Message.ID
				if frame.Message.Usage != nil {
					out.InputTokens = frame.Message.Usage.InputTokens + frame.Message.Usage.CacheRead
					out.CachedTokens = frame.Message.Usage.CacheRead
				}
			}
		case "content_block_start":
			if frame.ContentBlock != nil {
				blockKind[frame.Index] = frame.ContentBlock.Type
				if frame.ContentBlock.Type == "tool_use" {
					toolsByIndex[frame.Index] = &providerToolCall{
						CallID: frame.ContentBlock.ID,
						Name:   frame.ContentBlock.Name,
					}
					toolOrder = append(toolOrder, frame.Index)
				}
			}
		case "content_block_delta":
			if frame.Delta == nil {
				return nil
			}
			switch frame.Delta.Type {
			case "text_delta":
				if frame.Delta.Text != "" {
					textBuf.WriteString(frame.Delta.Text)
					if cb.onTextDelta != nil {
						cb.onTextDelta(frame.Delta.Text)
					}
				}
			case "thinking_delta":
				if frame.Delta.Thinking != "" {
					reasonBuf.WriteString(frame.Delta.Thinking)
					if cb.onThinkingDelta != nil {
						cb.onThinkingDelta(frame.Delta.Thinking)
					}
				}
			case "input_json_delta":
				if tc := toolsByIndex[frame.Index]; tc != nil {
					tc.ArgsJSON += frame.Delta.PartialJSON
				}
			}
		case "message_delta":
			if frame.Delta != nil && frame.Delta.StopReason != "" {
				out.FinishReason = frame.Delta.StopReason
			}
			if frame.Usage != nil && frame.Usage.OutputTokens > 0 {
				out.OutputTokens = frame.Usage.OutputTokens
			}
		case "error":
			if frame.Error != nil && frame.Error.Message != "" {
				return fmt.Errorf("anthropic stream error: %s", sanitizeDiagValue(frame.Error.Message, 240))
			}
			return errors.New("anthropic stream error")
		}
		return nil
	}

	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return providerTurnResult{}, err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(line[len("event:"):])
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimPrefix(line[len("data:"):], " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return providerTurnResult{}, err
	}
	if err := flush(); err != nil {
		return providerTurnResult{}, err
	}
	out.Text = textBuf.String()
	out.Reasoning = reasonBuf.String()
	for i, idx := range toolOrder {
		tc := toolsByIndex[idx]
		if tc == nil || tc.Name == "" {
			continue
		}
		cid := strings.TrimSpace(tc.CallID)
		if cid == "" {
			cid = fmt.Sprintf("toolu_%d", i+1)
		}
		args := strings.TrimSpace(tc.ArgsJSON)
		if args == "" {
			args = "{}"
		}
		out.ToolCalls = append(out.ToolCalls, providerToolCall{
			CallID:   cid,
			Name:     tc.Name,
			ArgsJSON: args,
		})
	}
	return out, nil
}

func callCompatibleV2(ctx context.Context, client *http.Client, target resolvedAgentProvider, prompt providerV2Prompt, cb providerStreamCallbacks) (providerTurnResult, error) {
	remoteModel := providerRemoteModel(target.Provider, target.Model)
	if remoteModel == "" {
		remoteModel = target.Model
	}
	msgs := make([]map[string]any, 0, len(prompt.Messages)*2+1)
	if strings.TrimSpace(prompt.Instructions) != "" {
		msgs = append(msgs, map[string]any{
			"role":    "system",
			"content": prompt.Instructions,
		})
	}
	for _, m := range prompt.Messages {
		switch m.Role {
		case "user":
			msgs = append(msgs, map[string]any{
				"role":    "user",
				"content": m.Content,
			})
		case "assistant":
			entry := map[string]any{
				"role": "assistant",
			}
			if m.Content != "" {
				entry["content"] = m.Content
			} else if len(m.ToolCalls) > 0 {
				entry["content"] = nil
			} else {
				continue
			}
			if len(m.ToolCalls) > 0 {
				tcs := make([]map[string]any, 0, len(m.ToolCalls))
				for i, tc := range m.ToolCalls {
					args := strings.TrimSpace(tc.ArgsJSON)
					if args == "" {
						args = "{}"
					}
					tcs = append(tcs, map[string]any{
						"id":   safeCallID(tc, i),
						"type": "function",
						"function": map[string]any{
							"name":      tc.ToolName,
							"arguments": args,
						},
					})
				}
				entry["tool_calls"] = tcs
			}
			msgs = append(msgs, entry)
			for i, tc := range m.ToolCalls {
				msgs = append(msgs, map[string]any{
					"role":         "tool",
					"tool_call_id": safeCallID(tc, i),
					"content":      toolOutputJSONForWire(tc),
				})
			}
		}
	}

	payload := map[string]any{
		"model":          remoteModel,
		"messages":       msgs,
		"max_tokens":     4096,
		"stream":         true,
		"stream_options": map[string]bool{"include_usage": true},
	}
	if len(prompt.Tools) > 0 {
		tools := make([]map[string]any, 0, len(prompt.Tools))
		for _, spec := range prompt.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        spec.Name,
					"description": spec.Description,
					"parameters":  spec.Parameters,
				},
			})
		}
		payload["tools"] = tools
		payload["tool_choice"] = "auto"
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return providerTurnResult{}, err
	}

	endpoint := providerOrigin(target.Provider) + providerBasePath(target.Provider) + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return providerTurnResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		return providerTurnResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return providerTurnResult{}, readProviderHTTPError(resp)
	}
	return parseCompatibleV2SSE(resp.Body, cb)
}

func parseCompatibleV2SSE(body io.Reader, cb providerStreamCallbacks) (providerTurnResult, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 8192), 2*1024*1024)

	var out providerTurnResult
	var textBuf, reasonBuf strings.Builder
	byIndex := map[int]*providerToolCall{}
	order := []int{}

	ensureTool := func(idx int) *providerToolCall {
		if tc, ok := byIndex[idx]; ok {
			return tc
		}
		tc := &providerToolCall{}
		byIndex[idx] = tc
		order = append(order, idx)
		return tc
	}

	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if raw == "" || raw == "[DONE]" {
			continue
		}
		var frame struct {
			ID      string `json:"id"`
			Choices []struct {
				Delta struct {
					Content          *string `json:"content"`
					ReasoningContent *string `json:"reasoning_content"`
					Reasoning        *string `json:"reasoning"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function *struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens        int64 `json:"prompt_tokens"`
				CompletionTokens    int64 `json:"completion_tokens"`
				PromptTokensDetails *struct {
					CachedTokens int64 `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(raw), &frame) != nil {
			continue
		}
		if frame.Error != nil && frame.Error.Message != "" {
			return providerTurnResult{}, fmt.Errorf("provider stream error: %s", sanitizeDiagValue(frame.Error.Message, 240))
		}
		if frame.ID != "" {
			out.ResponseID = frame.ID
		}
		if frame.Usage != nil {
			out.InputTokens = frame.Usage.PromptTokens
			out.OutputTokens = frame.Usage.CompletionTokens
			if frame.Usage.PromptTokensDetails != nil {
				out.CachedTokens = frame.Usage.PromptTokensDetails.CachedTokens
			}
		}
		for _, ch := range frame.Choices {
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				out.FinishReason = *ch.FinishReason
			}
			for _, piece := range []*string{ch.Delta.ReasoningContent, ch.Delta.Reasoning} {
				if piece != nil && *piece != "" {
					reasonBuf.WriteString(*piece)
					if cb.onThinkingDelta != nil {
						cb.onThinkingDelta(*piece)
					}
				}
			}
			if ch.Delta.Content != nil && *ch.Delta.Content != "" {
				textBuf.WriteString(*ch.Delta.Content)
				if cb.onTextDelta != nil {
					cb.onTextDelta(*ch.Delta.Content)
				}
			}
			for _, tcDelta := range ch.Delta.ToolCalls {
				tc := ensureTool(tcDelta.Index)
				if tcDelta.ID != "" {
					tc.CallID = tcDelta.ID
				}
				if tcDelta.Function != nil {
					if tcDelta.Function.Name != "" {
						tc.Name += tcDelta.Function.Name
					}
					if tcDelta.Function.Arguments != "" {
						tc.ArgsJSON += tcDelta.Function.Arguments
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return providerTurnResult{}, err
	}
	out.Text = textBuf.String()
	out.Reasoning = reasonBuf.String()
	for i, idx := range order {
		tc := byIndex[idx]
		if tc == nil || tc.Name == "" {
			continue
		}
		cid := strings.TrimSpace(tc.CallID)
		if cid == "" {
			cid = fmt.Sprintf("call_%d", i+1)
		}
		args := strings.TrimSpace(tc.ArgsJSON)
		if args == "" {
			args = "{}"
		}
		out.ToolCalls = append(out.ToolCalls, providerToolCall{
			CallID:   cid,
			Name:     tc.Name,
			ArgsJSON: args,
		})
	}
	return out, nil
}

func readProviderHTTPError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var parsed struct {
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
		Message string `json:"message"`
	}
	msg := ""
	if json.Unmarshal(raw, &parsed) == nil {
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		} else if parsed.Message != "" {
			msg = parsed.Message
		}
	}
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	return fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, sanitizeDiagValue(msg, 240))
}

func runFixtureProviderV2(_ context.Context, prompt providerV2Prompt, cb providerStreamCallbacks) (providerTurnResult, error) {
	// Deterministic fixture responder that exercises multi-step Tier-1 read tools
	// and Tier-2 owner-approved actions without external network calls.
	var lastUser string
	hasRecentToolResult := false
	var lastToolName, lastToolStatus, lastToolOutput string
	for i := len(prompt.Messages) - 1; i >= 0; i-- {
		m := prompt.Messages[i]
		if m.Role == "user" {
			lastUser = strings.ToLower(m.Content)
			break
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 && !hasRecentToolResult {
			hasRecentToolResult = true
			lastTC := m.ToolCalls[len(m.ToolCalls)-1]
			lastToolName = lastTC.ToolName
			lastToolStatus = lastTC.Status
			lastToolOutput = toolOutputJSONForWire(lastTC)
		}
	}

	if hasRecentToolResult {
		reply := fmt.Sprintf("Fixture agent reviewed `%s` (%s): %s", lastToolName, lastToolStatus, sanitizeV2Title(lastToolOutput, "completed"))
		for _, chunk := range splitFixtureChunks(reply, 18) {
			if cb.onTextDelta != nil {
				cb.onTextDelta(chunk)
			}
		}
		return providerTurnResult{
			ResponseID:   "fixture-resp-tool-followup",
			Text:         reply,
			InputTokens:  64,
			OutputTokens: 28,
			FinishReason: "completed",
		}, nil
	}

	if len(prompt.Tools) > 0 {
		switch {
		case strings.Contains(lastUser, "swap"):
			return providerTurnResult{
				ResponseID: "fixture-resp-swap",
				ToolCalls: []providerToolCall{{
					CallID:   "fixture_call_swap_1",
					Name:     "draft_swap",
					ArgsJSON: `{"fromToken":"USDC","toToken":"SOL","amount":"10","network":"devnet","maxSlippageBps":50,"rationale":"Fixture swap draft for owner review."}`,
				}},
				InputTokens:  48,
				OutputTokens: 24,
				FinishReason: "tool_calls",
			}, nil
		case strings.Contains(lastUser, "remember") || strings.Contains(lastUser, "save memory"):
			return providerTurnResult{
				ResponseID: "fixture-resp-mem",
				ToolCalls: []providerToolCall{{
					CallID:   "fixture_call_mem_1",
					Name:     "save_memory",
					ArgsJSON: `{"title":"Fixture trading rule","content":"Never risk more than 1% per setup.","tags":"risk,rule","pinned":true}`,
				}},
				InputTokens:  48,
				OutputTokens: 24,
				FinishReason: "tool_calls",
			}, nil
		case strings.Contains(lastUser, "wallet") || strings.Contains(lastUser, "account"):
			return providerTurnResult{
				ResponseID: "fixture-resp-wallets",
				ToolCalls: []providerToolCall{{
					CallID:   "fixture_call_wallets_1",
					Name:     "list_wallets",
					ArgsJSON: `{}`,
				}},
				InputTokens:  42,
				OutputTokens: 16,
				FinishReason: "tool_calls",
			}, nil
		case strings.Contains(lastUser, "status"):
			return providerTurnResult{
				ResponseID: "fixture-resp-status",
				ToolCalls: []providerToolCall{{
					CallID:   "fixture_call_status_1",
					Name:     "workspace_status",
					ArgsJSON: `{}`,
				}},
				InputTokens:  42,
				OutputTokens: 16,
				FinishReason: "tool_calls",
			}, nil
		}
	}

	reply := "Fixture response ready. Ask about wallets, status, or propose a draft swap."
	for _, chunk := range splitFixtureChunks(reply, 18) {
		if cb.onTextDelta != nil {
			cb.onTextDelta(chunk)
		}
	}
	return providerTurnResult{
		ResponseID:   "fixture-resp-text",
		Text:         reply,
		InputTokens:  32,
		OutputTokens: 18,
		FinishReason: "completed",
	}, nil
}

func splitFixtureChunks(s string, size int) []string {
	if size <= 0 || len(s) <= size {
		return []string{s}
	}
	var out []string
	for i := 0; i < len(s); i += size {
		end := i + size
		if end > len(s) {
			end = len(s)
		}
		out = append(out, s[i:end])
	}
	return out
}
