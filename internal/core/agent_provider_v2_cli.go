package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
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
			"--strict-mcp-config",
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

func init() {
	cliAgentAIDispatch = dispatchCLIAgentAI
}

func streamProviderCLIV2(ctx context.Context, s *Store, target resolvedAgentProvider, prompt providerV2Prompt, cb providerStreamCallbacks) (providerTurnResult, error) {
	s.provider.mu.Lock()
	v, err := s.aiConnectionLocked(target.Ref)
	state := s.credentialLocked(target.Ref)
	cred, ok := parseCLIProviderCredential(state.key)
	lockedOut := err != nil || v.Ref != target.Ref || v.Locked || v.Disabled || v.PlanPaused || v.AuthMethod != "cli" || !ok || cred.Provider != target.Provider
	runner := s.cliRunner()
	s.provider.mu.Unlock()
	if lockedOut {
		return providerTurnResult{}, errors.New("CLI subscription account is locked or unavailable")
	}

	return streamCLIWithCred(ctx, runner, target, cred, prompt, cb)
}

func streamCLIWithCred(ctx context.Context, runner providerCLIRunner, target resolvedAgentProvider, cred cliProviderCredential, prompt providerV2Prompt, cb providerStreamCallbacks) (providerTurnResult, error) {
	if cred.Binary == "antigravity" {
		return streamAntigravityACPV2(ctx, runner, target, cred, prompt, cb)
	}

	lookupTarget := cred.Binary
	if cred.BinaryPath != "" {
		if !validCustomCLIBinaryPath(cred.BinaryPath) {
			return providerTurnResult{}, errors.New("invalid CLI binary path")
		}
		lookupTarget = expandCLIUserPath(cred.BinaryPath)
	}
	binPath, err := runner.LookPath(lookupTarget)
	if (err != nil || binPath == "") && cred.BinaryPath != "" {
		if _, isOS := runner.(osCLIRunner); isOS && regularExecutableFile(lookupTarget) {
			binPath, err = lookupTarget, nil
		}
	}
	if err != nil || binPath == "" {
		return providerTurnResult{}, errors.New(cred.Binary + " CLI is not installed on this host")
	}

	sysPrompt, conversation := formatCLIPrompt(prompt)
	if target.Provider != "anthropic" && sysPrompt != "" {
		conversation = "System instructions:\n" + sysPrompt + "\n\n" + conversation
		sysPrompt = ""
	}
	extraArgs, err := parseSafeCLILaunchArgs(cred.LaunchArgs)
	if err != nil {
		return providerTurnResult{}, err
	}
	extraEnv := cliConfigEnvWithShadow(cred.Provider, cred.Binary, cred.ConfigDir, cred.ShadowHome)
	if cred.Binary == "codex" {
		appRes, used, appErr := runCodexAppServerTurn(ctx, runner, codexAppServerTurnRequest{
			BinPath:     binPath,
			Cred:        cred,
			Model:       target.Model,
			Prompt:      conversation,
			ExtraArgs:   extraArgs,
			ExtraEnv:    extraEnv,
			OnTextDelta: cb.onTextDelta,
		})
		if used {
			if appErr != nil {
				return providerTurnResult{}, appErr
			}
			return providerTurnResult{
				Text:         appRes.Text,
				InputTokens:  appRes.InputTokens,
				CachedTokens: appRes.CachedTokens,
				OutputTokens: appRes.OutputTokens,
				FinishReason: "stop",
			}, nil
		}
		if cred.ShadowHome != "" {
			if _, isOS := runner.(osCLIRunner); isOS {
				if _, prepErr := prepareCodexShadowHome(cred.ConfigDir, cred.ShadowHome); prepErr != nil {
					return providerTurnResult{}, prepErr
				}
			}
		}
	}

	args, _, err := cliExecArgs(cred.Provider, binPath, target.Model, sysPrompt)
	if err != nil {
		return providerTurnResult{}, err
	}
	if len(extraArgs) > 0 {
		args = appendSafeCLIExecArgs(cred.Provider, args, extraArgs)
	}

	execCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var stdout, stderr io.ReadCloser
	var waitFn func() error
	var killFn func()
	var startErr error
	if er, ok := runner.(envCLIRunner); ok && len(extraEnv) > 0 {
		stdout, stderr, waitFn, killFn, startErr = er.StartWithEnv(execCtx, binPath, args, conversation, extraEnv)
	} else {
		stdout, stderr, waitFn, killFn, startErr = runner.Start(execCtx, binPath, args, conversation)
	}
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

func decodeCLIReviewOutput(raw, requestID string) (string, []AIActionCard, bool) {
	trimmed := strings.TrimSpace(raw)
	var out struct {
		Answer string `json:"answer"`
		Cards  []struct {
			Kind  string `json:"kind"`
			Title string `json:"title"`
			Text  string `json:"text"`
		} `json:"cards"`
	}
	if uniqueAIJSON([]byte(trimmed)) {
		dec := json.NewDecoder(strings.NewReader(trimmed))
		dec.DisallowUnknownFields()
		if dec.Decode(&out) == nil && dec.Decode(new(any)) == io.EOF && validAgentText(out.Answer, 2000, true) && out.Cards != nil && len(out.Cards) <= 3 {
			cards := make([]AIActionCard, 0, len(out.Cards))
			validCards := true
			for _, c := range out.Cards {
				if (c.Kind != "reminder-draft" && c.Kind != "paper-strategy-draft") || !validAgentText(c.Title, 80, true) || !validAgentText(c.Text, 1000, true) {
					validCards = false
					break
				}
				cards = append(cards, AIActionCard{
					ID:     requestID + ":" + string(rune('1'+len(cards))),
					Kind:   c.Kind,
					Title:  c.Title,
					Text:   c.Text,
					Review: "unreviewed",
				})
			}
			if validCards {
				return out.Answer, cards, true
			}
		}
	}
	runes := []rune(trimmed)
	if len(runes) > 2000 {
		trimmed = strings.TrimSpace(string(runes[:2000]))
	}
	if validAgentText(trimmed, 2000, true) {
		return trimmed, []AIActionCard{}, true
	}
	return "", nil, false
}

func dispatchCLIAgentAI(ctx context.Context, s *Store, q AgentAIRequest, onDelta func(string)) (AgentAIRequest, bool, error) {
	if s == nil || q.ConnectionRef == "" {
		return q, false, nil
	}
	s.provider.mu.Lock()
	v, err := s.aiConnectionLocked(q.ConnectionRef)
	if err != nil || v.Ref != q.ConnectionRef || v.AuthMethod != "cli" {
		s.provider.mu.Unlock()
		return q, false, nil
	}
	state := s.credentialLocked(q.ConnectionRef)
	cred, ok := parseCLIProviderCredential(state.key)
	allowed := ok && !v.Disabled && !v.PlanPaused && cred.Provider == v.Provider && policyProvider(q.Policy) == v.Provider && validateAIModelPolicy(q, s.now().Unix()) == nil
	if allowed {
		if q.TaskID != "" {
			allowed = s.taskAccessMatches(q, state.taskAccess, true, state)
		} else {
			allowed = !v.Locked
		}
	}
	runner := s.cliRunner()
	s.provider.mu.Unlock()
	if !allowed {
		return q, true, errors.New("CLI subscription account is locked, disabled, or unavailable for this task")
	}

	nowUnix := s.now().Unix()
	target := resolvedAgentProvider{
		Ref:        v.Ref,
		Provider:   v.Provider,
		Name:       v.Name,
		AuthMethod: "cli",
		Model:      q.Model,
	}
	prompt := providerV2Prompt{
		Instructions: aiInstructions,
		Messages:     []AgentV2Message{{Role: "user", Content: q.Context}},
	}
	var streamed strings.Builder
	turn, callErr := streamCLIWithCred(ctx, runner, target, cred, prompt, providerStreamCallbacks{
		onTextDelta: func(delta string) {
			streamed.WriteString(delta)
			if onDelta != nil {
				onDelta(streamed.String())
			}
		},
	})
	q.Admission = &OpenAIAdmission{
		State:               "admitted",
		ModelRecordSHA256:   q.Policy.ID,
		AccessAt:            nowUnix,
		CountedAt:           nowUnix,
		InputTokens:         turn.InputTokens,
		GenerationAttempted: true,
	}
	if callErr != nil {
		return q, true, callErr
	}
	answer, cards, ok := decodeCLIReviewOutput(turn.Text, q.ID)
	q.InputTokens = turn.InputTokens
	q.OutputTokens = turn.OutputTokens
	q.CachedInputTokens = turn.CachedTokens
	q.CostBasis = cliUsageCostBasis
	q.HeldMicrousd = 0
	q.SpentMicrousd = 0
	if !ok {
		q.State = "failed"
		q.Failure = "CLI response failed review validation. No action was executed."
		return q, true, nil
	}
	q.State = "completed"
	q.Answer = answer
	q.Cards = cards
	q.Failure = ""
	return q, true, nil
}

func appendSafeCLIExecArgs(provider string, base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	if provider == "openai" && len(base) > 0 && base[len(base)-1] == "-" {
		out := make([]string, 0, len(base)+len(extra))
		out = append(out, base[:len(base)-1]...)
		out = append(out, extra...)
		out = append(out, "-")
		return out
	}
	out := make([]string, 0, len(base)+len(extra))
	out = append(out, base...)
	out = append(out, extra...)
	return out
}

func streamAntigravityACPV2(ctx context.Context, runner providerCLIRunner, target resolvedAgentProvider, cred cliProviderCredential, prompt providerV2Prompt, cb providerStreamCallbacks) (providerTurnResult, error) {
	interactive, ok := runner.(interactiveCLIRunner)
	if !ok {
		return providerTurnResult{}, errors.New("Antigravity ACP requires interactive stdio support")
	}
	binPath, err := discoverAntigravityACP(runner, cred.BinaryPath)
	if err != nil {
		return providerTurnResult{}, err
	}
	extraArgs, err := parseSafeCLILaunchArgs(cred.LaunchArgs)
	if err != nil {
		return providerTurnResult{}, err
	}

	cwd, err := os.MkdirTemp("", "fased-acp-turn-*")
	if err != nil {
		return providerTurnResult{}, err
	}
	defer os.RemoveAll(cwd)

	execCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	acpArgs := append([]string{"acp", "--stdio", "--disable-telemetry"}, extraArgs...)
	stdin, stdout, stderr, waitFn, killFn, err := interactive.StartInteractive(
		execCtx,
		binPath,
		acpArgs,
		antigravityACPEnv(cred.ConfigDir),
	)
	if err != nil {
		return providerTurnResult{}, errors.New("failed to launch Antigravity ACP")
	}

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

	conn := newACPConn(stdin, stdout)
	doneWatch := make(chan struct{})
	watchDone := make(chan struct{})
	var cancelOnce sync.Once
	var sessionIDMu sync.Mutex
	var activeSessionID string
	sendCancelAndKill := func() {
		cancelOnce.Do(func() {
			sessionIDMu.Lock()
			sid := activeSessionID
			sessionIDMu.Unlock()
			if sid != "" {
				sent := make(chan struct{})
				go func() {
					_ = conn.sendJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/cancel",
						"params":  map[string]any{"sessionId": sid},
					})
					close(sent)
				}()
				select {
				case <-sent:
				case <-time.After(150 * time.Millisecond):
				}
			}
			if killFn != nil {
				killFn()
			}
		})
	}
	go func() {
		defer close(watchDone)
		select {
		case <-execCtx.Done():
			sendCancelAndKill()
		case <-doneWatch:
			if execCtx.Err() != nil {
				sendCancelAndKill()
			}
		}
	}()
	defer func() {
		close(doneWatch)
		<-watchDone
		if stdin != nil {
			_ = stdin.Close()
		}
		if killFn != nil {
			killFn()
		}
		if waitFn != nil {
			_ = waitFn()
		}
	}()

	initResp, err := conn.call(execCtx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
		"clientInfo": map[string]any{"name": "fased", "version": Version},
	})
	if err != nil {
		if ctx.Err() != nil {
			return providerTurnResult{}, ctx.Err()
		}
		return providerTurnResult{}, errors.New("Antigravity ACP initialize failed")
	}
	if initResp.Error != nil {
		if isAntigravityAuthError(initResp.Error, "") {
			return providerTurnResult{}, errors.New("Antigravity ACP requires Google sign-in; run 'antigravity acp --login' and reconnect")
		}
		return providerTurnResult{}, errors.New("Antigravity ACP initialize error: " + sanitizeDiagValue(initResp.Error.Message, 160))
	}

	sessResp, err := conn.call(execCtx, "session/new", map[string]any{
		"cwd":        cwd,
		"mcpServers": []any{},
	})
	if err != nil {
		if ctx.Err() != nil {
			return providerTurnResult{}, ctx.Err()
		}
		return providerTurnResult{}, errors.New("Antigravity ACP session creation failed")
	}
	if sessResp.Error != nil {
		if isAntigravityAuthError(sessResp.Error, "") {
			return providerTurnResult{}, errors.New("Antigravity ACP requires Google sign-in; run 'antigravity acp --login' and reconnect")
		}
		return providerTurnResult{}, errors.New("Antigravity ACP session error: " + sanitizeDiagValue(sessResp.Error.Message, 160))
	}
	var sessRes acpSessionNewResult
	if json.Unmarshal(sessResp.Result, &sessRes) != nil || sessRes.SessionID == "" {
		return providerTurnResult{}, errors.New("Antigravity ACP returned an invalid session")
	}

	sessionIDMu.Lock()
	activeSessionID = sessRes.SessionID
	sessionIDMu.Unlock()

	_, configModelSet := antigravityModelsFromSession(sessRes)
	if modelOpt := resolveAntigravityModelOption(target.Model, configModelSet); modelOpt != "" {
		if len(configModelSet) == 0 || configModelSet[modelOpt] {
			_, _ = conn.call(execCtx, "session/set_config_option", map[string]any{
				"sessionId": sessRes.SessionID,
				"configId":  "model",
				"value":     modelOpt,
			})
		} else {
			_, _ = conn.call(execCtx, "session/set_model", map[string]any{
				"sessionId": sessRes.SessionID,
				"modelId":   target.Model,
			})
		}
	}

	sysPrompt, conversation := formatCLIPrompt(prompt)
	combinedPrompt := conversation
	if sysPrompt != "" {
		combinedPrompt = "System instructions:\n" + sysPrompt + "\n\n" + conversation
	}

	conn.nextID++
	promptID := conn.nextID
	wantPromptID := fmt.Sprintf("%d", promptID)
	if err := conn.sendJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      promptID,
		"method":  "session/prompt",
		"params": map[string]any{
			"sessionId": sessRes.SessionID,
			"prompt": []map[string]any{
				{"type": "text", "text": combinedPrompt},
			},
		},
	}); err != nil {
		return providerTurnResult{}, errors.New("failed to send prompt to Antigravity ACP")
	}

	var fullText strings.Builder
	var turnResult providerTurnResult
	for {
		if execCtx.Err() != nil {
			if ctx.Err() != nil {
				return providerTurnResult{}, ctx.Err()
			}
			return providerTurnResult{}, execCtx.Err()
		}
		env, readErr := conn.readEnvelope()
		if readErr != nil {
			if ctx.Err() != nil {
				return providerTurnResult{}, ctx.Err()
			}
			break
		}
		if env.Method != "" && len(env.ID) > 0 {
			_ = conn.handleServerRequest(env)
			continue
		}
		if env.Method == "session/update" {
			var notif struct {
				SessionID string `json:"sessionId"`
				Update    struct {
					SessionUpdate string `json:"sessionUpdate"`
					Used          int64  `json:"used"`
					Content       *struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content,omitempty"`
				} `json:"update"`
			}
			if json.Unmarshal(env.Params, &notif) == nil && (notif.SessionID == "" || notif.SessionID == sessRes.SessionID) {
				if notif.Update.SessionUpdate == "agent_message_chunk" && notif.Update.Content != nil && notif.Update.Content.Type == "text" && notif.Update.Content.Text != "" {
					if fullText.Len() < 1024*1024 {
						fullText.WriteString(notif.Update.Content.Text)
					}
					if cb.onTextDelta != nil {
						cb.onTextDelta(notif.Update.Content.Text)
					}
				}
				if notif.Update.SessionUpdate == "usage_update" && notif.Update.Used > turnResult.InputTokens {
					turnResult.InputTokens = notif.Update.Used
				}
			}
			continue
		}
		if len(env.ID) > 0 && strings.TrimSpace(string(env.ID)) == wantPromptID {
			if env.Error != nil {
				stderrMsg := ""
				select {
				case stderrMsg = <-errCh:
				default:
				}
				if isAntigravityAuthError(env.Error, stderrMsg) {
					return providerTurnResult{}, errors.New("Antigravity ACP requires Google sign-in; run 'antigravity acp --login' and reconnect")
				}
				return providerTurnResult{}, errors.New("Antigravity ACP error: " + sanitizeDiagValue(env.Error.Message, 160))
			}
			var res acpPromptResult
			if json.Unmarshal(env.Result, &res) == nil && res.Usage != nil {
				if res.Usage.InputTokens > 0 {
					turnResult.InputTokens = res.Usage.InputTokens
				}
				if res.Usage.OutputTokens > 0 {
					turnResult.OutputTokens = res.Usage.OutputTokens
				}
				if res.Usage.CachedReadTokens > 0 {
					turnResult.CachedTokens = res.Usage.CachedReadTokens
				}
			}
			break
		}
	}

	turnResult.Text = strings.TrimSpace(fullText.String())
	if turnResult.Text == "" {
		stderrMsg := ""
		select {
		case stderrMsg = <-errCh:
		case <-time.After(100 * time.Millisecond):
		}
		if stderrMsg != "" {
			return providerTurnResult{}, errors.New("Antigravity ACP failed: " + stderrMsg)
		}
		return providerTurnResult{}, errors.New("Antigravity ACP returned an empty reply")
	}
	turnResult.FinishReason = "stop"
	return turnResult, nil
}
