package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeACPServerFunc func(ctx context.Context, stdin io.Reader, stdout io.Writer, stderr io.Writer, args []string, env []string)

type fakeACPRunner struct {
	mu           sync.Mutex
	paths        map[string]string
	files        map[string]bool
	runHandler   func(binary string, args []string, stdin string, env []string) ([]byte, []byte, int, error)
	startEnvList [][]string
	runEnvList   [][]string
	acpServer    fakeACPServerFunc
}

func (f *fakeACPRunner) LookPath(binary string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.paths[binary]; ok && p != "" {
		return p, nil
	}
	return "", errors.New("not found")
}

func (f *fakeACPRunner) Run(ctx context.Context, binary string, args []string, stdin string) ([]byte, []byte, int, error) {
	return f.RunWithEnv(ctx, binary, args, stdin, nil)
}

func (f *fakeACPRunner) RunWithEnv(ctx context.Context, binary string, args []string, stdin string, extraEnv []string) ([]byte, []byte, int, error) {
	f.mu.Lock()
	if len(extraEnv) > 0 {
		f.runEnvList = append(f.runEnvList, append([]string(nil), extraEnv...))
	}
	handler := f.runHandler
	f.mu.Unlock()
	if handler != nil {
		return handler(binary, args, stdin, extraEnv)
	}
	return nil, nil, 1, errors.New("unexpected run")
}

func (f *fakeACPRunner) Start(ctx context.Context, binary string, args []string, stdin string) (io.ReadCloser, io.ReadCloser, func() error, func(), error) {
	return f.StartWithEnv(ctx, binary, args, stdin, nil)
}

func (f *fakeACPRunner) StartWithEnv(ctx context.Context, binary string, args []string, stdin string, extraEnv []string) (io.ReadCloser, io.ReadCloser, func() error, func(), error) {
	f.mu.Lock()
	if len(extraEnv) > 0 {
		f.startEnvList = append(f.startEnvList, append([]string(nil), extraEnv...))
	}
	f.mu.Unlock()
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	go func() {
		defer outW.Close()
		defer errW.Close()
		_, _ = io.WriteString(outW, `{"type":"result","subtype":"success","result":"ok","usage":{"input_tokens":10,"output_tokens":2}}`+"\n")
	}()
	return outR, errR, func() error { return nil }, func() {}, nil
}

func (f *fakeACPRunner) FileExistsNonEmpty(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.files[path]
}

