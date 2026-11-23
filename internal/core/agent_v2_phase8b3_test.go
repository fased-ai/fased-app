package core

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/signer"
)

func createPendingApprovalFixture(t *testing.T, s *Store, threadID, toolName string, rawArgs json.RawMessage) AgentApproval {
	t.Helper()
	entry, ok := lookupAgentV2Tool(toolName)
	if !ok || entry.preview == nil {
		t.Fatalf("missing action preview tool %s", toolName)
	}
	draft, err := entry.preview(t.Context(), s, "operator", rawArgs)
	if err != nil {
		t.Fatalf("preview %s: %v", toolName, err)
	}
	msg, err := s.AppendMessage(t.Context(), AgentV2Message{
		ThreadID: threadID,
		Role:     "assistant",
		Content:  "Waiting on owner approval.",
		Status:   "awaiting_approval",
	})
	if err != nil {
		t.Fatal(err)
	}
	tc, err := s.CreateToolCall(t.Context(), AgentToolCall{
		ThreadID:  threadID,
		MessageID: msg.ID,
		CallID:    "call_" + toolName,
		ToolName:  toolName,
		Tier:      toolTierAction,
		ArgsJSON:  string(rawArgs),
		Status:    "awaiting_approval",
	})
	if err != nil {
		t.Fatal(err)
	}
	ap, err := s.CreateApproval(t.Context(), AgentApproval{
		ThreadID:    threadID,
		MessageID:   msg.ID,
		ToolCallID:  tc.ID,
		ToolName:    toolName,
		Title:       draft.Title,
		Summary:     draft.Summary,
		PayloadJSON: draft.PayloadJSON,
		Status:      "pending",
	})
	if err != nil {
		t.Fatal(err)
	}
	return ap
}

