package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

func formatCLIPrompt(prompt providerV2Prompt) (systemPrompt string, conversation string) {
	sys := strings.TrimSpace(prompt.Instructions)
	var b strings.Builder
	if len(sys) > 6000 {
		b.WriteString("System instructions:\n")
		b.WriteString(sys)
		b.WriteString("\n\n")
		sys = ""
	}
	for _, m := range prompt.Messages {
		switch m.Role {
		case "user":
			if strings.TrimSpace(m.Content) != "" {
				b.WriteString("User:\n")
				b.WriteString(strings.TrimSpace(m.Content))
				b.WriteString("\n\n")
			}
		case "assistant":
			if strings.TrimSpace(m.Content) != "" {
				b.WriteString("Assistant:\n")
				b.WriteString(strings.TrimSpace(m.Content))
				b.WriteString("\n\n")
			}
		}
	}
	return sys, strings.TrimSpace(b.String())
}

func cliExecArgs(provider, binary, model, sysPrompt string) ([]string, string, error) {
	switch provider {
	case "anthropic":
		args := []string{
			"-p",
			"--output-format", "stream-json",
			"--verbose",
			"--include-partial-messages",
			"--no-session-persistence",
			"--tools", "",
		}
		if model != "" {
			args = append(args, "--model", model)
		}
		if sysPrompt != "" {
			args = append(args, "--system-prompt", sysPrompt)
		}
		return args, binary, nil

	case "openai":
		args := []string{
			"exec",
			"--json",
			"--ephemeral",
			"--skip-git-repo-check",
			"--sandbox", "read-only",
		}
		if model != "" {
			args = append(args, "-m", model)
		}
		args = append(args, "-")
		return args, binary, nil

	case "google":
		args := []string{"-o", "stream-json"}
		if model != "" {
			args = append(args, "-m", model)
		}
		return args, binary, nil

	case "xai":
		args := []string{"-p", "-"}
		if model != "" {
			args = append(args, "--model", model)
		}
		return args, binary, nil

	default:
		return nil, "", errors.New("unsupported CLI provider")
	}
}