func (f *fakeACPRunner) StartInteractive(ctx context.Context, binary string, args []string, env []string) (io.WriteCloser, io.ReadCloser, io.ReadCloser, func() error, func(), error) {
	f.mu.Lock()
	srv := f.acpServer
	if len(env) > 0 {
		f.startEnvList = append(f.startEnvList, append([]string(nil), env...))
	}
	f.mu.Unlock()
	if srv == nil {
		return nil, nil, nil, nil, nil, errors.New("no interactive ACP server")
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()

	done := make(chan struct{})
	srvCtx, cancelSrv := context.WithCancel(ctx)
	var closeOnce sync.Once
	kill := func() {
		closeOnce.Do(func() {
			cancelSrv()
			_ = inW.Close()
			_ = inR.Close()
			_ = outW.Close()
			_ = errW.Close()
		})
	}
	go func() {
		defer close(done)
		defer kill()
		srv(srvCtx, inR, outW, errW, args, env)
	}()
	wait := func() error {
		<-done
		return nil
	}
	return inW, outR, errR, wait, kill, nil
}

func writeFakeExecFile(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho ok\n"), 0755); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverAntigravityACP(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("FASED_ANTIGRAVITY_ACP_BIN", "")

	// Reject path traversal in custom overrides.
	if _, err := discoverAntigravityACP(osCLIRunner{}, "/tmp/../evil/antigravity"); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
	if validCLIConfigDir("../escape") || validCustomCLIBinaryPath("foo/../../bar") {
		t.Fatal("expected relative .. segment rejection")
	}

	// Reject Electron GUI wrapper containing resources.pak / LICENSE.electron.txt.
	guiDir := filepath.Join(t.TempDir(), "antigravity-gui")
	guiBin := filepath.Join(guiDir, antigravityBinaryName())
	writeFakeExecFile(t, guiBin)
	if err := os.WriteFile(filepath.Join(guiDir, "resources.pak"), []byte("pak"), 0644); err != nil {
		t.Fatal(err)
	}
	if !isElectronGUIAntigravity(guiBin) {
		t.Fatal("expected Electron GUI bundle detection")
	}
	if _, err := discoverAntigravityACP(osCLIRunner{}, guiBin); err == nil {
		t.Fatal("expected Electron GUI binary to be rejected as ACP binary")
	}

	// Prefer Fased-managed path over T3 path, and env override above both.
	arch := antigravityPlatformArch()
	binName := antigravityBinaryName()
	t3Bin := filepath.Join(tempHome, ".t3", "tools", "antigravity-acp", arch, "versions", "1.5.13", binName)
	writeFakeExecFile(t, t3Bin)
	got, err := discoverAntigravityACP(osCLIRunner{}, "")
	if err != nil || got != t3Bin {
		t.Fatalf("expected T3 ACP path %q, got %q err=%v", t3Bin, got, err)
	}

	fasedBin := filepath.Join(tempHome, ".fased", "tools", "antigravity-acp", arch, binName)
	writeFakeExecFile(t, fasedBin)
	got, err = discoverAntigravityACP(osCLIRunner{}, "")
	if err != nil || got != fasedBin {
		t.Fatalf("expected Fased ACP path %q, got %q err=%v", fasedBin, got, err)
	}

	envBin := filepath.Join(t.TempDir(), "custom", binName)
	writeFakeExecFile(t, envBin)
	t.Setenv("FASED_ANTIGRAVITY_ACP_BIN", envBin)
	got, err = discoverAntigravityACP(osCLIRunner{}, "")
	if err != nil || got != envBin {
		t.Fatalf("expected env ACP path %q, got %q err=%v", envBin, got, err)
	}
}

func TestAntigravityACPProbeStreamPermissionCancelAndAuth(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	authFailMode := false
	hangPromptMode := false
	hangPromptEntered := make(chan struct{}, 1)
	cancelObserved := make(chan string, 1)
	var permissionReply map[string]any
	var setModelReceived string
	var unknownReplyErrorCode int

	runner := &fakeACPRunner{
		paths: map[string]string{
			"antigravity": "/mock/bin/antigravity",
		},
		acpServer: func(ctx context.Context, stdin io.Reader, stdout io.Writer, stderr io.Writer, args []string, env []string) {
			reader := bufio.NewReader(stdin)
			writeMsg := func(v any) {
				b, _ := json.Marshal(v)
				_, _ = stdout.Write(append(b, '\n'))
			}
			for {
				line, err := reader.ReadBytes('\n')
				if len(line) > 0 {
					var req struct {
						ID     json.RawMessage `json:"id"`
						Method string          `json:"method"`
						Params json.RawMessage `json:"params"`
					}
					if json.Unmarshal(line, &req) == nil {
						switch req.Method {
						case "initialize":
							writeMsg(map[string]any{
								"jsonrpc": "2.0",
								"id":      json.RawMessage(req.ID),
								"result": map[string]any{
									"protocolVersion": 1,
									"agentInfo":       map[string]any{"name": "Antigravity ACP Server", "version": "1.5.13"},
								},
							})
						case "session/new":
							writeMsg(map[string]any{
								"jsonrpc": "2.0",
								"id":      json.RawMessage(req.ID),
								"result": map[string]any{
									"sessionId": "sess-ag-1",
									"configOptions": []any{
										map[string]any{
											"id":       "model",
											"category": "model",
											"options": []any{
												map[string]any{"value": "gemini-4-neon", "name": "Gemini 4 Neon"},
												map[string]any{"value": "gemini-3.8-flash", "name": "Gemini 3.8 Flash"},
												map[string]any{"value": "claude-opus-5-5", "name": "Claude Opus 5.5"},
											},
										},
									},
									"_meta": map[string]any{
										"antigravity": map[string]any{
											"tier": map[string]any{"id": "g1-ultra-tier", "name": "Google AI Ultra"},
										},
									},
								},
							})
						case "session/set_config_option":
							var p struct {
								ConfigID string `json:"configId"`
								Value    string `json:"value"`
							}
							_ = json.Unmarshal(req.Params, &p)
							mu.Lock()
							setModelReceived = p.Value
							mu.Unlock()
							writeMsg(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(req.ID), "result": map[string]any{}})
						case "session/cancel":
							select {
							case cancelObserved <- "cancelled":
							default:
							}
							return
						case "session/prompt":
							mu.Lock()
							failAuth := authFailMode
							hang := hangPromptMode
							mu.Unlock()
							if failAuth {
								writeMsg(map[string]any{
									"jsonrpc": "2.0",
									"id":      json.RawMessage(req.ID),
									"error":   map[string]any{"code": -32000, "message": "Authentication required: No browser available"},
								})
								continue
							}
							if hang {
								select {
								case hangPromptEntered <- struct{}{}:
								default:
								}
								continue
							}
							// Test permission request deny-by-default and unknown request refusal.
							writeMsg(map[string]any{
								"jsonrpc": "2.0",
								"id":      "perm-1",
								"method":  "session/request_permission",
								"params": map[string]any{
									"options": []any{
										map[string]any{"optionId": "allow_once", "kind": "allow_once"},
										map[string]any{"optionId": "reject_once", "kind": "reject_once"},
									},
								},
							})
							permLine, _ := reader.ReadBytes('\n')
							mu.Lock()
							_ = json.Unmarshal(permLine, &permissionReply)
							mu.Unlock()

							writeMsg(map[string]any{
								"jsonrpc": "2.0",
								"id":      "fs-1",
								"method":  "fs/read_text_file",
								"params":  map[string]any{"path": "/etc/passwd"},
							})
							fsLine, _ := reader.ReadBytes('\n')
							var fsResp struct {
								Error *acpRPCError `json:"error"`
							}
							_ = json.Unmarshal(fsLine, &fsResp)
							mu.Lock()
							if fsResp.Error != nil {
								unknownReplyErrorCode = fsResp.Error.Code
							}
							mu.Unlock()

							writeMsg(map[string]any{
								"jsonrpc": "2.0",
								"method":  "session/update",
								"params": map[string]any{
									"sessionId": "sess-ag-1",
									"update": map[string]any{
										"sessionUpdate": "agent_message_chunk",
										"content":       map[string]any{"type": "text", "text": "ACP "},
									},
								},
							})
							writeMsg(map[string]any{
								"jsonrpc": "2.0",
								"method":  "session/update",
								"params": map[string]any{
									"sessionId": "sess-ag-1",
									"update": map[string]any{
										"sessionUpdate": "agent_message_chunk",
										"content":       map[string]any{"type": "text", "text": "Verified"},
									},
								},
							})
							writeMsg(map[string]any{
								"jsonrpc": "2.0",
								"id":      json.RawMessage(req.ID),
								"result": map[string]any{
									"stopReason": "end_turn",
									"usage": map[string]any{
										"inputTokens":      1200,
										"outputTokens":     34,
										"cachedReadTokens": 200,
									},
								},
							})
						}
					}
				}
				if err != nil {
					return
				}
			}
		},
	}

	s := fixtureStore(t)
	s.provider.mu.Lock()
	s.provider.cliRunner = runner
	s.provider.mu.Unlock()

	view, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "cli-probe"})
	if err != nil {
		t.Fatalf("cli-probe failed: %v", err)
	}
	var ag *HostCLIStatus
	for i := range view.HostCLIs {
		if view.HostCLIs[i].Binary == "antigravity" {
			ag = &view.HostCLIs[i]
			break
		}
	}
	if ag == nil || !ag.Installed || !ag.Authenticated || ag.Version != "1.5.13" || ag.Account != "Google AI Ultra" || ag.Transport != "acp" {
		t.Fatalf("unexpected Antigravity ACP probe status: %+v", ag)
	}
	if !slices.Contains(ag.Models, "gemini-4-neon") || !slices.Contains(ag.Models, "claude-opus-5-5") {
		t.Fatalf("expected Antigravity ACP models in probe, got %v", ag.Models)
	}

	conn, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:     "cli-connect",
		Provider:   "google",
		Binary:     "antigravity",
		Name:       "Google Ultra ACP",
		Passphrase: testPassword,
	})
	if err != nil {
		t.Fatalf("cli-connect antigravity failed: %v", err)
	}
	if conn.CLIVersion != "1.5.13" || conn.Binary != "antigravity" {
		t.Fatalf("expected CLIVersion=1.5.13 and Binary=antigravity, got %+v", conn)
	}

	thread, err := s.CreateThread(t.Context(), "wen-operator", "Antigravity Thread", conn.Ref, "gemini-3.8-flash-high")
	if err != nil {
		t.Fatalf("CreateThread failed: %v", err)
	}
	var deltas []string
	res, err := s.RunAgentV2Turn(t.Context(), thread.ID, AgentV2PromptInput{Content: "Ping ACP"}, func(ev AgentStreamEvent) {
		if ev.Type == "text_delta" && ev.Delta != "" {
			deltas = append(deltas, ev.Delta)
		}
	})
	if err != nil {
		t.Fatalf("RunAgentV2Turn failed: %v", err)
	}
	if res.LastMessage == nil || res.LastMessage.Content != "ACP Verified" || strings.Join(deltas, "") != "ACP Verified" {
		t.Fatalf("unexpected streamed reply: msg=%+v deltas=%v", res.LastMessage, deltas)
	}
	if res.LastMessage.InputTokens != 1200 || res.LastMessage.OutputTokens != 34 || res.LastMessage.CachedTokens != 200 {
		t.Fatalf("unexpected ACP usage: %+v", res.LastMessage)
	}
	mu.Lock()
	gotSetModel := setModelReceived
	gotPerm := permissionReply
	gotUnknownCode := unknownReplyErrorCode
	mu.Unlock()
	if gotSetModel != "gemini-3.8-flash" {
		t.Fatalf("expected stripped config model gemini-3.8-flash, got %q", gotSetModel)
	}
	if gotUnknownCode != -32601 {
		t.Fatalf("expected fs/read_text_file refusal -32601, got %d", gotUnknownCode)
	}
	outcome, _ := gotPerm["result"].(map[string]any)["outcome"].(map[string]any)
	if outcome["optionId"] != "reject_once" {
		t.Fatalf("expected reject_once permission denial, got %+v", gotPerm)
	}

	// Auth error (-32000) maps cleanly to actionable sign-in error.
	mu.Lock()
	authFailMode = true
	mu.Unlock()
	if _, err := s.RunAgentV2Turn(t.Context(), thread.ID, AgentV2PromptInput{Content: "Auth fail"}, nil); err == nil || !strings.Contains(err.Error(), "antigravity acp --login") {
		t.Fatalf("expected actionable Antigravity sign-in error, got %v", err)
	}

	// Cancellation sends session/cancel and terminates cleanly.
	mu.Lock()
	authFailMode = false
	hangPromptMode = true
	mu.Unlock()
	cancelCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		select {
		case <-hangPromptEntered:
			cancel()
		case <-time.After(4 * time.Second):
			cancel()
		}
	}()
	if _, err := s.RunAgentV2Turn(cancelCtx, thread.ID, AgentV2PromptInput{Content: "Cancel me"}, nil); !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation error, got %v", err)
	}
	select {
	case <-cancelObserved:
	case <-time.After(2 * time.Second):
		t.Fatal("expected session/cancel notification on ACP cancel")
	}
}

