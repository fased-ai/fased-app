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
	"sync/atomic"
	"testing"
)

func TestCLILaunchArgsValidation(t *testing.T) {
	validCases := []struct {
		raw  string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"--enable web_search_request", []string{"--enable", "web_search_request"}},
		{`-c model_reasoning_effort="high"`, []string{"-c", "model_reasoning_effort=high"}},
		{"--config=model_reasoning_effort=medium --effort high", []string{"--config=model_reasoning_effort=medium", "--effort", "high"}},
	}
	for _, tc := range validCases {
		got, err := parseSafeCLILaunchArgs(tc.raw)
		if err != nil {
			t.Fatalf("expected valid launch args for %q, got error: %v", tc.raw, err)
		}
		if !slices.Equal(got, tc.want) {
			t.Fatalf("parseSafeCLILaunchArgs(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}

	invalidCases := []string{
		"--enable foo; rm -rf /",
		"--enable $(whoami)",
		"--enable `id`",
		"--enable foo && bar",
		"--enable foo | cat",
		"--enable \"unterminated",
		"--search\n--yolo",
		"-",
		"--",
		"--search",
		"--chrome",
		"exec --search",
		"--search extra-positional",
		"--yolo",
		"--dangerously-bypass-approvals-and-sandbox",
		"--dangerously-skip-permissions",
		"--sandbox danger-full-access",
		"-s workspace-write",
		"--ask-for-approval=never",
		"--full-auto",
		"--tools bash",
		"--output-format text",
		"--listen ws://0.0.0.0:8080",
		"--stdio",
		"-C /etc",
		"-c approval_policy=never",
		"--config=sandbox_mode=danger-full-access",
		"-c mcp_servers.evil.command=sh",
		"-c shell_environment_policy.inherit=all",
		"-c auth.token=abc",
		"--config missingvalue",
	}
	for _, raw := range invalidCases {
		if got, err := parseSafeCLILaunchArgs(raw); err == nil {
			t.Fatalf("expected rejection for unsafe launch args %q, got %v", raw, got)
		}
	}
}

func TestCodexShadowHomeIsolationAndPersistence(t *testing.T) {
	t.Cleanup(evictCodexAppServerSessions)

	baseDir := filepath.Join(t.TempDir(), "base-codex")
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	wantCfg := "model = \"gpt-5.4\"\n"
	if err := os.WriteFile(filepath.Join(baseDir, "config.toml"), []byte(wantCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "auth.json"), []byte(`{"token":"secret-primary"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	shadowDir := filepath.Join(t.TempDir(), "shadow-personal")
	prepared, err := prepareCodexShadowHome(baseDir, shadowDir)
	if err != nil {
		t.Fatalf("prepareCodexShadowHome failed: %v", err)
	}
	if prepared != shadowDir {
		t.Fatalf("prepared = %q, want %q", prepared, shadowDir)
	}
	dirInfo, err := os.Lstat(shadowDir)
	if err != nil || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("expected 0700 shadow dir, got info=%v err=%v", dirInfo, err)
	}
	cfgInfo, err := os.Lstat(filepath.Join(shadowDir, "config.toml"))
	if err != nil || cfgInfo.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 copied config.toml, got info=%v err=%v", cfgInfo, err)
	}
	gotCfg, err := os.ReadFile(filepath.Join(shadowDir, "config.toml"))
	if err != nil || string(gotCfg) != wantCfg {
		t.Fatalf("unexpected shadow config.toml content=%q err=%v", string(gotCfg), err)
	}
	if _, err := os.Lstat(filepath.Join(shadowDir, "auth.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected auth.json NOT to be copied to shadow home, got err=%v", err)
	}

	// Reject symlink and oversized config.toml.
	symBase := filepath.Join(t.TempDir(), "sym-base")
	if err := os.MkdirAll(symBase, 0o700); err != nil {
		t.Fatal(err)
	}
	targetFile := filepath.Join(t.TempDir(), "outside.toml")
	_ = os.WriteFile(targetFile, []byte("x = 1\n"), 0o600)
	_ = os.Symlink(targetFile, filepath.Join(symBase, "config.toml"))
	if _, err := prepareCodexShadowHome(symBase, filepath.Join(t.TempDir(), "sym-shadow")); err == nil {
		t.Fatal("expected symlink config.toml to be rejected")
	}

	bigBase := filepath.Join(t.TempDir(), "big-base")
	_ = os.MkdirAll(bigBase, 0o700)
	_ = os.WriteFile(filepath.Join(bigBase, "config.toml"), make([]byte, codexShadowConfigMaxBytes+1), 0o600)
	if _, err := prepareCodexShadowHome(bigBase, filepath.Join(t.TempDir(), "big-shadow")); err == nil {
		t.Fatal("expected oversized config.toml to be rejected")
	}

	runner := &fakeACPRunner{
		paths: map[string]string{
			"codex":  "/usr/local/bin/codex",
			"claude": "/usr/local/bin/claude",
		},
		runHandler: func(binary string, args []string, stdin string, env []string) ([]byte, []byte, int, error) {
			if len(args) == 1 && args[0] == "--version" {
				return []byte("codex-cli 0.171.0\n"), nil, 0, nil
			}
			if strings.Contains(binary, "codex") && len(args) >= 2 && args[0] == "login" && args[1] == "status" {
				return []byte("Logged in using ChatGPT\n"), nil, 0, nil
			}
			if strings.Contains(binary, "claude") && len(args) >= 2 && args[0] == "auth" && args[1] == "status" {
				return []byte(`{"loggedIn":true,"email":"u@example.com"}`), nil, 0, nil
			}
			return nil, nil, 1, errors.New("unexpected run")
		},
	}

	s := fixtureStore(t)
	s.provider.mu.Lock()
	s.provider.cliRunner = runner
	s.provider.mu.Unlock()

	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:     "cli-connect",
		Provider:   "anthropic",
		Binary:     "claude",
		ShadowHome: "~/.codex-fased/invalid",
		Passphrase: testPassword,
	}); err == nil {
		t.Fatal("expected shadow home on non-Codex CLI to be rejected")
	}

	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:     "cli-connect",
		Provider:   "openai",
		Binary:     "codex",
		LaunchArgs: "--yolo",
		Passphrase: testPassword,
	}); err == nil {
		t.Fatal("expected unsafe launchArgs on cli-connect to be rejected")
	}

	conn, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:     "cli-connect",
		Provider:   "openai",
		Binary:     "codex",
		Name:       "Codex Personal",
		ConfigDir:  "~/.codex",
		ShadowHome: "~/.codex-fased/personal",
		LaunchArgs: "--enable web_search_request",
		Passphrase: testPassword,
	})
	if err != nil {
		t.Fatalf("cli-connect codex with shadowHome failed: %v", err)
	}
	if conn.ShadowHome != "~/.codex-fased/personal" || conn.LaunchArgs != "--enable web_search_request" {
		t.Fatalf("unexpected persisted view fields: %+v", conn)
	}

	runner.mu.Lock()
	var sawShadowEnv bool
	for _, env := range runner.runEnvList {
		for _, kv := range env {
			if strings.HasPrefix(kv, "CODEX_HOME=") && strings.HasSuffix(kv, filepath.Join(".codex-fased", "personal")) {
				sawShadowEnv = true
			}
		}
	}
	runner.mu.Unlock()
	if !sawShadowEnv {
		t.Fatal("expected login status probe to receive CODEX_HOME set to shadow home")
	}

	reloaded, err := s.AIConnection(conn.Ref)
	if err != nil {
		t.Fatalf("AIConnection reload failed: %v", err)
	}
	if reloaded.ShadowHome != "~/.codex-fased/personal" || reloaded.LaunchArgs != "--enable web_search_request" {
		t.Fatalf("expected ShadowHome and LaunchArgs to persist after reload, got %+v", reloaded)
	}
}

func TestCodexAppServerPersistentPoolAndFallback(t *testing.T) {
	t.Cleanup(evictCodexAppServerSessions)

	var startCount atomic.Int32
	var sawInitialized atomic.Bool
	var deniedServerReq atomic.Bool
	var turnSeq atomic.Int32
	var capturedArgsMu sync.Mutex
	var capturedArgs []string

	runner := &fakeACPRunner{
		paths: map[string]string{
			"codex": "/usr/local/bin/codex",
		},
		runHandler: func(binary string, args []string, stdin string, env []string) ([]byte, []byte, int, error) {
			if len(args) == 1 && args[0] == "--version" {
				return []byte("codex-cli 0.171.0\n"), nil, 0, nil
			}
			if len(args) >= 2 && args[0] == "login" && args[1] == "status" {
				return []byte("Logged in using ChatGPT\n"), nil, 0, nil
			}
			return nil, nil, 1, errors.New("unexpected run")
		},
		acpServer: func(ctx context.Context, stdin io.Reader, stdout io.Writer, stderr io.Writer, args []string, env []string) {
			startCount.Add(1)
			capturedArgsMu.Lock()
			capturedArgs = append([]string(nil), args...)
			capturedArgsMu.Unlock()

			scanner := bufio.NewScanner(stdin)
			scanner.Buffer(make([]byte, 8192), 256*1024)
			writeMsg := func(v any) {
				b, _ := json.Marshal(v)
				_, _ = stdout.Write(append(b, '\n'))
			}
			currentThreadID := ""
			for scanner.Scan() {
				var msg codexRPCEnvelope
				if json.Unmarshal(scanner.Bytes(), &msg) != nil {
					continue
				}
				// Response to server-initiated request.
				if msg.Method == "" && msg.Error != nil && string(msg.ID) == `"srv-req-1"` {
					if msg.Error.Code == -32601 {
						deniedServerReq.Store(true)
					}
					continue
				}
				switch msg.Method {
				case "initialize":
					writeMsg(map[string]any{
						"jsonrpc": "2.0",
						"id":      json.RawMessage(msg.ID),
						"result": map[string]any{
							"userAgent":      "codex-cli 0.171.0",
							"codexHome":      "/tmp/shadow",
							"platformFamily": "unix",
							"platformOs":     "linux",
						},
					})
				case "initialized":
					sawInitialized.Store(true)
				case "thread/start":
					var params map[string]any
					_ = json.Unmarshal(msg.Params, &params)
					if params["approvalPolicy"] != "untrusted" || params["sandbox"] != "read-only" || params["ephemeral"] != true {
						writeMsg(map[string]any{
							"jsonrpc": "2.0",
							"id":      json.RawMessage(msg.ID),
							"error":   map[string]any{"code": -32602, "message": "expected non-custodial untrusted read-only policy"},
						})
						continue
					}
					n := turnSeq.Add(1)
					currentThreadID = "thr-" + string(rune('0'+n))
					writeMsg(map[string]any{
						"jsonrpc": "2.0",
						"id":      json.RawMessage(msg.ID),
						"result": map[string]any{
							"thread":         map[string]any{"id": currentThreadID},
							"approvalPolicy": "untrusted",
							"sandbox":        "read-only",
						},
					})
				case "turn/start":
					turnID := "turn-" + currentThreadID
					writeMsg(map[string]any{
						"jsonrpc": "2.0",
						"id":      json.RawMessage(msg.ID),
						"result": map[string]any{
							"turn": map[string]any{"id": turnID},
						},
					})
					if !deniedServerReq.Load() {
						writeMsg(map[string]any{
							"jsonrpc": "2.0",
							"id":      "srv-req-1",
							"method":  "item/commandExecution/requestApproval",
							"params":  map[string]any{"command": "rm -rf /"},
						})
						for scanner.Scan() {
							var reply codexRPCEnvelope
							if json.Unmarshal(scanner.Bytes(), &reply) == nil && reply.Method == "" && reply.Error != nil && string(reply.ID) == `"srv-req-1"` {
								if reply.Error.Code == -32601 {
									deniedServerReq.Store(true)
								}
								break
							}
						}
					}
					writeMsg(map[string]any{
						"jsonrpc": "2.0",
						"method":  "item/agentMessage/delta",
						"params": map[string]any{
							"threadId": currentThreadID,
							"turnId":   turnID,
							"itemId":   "msg-1",
							"delta":    "Hello ",
						},
					})
					writeMsg(map[string]any{
						"jsonrpc": "2.0",
						"method":  "item/agentMessage/delta",
						"params": map[string]any{
							"threadId": currentThreadID,
							"turnId":   turnID,
							"itemId":   "msg-1",
							"delta":    "app-server",
						},
					})
					// Duplicate completed item with same text must not double output.
					writeMsg(map[string]any{
						"jsonrpc": "2.0",
						"method":  "item/completed",
						"params": map[string]any{
							"threadId": currentThreadID,
							"turnId":   turnID,
							"item": map[string]any{
								"id":   "msg-1",
								"type": "agentMessage",
								"text": "Hello app-server",
							},
						},
					})
					writeMsg(map[string]any{
						"jsonrpc": "2.0",
						"method":  "thread/tokenUsage/updated",
						"params": map[string]any{
							"threadId": currentThreadID,
							"turnId":   turnID,
							"tokenUsage": map[string]any{
								"last": map[string]any{
									"inputTokens":       120,
									"cachedInputTokens": 40,
									"outputTokens":      18,
								},
							},
						},
					})
					writeMsg(map[string]any{
						"jsonrpc": "2.0",
						"method":  "turn/completed",
						"params": map[string]any{
							"threadId": currentThreadID,
							"turn": map[string]any{
								"id":     turnID,
								"status": "completed",
							},
						},
					})
				}
			}
		},
	}

	s := fixtureStore(t)
	s.provider.mu.Lock()
	s.provider.cliRunner = runner
	s.provider.mu.Unlock()

	conn, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:     "cli-connect",
		Provider:   "openai",
		Binary:     "codex",
		Name:       "Codex AppServer",
		ShadowHome: "~/.codex-fased/work",
		LaunchArgs: `-c model_reasoning_effort="high"`,
		Passphrase: testPassword,
	})
	if err != nil {
		t.Fatalf("cli-connect codex failed: %v", err)
	}

	thread, err := s.CreateThread(t.Context(), "wen-operator", "Codex Pool Thread", conn.Ref, "gpt-5.4")
	if err != nil {
		t.Fatalf("CreateThread failed: %v", err)
	}

	// Turn 1 starts warm session #1.
	var deltas1 []string
	res1, err := s.RunAgentV2Turn(t.Context(), thread.ID, AgentV2PromptInput{Content: "Turn 1"}, func(ev AgentStreamEvent) {
		if ev.Type == "text_delta" && ev.Delta != "" {
			deltas1 = append(deltas1, ev.Delta)
		}
	})
	if err != nil {
		t.Fatalf("Turn 1 failed: %v", err)
	}
	if res1.LastMessage == nil || res1.LastMessage.Content != "Hello app-server" {
		t.Fatalf("Turn 1 LastMessage = %+v, want Content=%q", res1.LastMessage, "Hello app-server")
	}
	if strings.Join(deltas1, "") != "Hello app-server" {
		t.Fatalf("Turn 1 deltas = %q, want deduplicated stream", strings.Join(deltas1, ""))
	}
	if res1.LastMessage.InputTokens != 120 || res1.LastMessage.CachedTokens != 40 || res1.LastMessage.OutputTokens != 18 {
		t.Fatalf("unexpected Turn 1 token counts: %+v", res1.LastMessage)
	}
	if startCount.Load() != 1 || !sawInitialized.Load() || !deniedServerReq.Load() {
		t.Fatalf("unexpected Turn 1 pool state: starts=%d initialized=%v denied=%v", startCount.Load(), sawInitialized.Load(), deniedServerReq.Load())
	}

	capturedArgsMu.Lock()
	gotArgs := append([]string(nil), capturedArgs...)
	capturedArgsMu.Unlock()
	if !slices.Equal(gotArgs, []string{"app-server", "--stdio", "-c", "model_reasoning_effort=high"}) {
		t.Fatalf("unexpected app-server launch args: %v", gotArgs)
	}

	// Turn 2 reuses the SAME warm session without respawning app-server.
	res2, err := s.RunAgentV2Turn(t.Context(), thread.ID, AgentV2PromptInput{Content: "Turn 2"}, nil)
	if err != nil {
		t.Fatalf("Turn 2 failed: %v", err)
	}
	if res2.LastMessage == nil || res2.LastMessage.Content != "Hello app-server" {
		t.Fatalf("Turn 2 LastMessage = %+v", res2.LastMessage)
	}
	if startCount.Load() != 1 {
		t.Fatalf("expected warm pooled app-server reuse (startCount=1), got %d", startCount.Load())
	}

	// Toggling set-enabled must evict warm sessions so next turn spawns a new process.
	disabled := true
	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:   "set-enabled",
		Ref:      conn.Ref,
		Disabled: &disabled,
	}); err != nil {
		t.Fatalf("set-enabled disable failed: %v", err)
	}
	disabled = false
	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:   "set-enabled",
		Ref:      conn.Ref,
		Disabled: &disabled,
	}); err != nil {
		t.Fatalf("set-enabled re-enable failed: %v", err)
	}
	if _, err := s.RunAgentV2Turn(t.Context(), thread.ID, AgentV2PromptInput{Content: "Turn 3"}, nil); err != nil {
		t.Fatalf("Turn 3 after re-enable failed: %v", err)
	}
	if startCount.Load() != 2 {
		t.Fatalf("expected set-enabled eviction to respawn app-server (startCount=2), got %d", startCount.Load())
	}

	// Fallback test: pre-delta initialize failure falls back cleanly to `codex exec`.
	var execArgs []string
	var execEnv []string
	fallbackRunner := &codexFallbackTestRunner{
		fakeACPRunner: &fakeACPRunner{
			paths: map[string]string{"codex": "/usr/local/bin/codex"},
			runHandler: func(binary string, args []string, stdin string, env []string) ([]byte, []byte, int, error) {
				if len(args) == 1 && args[0] == "--version" {
					return []byte("codex-cli 0.171.0\n"), nil, 0, nil
				}
				return []byte("Logged in using ChatGPT\n"), nil, 0, nil
			},
			acpServer: func(ctx context.Context, stdin io.Reader, stdout io.Writer, stderr io.Writer, args []string, env []string) {
				scanner := bufio.NewScanner(stdin)
				if scanner.Scan() {
					var msg codexRPCEnvelope
					if json.Unmarshal(scanner.Bytes(), &msg) == nil {
						b, _ := json.Marshal(map[string]any{
							"jsonrpc": "2.0",
							"id":      json.RawMessage(msg.ID),
							"error":   map[string]any{"code": -32601, "message": "app-server unsupported"},
						})
						_, _ = stdout.Write(append(b, '\n'))
					}
				}
			},
		},
		onExec: func(args, env []string) string {
			execArgs = append([]string(nil), args...)
			execEnv = append([]string(nil), env...)
			return strings.Join([]string{
				`{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":"Fallback exec ok"}}`,
				`{"type":"turn.completed","usage":{"input_tokens":15,"cached_input_tokens":0,"output_tokens":4}}`,
			}, "\n") + "\n"
		},
	}

	s.provider.mu.Lock()
	s.provider.cliRunner = fallbackRunner
	s.provider.mu.Unlock()

	resFallback, err := s.RunAgentV2Turn(t.Context(), thread.ID, AgentV2PromptInput{Content: "Fallback turn"}, nil)
	if err != nil {
		t.Fatalf("expected seamless codex exec fallback, got error: %v", err)
	}
	if resFallback.LastMessage == nil || resFallback.LastMessage.Content != "Fallback exec ok" {
		t.Fatalf("fallback LastMessage = %+v, want Content=%q", resFallback.LastMessage, "Fallback exec ok")
	}
	if len(execArgs) < 3 || execArgs[len(execArgs)-1] != "-" || !slices.Contains(execArgs, "model_reasoning_effort=high") {
		t.Fatalf("expected validated launchArgs placed before '-' in codex exec args, got %v", execArgs)
	}
	if !slices.ContainsFunc(execEnv, func(kv string) bool {
		return strings.HasPrefix(kv, "CODEX_HOME=") && strings.HasSuffix(kv, filepath.Join(".codex-fased", "work"))
	}) {
		t.Fatalf("expected shadow CODEX_HOME on fallback exec env, got %v", execEnv)
	}
}

type codexFallbackTestRunner struct {
	*fakeACPRunner
	onExec func(args, env []string) string
}

func (r *codexFallbackTestRunner) StartWithEnv(ctx context.Context, binary string, args []string, stdin string, extraEnv []string) (io.ReadCloser, io.ReadCloser, func() error, func(), error) {
	payload := r.onExec(args, extraEnv)
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	go func() {
		defer outW.Close()
		defer errW.Close()
		_, _ = io.WriteString(outW, payload)
	}()
	return outR, errR, func() error { return nil }, func() {}, nil
}
