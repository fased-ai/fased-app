package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeCLIStartResult struct {
	stdout string
	stderr string
	lines  []string
	hold   bool
	err    error
}

type fakeCLIRunner struct {
	mu          sync.Mutex
	paths       map[string]string
	runHandler  func(binary string, args []string, stdin string) ([]byte, []byte, int, error)
	startFn     func(ctx context.Context, binary string, args []string, stdin string) fakeCLIStartResult
	files       map[string]bool
	lookups     []string
	runs        [][]string
	starts      [][]string
	killedCount int
}

func (f *fakeCLIRunner) LookPath(binary string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups = append(f.lookups, binary)
	if p, ok := f.paths[binary]; ok && p != "" {
		return p, nil
	}
	return "", errors.New("not found")
}

func (f *fakeCLIRunner) Run(ctx context.Context, binary string, args []string, stdin string) ([]byte, []byte, int, error) {
	f.mu.Lock()
	f.runs = append(f.runs, append([]string{binary}, args...))
	handler := f.runHandler
	f.mu.Unlock()
	if handler != nil {
		return handler(binary, args, stdin)
	}
	return nil, nil, 1, errors.New("unexpected run")
}

func (f *fakeCLIRunner) Start(ctx context.Context, binary string, args []string, stdin string) (io.ReadCloser, io.ReadCloser, func() error, func(), error) {
	f.mu.Lock()
	f.starts = append(f.starts, append([]string{binary}, args...))
	fn := f.startFn
	f.mu.Unlock()
	if fn == nil {
		return nil, nil, nil, nil, errors.New("unexpected start")
	}
	res := fn(ctx, binary, args, stdin)
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()

	done := make(chan struct{})
	var closeOnce sync.Once
	cancelCh := make(chan struct{})
	kill := func() {
		closeOnce.Do(func() {
			f.mu.Lock()
			f.killedCount++
			f.mu.Unlock()
			close(cancelCh)
		})
	}

	go func() {
		defer close(done)
		defer outW.Close()
		defer errW.Close()
		if len(res.lines) > 0 {
			for _, line := range res.lines {
				_, _ = io.WriteString(outW, line+"\n")
			}
		} else if res.stdout != "" {
			_, _ = io.WriteString(outW, res.stdout)
		}
		if res.stderr != "" {
			_, _ = io.WriteString(errW, res.stderr)
		}
		if res.hold {
			select {
			case <-cancelCh:
			case <-ctx.Done():
			}
		}
	}()

	wait := func() error {
		<-done
		select {
		case <-cancelCh:
			if res.err != nil {
				return res.err
			}
			return errors.New("killed")
		default:
		}
		return res.err
	}
	return outR, errR, wait, kill, nil
}

func (f *fakeCLIRunner) FileExistsNonEmpty(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, ok := range f.files {
		if ok && strings.HasSuffix(path, k) {
			return true
		}
	}
	return false
}

func (f *fakeCLIRunner) runCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.runs)
}

func (f *fakeCLIRunner) killed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.killedCount
}