func TestSecretCardProviderAndRPCFlow(t *testing.T) {
	t.Parallel()

	t.Run("ProviderAPIKey-ZeroTranscript-RetryAndGuards", func(t *testing.T) {
		t.Parallel()
		s := fixtureStore(t)
		ts, token, csrf := startV2HTTPTestServer(t, s)

		// Set an initial connection so we have one, OR verify request_secret adds a new connection cleanly.
		thread, err := s.CreateThread(t.Context(), "operator", "Connect Anthropic Key", "", "")
		if err != nil {
			t.Fatal(err)
		}
		ap := createPendingApprovalFixture(t, s, thread.ID, "request_secret", json.RawMessage(`{"kind":"provider_api_key","provider":"anthropic","label":"Anthropic Research Key","reason":"Needed for Claude synthesis"}`))

		// Non-secret payload check: no secret fields, accurate target.
		var payload map[string]any
		if err := json.Unmarshal([]byte(ap.PayloadJSON), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["kind"] != "provider_api_key" || payload["provider"] != "anthropic" || payload["label"] != "Anthropic Research Key" {
			t.Fatalf("unexpected request_secret payload: %+v", payload)
		}

		// Direct execution without secret submission refuses cleanly.
		entry, _ := lookupAgentV2Tool("request_secret")
		if _, err := entry.execute(t.Context(), s, "operator", json.RawMessage(`{}`)); err == nil {
			t.Fatal("expected direct execute without secret input to fail")
		}

		// Approving without secret keeps card pending and errors.
		emptyResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/approvals/"+ap.ID, ts.URL, token, csrf, `{"decision":"approve","resume":false}`, "application/json")
		emptyResp.Body.Close()
		if emptyResp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 on missing secret, got %d", emptyResp.StatusCode)
		}

		// Approving with short/invalid key fails before DecideApproval so card stays pending for retry!
		badBody, _ := json.Marshal(map[string]any{
			"decision": "approve",
			"resume":   false,
			"secret": map[string]any{
				"value":      "short",
				"passphrase": testVaultPassword,
			},
		})
		badResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/approvals/"+ap.ID, ts.URL, token, csrf, string(badBody), "application/json")
		badResp.Body.Close()
		if badResp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 on invalid secret key, got %d", badResp.StatusCode)
		}
		stillPending, err := s.GetApproval(t.Context(), ap.ID)
		if err != nil || stillPending.Status != "pending" {
			t.Fatalf("approval must stay pending after failed vault save, got %+v err=%v", stillPending, err)
		}

		// Reject note echoing the secret is refused cleanly.
		leakNoteBody, _ := json.Marshal(map[string]any{
			"decision": "approve",
			"note":     "saved " + testAPIKey,
			"resume":   false,
			"secret": map[string]any{
				"value":      testAPIKey,
				"passphrase": testVaultPassword,
			},
		})
		leakResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/approvals/"+ap.ID, ts.URL, token, csrf, string(leakNoteBody), "application/json")
		leakResp.Body.Close()
		if leakResp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 when note echoes secret, got %d", leakResp.StatusCode)
		}

		// Valid approval saves encrypted provider key without persisting secret or passphrase to agents.sqlite.
		okBody, _ := json.Marshal(map[string]any{
			"decision": "approve",
			"resume":   false,
			"secret": map[string]any{
				"value":      testAPIKey,
				"passphrase": testVaultPassword,
				"name":       "Anthropic Workspace Key",
			},
		})
		okResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/approvals/"+ap.ID, ts.URL, token, csrf, string(okBody), "application/json")
		defer okResp.Body.Close()
		if okResp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(okResp.Body)
			t.Fatalf("expected 200 on secret card approval, got %d: %s", okResp.StatusCode, raw)
		}
		okRaw, _ := io.ReadAll(okResp.Body)
		if strings.Contains(string(okRaw), testAPIKey) || strings.Contains(string(okRaw), testVaultPassword) {
			t.Fatal("secret or passphrase leaked into HTTP approval response")
		}

		// Verify encrypted provider connection was added to Store vault.
		connView, err := s.AIConnection()
		if err != nil || len(connView.Connections) != 1 || connView.Connections[0].Provider != "anthropic" || connView.Connections[0].Name != "Anthropic Workspace Key" {
			t.Fatalf("expected saved anthropic connection in vault, got %+v err=%v", connView, err)
		}

		// Verify zero trace of secret or passphrase in agents.sqlite API detail or on disk.
		detail, err := s.GetThread(t.Context(), thread.ID)
		if err != nil {
			t.Fatal(err)
		}
		detailJSON, _ := json.Marshal(detail)
		if strings.Contains(string(detailJSON), testAPIKey) || strings.Contains(string(detailJSON), testVaultPassword) {
			t.Fatal("secret or passphrase persisted into Agent V2 thread detail")
		}
		dbRaw, err := os.ReadFile(filepath.Join(s.dir, "agents.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(dbRaw), testAPIKey) || strings.Contains(string(dbRaw), testVaultPassword) {
			t.Fatal("secret or passphrase persisted inside agents.sqlite file")
		}

		// Secret input passed to non-request_secret approval is refused cleanly.
		memAp := createPendingApprovalFixture(t, s, thread.ID, "save_memory", json.RawMessage(`{"title":"Rule","content":"Keep SOL allocation capped."}`))
		wrongToolBody, _ := json.Marshal(map[string]any{
			"decision": "approve",
			"resume":   false,
			"secret":   map[string]any{"value": testAPIKey},
		})
		wrongToolResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/agents/v2/approvals/"+memAp.ID, ts.URL, token, csrf, string(wrongToolBody), "application/json")
		wrongToolResp.Body.Close()
		if wrongToolResp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 when attaching secret to non-request_secret approval, got %d", wrongToolResp.StatusCode)
		}
	})

	t.Run("RPCAPIKey-SavesEncryptedVault-ZeroTranscriptLeak", func(t *testing.T) {
		t.Parallel()
		s := fixtureStore(t)
		claimFixture(t, s)
		rec := defaultAccounts()
		acc := publicRecord()
		rec.Accounts = []Account{acc}
		accView, err := s.UpdateAccounts(t.Context(), rec)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.ConfigureAccountRPC(t.Context(), RPCConfigInput{
			AccountID: acc.ID,
			Revision:  accView.Record.Revision,
			Endpoint:  FixtureRPCURL,
			Network:   signer.FixtureNetwork,
		}); err != nil {
			t.Fatal(err)
		}

		thread, err := s.CreateThread(t.Context(), "operator", "RPC Secret Test", "", "")
		if err != nil {
			t.Fatal(err)
		}
		ap := createPendingApprovalFixture(t, s, thread.ID, "request_secret", json.RawMessage(`{"kind":"rpc_api_key"}`))

		rpcSecret := "fased-rpc-test-disposable-secret-012345"
		rpcPass := "Disposable RPC vault password 123"
		res, err := s.ResolveAgentV2ApprovalWithSecret(t.Context(), ap.ID, "approve", "", &AgentV2SecretSubmission{
			Value:      rpcSecret,
			Passphrase: rpcPass,
		}, false, nil)
		if err != nil {
			t.Fatalf("ResolveAgentV2ApprovalWithSecret failed: %v", err)
		}
		if res.Approval.Status != "approved" || res.ToolCall.Status != "completed" {
			t.Fatalf("unexpected RPC secret result: %+v", res)
		}

		// Confirm RPC vault saved cleanly and verifies with passphrase.
		curAcc, err := s.Accounts(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		status, err := s.RPCCredential(t.Context(), RPCCredentialCommand{
			AccountID: acc.ID,
			Revision:  curAcc.Record.Revision,
			Action:    "status",
		})
		if err != nil || !status.Saved || status.Ref == "" {
			t.Fatalf("expected saved RPC credential after secret card, got %+v err=%v", status, err)
		}
		if _, err := s.RPCCredential(t.Context(), RPCCredentialCommand{
			AccountID:  acc.ID,
			Revision:   curAcc.Record.Revision,
			Ref:        status.Ref,
			Action:     "verify",
			Passphrase: rpcPass,
		}); err != nil {
			t.Fatalf("verify saved RPC credential failed: %v", err)
		}

		// Ensure RPC secret/passphrase never touched agents.sqlite.
		dbRaw, err := os.ReadFile(filepath.Join(s.dir, "agents.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(dbRaw), rpcSecret) || strings.Contains(string(dbRaw), rpcPass) {
			t.Fatal("RPC secret or passphrase leaked into agents.sqlite")
		}
	})
}

func TestModelManifestValidationAndOverlay(t *testing.T) {
	t.Parallel()

	s := fixtureStore(t)
	ts, token, csrf := startV2HTTPTestServer(t, s)

	// Empty / built-in initial status.
	initResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/agents/v2/model-manifest", ts.URL, token, "", "", "application/json")
	defer initResp.Body.Close()
	var initStatus ModelManifestStatus
	if err := json.NewDecoder(initResp.Body).Decode(&initStatus); err != nil {
		t.Fatal(err)
	}
	if initStatus.Loaded || initStatus.ModelCount != 0 {
		t.Fatalf("expected unloaded manifest initially, got %+v", initStatus)
	}

	// ParseAndValidateModelManifest rejects malformed inputs cleanly.
	invalidManifests := []string{
		`{}`,
		`{"version":2,"updatedAt":1795000000,"models":[{"provider":"openai","id":"gpt-5.4"}]}`,
		`{"version":1,"updatedAt":1795000000,"models":[{"provider":"local","id":"llama3"}]}`,
		`{"version":1,"updatedAt":1795000000,"models":[{"provider":"openai","id":"bad model id!"}]}`,
		`{"version":1,"updatedAt":1795000000,"models":[{"provider":"openai","id":"gpt-5.4","unknownField":1}]}`,
		`{"version":1,"updatedAt":1795000000,"models":[{"provider":"openai","id":"gpt-5.4","input":100,"cached":200,"output":200}]}`,
		`{"version":1,"updatedAt":1795000000,"models":[{"provider":"openai","id":"gpt-5.4","capabilities":["exec"]}]}`,
	}
	for _, raw := range invalidManifests {
		if _, err := ParseAndValidateModelManifest([]byte(raw)); err == nil {
			t.Fatalf("expected ParseAndValidateModelManifest to fail on %s", raw)
		}
	}

	// Save a provider connection + thread turns using an unpriced custom model ID so we can test live overlay pricing & context.
	conn := saveTestConnection(t, s)
	thread, err := s.CreateThread(t.Context(), "operator", "Manifest Overlay Thread", conn.Ref, "gpt-5.4-preview")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMessage(t.Context(), AgentV2Message{
		ThreadID:     thread.ID,
		Role:         "assistant",
		Content:      "Done",
		Status:       "completed",
		Provider:     "openai",
		Model:        "gpt-5.4-preview",
		InputTokens:  1000000,
		CachedTokens: 0,
		OutputTokens: 1000000,
	}); err != nil {
		t.Fatal(err)
	}

	// Before manifest: custom model pricing is unknown, local remains unpriced.
	beforeUsage, err := s.GetAgentV2Usage(t.Context(), "operator", 30)
	if err != nil {
		t.Fatal(err)
	}
	if beforeUsage.CostKnown {
		t.Fatalf("expected unknown cost before manifest for custom model, got %+v", beforeUsage)
	}

	// SSRF guard refuses http:// and loopback URLs on PUT /model-manifest.
	ssrfResp := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/agents/v2/model-manifest", ts.URL, token, csrf, `{"url":"http://127.0.0.1:8080/manifest.json"}`, "application/json")
	ssrfResp.Body.Close()
	if ssrfResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 on SSRF URL import, got %d", ssrfResp.StatusCode)
	}

	// Valid remote URL import via mocked agentV2TransportOverride.
	manifestPayload := `{
		"version": 1,
		"updatedAt": 1795410000,
		"models": [
			{
				"provider": "openai",
				"id": "gpt-5.4-preview",
				"name": "GPT 5.4 Preview",
				"contextTokens": 5000,
				"outputTokens": 16384,
				"input": 2000000,
				"cached": 500000,
				"output": 8000000,
				"currency": "USD",
				"reasoning": "high",
				"capabilities": ["tools", "reasoning", "search"]
			}
		]
	}`
	s.agentV2TransportOverride = aiTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://models.fased.invalid/manifest.json" {
			t.Fatalf("unexpected manifest fetch URL: %s", r.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(manifestPayload)),
		}, nil
	})
	putResp := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/agents/v2/model-manifest", ts.URL, token, csrf, `{"url":"https://models.fased.invalid/manifest.json"}`, "application/json")
	defer putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(putResp.Body)
		t.Fatalf("expected 200 on manifest import, got %d: %s", putResp.StatusCode, raw)
	}
	var loadedStatus ModelManifestStatus
	if err := json.NewDecoder(putResp.Body).Decode(&loadedStatus); err != nil {
		t.Fatal(err)
	}
	if !loadedStatus.Loaded || loadedStatus.ModelCount != 1 || loadedStatus.Source != "https://models.fased.invalid/manifest.json" {
		t.Fatalf("unexpected loaded manifest status: %+v", loadedStatus)
	}

	// Usage calculation picks up overlay USD pricing ($2 + $8 = $10 = 10_000_000 microUSD).
	afterUsage, err := s.GetAgentV2Usage(t.Context(), "operator", 30)
	if err != nil {
		t.Fatal(err)
	}
	if !afterUsage.CostKnown || afterUsage.EstimatedCostUS != 10000000 {
		t.Fatalf("expected manifest pricing 10000000 microUsd, got %+v", afterUsage)
	}

	// Catalog hook includes and enriches the manifest model.
	connAfter, err := s.AIConnection(conn.Ref)
	if err != nil {
		t.Fatal(err)
	}
	foundManifestModel := false
	for _, m := range connAfter.Catalog.Models {
		if m.ID == "gpt-5.4-preview" {
			foundManifestModel = true
			if m.Name != "GPT 5.4 Preview" || m.Policy == nil || m.Policy.ContextTokens != 5000 {
				t.Fatalf("unexpected enriched manifest model in catalog: %+v", m)
			}
		}
	}
	if !foundManifestModel {
		t.Fatalf("expected gpt-5.4-preview in AIConnection catalog, got %+v", connAfter.Catalog.Models)
	}

	// Local models never receive fabricated costs even if a manifest exists.
	if cost, known := estimateV2ModelCostMicroUSDForDir(s.dir, "local", "gpt-5.4-preview", 1000, 0, 1000); known || cost != 0 {
		t.Fatalf("expected local model to stay unpriced, got %d %v", cost, known)
	}

	// Fresh Store handle opening same dir loads persisted model-manifest.json automatically.
	s2, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if !s2.ModelManifestStatus(t.Context()).Loaded {
		t.Fatal("expected persisted model-manifest.json on reopened Store")
	}

	// DELETE clears manifest cleanly.
	delResp := doV2JSONRequest(t, http.MethodDelete, ts.URL+"/api/v1/agents/v2/model-manifest", ts.URL, token, csrf, "", "application/json")
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on manifest delete, got %d", delResp.StatusCode)
	}
	if s.ModelManifestStatus(t.Context()).Loaded {
		t.Fatal("expected manifest cleared after DELETE")
	}
}