func TestProviderCLIEnabledToggleConfigDirAndAccentColor(t *testing.T) {
	t.Parallel()

	runner := &fakeACPRunner{
		paths: map[string]string{
			"claude":       "/usr/local/bin/claude",
			"/opt/claude2": "/opt/claude2",
		},
		runHandler: func(binary string, args []string, stdin string, extraEnv []string) ([]byte, []byte, int, error) {
			if len(args) == 1 && args[0] == "--version" {
				return []byte("2.1.329 (Claude Code)\n"), nil, 0, nil
			}
			return []byte(`{"loggedIn":true,"email":"work@example.com","authMethod":"claude.ai"}`), nil, 0, nil
		},
	}
	s := openLiveCLIStore(t, runner)

	conn, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:      "cli-connect",
		Provider:    "anthropic",
		Binary:      "claude",
		BinaryPath:  "/opt/claude2",
		ConfigDir:   "~/work-claude",
		AccentColor: "#D97757",
		Name:        "Work Claude",
		Passphrase:  testPassword,
	})
	if err != nil {
		t.Fatalf("cli-connect failed: %v", err)
	}
	if conn.BinaryPath != "/opt/claude2" || conn.ConfigDir != "~/work-claude" || conn.AccentColor != "#D97757" || conn.CLIVersion != "2.1.329" {
		t.Fatalf("unexpected CLI connection metadata: %+v", conn)
	}

	// Verify CLAUDE_CONFIG_DIR env is passed during probe and streaming turn.
	thread, err := s.CreateThread(t.Context(), "wen-operator", "Claude Work", conn.Ref, "claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("CreateThread failed: %v", err)
	}
	if _, err := s.RunAgentV2Turn(t.Context(), thread.ID, AgentV2PromptInput{Content: "Hi"}, nil); err != nil {
		t.Fatalf("RunAgentV2Turn failed: %v", err)
	}
	runner.mu.Lock()
	envs := append(append([][]string(nil), runner.runEnvList...), runner.startEnvList...)
	runner.mu.Unlock()
	foundConfigEnv := false
	for _, list := range envs {
		for _, item := range list {
			if strings.HasPrefix(item, "CLAUDE_CONFIG_DIR=") && strings.HasSuffix(item, "work-claude") {
				foundConfigEnv = true
			}
		}
	}
	if !foundConfigEnv {
		t.Fatalf("expected CLAUDE_CONFIG_DIR in env calls, got %+v", envs)
	}

	// Disable account via set-enabled; unlock state must remain while execution/models block.
	disabledView, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:   "set-enabled",
		Ref:      conn.Ref,
		Disabled: boolPtr(true),
	})
	if err != nil || !disabledView.Disabled || disabledView.Locked {
		t.Fatalf("expected Disabled=true and Locked=false preserved, got %+v err=%v", disabledView, err)
	}
	for _, m := range disabledView.Catalog.Models {
		if m.Selectable || m.Availability != "disabled" {
			t.Fatalf("expected disabled catalog model, got %+v", m)
		}
	}
	if _, err := resolveAgentV2Provider(t.Context(), s, conn.Ref, "", "", "", "wen-operator"); err == nil {
		t.Fatal("expected disabled provider to be rejected in resolveAgentV2Provider")
	}
	if s.providerRequestAllowed(AgentAIRequest{ConnectionRef: conn.Ref, Model: "claude-sonnet-4-6", Policy: dynamicModelPolicy("anthropic", "claude-sonnet-4-6", "cli")}, false) {
		t.Fatal("expected providerRequestAllowed=false while disabled")
	}
	if s.aiRequestConnectionValid(AgentAIRequest{ConnectionRef: conn.Ref}) {
		t.Fatal("expected aiRequestConnectionValid=false while disabled")
	}

	// Re-enable and verify resolution works again.
	enabledView, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:   "set-enabled",
		Ref:      conn.Ref,
		Disabled: boolPtr(false),
	})
	if err != nil || enabledView.Disabled {
		t.Fatalf("re-enable failed: %+v err=%v", enabledView, err)
	}
	if _, err := resolveAgentV2Provider(t.Context(), s, conn.Ref, "", "", "", "wen-operator"); err != nil {
		t.Fatalf("expected re-enabled provider to resolve, got %v", err)
	}
}