func TestProviderCLIProbeAndConnectAllVendors(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	runner := &fakeCLIRunner{
		paths: map[string]string{
			"codex":  "/usr/local/bin/codex",
			"claude": "/usr/local/bin/claude",
			"gemini": "/usr/local/bin/gemini",
			"grok":   "/usr/local/bin/grok",
		},
		files: map[string]bool{
			"/.gemini/oauth_creds.json": true,
		},
		runHandler: func(binary string, args []string, stdin string) ([]byte, []byte, int, error) {
			switch {
			case strings.HasSuffix(binary, "codex"):
				return []byte("Logged in using ChatGPT\n"), nil, 0, nil
			case strings.HasSuffix(binary, "claude"):
				return []byte(`{"loggedIn":true,"email":"owner@example.com","authMethod":"claude.ai"}`), nil, 0, nil
			case strings.HasSuffix(binary, "grok"):
				return []byte("Authenticated as @owner\n"), nil, 0, nil
			default:
				return nil, nil, 1, errors.New("unsupported")
			}
		},
	}
	s.provider.mu.Lock()
	s.provider.cliRunner = runner
	s.provider.mu.Unlock()

	view, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "cli-probe"})
	if err != nil {
		t.Fatalf("cli-probe failed: %v", err)
	}
	if len(view.HostCLIs) != 5 {
		t.Fatalf("expected 5 host CLIs, got %d", len(view.HostCLIs))
	}
	byProvider := map[string]HostCLIStatus{}
	byBinary := map[string]HostCLIStatus{}
	for _, st := range view.HostCLIs {
		byBinary[st.Binary] = st
		if st.Provider == "google" && st.Binary == "antigravity" {
			// In this mock runner antigravity is not configured for ACP; gemini is checked below.
			continue
		}
		byProvider[st.Provider] = st
		if !st.Installed || !st.Authenticated {
			t.Fatalf("expected %s (%s) installed+authenticated, got %+v", st.Provider, st.Binary, st)
		}
	}
	if _, ok := byBinary["antigravity"]; !ok {
		t.Fatal("expected antigravity runtime in HostCLIs")
	}
	if byProvider["anthropic"].Account != "owner@example.com" {
		t.Fatalf("expected claude email account, got %q", byProvider["anthropic"].Account)
	}
	if byProvider["openai"].Account != "Logged in using ChatGPT" {
		t.Fatalf("expected codex account line, got %q", byProvider["openai"].Account)
	}
	if byProvider["google"].Account != "Google CLI" {
		t.Fatalf("expected gemini account label, got %q", byProvider["google"].Account)
	}
	if byProvider["xai"].Account != "Authenticated as @owner" {
		t.Fatalf("expected grok account label, got %q", byProvider["xai"].Account)
	}

	// Verify xai and anthropic OAuth sign-in-start remain refused.
	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "xai"}); err == nil {
		t.Fatal("expected xai sign-in-start to stay refused")
	}
	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "sign-in-start", Provider: "anthropic"}); err == nil {
		t.Fatal("expected anthropic sign-in-start to stay refused")
	}

	refs := map[string]string{}
	for _, p := range []string{"openai", "anthropic", "google", "xai"} {
		conn, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
			Action:     "cli-connect",
			Provider:   p,
			Name:       strings.ToUpper(p) + " CLI",
			Passphrase: testPassword,
		})
		if err != nil {
			t.Fatalf("cli-connect %s failed: %v", p, err)
		}
		if conn.AuthMethod != "cli" || conn.CredentialMode != "cli" {
			t.Fatalf("unexpected auth/credential mode for %s: %+v", p, conn)
		}
		if conn.Catalog.State != "ready" {
			t.Fatalf("expected catalog ready for %s, got %q", p, conn.Catalog.State)
		}
		hasSelectableCLI := false
		for _, m := range conn.Catalog.Models {
			if m.Method == "cli" && m.Selectable {
				hasSelectableCLI = true
				break
			}
		}
		if !hasSelectableCLI {
			t.Fatalf("expected at least one selectable CLI model for %s: %+v", p, conn.Catalog.Models)
		}
		refs[p] = conn.Ref
	}

	// Inspect encrypted v8 vault on disk and verify zero vendor secrets & AAD tamper resistance.
	col, err := s.readAIVaultCollection()
	if err != nil {
		t.Fatalf("readAIVaultCollection failed: %v", err)
	}
	if len(col.Accounts) != 4 {
		t.Fatalf("expected 4 v8 vault envelopes, got %d", len(col.Accounts))
	}
	for _, env := range col.Accounts {
		if env.Version != 8 || env.Mode != "cli" {
			t.Fatalf("expected Version=8 Mode=cli, got %+v", env)
		}
		plain, err := s.openAIVaultWithContext(t.Context(), env, testPassword)
		if err != nil {
			t.Fatalf("openAIVault %s failed: %v", env.Provider, err)
		}
		if strings.Contains(string(plain), "owner@example.com") || strings.Contains(string(plain), "sk-") {
			t.Fatalf("vault payload must not store raw vendor tokens/emails: %s", string(plain))
		}
		cred, ok := parseCLIProviderCredential(plain)
		if !ok || cred.Provider != env.Provider {
			t.Fatalf("unexpected v8 credential payload: %s", string(plain))
		}
		// Mutate Provider AAD
		tampered := env
		tampered.Provider = "openai"
		if tampered.Provider == env.Provider {
			tampered.Provider = "anthropic"
		}
		if _, err := s.openAIVaultWithContext(t.Context(), tampered, testPassword); err == nil {
			t.Fatalf("expected AAD tamper rejection when mutating Provider for %s", env.Ref)
		}
		// Mutate Ref AAD
		tamperedRef := env
		tamperedRef.Ref = env.Ref + "-tamper"
		if _, err := s.openAIVaultWithContext(t.Context(), tamperedRef, testPassword); err == nil {
			t.Fatalf("expected AAD tamper rejection when mutating Ref for %s", env.Ref)
		}
	}

	// Verify save-over-CLI is refused.
	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:         "save",
		Ref:            refs["openai"],
		Provider:       "openai",
		CredentialMode: "test",
		Key:            "fased-test-key-should-be-refused",
		Passphrase:     testPassword,
	}); err == nil {
		t.Fatal("expected save over CLI account to be refused")
	}

	// Verify models-refresh forces a host CLI re-probe and preserves ready catalog.
	beforeRuns := runner.runCount()
	refreshed, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action: "models-refresh",
		Ref:    refs["openai"],
	})
	if err != nil {
		t.Fatalf("models-refresh on CLI account failed: %v", err)
	}
	if runner.runCount() <= beforeRuns {
		t.Fatalf("expected models-refresh to invoke runner probe (before=%d, after=%d)", beforeRuns, runner.runCount())
	}
	if refreshed.Catalog.State != "ready" {
		t.Fatalf("expected catalog ready after refresh, got %q", refreshed.Catalog.State)
	}

	// Lock -> Unlock -> Remove lifecycle.
	locked, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "lock", Ref: refs["anthropic"]})
	if err != nil || !locked.Locked {
		t.Fatalf("lock failed: v=%+v err=%v", locked, err)
	}
	unlocked, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:     "unlock",
		Ref:        refs["anthropic"],
		Passphrase: testPassword,
	})
	if err != nil || unlocked.Locked || unlocked.AuthMethod != "cli" {
		t.Fatalf("unlock failed: v=%+v err=%v", unlocked, err)
	}
	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:     "remove",
		Ref:        refs["anthropic"],
		Passphrase: testPassword,
	}); err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	afterRemove, err := s.AIConnection()
	if err != nil {
		t.Fatalf("AIConnection after remove failed: %v", err)
	}
	if len(afterRemove.Connections) != 3 {
		t.Fatalf("expected 3 connections after remove, got %d", len(afterRemove.Connections))
	}
}