func parseClaudeStreamLine(line string, fullText *strings.Builder, result *providerTurnResult, cb providerStreamCallbacks) (errMsg string) {
	var ev struct {
		Type    string `json:"type"`
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
		Event   struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		} `json:"event"`
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
		Usage *struct {
			Input     int64 `json:"input_tokens"`
			Output    int64 `json:"output_tokens"`
			CacheRead int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal([]byte(line), &ev) != nil {
		return ""
	}
	switch ev.Type {
	case "stream_event":
		if ev.Event.Type == "content_block_delta" && ev.Event.Delta.Type == "text_delta" && ev.Event.Delta.Text != "" {
			fullText.WriteString(ev.Event.Delta.Text)
			if cb.onTextDelta != nil {
				cb.onTextDelta(ev.Event.Delta.Text)
			}
		}
	case "assistant":
		if fullText.Len() == 0 {
			for _, part := range ev.Message.Content {
				if part.Type == "text" && part.Text != "" {
					fullText.WriteString(part.Text)
					if cb.onTextDelta != nil {
						cb.onTextDelta(part.Text)
					}
				}
			}
		}
	case "result":
		if ev.Usage != nil {
			result.InputTokens = ev.Usage.Input
			result.OutputTokens = ev.Usage.Output
			result.CachedTokens = ev.Usage.CacheRead
		}
		if ev.IsError && ev.Result != "" {
			return sanitizeDiagValue(ev.Result, 200)
		}
		if fullText.Len() == 0 && ev.Result != "" {
			fullText.WriteString(ev.Result)
			if cb.onTextDelta != nil {
				cb.onTextDelta(ev.Result)
			}
		}
	}
	return ""
}

func parseCodexStreamLine(line string, fullText *strings.Builder, itemSeen map[string]string, result *providerTurnResult, cb providerStreamCallbacks) (errMsg string) {
	var ev struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error"`
		Item *struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"item"`
		Usage *struct {
			Input  int64 `json:"input_tokens"`
			Cached int64 `json:"cached_input_tokens"`
			Output int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal([]byte(line), &ev) != nil {
		return ""
	}
	switch ev.Type {
	case "item.updated", "item.completed":
		if ev.Item != nil && (ev.Item.Type == "agent_message" || ev.Item.Type == "message") && ev.Item.Text != "" {
			key := ev.Item.ID
			if key == "" {
				key = "_default"
			}
			prev := itemSeen[key]
			if strings.HasPrefix(ev.Item.Text, prev) {
				delta := ev.Item.Text[len(prev):]
				if delta != "" {
					itemSeen[key] = ev.Item.Text
					fullText.WriteString(delta)
					if cb.onTextDelta != nil {
						cb.onTextDelta(delta)
					}
				}
			} else if ev.Item.Text != prev {
				itemSeen[key] = ev.Item.Text
				fullText.WriteString(ev.Item.Text)
				if cb.onTextDelta != nil {
					cb.onTextDelta(ev.Item.Text)
				}
			}
		}
	case "turn.completed":
		if ev.Usage != nil {
			result.InputTokens = ev.Usage.Input
			result.CachedTokens = ev.Usage.Cached
			result.OutputTokens = ev.Usage.Output
		}
	case "error", "turn.failed":
		if ev.Error != nil && ev.Error.Message != "" {
			return sanitizeDiagValue(ev.Error.Message, 200)
		}
		if ev.Message != "" {
			return sanitizeDiagValue(ev.Message, 200)
		}
	}
	return ""
}

func parseGeminiStreamLine(line string, fullText *strings.Builder, result *providerTurnResult, cb providerStreamCallbacks) (errMsg string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}
	if !strings.HasPrefix(trimmed, "{") {
		if fullText.Len() > 0 {
			fullText.WriteByte('\n')
			if cb.onTextDelta != nil {
				cb.onTextDelta("\n")
			}
		}
		fullText.WriteString(line)
		if cb.onTextDelta != nil {
			cb.onTextDelta(line)
		}
		return ""
	}
	var ev struct {
		Type     string `json:"type"`
		Role     string `json:"role"`
		Content  string `json:"content"`
		Response string `json:"response"`
		Error    *struct {
			Message string `json:"message"`
		} `json:"error"`
		Stats *struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
			Cached       int64 `json:"cached"`
		} `json:"stats"`
	}
	if json.Unmarshal([]byte(trimmed), &ev) != nil {
		return ""
	}
	if ev.Error != nil && ev.Error.Message != "" {
		return sanitizeDiagValue(ev.Error.Message, 200)
	}
	if (ev.Type == "message" || ev.Role == "assistant" || ev.Role == "model") && ev.Content != "" {
		fullText.WriteString(ev.Content)
		if cb.onTextDelta != nil {
			cb.onTextDelta(ev.Content)
		}
	}
	if ev.Type == "result" {
		if ev.Stats != nil {
			result.InputTokens = ev.Stats.InputTokens
			result.OutputTokens = ev.Stats.OutputTokens
			result.CachedTokens = ev.Stats.Cached
		}
		if fullText.Len() == 0 && ev.Response != "" {
			fullText.WriteString(ev.Response)
			if cb.onTextDelta != nil {
				cb.onTextDelta(ev.Response)
			}
		}
	}
	return ""
}

func streamProviderCLIV2(ctx context.Context, s *Store, target resolvedAgentProvider, prompt providerV2Prompt, cb providerStreamCallbacks) (providerTurnResult, error) {
	s.provider.mu.Lock()
	v, err := s.aiConnectionLocked(target.Ref)
	state := s.credentialLocked(target.Ref)
	cred, ok := parseCLIProviderCredential(state.key)
	lockedOut := err != nil || v.Ref != target.Ref || v.Locked || v.PlanPaused || v.AuthMethod != "cli" || !ok || cred.Provider != target.Provider
	runner := s.cliRunner()
	s.provider.mu.Unlock()
	if lockedOut {
		return providerTurnResult{}, errors.New("CLI subscription account is locked or unavailable")
	}

	binPath, err := runner.LookPath(cred.Binary)
	if err != nil || binPath == "" {
		return providerTurnResult{}, errors.New(cred.Binary + " CLI is not installed on this host")
	}

	sysPrompt, conversation := formatCLIPrompt(prompt)
	if target.Provider != "anthropic" && sysPrompt != "" {
		conversation = "System instructions:\n" + sysPrompt + "\n\n" + conversation
		sysPrompt = ""
	}
	args, _, err := cliExecArgs(cred.Provider, binPath, target.Model, sysPrompt)
	if err != nil {
		return providerTurnResult{}, err
	}

	execCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	stdout, stderr, waitFn, killFn, startErr := runner.Start(execCtx, binPath, args, conversation)
	if startErr != nil {
		return providerTurnResult{}, errors.New("failed to launch " + cred.Binary + " CLI")
	}
	doneWatch := make(chan struct{})
	go func() {
		select {
		case <-execCtx.Done():
			if killFn != nil {
				killFn()
			}
		case <-doneWatch:
		}
	}()
	defer close(doneWatch)

	errCh := make(chan string, 1)
	go func() {
		if stderr == nil {
			errCh <- ""
			return
		}
		defer stderr.Close()
		raw, _ := io.ReadAll(io.LimitReader(stderr, 8192))
		errCh <- sanitizeDiagValue(stripANSI(string(raw)), 240)
	}()

	var fullText strings.Builder
	var turnResult providerTurnResult
	var streamErrMsg string
	codexSeen := map[string]string{}

	if stdout != nil {
		defer stdout.Close()
		scanner := bufio.NewScanner(io.LimitReader(stdout, 2*1024*1024))
		scanner.Buffer(make([]byte, 8192), 256*1024)
		for scanner.Scan() {
			line := scanner.Text()
			switch cred.Provider {
			case "anthropic":
				if msg := parseClaudeStreamLine(line, &fullText, &turnResult, cb); msg != "" {
					streamErrMsg = msg
				}
			case "openai":
				if msg := parseCodexStreamLine(line, &fullText, codexSeen, &turnResult, cb); msg != "" {
					streamErrMsg = msg
				}
			case "google":
				if msg := parseGeminiStreamLine(line, &fullText, &turnResult, cb); msg != "" {
					streamErrMsg = msg
				}
			default:
				if fullText.Len() > 0 {
					fullText.WriteByte('\n')
					if cb.onTextDelta != nil {
						cb.onTextDelta("\n")
					}
				}
				fullText.WriteString(line)
				if cb.onTextDelta != nil {
					cb.onTextDelta(line)
				}
			}
			if fullText.Len() > 1024*1024 {
				break
			}
		}
	}

	stderrMsg := <-errCh
	waitErr := error(nil)
	if waitFn != nil {
		waitErr = waitFn()
	}
	if ctx.Err() != nil {
		return providerTurnResult{}, ctx.Err()
	}
	if streamErrMsg != "" && fullText.Len() == 0 {
		return providerTurnResult{}, errors.New(streamErrMsg)
	}
	if waitErr != nil {
		if stderrMsg != "" {
			return providerTurnResult{}, errors.New(cred.Binary + " failed: " + stderrMsg)
		}
		return providerTurnResult{}, errors.New(cred.Binary + " execution failed")
	}
	turnResult.Text = strings.TrimSpace(fullText.String())
	if turnResult.Text == "" {
		return providerTurnResult{}, errors.New(cred.Binary + " returned an empty reply")
	}
	turnResult.FinishReason = "stop"
	return turnResult, nil
}