func TestProviderCLIScheduledTaskDispatch(t *testing.T) {
	t.Parallel()

	runner := &fakeACPRunner{
		paths: map[string]string{"antigravity": "/mock/bin/antigravity"},
		acpServer: func(ctx context.Context, stdin io.Reader, stdout io.Writer, stderr io.Writer, args []string, env []string) {
			reader := bufio.NewReader(stdin)
			writeMsg := func(v any) {
				b, _ := json.Marshal(v)
				_, _ = stdout.Write(append(b, '\n'))
			}
			for {
				line, err := reader.ReadBytes('\n')
				if len(line) > 0 {
					var req struct {
						ID     json.RawMessage `json:"id"`
						Method string          `json:"method"`
					}
					if json.Unmarshal(line, &req) == nil {
						switch req.Method {
						case "initialize":
							writeMsg(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(req.ID), "result": map[string]any{"protocolVersion": 1, "agentInfo": map[string]any{"name": "Antigravity ACP Server", "version": "1.5.13"}}})
						case "session/new":
							writeMsg(map[string]any{
								"jsonrpc": "2.0",
								"id":      json.RawMessage(req.ID),
								"result": map[string]any{
									"sessionId": "sess-task-1",
									"configOptions": []any{
										map[string]any{
											"id":       "model",
											"category": "model",
											"options":  []any{map[string]any{"value": "gemini-4-neon", "name": "Gemini 4 Neon"}},
										},
									},
									"_meta": map[string]any{"antigravity": map[string]any{"tier": map[string]any{"id": "g1-ultra-tier", "name": "Google AI Ultra"}}},
								},
							})
						case "session/set_config_option":
							writeMsg(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(req.ID), "result": map[string]any{}})
						case "session/prompt":
							writeMsg(map[string]any{
								"jsonrpc": "2.0",
								"method":  "session/update",
								"params": map[string]any{
									"sessionId": "sess-task-1",
									"update": map[string]any{
										"sessionUpdate": "agent_message_chunk",
										"content":       map[string]any{"type": "text", "text": `{"answer":"SOL watch clean","cards":[]}`},
									},
								},
							})
							writeMsg(map[string]any{
								"jsonrpc": "2.0",
								"id":      json.RawMessage(req.ID),
								"result":  map[string]any{"stopReason": "end_turn", "usage": map[string]any{"inputTokens": 300, "outputTokens": 18, "cachedReadTokens": 0}},
							})
						}
					}
				}
				if err != nil {
					return
				}
			}
		},
	}
	s := openLiveCLIStore(t, runner)

	conn, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:     "cli-connect",
		Provider:   "google",
		Binary:     "antigravity",
		Name:       "Antigravity Scheduler",
		Passphrase: testPassword,
	})
	if err != nil {
		t.Fatalf("cli-connect failed: %v", err)
	}

	profile := "trading-analyst"
	v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: profile, Name: "ACP task context"})
	sessID := v.Sessions[len(v.Sessions)-1].ID

	prep, err := s.ChangeAgents(t.Context(), AgentCommand{
		Action:               "task-ai-prepare",
		ProfileID:            profile,
		Revision:             v.Revision,
		ID:                   sessID,
		ConnectionRef:        conn.Ref,
		Model:                "gemini-4-neon",
		Name:                 "Antigravity scheduled reminder",
		Text:                 "Summarize status concisely.",
		IntervalSeconds:      60,
		MaxRuns:              1,
		AuthorizationSeconds: 3600,
		Limits:               &AILimits{MaxInputTokens: 8000, MaxOutputTokens: 512, DeadlineSeconds: 10, BudgetMicrousd: 0},
		TotalBudgetMicrousd:  0,
	})
	if err != nil {
		t.Fatalf("task-ai-prepare failed: %v", err)
	}
	task := prep.Tasks[len(prep.Tasks)-1]
	authView, err := s.ChangeAgents(t.Context(), AgentCommand{
		Action:        "task-ai-authorize",
		ProfileID:     profile,
		Revision:      prep.Revision,
		ID:            task.ID,
		ConsentDigest: task.AI.Digest,
	})
	if err != nil {
		t.Fatalf("task-ai-authorize failed: %v", err)
	}
	review, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:               "task-access-review",
		Ref:                  conn.Ref,
		ProfileID:            profile,
		Revision:             authView.Revision,
		TaskIDs:              []string{task.ID},
		AuthorizationSeconds: 3600,
	})
	if err != nil || review.AccessReview == nil {
		t.Fatalf("task-access-review failed: v=%+v err=%v", review, err)
	}
	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:               "task-access-allow",
		Ref:                  conn.Ref,
		ProfileID:            profile,
		Revision:             authView.Revision,
		TaskIDs:              []string{task.ID},
		AuthorizationSeconds: 3600,
		Allowance:            review.AccessReview,
		ConsentDigest:        review.AccessReview.ID,
		Passphrase:           testPassword,
	}); err != nil {
		t.Fatalf("task-access-allow failed: %v", err)
	}

	// Make task immediately due and execute scheduler -> ACP dispatch -> completion.
	tx, err := s.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	doc, rev, err := readAgents(t.Context(), tx)
	if err != nil {
		t.Fatal(err)
	}
	doc.Tasks[len(doc.Tasks)-1].NextAt = 0
	if err := saveAgents(t.Context(), tx, doc, rev); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := s.runAgentTasks(t.Context(), false); err != nil {
		t.Fatalf("runAgentTasks failed: %v", err)
	}
	if err := s.advanceAgentAI(t.Context(), nil, false); err != nil {
		t.Fatalf("advanceAgentAI failed: %v", err)
	}

	after, err := s.Agents(t.Context(), profile, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(after.AIRequests) != 1 || after.AIRequests[0].State != "completed" || after.AIRequests[0].Answer != "SOL watch clean" || after.AIRequests[0].CostBasis != cliUsageCostBasis || after.AIRequests[0].SpentMicrousd != 0 {
		t.Fatalf("unexpected completed task AIRequest: %+v", after.AIRequests)
	}
	if len(after.Tasks) == 0 || after.Tasks[len(after.Tasks)-1].Runs != 1 {
		t.Fatalf("expected scheduled task Runs=1, got %+v", after.Tasks)
	}
}