func TestMCPSecurityToolsAndResources(t *testing.T) {
	t.Parallel()

	s := fixtureStore(t)
	ts, token, csrf := startV2HTTPTestServer(t, s)

	// Unauthenticated request refused with 401.
	unauthResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/mcp", ts.URL, "", "", "", "application/json")
	unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthenticated GET /api/v1/mcp, got %d", unauthResp.StatusCode)
	}

	// Authenticated GET discovery succeeds.
	discResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/mcp", ts.URL, token, "", "", "application/json")
	defer discResp.Body.Close()
	if discResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 GET /api/v1/mcp, got %d", discResp.StatusCode)
	}
	var disc struct {
		Name            string   `json:"name"`
		ProtocolVersion string   `json:"protocolVersion"`
		Scopes          []string `json:"scopes"`
		ToolCount       int      `json:"toolCount"`
		ResourceCount   int      `json:"resourceCount"`
	}
	if err := json.NewDecoder(discResp.Body).Decode(&disc); err != nil {
		t.Fatal(err)
	}
	if disc.Name != "fased-mcp" || disc.ProtocolVersion != mcpProtocolVersion || disc.ToolCount != 15 || disc.ResourceCount != 5 {
		t.Fatalf("unexpected MCP discovery payload: %+v", disc)
	}

	// CSRF enforced for cookie/browser POST requests.
	badCSRF := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/mcp", ts.URL, token, "", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "application/json")
	badCSRF.Body.Close()
	if badCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 missing CSRF on browser-origin POST /api/v1/mcp, got %d", badCSRF.StatusCode)
	}

	// initialize & tools/list & resources/list work over JSON-RPC 2.0.
	callRPC := func(body string) (int, map[string]any) {
		resp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/mcp", ts.URL, token, csrf, body, "application/json")
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	code, initRPC := callRPC(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if code != 200 || initRPC["result"] == nil {
		t.Fatalf("initialize failed: %d %+v", code, initRPC)
	}

	notifResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/mcp", ts.URL, token, csrf, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, "application/json")
	notifResp.Body.Close()
	if notifResp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 notifications/initialized, got %d", notifResp.StatusCode)
	}

	_, listRPC := callRPC(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	listResult := listRPC["result"].(map[string]any)
	rawTools := listResult["tools"].([]any)
	toolNames := map[string]bool{}
	for _, item := range rawTools {
		toolNames[item.(map[string]any)["name"].(string)] = true
	}
	for _, disallowed := range []string{"request_secret", "save_memory", "delegate_to_agent"} {
		if toolNames[disallowed] {
			t.Fatalf("disallowed tool %s exposed by MCP tools/list", disallowed)
		}
	}
	for _, allowed := range []string{"workspace_status", "list_wallets", "draft_task", "draft_swap", "draft_strategy"} {
		if !toolNames[allowed] {
			t.Fatalf("expected tool %s missing from MCP tools/list", allowed)
		}
	}

	// Disallowed tools/call returns JSON-RPC invalid params (-32602).
	_, disallowedCall := callRPC(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"request_secret","arguments":{"kind":"provider_api_key","provider":"openai"}}}`)
	if disallowedCall["error"] == nil || int(disallowedCall["error"].(map[string]any)["code"].(float64)) != -32602 {
		t.Fatalf("expected -32602 for disallowed tool call, got %+v", disallowedCall)
	}

	// Allowed read & non-custodial draft tool calls succeed cleanly.
	_, wsCall := callRPC(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"workspace_status","arguments":{}}}`)
	if wsCall["result"] == nil || wsCall["result"].(map[string]any)["isError"] != false {
		t.Fatalf("workspace_status call failed: %+v", wsCall)
	}
	_, draftCall := callRPC(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"draft_task","arguments":{"name":"SOL watch","description":"Check SOL price","intervalSeconds":300}}}`)
	if draftCall["result"] == nil || draftCall["result"].(map[string]any)["isError"] != false {
		t.Fatalf("draft_task call failed: %+v", draftCall)
	}

	// Seed a workspace memory + check fased://memories returns only non-draft memories!
	if _, err := s.CreateMemory(t.Context(), MemoryInput{
		ProfileID: "operator",
		Scope:     "workspace",
		Title:     "MCP Visible Rule",
		Content:   "Always keep 30% stablecoins.",
		Source:    "user",
	}); err != nil {
		t.Fatal(err)
	}

	_, resCall := callRPC(`{"jsonrpc":"2.0","id":6,"method":"resources/read","params":{"uri":"fased://memories"}}`)
	resResult := resCall["result"].(map[string]any)
	contents := resResult["contents"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(contents, "MCP Visible Rule") || strings.Contains(contents, "task_draft") {
		t.Fatalf("unexpected fased://memories payload: %s", contents)
	}

	// Draft tool call refuses when instance is paused!
	if _, err := s.UpdatePause(t.Context(), pauseCommand(t, s, 1)); err != nil {
		t.Fatal(err)
	}
	_, pausedDraft := callRPC(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"draft_task","arguments":{"name":"Blocked","description":"Should pause","intervalSeconds":300}}}`)
	if pausedDraft["error"] == nil {
		t.Fatalf("expected MCP draft tool error when paused, got %+v", pausedDraft)
	}
}