func TestProviderCLIAuthDeviceCodeAndCancel(t *testing.T) {
	t.Parallel()

	// 1. ANSI stripping & trailing punctuation cleanup
	vURL, code := parseDeviceAuthLine("\x1b[1;36mOpen https://auth.openai.com/codex/device).\x1b[0m Code: \x1b[33mABCD-1234\x1b[0m")
	if vURL != "https://auth.openai.com/codex/device" {
		t.Fatalf("unexpected verificationUrl: %q", vURL)
	}
	if code != "ABCD-1234" {
		t.Fatalf("unexpected userCode: %q", code)
	}

	s := fixtureStore(t)
	var mu sync.Mutex
	authed := false
	holdMode := true
	runner := &fakeCLIRunner{
		paths: map[string]string{
			"codex":  "/usr/local/bin/codex",
			"gemini": "/usr/local/bin/gemini",
		},
		runHandler: func(binary string, args []string, stdin string) ([]byte, []byte, int, error) {
			mu.Lock()
			ok := authed
			mu.Unlock()
			if ok {
				return []byte("Logged in using ChatGPT\n"), nil, 0, nil
			}
			return nil, []byte("Not logged in"), 1, errors.New("exit 1")
		},
		startFn: func(ctx context.Context, binary string, args []string, stdin string) fakeCLIStartResult {
			mu.Lock()
			hold := holdMode
			mu.Unlock()
			return fakeCLIStartResult{
				lines: []string{
					"\x1b[32mVisit https://auth.openai.com/codex/device\x1b[0m",
					"Enter code: ABCD-1234",
				},
				hold: hold,
			}
		},
	}
	s.provider.mu.Lock()
	s.provider.cliRunner = runner
	s.provider.mu.Unlock()

	// Google CLI interactive terminal error
	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:   "cli-auth-start",
		Provider: "google",
	}); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("expected google interactive terminal guidance error, got: %v", err)
	}

	// Start pending device-code auth and cancel it
	started, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:   "cli-auth-start",
		Provider: "openai",
	})
	if err != nil || started.CLIAuth == nil {
		t.Fatalf("cli-auth-start failed: v=%+v err=%v", started, err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var cur AIConnectionView
	for time.Now().Before(deadline) {
		cur, err = s.AIConnection()
		if err == nil && cur.CLIAuth != nil && cur.CLIAuth.State == "awaiting-code" {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if cur.CLIAuth == nil || cur.CLIAuth.State != "awaiting-code" || cur.CLIAuth.VerificationURL != "https://auth.openai.com/codex/device" || cur.CLIAuth.UserCode != "ABCD-1234" {
		t.Fatalf("expected awaiting-code with URL+code, got %+v", cur.CLIAuth)
	}

	afterCancel, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:   "cli-auth-cancel",
		SignInID: cur.CLIAuth.ID,
	})
	if err != nil {
		t.Fatalf("cli-auth-cancel failed: %v", err)
	}
	if afterCancel.CLIAuth != nil {
		t.Fatalf("expected CLIAuth cleared on cancel, got %+v", afterCancel.CLIAuth)
	}
	if runner.killed() < 1 {
		t.Fatalf("expected process kill callback on cancel, got %d", runner.killed())
	}

	// Complete device-code auth flow where process exits 0 and probe reports authenticated=true
	mu.Lock()
	holdMode = false
	authed = true
	mu.Unlock()
	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:   "cli-auth-start",
		Provider: "openai",
	}); err != nil {
		t.Fatalf("second cli-auth-start failed: %v", err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		cur, err = s.AIConnection()
		if err == nil && cur.CLIAuth != nil && cur.CLIAuth.State == "authenticated" {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if cur.CLIAuth == nil || cur.CLIAuth.State != "authenticated" {
		t.Fatalf("expected CLIAuth authenticated state, got %+v", cur.CLIAuth)
	}
}

func TestProviderCLIStreamingAgentV2(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)

	var mu sync.Mutex
	failNext := false
	runner := &fakeCLIRunner{
		paths: map[string]string{
			"codex":  "/usr/local/bin/codex",
			"claude": "/usr/local/bin/claude",
			"gemini": "/usr/local/bin/gemini",
		},
		files: map[string]bool{
			"/.gemini/oauth_creds.json": true,
		},
		runHandler: func(binary string, args []string, stdin string) ([]byte, []byte, int, error) {
			switch {
			case strings.HasSuffix(binary, "codex"):
				return []byte("Logged in using ChatGPT"), nil, 0, nil
			case strings.HasSuffix(binary, "claude"):
				return []byte(`{"loggedIn":true,"email":"owner@example.com"}`), nil, 0, nil
			default:
				return nil, nil, 1, errors.New("unused")
			}
		},
		startFn: func(ctx context.Context, binary string, args []string, stdin string) fakeCLIStartResult {
			mu.Lock()
			shouldFail := failNext
			mu.Unlock()
			if shouldFail {
				return fakeCLIStartResult{
					stderr: "\x1b[31mError:\x1b[0m rate limited by subscription quota",
					err:    errors.New("exit status 1"),
				}
			}
			switch {
			case strings.HasSuffix(binary, "claude"):
				return fakeCLIStartResult{
					lines: []string{
						`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello "}}}`,
						`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"from Claude CLI"}}}`,
						`{"type":"result","usage":{"input_tokens":120,"output_tokens":34,"cache_read_input_tokens":10}}`,
					},
				}
			case strings.HasSuffix(binary, "codex"):
				return fakeCLIStartResult{
					lines: []string{
						`{"type":"item.updated","item":{"id":"m1","type":"agent_message","text":"Hello"}}`,
						`{"type":"item.updated","item":{"id":"m1","type":"agent_message","text":"Hello from Codex CLI"}}`,
						`{"type":"item.completed","item":{"id":"m1","type":"agent_message","text":"Hello from Codex CLI"}}`,
						`{"type":"turn.completed","usage":{"input_tokens":200,"cached_input_tokens":50,"output_tokens":45}}`,
					},
				}
			case strings.HasSuffix(binary, "gemini"):
				return fakeCLIStartResult{
					lines: []string{
						`{"type":"message","role":"assistant","content":"Hello from "}`,
						`{"type":"message","role":"assistant","content":"Gemini CLI"}`,
						`{"type":"result","stats":{"input_tokens":90,"output_tokens":22,"cached":8}}`,
					},
				}
			default:
				return fakeCLIStartResult{err: errors.New("unknown binary")}
			}
		},
	}
	s.provider.mu.Lock()
	s.provider.cliRunner = runner
	s.provider.mu.Unlock()

	type expectation struct {
		provider   string
		wantText   string
		wantDeltas []string
		inTok      int64
		outTok     int64
		cacheTok   int64
	}
	cases := []expectation{
		{
			provider:   "anthropic",
			wantText:   "Hello from Claude CLI",
			wantDeltas: []string{"Hello ", "from Claude CLI"},
			inTok:      120,
			outTok:     34,
			cacheTok:   10,
		},
		{
			provider:   "openai",
			wantText:   "Hello from Codex CLI",
			wantDeltas: []string{"Hello", " from Codex CLI"},
			inTok:      200,
			outTok:     45,
			cacheTok:   50,
		},
		{
			provider:   "google",
			wantText:   "Hello from Gemini CLI",
			wantDeltas: []string{"Hello from ", "Gemini CLI"},
			inTok:      90,
			outTok:     22,
			cacheTok:   8,
		},
	}

	var anthropicRef string
	for _, tc := range cases {
		conn, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
			Action:     "cli-connect",
			Provider:   tc.provider,
			Name:       tc.provider + " sub",
			Passphrase: testPassword,
		})
		if err != nil {
			t.Fatalf("cli-connect %s failed: %v", tc.provider, err)
		}
		if tc.provider == "anthropic" {
			anthropicRef = conn.Ref
		}
		thread, err := s.CreateThread(t.Context(), "wen-operator", "CLI "+tc.provider, conn.Ref, "")
		if err != nil {
			t.Fatalf("CreateThread %s failed: %v", tc.provider, err)
		}
		var deltas []string
		res, err := s.RunAgentV2Turn(t.Context(), thread.ID, AgentV2PromptInput{Content: "Say hi"}, func(ev AgentStreamEvent) {
			if ev.Type == "text_delta" && ev.Delta != "" {
				deltas = append(deltas, ev.Delta)
			}
		})
		if err != nil {
			t.Fatalf("RunAgentV2Turn %s failed: %v", tc.provider, err)
		}
		if res.LastMessage == nil || res.LastMessage.Content != tc.wantText {
			t.Fatalf("%s message mismatch: got %+v, want %q", tc.provider, res.LastMessage, tc.wantText)
		}
		if jsonDelta, _ := json.Marshal(deltas); string(jsonDelta) != func() string { b, _ := json.Marshal(tc.wantDeltas); return string(b) }() {
			t.Fatalf("%s deltas mismatch: got %v, want %v", tc.provider, deltas, tc.wantDeltas)
		}
		if res.LastMessage.InputTokens != tc.inTok || res.LastMessage.OutputTokens != tc.outTok || res.LastMessage.CachedTokens != tc.cacheTok {
			t.Fatalf("%s usage mismatch: got %+v", tc.provider, res.LastMessage)
		}
	}

	// Non-zero exit with ANSI stderr produces sanitized error without ANSI codes.
	mu.Lock()
	failNext = true
	mu.Unlock()
	thread, err := s.CreateThread(t.Context(), "wen-operator", "CLI error", anthropicRef, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunAgentV2Turn(t.Context(), thread.ID, AgentV2PromptInput{Content: "Trigger error"}, nil); err == nil {
		t.Fatal("expected non-zero CLI exit to fail turn")
	} else if strings.Contains(err.Error(), "\x1b") || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("expected ANSI-sanitized error mentioning rate limited, got %q", err.Error())
	}
}
