package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/fased-ai/fased-app/internal/signer"
)

func sseHTTPResponse(lines ...string) *http.Response {
	body := strings.Join(lines, "\n")
	if !strings.HasSuffix(body, "\n\n") {
		body += "\n\n"
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func readV2SSEEvents(t *testing.T, body io.Reader) []AgentStreamEvent {
	t.Helper()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	var events []AgentStreamEvent
	var dataLines []string
	flush := func() {
		if len(dataLines) == 0 {
			return
		}
		raw := strings.Join(dataLines, "\n")
		dataLines = nil
		var ev AgentStreamEvent
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			t.Fatalf("invalid SSE payload %q: %v", raw, err)
		}
		events = append(events, ev)
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read SSE stream: %v", err)
	}
	flush()
	return events
}

func startV2HTTPTestServer(t *testing.T, s *Store) (*httptest.Server, string, string) {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	h, err := NewHTTPHandler(s, "http://"+ts.Listener.Addr().String(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	h.agentFixture = s.providerFixture
	ts.Config.Handler = h
	ts.Start()
	t.Cleanup(ts.Close)

	token, err := s.NewSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return ts, token, csrfToken(token)
}

func doV2JSONRequest(t *testing.T, method, url, origin, token, csrf, body string, accept string) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, url, bodyReader)
	if err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestAgentV2StoreSchemaAndFTS5(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	dir := s.dir
	agentsDBPath := filepath.Join(dir, "agents.sqlite")

	// Lazy creation: core store opens without creating agents.sqlite until v2 is accessed.
	if _, err := os.Stat(agentsDBPath); !os.IsNotExist(err) {
		t.Fatalf("agents.sqlite created eagerly before v2 use: %v", err)
	}
	var coreSchema int
	if err := s.db.QueryRowContext(t.Context(), `PRAGMA user_version`).Scan(&coreSchema); err != nil {
		t.Fatal(err)
	}
	if coreSchema != StateSchema {
		t.Fatalf("unexpected core.sqlite user_version: got %d want %d", coreSchema, StateSchema)
	}

	// Verify enrollment classification on Linux rules.
	if inc, _, ok := enrollmentPath("agents.sqlite"); !ok || !inc {
		t.Fatalf("agents.sqlite must be included in enrollment image: inc=%v ok=%v", inc, ok)
	}
	for _, transient := range []string{"agents.sqlite-wal", "agents.sqlite-shm"} {
		if inc, _, ok := enrollmentPath(transient); !ok || inc {
			t.Fatalf("%s must be classified transient (excluded): inc=%v ok=%v", transient, inc, ok)
		}
	}

	thread, err := s.CreateThread(t.Context(), "trading-analyst", "SOL/USDC momentum check", "", "")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(agentsDBPath)
	if err != nil {
		t.Fatalf("agents.sqlite missing after CreateThread: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("agents.sqlite mode = %o, want 0600", info.Mode().Perm())
	}

	if _, err := s.AppendMessage(t.Context(), AgentV2Message{
		ThreadID: thread.ID,
		Role:     "user",
		Content:  "Check SOL/USDC liquidity",
	}); err != nil {
		t.Fatal(err)
	}
	aMsg, err := s.AppendMessage(t.Context(), AgentV2Message{
		ThreadID: thread.ID,
		Role:     "assistant",
		Content:  "Proposing bounded swap draft.",
		Status:   "awaiting_approval",
	})
	if err != nil {
		t.Fatal(err)
	}
	tc, err := s.CreateToolCall(t.Context(), AgentToolCall{
		ThreadID:  thread.ID,
		MessageID: aMsg.ID,
		CallID:    "call_1",
		ToolName:  "draft_swap",
		Tier:      toolTierAction,
		ArgsJSON:  `{"fromToken":"USDC","toToken":"SOL","amount":"15","network":"solana-mainnet","rationale":"rebalance"}`,
		Status:    "awaiting_approval",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateApproval(t.Context(), AgentApproval{
		ThreadID:    thread.ID,
		MessageID:   aMsg.ID,
		ToolCallID:  tc.ID,
		ToolName:    "draft_swap",
		Title:       "Swap 15 USDC -> SOL",
		Summary:     "solana-mainnet draft",
		PayloadJSON: tc.ArgsJSON,
		Status:      "pending",
	}); err != nil {
		t.Fatal(err)
	}

	// Create FTS5 memories and test BM25 + tricky punctuation sanitization.
	mPinned, err := s.CreateMemory(t.Context(), MemoryInput{
		ProfileID: "trading-analyst",
		Title:     "Slippage Limit",
		Content:   "Always cap Solana SOL/USDC swap slippage at 50 bps.",
		Tags:      "solana,risk",
		Pinned:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMemory(t.Context(), MemoryInput{
		ProfileID: "trading-analyst",
		Title:     "ETH thesis",
		Content:   "Monitor Ethereum staking yield trends weekly.",
		Tags:      "eth,yield",
	}); err != nil {
		t.Fatal(err)
	}

	// Legacy memory in core.sqlite must also surface in SearchMemories.
	agentApply(t, s, AgentCommand{
		Action:    "memory-put",
		ProfileID: "trading-analyst",
		Text:      "Legacy fact: keep Solana reserve above 2 SOL for fees.",
		Source:    "manual",
	})

	for _, query := range []string{"SOL/USDC", `"slippage" / (solana)`, "solana"} {
		hits, err := s.SearchMemories(t.Context(), "trading-analyst", query, 10)
		if err != nil {
			t.Fatalf("SearchMemories(%q) failed: %v", query, err)
		}
		if len(hits) == 0 || hits[0].ID != mPinned.ID {
			t.Fatalf("SearchMemories(%q) = %+v, expected pinned FTS match first", query, hits)
		}
	}
	if _, err := s.SearchMemories(t.Context(), "trading-analyst", `"bad" -query NOT OR *:`, 10); err != nil {
		t.Fatalf("operator-heavy FTS query failed: %v", err)
	}
	allSolana, err := s.SearchMemories(t.Context(), "trading-analyst", "solana", 10)
	if err != nil || len(allSolana) < 2 {
		t.Fatalf("expected both FTS5 and legacy memories for 'solana', got %+v err=%v", allSolana, err)
	}

	// Empty punctuation-only search should fall back cleanly without FTS5 syntax error.
	if _, err := s.SearchMemories(t.Context(), "trading-analyst", "??!///", 10); err != nil {
		t.Fatalf("punctuation fallback failed: %v", err)
	}

	// Persistence across Store.Close() and reopen.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := reopenFixtureStore(t, dir)
	detail, err := reopened.GetThread(t.Context(), thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Messages) != 2 || len(detail.PendingApprovals) != 1 || len(detail.Messages[1].ToolCalls) != 1 {
		t.Fatalf("unexpected reopened thread detail: %+v", detail)
	}
}

func TestAgentV2ScopedMemoriesHTTP(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)

	if _, err := s.CreateMemory(t.Context(), MemoryInput{
		ProfileID: "operator",
		Scope:     "workspace",
		Title:     "General note",
		Content:   "SOL spread note",
		Source:    "user",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMemory(t.Context(), MemoryInput{
		ProfileID: "operator",
		Scope:     "swap_draft",
		Title:     "Swap draft: 1 SOL -> USDC",
		Content:   `{"fromToken":"SOL","toToken":"USDC"}`,
		Source:    "agent",
	}); err != nil {
		t.Fatal(err)
	}
	taskMem, err := s.CreateMemory(t.Context(), MemoryInput{
		ProfileID: "operator",
		Scope:     "task_draft",
		Title:     "Task draft: SOL Volatility Watch",
		Content:   `{"name":"SOL Volatility Watch","description":"Watch SOL/USDC spread","intervalSeconds":300}`,
		Source:    "agent",
	})
	if err != nil {
		t.Fatal(err)
	}

	ts, token, _ := startV2HTTPTestServer(t, s)
	resp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/agents/v2/memories?profileId=operator&scope=task_draft", ts.URL, token, "", "", "application/json")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for scoped memories, got %d", resp.StatusCode)
	}
	var body struct {
		Memories []AgentV2Memory `json:"memories"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Memories) != 1 || body.Memories[0].ID != taskMem.ID || body.Memories[0].Scope != "task_draft" {
		t.Fatalf("expected only task_draft memory, got %+v", body.Memories)
	}

	// Empty q + q&scope should filter search results to the requested scope as well.
	filteredResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/agents/v2/memories?profileId=operator&q=SOL&scope=task_draft", ts.URL, token, "", "", "application/json")
	defer filteredResp.Body.Close()
	var filteredBody struct {
		Memories []AgentV2Memory `json:"memories"`
	}
	if err := json.NewDecoder(filteredResp.Body).Decode(&filteredBody); err != nil {
		t.Fatal(err)
	}
	if len(filteredBody.Memories) != 1 || filteredBody.Memories[0].Scope != "task_draft" {
		t.Fatalf("expected q+scope filtered task_draft only, got %+v", filteredBody.Memories)
	}
}

func TestAgentV2WireAdaptersAllFamilies(t *testing.T) {
	t.Parallel()

	t.Run("OpenAI-APIKey-Streaming-And-KeyScrub", func(t *testing.T) {
		t.Parallel()
		s := fixtureStore(t)
		conn := saveTestConnection(t, s)

		var seenPath string
		var echoSecret bool
		s.agentV2TransportOverride = aiTestTransport(func(r *http.Request) (*http.Response, error) {
			seenPath = r.URL.Path
			if got := r.Header.Get("Authorization"); got != "Bearer "+testAPIKey {
				t.Fatalf("missing bearer credential, got %q", got)
			}
			if echoSecret {
				return sseHTTPResponse(
					`event: response.created`,
					`data: {"type":"response.created","response":{"id":"resp_echo"}}`,
					``,
					`event: response.output_text.delta`,
					`data: {"type":"response.output_text.delta","delta":"leaked=`+testAPIKey+`"}`,
					``,
					`event: response.completed`,
					`data: {"type":"response.completed","response":{"id":"resp_echo","status":"completed"}}`,
				), nil
			}
			return sseHTTPResponse(
				`event: response.created`,
				`data: {"type":"response.created","response":{"id":"resp_1"}}`,
				``,
				`event: response.reasoning_summary_text.delta`,
				`data: {"type":"response.reasoning_summary_text.delta","delta":"Checking balance. "}`,
				``,
				`event: response.output_text.delta`,
				`data: {"type":"response.output_text.delta","delta":"Hello from OpenAI!"}`,
				``,
				`event: response.completed`,
				`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":42,"output_tokens":15,"input_tokens_details":{"cached_tokens":4}}}}`,
			), nil
		})

		thread, err := s.CreateThread(t.Context(), "wen-operator", "OpenAI Test", conn.Ref, "")
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.RunAgentV2Turn(t.Context(), thread.ID, AgentV2PromptInput{Content: "Say hello"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if seenPath != "/v1/responses" {
			t.Fatalf("unexpected endpoint path: %s", seenPath)
		}
		if res.LastMessage == nil || res.LastMessage.Reasoning != "Checking balance. " || res.LastMessage.Content != "Hello from OpenAI!" {
			t.Fatalf("unexpected response message: %+v", res.LastMessage)
		}
		if res.LastMessage.InputTokens != 42 || res.LastMessage.OutputTokens != 15 || res.LastMessage.CachedTokens != 4 {
			t.Fatalf("unexpected usage metrics: %+v", res.LastMessage)
		}

		// Credential echo in SSE stream must be refused by guardedAIBody and never persisted.
		echoSecret = true
		if _, err := s.RunAgentV2Turn(t.Context(), thread.ID, AgentV2PromptInput{Content: "Echo secret check"}, nil); err == nil {
			t.Fatal("expected echoed credential stream to be refused")
		}
		detail, err := s.GetThread(t.Context(), thread.ID)
		if err != nil {
			t.Fatal(err)
		}
		rawDetail, _ := json.Marshal(detail)
		if strings.Contains(string(rawDetail), testAPIKey) {
			t.Fatal("credential key persisted into thread detail after stream refusal")
		}

		// Locked connection must fail closed before calling upstream transport.
		s.lockAIConnection()
		calledWhenLocked := false
		s.agentV2TransportOverride = aiTestTransport(func(*http.Request) (*http.Response, error) {
			calledWhenLocked = true
			return nil, nil
		})
		if _, err := s.RunAgentV2Turn(t.Context(), thread.ID, AgentV2PromptInput{Content: "Retry"}, nil); err == nil || calledWhenLocked {
			t.Fatalf("locked connection must refuse turn without calling upstream: err=%v called=%v", err, calledWhenLocked)
		}
	})

	t.Run("OpenAI-ChatGPT-SignIn-OmitAPIOnlyFields", func(t *testing.T) {
		t.Parallel()
		s := fixtureStore(t)
		signIn := completeSignInFixture(t, s, false)

		var payload map[string]any
		s.agentV2TransportOverride = aiTestTransport(func(r *http.Request) (*http.Response, error) {
			raw, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			return sseHTTPResponse(
				`event: response.created`,
				`data: {"type":"response.created","response":{"id":"resp_signin_1"}}`,
				``,
				`event: response.output_text.delta`,
				`data: {"type":"response.output_text.delta","delta":"ChatGPT plan streaming ready."}`,
				``,
				`event: response.completed`,
				`data: {"type":"response.completed","response":{"id":"resp_signin_1","status":"completed","usage":{"input_tokens":20,"output_tokens":7}}}`,
			), nil
		})

		thread, err := s.CreateThread(t.Context(), "wen-operator", "SignIn", signIn.Ref, admittedOpenAIModel)
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.RunAgentV2Turn(t.Context(), thread.ID, AgentV2PromptInput{Content: "Test ChatGPT plan"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if res.LastMessage == nil || res.LastMessage.Content != "ChatGPT plan streaming ready." {
			t.Fatalf("unexpected response: %+v", res.LastMessage)
		}
		if payload["store"] != false || payload["stream"] != true {
			t.Fatalf("expected store:false stream:true, got %+v", payload)
		}
		for _, forbidden := range []string{"max_output_tokens", "background", "service_tier", "truncation", "prompt_cache_options"} {
			if _, exists := payload[forbidden]; exists {
				t.Fatalf("forbidden field %q present on ChatGPT sign-in payload", forbidden)
			}
		}
	})

	t.Run("Anthropic-Messages-ToolLoop-StrictAlternation", func(t *testing.T) {
		t.Parallel()
		s := fixtureStore(t)
		antKey := "fased-test-disposable-anthropic-connection-key"
		conn, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
			Action:     "add",
			Provider:   "anthropic",
			Key:        antKey,
			Passphrase: testVaultPassword,
		})
		if err != nil {
			t.Fatal(err)
		}

		round := 0
		var round2Messages []map[string]any
		s.agentV2TransportOverride = aiTestTransport(func(r *http.Request) (*http.Response, error) {
			round++
			if r.URL.Path != "/v1/messages" {
				t.Fatalf("unexpected Anthropic path: %s", r.URL.Path)
			}
			if r.Header.Get("x-api-key") != antKey || r.Header.Get("anthropic-version") != "2026-10-21" {
				t.Fatalf("missing Anthropic auth/version headers: %+v", r.Header)
			}
			raw, _ := io.ReadAll(r.Body)
			var body struct {
				Messages []map[string]any `json:"messages"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			if round == 1 {
				return sseHTTPResponse(
					`event: message_start`,
					`data: {"type":"message_start","message":{"id":"msg_ant_1","usage":{"input_tokens":50,"cache_read_input_tokens":10}}}`,
					``,
					`event: content_block_start`,
					`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`,
					``,
					`event: content_block_delta`,
					`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Inspecting status"}}`,
					``,
					`event: content_block_start`,
					`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"workspace_status"}}`,
					``,
					`event: content_block_delta`,
					`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
					``,
					`event: message_delta`,
					`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":12}}`,
					``,
					`event: message_stop`,
					`data: {"type":"message_stop"}`,
				), nil
			}
			round2Messages = body.Messages
			return sseHTTPResponse(
				`event: message_start`,
				`data: {"type":"message_start","message":{"id":"msg_ant_2","usage":{"input_tokens":95,"cache_read_input_tokens":10}}}`,
				``,
				`event: content_block_start`,
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
				``,
				`event: content_block_delta`,
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Workspace verified on Anthropic."}}`,
				``,
				`event: message_delta`,
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":8}}`,
				``,
				`event: message_stop`,
				`data: {"type":"message_stop"}`,
			), nil
		})

		thread, err := s.CreateThread(t.Context(), "wen-operator", "Anthropic", conn.Ref, "")
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.RunAgentV2Turn(t.Context(), thread.ID, AgentV2PromptInput{Content: "Check workspace status"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if round != 2 || res.LastMessage == nil || res.LastMessage.Content != "Workspace verified on Anthropic." {
			t.Fatalf("unexpected Anthropic result: round=%d msg=%+v", round, res.LastMessage)
		}
		// Verify strict user -> assistant(tool_use) -> user(tool_result) alternation.
		if len(round2Messages) != 3 || round2Messages[0]["role"] != "user" || round2Messages[1]["role"] != "assistant" || round2Messages[2]["role"] != "user" {
			t.Fatalf("broken Anthropic alternation: %+v", round2Messages)
		}
	})

	t.Run("Compatible-OpenRouter-Streamed-Tools", func(t *testing.T) {
		t.Parallel()
		s := fixtureStore(t)
		orKey := "fased-test-disposable-openrouter-key"
		conn, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
			Action:     "add",
			Provider:   "openrouter",
			Key:        orKey,
			Passphrase: testVaultPassword,
		})
		if err != nil {
			t.Fatal(err)
		}

		round := 0
		s.agentV2TransportOverride = aiTestTransport(func(r *http.Request) (*http.Response, error) {
			round++
			if r.URL.Path != "/api/v1/chat/completions" {
				t.Fatalf("unexpected OpenRouter path: %s", r.URL.Path)
			}
			if round == 1 {
				return sseHTTPResponse(
					`data: {"id":"chatcmpl_1","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_or_1","type":"function","function":{"name":"list_","arguments":""}}]}}]}`,
					`data: {"id":"chatcmpl_1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"wallets","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":30,"completion_tokens":9}}`,
					`data: [DONE]`,
				), nil
			}
			return sseHTTPResponse(
				`data: {"id":"chatcmpl_2","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"No keys in view. ","content":"OpenRouter saw 0 wallets."},"finish_reason":"stop"}],"usage":{"prompt_tokens":75,"completion_tokens":11}}`,
				`data: [DONE]`,
			), nil
		})

		thread, err := s.CreateThread(t.Context(), "trading-analyst", "OpenRouter", conn.Ref, "")
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.RunAgentV2Turn(t.Context(), thread.ID, AgentV2PromptInput{Content: "List wallets"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if round != 2 || res.LastMessage == nil || res.LastMessage.Content != "OpenRouter saw 0 wallets." || res.LastMessage.Reasoning != "No keys in view. " {
			t.Fatalf("unexpected compatible provider result: round=%d msg=%+v", round, res.LastMessage)
		}
	})
}

func TestAgentV2SSEToolLoopAndApprovals(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	conn := saveTestConnection(t, s)

	// Seed a read-only/watch wallet in accounts and a task in agent_workspace.
	acctView, err := s.Accounts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAccounts(t.Context(), AccountsRecord{
		Version:  acctView.Record.Version,
		Revision: acctView.Record.Revision,
		Accounts: []Account{{
			Version:   1,
			ID:        "0123456789abcdef0123456789abcdef",
			Name:      "Main Watch",
			Mode:      "watch",
			Network:   signer.FixtureNetwork,
			PublicKey: strings.Repeat("a", 64),
		}},
		Proposals: []ProposalDraft{},
	}); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	turnSeq := 0
	s.agentV2TransportOverride = aiTestTransport(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		turnSeq++
		step := turnSeq
		mu.Unlock()
		switch step {
		case 1:
			// Turn 1 Round 1: two Tier-1 read tools in parallel.
			return sseHTTPResponse(
				`event: response.output_item.added`,
				`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_w","name":"list_wallets","arguments":""}}`,
				``,
				`event: response.function_call_arguments.delta`,
				`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"{}"}`,
				``,
				`event: response.output_item.added`,
				`data: {"type":"response.output_item.added","output_index":1,"item":{"id":"fc_2","type":"function_call","call_id":"call_s","name":"workspace_status","arguments":"{}"}}`,
				``,
				`event: response.completed`,
				`data: {"type":"response.completed","response":{"id":"r1","status":"completed","usage":{"input_tokens":40,"output_tokens":10}}}`,
			), nil
		case 2:
			// Turn 1 Round 2: text response synthesizing Tier-1 tool outputs.
			return sseHTTPResponse(
				`event: response.output_text.delta`,
				`data: {"type":"response.output_text.delta","delta":"Checked Main Watch; "}`,
				``,
				`event: response.output_text.delta`,
				`data: {"type":"response.output_text.delta","delta":"signing disabled."}`,
				``,
				`event: response.completed`,
				`data: {"type":"response.completed","response":{"id":"r2","status":"completed","usage":{"input_tokens":80,"output_tokens":12}}}`,
			), nil
		case 3:
			// Turn 2: proposes Tier-2 draft_swap (must pause for owner approval).
			return sseHTTPResponse(
				`event: response.output_item.added`,
				`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"fc_3","type":"function_call","call_id":"call_swap","name":"draft_swap","arguments":"{\"fromToken\":\"USDC\",\"toToken\":\"SOL\",\"amount\":\"25\",\"network\":\"solana-mainnet\",\"maxSlippageBps\":50,\"rationale\":\"Dollar cost average\"}"}}`,
				``,
				`event: response.completed`,
				`data: {"type":"response.completed","response":{"id":"r3","status":"completed","usage":{"input_tokens":90,"output_tokens":20}}}`,
			), nil
		case 4:
			// Turn 2 resumed after approval.
			return sseHTTPResponse(
				`event: response.output_text.delta`,
				`data: {"type":"response.output_text.delta","delta":"Swap draft recorded as review-only."}`,
				``,
				`event: response.completed`,
				`data: {"type":"response.completed","response":{"id":"r4","status":"completed","usage":{"input_tokens":110,"output_tokens":9}}}`,
			), nil
		case 5:
			// Turn 3: proposes Tier-2 draft_strategy (owner will reject).
			return sseHTTPResponse(
				`event: response.output_item.added`,
				`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"fc_5","type":"function_call","call_id":"call_strat","name":"draft_strategy","arguments":"{\"name\":\"SOL Grid\",\"assetPair\":\"SOL/USDC\",\"thesis\":\"Range mean reversion\",\"entryRule\":\"RSI < 30\",\"exitRule\":\"RSI > 60\"}"}}`,
				``,
				`event: response.completed`,
				`data: {"type":"response.completed","response":{"id":"r5","status":"completed","usage":{"input_tokens":100,"output_tokens":18}}}`,
			), nil
		default:
			// Turn 3 resumed after rejection.
			return sseHTTPResponse(
				`event: response.output_text.delta`,
				`data: {"type":"response.output_text.delta","delta":"Understood, strategy draft discarded."}`,
				``,
				`event: response.completed`,
				`data: {"type":"response.completed","response":{"id":"r6","status":"completed","usage":{"input_tokens":120,"output_tokens":8}}}`,
			), nil
		}
	})

	ts, token, csrf := startV2HTTPTestServer(t, s)

	// Unauthenticated request rejected with 401.
	unauthResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/agents/v2/threads", ts.URL, "", "", "", "application/json")
	unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthenticated, got %d", unauthResp.StatusCode)
	}

	// Missing CSRF rejected with 403.
	noCSRF := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/threads", ts.URL, token, "", `{"profileId":"trading-analyst"}`, "application/json")
	noCSRF.Body.Close()
	if noCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 missing CSRF, got %d", noCSRF.StatusCode)
	}

	// GET /tools lists both read and action tools.
	toolsResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/agents/v2/tools", ts.URL, token, "", "", "application/json")
	var toolsBody struct {
		Tools []AgentToolSpec `json:"tools"`
	}
	if err := json.NewDecoder(toolsResp.Body).Decode(&toolsBody); err != nil {
		t.Fatal(err)
	}
	toolsResp.Body.Close()
	if len(toolsBody.Tools) != 15 {
		t.Fatalf("expected 15 registered tools, got %d", len(toolsBody.Tools))
	}

	// Create thread -> 201.
	createResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/threads", ts.URL, token, csrf, `{"profileId":"trading-analyst","connectionRef":"`+conn.Ref+`"}`, "application/json")
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating thread, got %d", createResp.StatusCode)
	}
	var created struct {
		Thread AgentThread `json:"thread"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	createResp.Body.Close()
	threadID := created.Thread.ID

	// Turn 1 over SSE: auto-runs list_wallets + workspace_status, streams reply.
	msgResp1 := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/threads/"+threadID+"/messages", ts.URL, token, csrf, `{"content":"Check my wallets and workspace status"}`, "text/event-stream")
	if msgResp1.StatusCode != http.StatusOK || !strings.HasPrefix(msgResp1.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("expected SSE 200, got %d (%s)", msgResp1.StatusCode, msgResp1.Header.Get("Content-Type"))
	}
	events1 := readV2SSEEvents(t, msgResp1.Body)
	msgResp1.Body.Close()

	var sawToolResult, sawDone bool
	for _, ev := range events1 {
		if ev.Type == "tool_result" && ev.ToolCall != nil && ev.ToolCall.ToolName == "list_wallets" {
			if !strings.Contains(ev.ToolCall.ResultJSON, "Main Watch") || !strings.Contains(ev.ToolCall.ResultJSON, `"signingEnabled":false`) {
				t.Fatalf("unexpected list_wallets tool result: %s", ev.ToolCall.ResultJSON)
			}
			sawToolResult = true
		}
		if ev.Type == "message_done" && !ev.AwaitingApproval {
			sawDone = true
		}
	}
	if !sawToolResult || !sawDone {
		t.Fatalf("missing expected SSE events in turn 1: %+v", events1)
	}

	// Turn 2 over SSE: pauses at draft_swap approval_required.
	msgResp2 := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/threads/"+threadID+"/messages", ts.URL, token, csrf, `{"content":"Prepare swap 25 USDC to SOL"}`, "text/event-stream")
	events2 := readV2SSEEvents(t, msgResp2.Body)
	msgResp2.Body.Close()

	var swapApprovalID string
	for _, ev := range events2 {
		if ev.Type == "approval_required" && ev.Approval != nil {
			swapApprovalID = ev.Approval.ID
		}
	}
	if swapApprovalID == "" {
		t.Fatalf("expected approval_required event, got %+v", events2)
	}
	// Before approval, zero swap_draft memories exist.
	beforeSwapMems, err := s.SearchMemories(t.Context(), "trading-analyst", "Swap draft", 10)
	if err != nil || len(beforeSwapMems) != 0 {
		t.Fatalf("swap draft executed before owner approval: %+v err=%v", beforeSwapMems, err)
	}

	// Missing CSRF on approval POST -> 403.
	apNoCSRF := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/approvals/"+swapApprovalID, ts.URL, token, "", `{"decision":"approve"}`, "application/json")
	apNoCSRF.Body.Close()
	if apNoCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 missing CSRF on approval, got %d", apNoCSRF.StatusCode)
	}

	// Missing approval ID -> 404.
	apMissing := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/approvals/nonexistent-id-123", ts.URL, token, csrf, `{"decision":"approve"}`, "application/json")
	apMissing.Body.Close()
	if apMissing.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown approval, got %d", apMissing.StatusCode)
	}

	// Invalid decision -> 400.
	apInvalid := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/approvals/"+swapApprovalID, ts.URL, token, csrf, `{"decision":"maybe"}`, "application/json")
	apInvalid.Body.Close()
	if apInvalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 invalid decision, got %d", apInvalid.StatusCode)
	}

	// Valid approve over SSE: executes non-custodial swap draft and resumes stream.
	apResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/approvals/"+swapApprovalID, ts.URL, token, csrf, `{"decision":"approve","note":"ok"}`, "text/event-stream")
	if apResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 approving swap, got %d", apResp.StatusCode)
	}
	apEvents := readV2SSEEvents(t, apResp.Body)
	apResp.Body.Close()

	var sawSwapExecuted, sawResumedText bool
	for _, ev := range apEvents {
		if ev.Type == "tool_result" && ev.ToolCall != nil && ev.ToolCall.Status == "completed" {
			if !strings.Contains(ev.ToolCall.ResultJSON, `"liveExecution":false`) || !strings.Contains(ev.ToolCall.ResultJSON, `"signed":false`) {
				t.Fatalf("swap draft missing non-custodial markers: %s", ev.ToolCall.ResultJSON)
			}
			sawSwapExecuted = true
		}
		if ev.Type == "message_done" && ev.Message != nil && strings.Contains(ev.Message.Content, "review-only") {
			sawResumedText = true
		}
	}
	if !sawSwapExecuted || !sawResumedText {
		t.Fatalf("expected swap execution & resumed reply, got %+v", apEvents)
	}
	afterSwapMems, err := s.SearchMemories(t.Context(), "trading-analyst", "USDC SOL", 10)
	if err != nil || len(afterSwapMems) != 1 || afterSwapMems[0].Scope != "swap_draft" {
		t.Fatalf("expected persisted swap_draft memory after approve, got %+v err=%v", afterSwapMems, err)
	}

	// Replaying already-decided approval -> 409 Conflict.
	apReplay := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/approvals/"+swapApprovalID, ts.URL, token, csrf, `{"decision":"approve"}`, "application/json")
	apReplay.Body.Close()
	if apReplay.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 replaying approval, got %d", apReplay.StatusCode)
	}

	// Turn 3 + Reject path over JSON mode (Accept: application/json).
	msgResp3 := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/threads/"+threadID+"/messages", ts.URL, token, csrf, `{"content":"Propose strategy"}`, "application/json")
	var run3 AgentV2RunResult
	if err := json.NewDecoder(msgResp3.Body).Decode(&run3); err != nil {
		t.Fatal(err)
	}
	msgResp3.Body.Close()
	if !run3.AwaitingApproval || len(run3.PendingApprovals) != 1 {
		t.Fatalf("expected pending strategy approval, got %+v", run3)
	}
	rejResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/approvals/"+run3.PendingApprovals[0].ID, ts.URL, token, csrf, `{"decision":"reject","note":"too volatile"}`, "application/json")
	if rejResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 rejecting approval, got %d", rejResp.StatusCode)
	}
	var rejRes AgentV2ApprovalResult
	if err := json.NewDecoder(rejResp.Body).Decode(&rejRes); err != nil {
		t.Fatal(err)
	}
	rejResp.Body.Close()
	if rejRes.ToolCall.Status != "rejected" || rejRes.ResumedMessage == nil || !strings.Contains(rejRes.ResumedMessage.Content, "discarded") {
		t.Fatalf("unexpected reject outcome: %+v", rejRes)
	}
	stratMems, _ := s.SearchMemories(t.Context(), "trading-analyst", "SOL Grid", 10)
	if len(stratMems) != 0 {
		t.Fatalf("rejected strategy still created memory: %+v", stratMems)
	}
}

func TestAgentV2CompactionAndDisconnectCancel(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	conn := saveTestConnection(t, s)

	thread, err := s.CreateThread(t.Context(), "wen-operator", "Long thread", conn.Ref, "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 28; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if _, err := s.AppendMessage(t.Context(), AgentV2Message{
			ThreadID: thread.ID,
			Role:     role,
			Content:  strings.Repeat("x", 120) + " turn",
		}); err != nil {
			t.Fatal(err)
		}
	}
	detail, err := s.GetThread(t.Context(), thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	summary, window, err := s.buildContextWindow(t.Context(), detail)
	if err != nil {
		t.Fatal(err)
	}
	if summary == "" || len(window) > agentV2KeepRecentMsgs {
		t.Fatalf("expected rolling summary compaction, summary=%q window=%d", summary, len(window))
	}
	reloaded, err := s.GetThread(t.Context(), thread.ID)
	if err != nil || len(reloaded.Messages) != 28 || reloaded.Thread.Summary == "" {
		t.Fatalf("historical rows lost or summary not persisted: len=%d summary=%q err=%v", len(reloaded.Messages), reloaded.Thread.Summary, err)
	}

	// Disconnect cancellation test: slow upstream stream aborts immediately when client cancels.
	started := make(chan struct{})
	aborted := make(chan struct{})
	s.agentV2TransportOverride = aiTestTransport(func(r *http.Request) (*http.Response, error) {
		pr, pw := io.Pipe()
		go func() {
			defer pw.Close()
			_, _ = io.WriteString(pw, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Working...\"}\n\n")
			close(started)
			<-r.Context().Done()
			close(aborted)
		}()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream; charset=utf-8"}},
			Body:       pr,
		}, nil
	})

	ts, token, csrf := startV2HTTPTestServer(t, s)
	reqCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, ts.URL+"/api/v1/agents/v2/threads/"+thread.ID+"/messages", strings.NewReader(`{"content":"Stream slow reply"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", ts.URL)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream stream never started")
	}
	cancel()
	select {
	case <-aborted:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream provider context did not cancel after SSE client disconnect")
	}
}

func TestAgentV2FixtureMode(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	ts, token, csrf := startV2HTTPTestServer(t, s)

	// No AI connection configured: fixtureStore runs deterministic in-process fixture provider.
	cResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/threads", ts.URL, token, csrf, `{"profileId":"wen-operator"}`, "application/json")
	if cResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating fixture thread, got %d", cResp.StatusCode)
	}
	var created struct {
		Thread AgentThread `json:"thread"`
	}
	if err := json.NewDecoder(cResp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	cResp.Body.Close()

	// Wallet prompt exercises Tier-1 auto execution and chunked streaming.
	wResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/threads/"+created.Thread.ID+"/messages", ts.URL, token, csrf, `{"content":"Check my wallet list"}`, "text/event-stream")
	wEvents := readV2SSEEvents(t, wResp.Body)
	wResp.Body.Close()
	sawDelta := false
	for _, ev := range wEvents {
		if ev.Type == "text_delta" && ev.Delta != "" {
			sawDelta = true
		}
	}
	if !sawDelta {
		t.Fatalf("expected streamed fixture text_delta events, got %+v", wEvents)
	}

	// Swap prompt triggers Tier-2 draft_swap approval pause -> approve -> resume.
	sResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/threads/"+created.Thread.ID+"/messages", ts.URL, token, csrf, `{"content":"Propose a small swap"}`, "application/json")
	var swapRun AgentV2RunResult
	if err := json.NewDecoder(sResp.Body).Decode(&swapRun); err != nil {
		t.Fatal(err)
	}
	sResp.Body.Close()
	if !swapRun.AwaitingApproval || len(swapRun.PendingApprovals) != 1 || swapRun.PendingApprovals[0].ToolName != "draft_swap" {
		t.Fatalf("expected fixture draft_swap approval pause, got %+v", swapRun)
	}

	apResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/approvals/"+swapRun.PendingApprovals[0].ID, ts.URL, token, csrf, `{"decision":"approve"}`, "application/json")
	var apOut AgentV2ApprovalResult
	if err := json.NewDecoder(apResp.Body).Decode(&apOut); err != nil {
		t.Fatal(err)
	}
	apResp.Body.Close()
	if apOut.Approval.Status != "approved" || apOut.ToolCall.Status != "completed" || apOut.ResumedMessage == nil {
		t.Fatalf("unexpected fixture approval result: %+v", apOut)
	}
}

func TestAgentV2MultiProviderParity(t *testing.T) {
	t.Parallel()

	t.Run("Anthropic-Discovered-DisplayName-Tools", func(t *testing.T) {
		t.Parallel()
		s := fixtureStore(t)
		ant := addNativeAccount(t, s, "anthropic", false)
		v, err := s.refreshProviderModels(t.Context(), ant.Ref, aiTestTransport(func(r *http.Request) (*http.Response, error) {
			return fixtureAdmissionResponse(r, map[string]any{
				"data": []map[string]any{{
					"id":           "claude-sonnet-6-0",
					"type":         "model",
					"lifecycle":    "active",
					"line":         "sonnet",
					"display_name": "Claude Sonnet 6.0",
					"created_at":   "2026-10-08T00:00:00Z",
				}},
				"has_more": false,
				"last_id":  "claude-sonnet-6-0",
			}), nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		if m := modelChoice(v, "claude-sonnet-6-0"); !m.Selectable || m.Name != "Claude Sonnet 6.0" {
			t.Fatalf("discovered Anthropic model missing name: %+v", m)
		}

		round := 0
		s.agentV2TransportOverride = aiTestTransport(func(r *http.Request) (*http.Response, error) {
			round++
			if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") == "" {
				t.Fatalf("unexpected Anthropic request: %s %s", r.Method, r.URL.Path)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["model"] != "claude-sonnet-6-0" {
				t.Fatalf("expected discovered Anthropic model, got %v", body["model"])
			}
			if round == 1 {
				return sseHTTPResponse(
					"event: message_start",
					`data: {"type":"message_start","message":{"id":"msg_ant_p","usage":{"input_tokens":30}}}`,
					"",
					"event: content_block_start",
					`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_p1","name":"list_wallets"}}`,
					"",
					"event: content_block_delta",
					`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
					"",
					"event: message_delta",
					`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":8}}`,
					"",
					"event: message_stop",
					`data: {"type":"message_stop"}`,
				), nil
			}
			return sseHTTPResponse(
				"event: message_start",
				`data: {"type":"message_start","message":{"id":"msg_ant_p2","usage":{"input_tokens":55}}}`,
				"",
				"event: content_block_start",
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				"",
				"event: content_block_delta",
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Claude Sonnet 6.0 checked wallets."}}`,
				"",
				"event: message_delta",
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":9}}`,
				"",
				"event: message_stop",
				`data: {"type":"message_stop"}`,
			), nil
		})

		thread, err := s.CreateThread(t.Context(), "trading-analyst", "Anthropic Dyn", ant.Ref, "claude-sonnet-6-0")
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.RunAgentV2Turn(t.Context(), thread.ID, AgentV2PromptInput{Content: "List my wallets"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if round != 2 || res.LastMessage == nil || res.LastMessage.Content != "Claude Sonnet 6.0 checked wallets." {
			t.Fatalf("unexpected Anthropic V2 result: round=%d msg=%+v", round, res.LastMessage)
		}
	})

	t.Run("Google-APIKey-And-OAuth-Tools", func(t *testing.T) {
		t.Parallel()
		s := fixtureStore(t)
		apiConn := addNativeAccount(t, s, "google", false)
		oauthConn := connectGoogleFixture(t, s, true)

		var seenHeaders []string
		round := 0
		s.agentV2TransportOverride = aiTestTransport(func(r *http.Request) (*http.Response, error) {
			round++
			if r.URL.Path != "/v1beta/openai/chat/completions" {
				t.Fatalf("unexpected Google V2 path: %s", r.URL.Path)
			}
			seenHeaders = append(seenHeaders, r.Header.Get("Authorization")+"|"+r.Header.Get("X-Goog-User-Project"))
			if round%2 == 1 {
				return sseHTTPResponse(
					`data: {"id":"g_1","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_g_1","type":"function","function":{"name":"list_wallets","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":5}}`,
					`data: [DONE]`,
				), nil
			}
			return sseHTTPResponse(
				`data: {"id":"g_2","choices":[{"index":0,"delta":{"role":"assistant","content":"Google V2 ok."},"finish_reason":"stop"}],"usage":{"prompt_tokens":40,"completion_tokens":6}}`,
				`data: [DONE]`,
			), nil
		})

		for _, tc := range []struct {
			name string
			ref  string
			want string
		}{
			{"api-key", apiConn.Ref, "Bearer fased-test-google-disposable-account|"},
			{"oauth", oauthConn.Ref, "Bearer fased-fixture-google-access-token|fased-fixture-project"},
		} {
			before := len(seenHeaders)
			thread, err := s.CreateThread(t.Context(), "trading-analyst", tc.name, tc.ref, "gemini-3.8-flash")
			if err != nil {
				t.Fatal(err)
			}
			res, err := s.RunAgentV2Turn(t.Context(), thread.ID, AgentV2PromptInput{Content: "Check wallets"}, nil)
			if err != nil {
				t.Fatalf("%s failed: %v", tc.name, err)
			}
			if res.LastMessage == nil || res.LastMessage.Content != "Google V2 ok." {
				t.Fatalf("%s bad output: %+v", tc.name, res.LastMessage)
			}
			if len(seenHeaders) != before+2 || seenHeaders[before] != tc.want {
				t.Fatalf("%s auth/project mismatch: got %v want %s", tc.name, seenHeaders[before:], tc.want)
			}
		}
	})

	t.Run("OllamaCloud-Strips-Prefix-And-OpenRouter-Dynamic", func(t *testing.T) {
		t.Parallel()
		s := fixtureStore(t)
		oc := addNativeAccount(t, s, "ollama-cloud", false)
		or := addNativeAccount(t, s, "openrouter", false)
		if _, err := s.refreshProviderModels(t.Context(), or.Ref, aiTestTransport(func(r *http.Request) (*http.Response, error) {
			return fixtureAdmissionResponse(r, map[string]any{
				"data": []map[string]any{{"id": "anthropic/claude-9-opus", "name": "Anthropic: Claude 9 Opus", "created": 1744588800}},
			}), nil
		})); err != nil {
			t.Fatal(err)
		}

		var modelsSent []string
		s.agentV2TransportOverride = aiTestTransport(func(r *http.Request) (*http.Response, error) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			m, _ := body["model"].(string)
			modelsSent = append(modelsSent, r.URL.Path+":"+m)
			return sseHTTPResponse(
				`data: {"id":"c_1","choices":[{"index":0,"delta":{"role":"assistant","content":"Done."},"finish_reason":"stop"}],"usage":{"prompt_tokens":15,"completion_tokens":3}}`,
				`data: [DONE]`,
			), nil
		})

		t1, err := s.CreateThread(t.Context(), "trading-analyst", "Ollama Cloud", oc.Ref, "ollama-cloud/gemma4:31b")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.RunAgentV2Turn(t.Context(), t1.ID, AgentV2PromptInput{Content: "Hi"}, nil); err != nil {
			t.Fatal(err)
		}
		t2, err := s.CreateThread(t.Context(), "trading-analyst", "OpenRouter Dyn", or.Ref, "anthropic/claude-9-opus")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.RunAgentV2Turn(t.Context(), t2.ID, AgentV2PromptInput{Content: "Hi"}, nil); err != nil {
			t.Fatal(err)
		}
		if len(modelsSent) != 2 || modelsSent[0] != "/v1/chat/completions:gemma4:31b" || modelsSent[1] != "/api/v1/chat/completions:anthropic/claude-9-opus" {
			t.Fatalf("unexpected remote wire models: %+v", modelsSent)
		}
	})

	t.Run("LocalOllama-Picks-Live-Model-And-Falls-Back-To-ChatCompletions", func(t *testing.T) {
		t.Parallel()
		s := fixtureStore(t)
		loc := addLocalFixtureAccount(t, s)
		if _, err := s.refreshProviderModels(t.Context(), loc.Ref, aiTestTransport(func(r *http.Request) (*http.Response, error) {
			return fixtureAdmissionResponse(r, map[string]any{
				"data": []map[string]any{{"id": "qwen3:32b", "name": "Qwen 3 32B", "owned_by": "library", "created": 1}},
			}), nil
		})); err != nil {
			t.Fatal(err)
		}

		// When curated gemma4:e2b is absent, resolver picks live discovered qwen3:32b automatically.
		resolved, err := resolveAgentV2Provider(t.Context(), s, loc.Ref, "", "", "", "wen-operator")
		if err != nil || resolved.Model != "qwen3:32b" {
			t.Fatalf("expected live discovered qwen3:32b, got %+v err=%v", resolved, err)
		}

		// Case A: /v1/responses natively succeeds.
		var sawNativeResponses bool
		s.agentV2TransportOverride = aiTestTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/v1/responses" {
				sawNativeResponses = true
				return sseHTTPResponse(
					"event: response.output_text.delta",
					`data: {"type":"response.output_text.delta","delta":"Native responses ok."}`,
					"",
					"event: response.completed",
					`data: {"type":"response.completed","response":{"id":"resp_loc_1","status":"completed","usage":{"input_tokens":12,"output_tokens":4}}}`,
				), nil
			}
			return nil, errors.New("unexpected fallback")
		})
		threadA, err := s.CreateThread(t.Context(), "wen-operator", "Local Native", loc.Ref, "")
		if err != nil {
			t.Fatal(err)
		}
		resA, err := s.RunAgentV2Turn(t.Context(), threadA.ID, AgentV2PromptInput{Content: "Hello"}, nil)
		if err != nil || !sawNativeResponses || resA.LastMessage == nil || resA.LastMessage.Content != "Native responses ok." {
			t.Fatalf("local /v1/responses failed: err=%v saw=%v msg=%+v", err, sawNativeResponses, resA.LastMessage)
		}

		// Case B: /v1/responses returns 404 (older Ollama server) -> transparent fallback to /v1/chat/completions + tools.
		var calls []string
		round := 0
		s.agentV2TransportOverride = aiTestTransport(func(r *http.Request) (*http.Response, error) {
			calls = append(calls, r.URL.Path)
			if r.URL.Path == "/v1/responses" {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"404 page not found"}}`)),
					Request:    r,
				}, nil
			}
			if r.URL.Path != "/v1/chat/completions" {
				t.Fatalf("unexpected fallback path: %s", r.URL.Path)
			}
			round++
			if round == 1 {
				return sseHTTPResponse(
					`data: {"id":"ol_1","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_ol_1","type":"function","function":{"name":"list_wallets","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":5}}`,
					`data: [DONE]`,
				), nil
			}
			return sseHTTPResponse(
				`data: {"id":"ol_2","choices":[{"index":0,"delta":{"role":"assistant","content":"Ollama chat fallback checked wallets."},"finish_reason":"stop"}],"usage":{"prompt_tokens":45,"completion_tokens":9}}`,
				`data: [DONE]`,
			), nil
		})
		threadB, err := s.CreateThread(t.Context(), "wen-operator", "Local Fallback", loc.Ref, "")
		if err != nil {
			t.Fatal(err)
		}
		resB, err := s.RunAgentV2Turn(t.Context(), threadB.ID, AgentV2PromptInput{Content: "List my wallets"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if resB.LastMessage == nil || resB.LastMessage.Content != "Ollama chat fallback checked wallets." {
			t.Fatalf("unexpected Ollama fallback answer: %+v", resB.LastMessage)
		}
		// 2 rounds of /v1/responses (404) -> /v1/chat/completions (tool_use + final answer).
		if strings.Join(calls, ",") != "/v1/responses,/v1/chat/completions,/v1/responses,/v1/chat/completions" {
			t.Fatalf("unexpected fallback sequence: %+v", calls)
		}
	})
}

func TestAgentV2PauseSlotsAndApprovalErrorPropagation(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	s.providerFixture = true
	ts, token, csrf := startV2HTTPTestServer(t, s)
	h := ts.Config.Handler.(*HTTPHandler)

	thread, err := s.CreateThread(t.Context(), "trading-analyst", "Guardrails", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// 1. aiSlots saturated returns 429 on /messages.
	h.aiSlots <- struct{}{}
	h.aiSlots <- struct{}{}
	slotResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/threads/"+thread.ID+"/messages", ts.URL, token, csrf, `{"content":"hello"}`, "application/json")
	slotResp.Body.Close()
	<-h.aiSlots
	<-h.aiSlots
	if slotResp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 when aiSlots full, got %d", slotResp.StatusCode)
	}

	// 2. Non-SSE ResolveAgentV2Approval propagates resume errors as 400 rather than returning 200.
	swapResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/threads/"+thread.ID+"/messages", ts.URL, token, csrf, `{"content":"Propose a small swap"}`, "application/json")
	var swapRun AgentV2RunResult
	if err := json.NewDecoder(swapResp.Body).Decode(&swapRun); err != nil {
		t.Fatal(err)
	}
	swapResp.Body.Close()
	if swapResp.StatusCode != http.StatusOK || !swapRun.AwaitingApproval || len(swapRun.PendingApprovals) != 1 {
		t.Fatalf("expected draft_swap pending approval, status=%d run=%+v", swapResp.StatusCode, swapRun)
	}

	// Disable fixture provider before resuming so resolveAgentV2Provider errors in JSON mode (emit == nil).
	s.providerFixture = false
	apResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/approvals/"+swapRun.PendingApprovals[0].ID, ts.URL, token, csrf, `{"decision":"approve","resume":true}`, "application/json")
	apResp.Body.Close()
	if apResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 when non-SSE approval resume fails, got %d", apResp.StatusCode)
	}

	// 3. v2Mutation returns 409 when core is paused for update/backup.
	_ = pausedBackup(t, s)
	pausedResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/threads/"+thread.ID+"/messages", ts.URL, token, csrf, `{"content":"while paused"}`, "application/json")
	pausedResp.Body.Close()
	if pausedResp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 when core paused, got %d", pausedResp.StatusCode)
	}
}

func TestAgentV2ToolGetPortfolioSummary(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)

	// Configure a watch wallet without RPC so portfolio summary cleanly summarizes status & counts.
	view, err := s.Accounts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	s.rpcAdmission = true
	if _, err := s.UpdateAccounts(t.Context(), AccountsRecord{
		Version:  view.Record.Version,
		Revision: view.Record.Revision,
		Accounts: []Account{{
			Version:   2,
			ID:        "11111111111111111111111111111111",
			Name:      "Primary Watch",
			Mode:      "watch",
			Network:   "mainnet-beta",
			PublicKey: strings.Repeat("11", 32),
		}},
		Proposals: []ProposalDraft{},
	}); err != nil {
		t.Fatal(err)
	}
	s.rpcAdmission = false

	entry, ok := lookupAgentV2Tool("get_portfolio_summary")
	if !ok || entry.spec.Tier != toolTierRead {
		t.Fatalf("get_portfolio_summary tool missing or wrong tier: ok=%v spec=%+v", ok, entry.spec)
	}
	resAny, err := entry.execute(t.Context(), s, "trading-analyst", json.RawMessage(`{"network":"mainnet-beta"}`))
	if err != nil {
		t.Fatalf("get_portfolio_summary failed: %v", err)
	}
	res := resAny.(map[string]any)
	if res["walletCount"] != 1 || res["signingEnabled"] != false {
		t.Fatalf("unexpected portfolio summary: %+v", res)
	}
	wallets := res["wallets"].([]map[string]any)
	if len(wallets) != 1 || wallets[0]["name"] != "Primary Watch" || wallets[0]["status"] != "rpc_not_configured" {
		t.Fatalf("unexpected wallet entry: %+v", wallets)
	}
}

func TestAgentV2ToolFetchWebPageAndSSRFGuards(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	s.marketDataTransport = aiTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/redirect-ssrf" {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"https://169.254.169.254/latest/meta-data/"}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    r,
			}, nil
		}
		htmlBody := `<!doctype html><html><head><title>Solana &amp; Ecosystem Note</title><style>body{color:#fff}</style></head><body><script>alert("xss")</script><h1>Hello   World</h1><p>Bounded summary paragraph.</p></body></html>`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(htmlBody)),
			Request:    r,
		}, nil
	})

	entry, ok := lookupAgentV2Tool("fetch_web_page")
	if !ok || entry.spec.Tier != toolTierRead {
		t.Fatalf("fetch_web_page tool missing or wrong tier: ok=%v spec=%+v", ok, entry.spec)
	}
	resAny, err := entry.execute(t.Context(), s, "", json.RawMessage(`{"url":"https://docs.fased.invalid/guide","maxChars":500}`))
	if err != nil {
		t.Fatalf("fetch_web_page valid HTML failed: %v", err)
	}
	res := resAny.(map[string]any)
	if res["title"] != "Solana & Ecosystem Note" || res["untrusted"] != true {
		t.Fatalf("unexpected title/untrusted: %+v", res)
	}
	txt := res["text"].(string)
	if !strings.Contains(txt, "Hello World") || !strings.Contains(txt, "Bounded summary paragraph.") || strings.Contains(txt, "alert(") || strings.Contains(txt, "color:#fff") {
		t.Fatalf("script/style not stripped or text missing: %q", txt)
	}

	for _, badURL := range []string{
		"http://example.com/",
		"https://127.0.0.1/",
		"https://169.254.169.254/latest/meta-data/",
		"https://10.0.0.1/",
		"https://service.local/",
		"https://metadata.google.internal/",
		"https://docs.fased.invalid/redirect-ssrf",
	} {
		argsJSON, _ := json.Marshal(map[string]any{"url": badURL})
		if _, err := entry.execute(t.Context(), s, "", argsJSON); err == nil {
			t.Fatalf("SSRF guard failed to block %q", badURL)
		}
	}
}

func TestAgentV2MemoryUpdatePinnedPromptAndDraftFiltering(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)

	longRule := "Always cap single SOL trade allocation below 5 percent of total equity, enforce max slippage 40 bps on Jupiter routes, avoid illiquid tokens below 250k daily volume, and wait for two confirmations before logging entry fill status. UNIQUE_END_MARKER_260"
	if utf8.RuneCountInString(longRule) < 220 {
		t.Fatalf("test setup expected >220 chars, got %d", utf8.RuneCountInString(longRule))
	}

	pinnedMem, err := s.CreateMemory(t.Context(), MemoryInput{
		ProfileID: "trading-analyst",
		Scope:     "workspace",
		Title:     "Risk Cap",
		Content:   longRule,
		Pinned:    true,
		Source:    "user",
	})
	if err != nil {
		t.Fatal(err)
	}
	editableMem, err := s.CreateMemory(t.Context(), MemoryInput{
		ProfileID: "trading-analyst",
		Scope:     "workspace",
		Title:     "Venue Note",
		Content:   "Prefer alphaanchor aggregator before midnight.",
		Pinned:    false,
		Source:    "user",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"swap_draft", "task_draft", "strategy_draft"} {
		if _, err := s.CreateMemory(t.Context(), MemoryInput{
			ProfileID: "trading-analyst",
			Scope:     scope,
			Title:     "Draft: " + scope,
			Content:   `{"secret":"SHOULD_NOT_APPEAR_IN_PROMPT_DRAFT_LEAK"}`,
			Pinned:    true,
			Source:    "agent",
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Legacy expired V1 note in core.sqlite must be filtered out even if raw Expired bool is false.
	agentApply(t, s, AgentCommand{
		Action:    "memory-put",
		ProfileID: "trading-analyst",
		Text:      "EXPIRED_LEGACY_MARKER do not use outdated leverage setting",
		Source:    "manual",
		ExpiresAt: s.now().Unix() - 10,
	})
	legacyHits, err := s.SearchMemories(t.Context(), "trading-analyst", "EXPIRED_LEGACY_MARKER", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(legacyHits) != 0 {
		t.Fatalf("expected expired V1 memory to be excluded, got %+v", legacyHits)
	}

	// buildAgentV2Instructions injects pinned rules unconditionally on unrelated queries,
	// preserves >80 chars up to 500 runes, and excludes *_draft rows.
	instructions := buildAgentV2Instructions(t.Context(), s, "trading-analyst", "", "hello weather forecast today")
	if !strings.Contains(instructions, "Pinned · Risk Cap") || !strings.Contains(instructions, "UNIQUE_END_MARKER_260") {
		t.Fatalf("expected full >220 char pinned rule in instructions on unrelated query, got:\n%s", instructions)
	}
	if strings.Contains(instructions, "SHOULD_NOT_APPEAR_IN_PROMPT_DRAFT_LEAK") {
		t.Fatalf("draft scope leaked into instructions:\n%s", instructions)
	}

	// HTTP PATCH updates content/pinned and refreshes FTS5; excludeDrafts=1 excludes *_draft rows.
	ts, token, csrf := startV2HTTPTestServer(t, s)
	nextTitle := "Updated Venue Rule"
	nextContent := "Prefer betalimit routing after noon."
	nextPinned := true
	patchBytes, _ := json.Marshal(MemoryUpdateInput{
		Title:   &nextTitle,
		Content: &nextContent,
		Pinned:  &nextPinned,
	})
	patchResp := doV2JSONRequest(t, http.MethodPatch, ts.URL+"/api/v1/agents/v2/memories/"+editableMem.ID, ts.URL, token, csrf, string(patchBytes), "application/json")
	defer patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH memory expected 200, got %d", patchResp.StatusCode)
	}
	var patchBody struct {
		Memory AgentV2Memory `json:"memory"`
	}
	if err := json.NewDecoder(patchResp.Body).Decode(&patchBody); err != nil {
		t.Fatal(err)
	}
	if patchBody.Memory.Title != nextTitle || patchBody.Memory.Content != nextContent || !patchBody.Memory.Pinned || patchBody.Memory.ID != pinnedMem.ID && patchBody.Memory.ID != editableMem.ID {
		t.Fatalf("unexpected PATCH response: %+v", patchBody.Memory)
	}

	missingResp := doV2JSONRequest(t, http.MethodPatch, ts.URL+"/api/v1/agents/v2/memories/doesnotexist1234", ts.URL, token, csrf, string(patchBytes), "application/json")
	missingResp.Body.Close()
	if missingResp.StatusCode != http.StatusNotFound {
		t.Fatalf("PATCH unknown memory expected 404, got %d", missingResp.StatusCode)
	}

	oldHits, err := s.SearchMemories(t.Context(), "trading-analyst", "alphaanchor", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(oldHits) != 0 {
		t.Fatalf("expected old term alphaanchor removed from FTS5 after PATCH, got %+v", oldHits)
	}
	newHits, err := s.SearchMemories(t.Context(), "trading-analyst", "betalimit", 10)
	if err != nil || len(newHits) != 1 || newHits[0].ID != editableMem.ID {
		t.Fatalf("expected updated term betalimit in FTS5, got %+v err=%v", newHits, err)
	}

	allResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/agents/v2/memories?profileId=trading-analyst&limit=50", ts.URL, token, "", "", "application/json")
	defer allResp.Body.Close()
	var allBody struct {
		Memories []AgentV2Memory `json:"memories"`
	}
	if err := json.NewDecoder(allResp.Body).Decode(&allBody); err != nil {
		t.Fatal(err)
	}
	if len(allBody.Memories) < 5 {
		t.Fatalf("expected drafts included without excludeDrafts=1, got %d rows", len(allBody.Memories))
	}

	noDraftResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/agents/v2/memories?profileId=trading-analyst&excludeDrafts=1&limit=50", ts.URL, token, "", "", "application/json")
	defer noDraftResp.Body.Close()
	var noDraftBody struct {
		Memories []AgentV2Memory `json:"memories"`
	}
	if err := json.NewDecoder(noDraftResp.Body).Decode(&noDraftBody); err != nil {
		t.Fatal(err)
	}
	for _, m := range noDraftBody.Memories {
		if isV2DraftScope(m.Scope) {
			t.Fatalf("excludeDrafts=1 leaked draft row: %+v", m)
		}
	}
	if len(noDraftBody.Memories) != 2 {
		t.Fatalf("expected 2 non-draft memories with excludeDrafts=1, got %d (%+v)", len(noDraftBody.Memories), noDraftBody.Memories)
	}
}