func openLiveCLIStore(t *testing.T, runner providerCLIRunner) *Store {
	t.Helper()
	s := fixtureStore(t)
	s.providerFixture = false
	s.provider.mu.Lock()
	s.provider.cliRunner = runner
	s.provider.apiCredentials = true
	s.provider.mu.Unlock()
	return s
}

func TestProviderAccentColorNonCLISave(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)

	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:      "add",
		Provider:    "openai",
		Name:        "Bad Color",
		Key:         testAPIKey,
		Passphrase:  testVaultPassword,
		AccentColor: "not-hex",
	}); err == nil {
		t.Fatal("expected invalid accent color error on add")
	}
	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:      "lock",
		AccentColor: "#27C5DA",
	}); err == nil {
		t.Fatal("expected accent color on lock to fail")
	}

	added, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:      "add",
		Provider:    "openai",
		Name:        "Primary API",
		Key:         testAPIKey,
		Passphrase:  testVaultPassword,
		AccentColor: "#27C5DA",
	})
	if err != nil {
		t.Fatalf("add with AccentColor failed: %v", err)
	}
	if added.AccentColor != "#27C5DA" {
		t.Fatalf("expected AccentColor #27C5DA, got %q", added.AccentColor)
	}

	// Replacing via save without specifying AccentColor preserves the existing swatch.
	saved, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:     "save",
		Ref:        added.Ref,
		Provider:   "openai",
		Name:       "Primary API Renamed",
		Key:        testAPIKey + "-2",
		Passphrase: testVaultPassword,
	})
	if err != nil {
		t.Fatalf("save preserving AccentColor failed: %v", err)
	}
	if saved.AccentColor != "#27C5DA" {
		t.Fatalf("expected preserved AccentColor #27C5DA, got %q", saved.AccentColor)
	}
}

func boolPtr(v bool) *bool { return &v }
