package core

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	if len(toolsBody.Tools) != 13 {
		t.Fatalf("expected 13 registered tools, got %d", len(toolsBody.Tools))
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
