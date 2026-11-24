package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
)

const (
	mcpProtocolVersion = "2025-03-26"
	mcpMaxRequestBytes = 64 * 1024
)

var mcpReadTools = map[string]bool{
	"workspace_status":       true,
	"list_wallets":           true,
	"get_wallet_balances":    true,
	"get_portfolio_summary":  true,
	"web_search":             true,
	"fetch_web_page":         true,
	"get_token_price":        true,
	"get_swap_quote":         true,
	"list_tasks":             true,
	"search_memories":        true,
	"search_news":            true,
	"list_strategies":        true,
	"get_market_candles":     true,
	"get_orderbook":          true,
	"get_footprint":          true,
	"get_macro_or_sec":       true,
	"jev_evaluate":           true,
	"call_external_mcp_tool": true,
}

var mcpDraftTools = map[string]bool{
	"draft_task":     true,
	"draft_swap":     true,
	"draft_strategy": true,
}

type mcpResourceDescriptor struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

var mcpResources = []mcpResourceDescriptor{
	{
		URI:         "fased://workspace/status",
		Name:        "Workspace status",
		Description: "Deployment mode, connected AI providers, agent profiles, and non-custodial capability flags.",
		MimeType:    "application/json",
	},
	{
		URI:         "fased://wallets",
		Name:        "Public wallets",
		Description: "Configured watch wallets, public addresses, networks, and RPC endpoints.",
		MimeType:    "application/json",
	},
	{
		URI:         "fased://tasks",
		Name:        "Scheduled tasks",
		Description: "Configured agent tasks and recent bounded run outcomes.",
		MimeType:    "application/json",
	},
	{
		URI:         "fased://memories",
		Name:        "Workspace memories",
		Description: "Non-draft durable workspace rules and factual memories.",
		MimeType:    "application/json",
	},
	{
		URI:         "fased://strategies",
		Name:        "Trading strategies",
		Description: "Saved non-custodial strategies and paper trading definitions.",
		MimeType:    "application/json",
	},
	{
		URI:         "fased://market/watchlists",
		Name:        "Market watchlists",
		Description: "Saved market data watchlists and tracked instruments.",
		MimeType:    "application/json",
	},
	{
		URI:         "fased://mcp/clients",
		Name:        "External MCP clients",
		Description: "Configured external MCP client servers, read-only policy flags, and discovered tool schemas.",
		MimeType:    "application/json",
	},
}

type mcpRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func mcpToolDefinitions() []map[string]any {
	entries := agentV2ToolRegistry()
	out := make([]map[string]any, 0, len(mcpReadTools)+len(mcpDraftTools))
	for _, entry := range entries {
		name := entry.spec.Name
		if !mcpReadTools[name] && !mcpDraftTools[name] {
			continue
		}
		out = append(out, map[string]any{
			"name":        name,
			"description": entry.spec.Description,
			"inputSchema": entry.spec.Parameters,
		})
	}
	return out
}

func writeMCPRPCResult(w http.ResponseWriter, id any, result any) {
	writeJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func writeMCPRPCError(w http.ResponseWriter, id any, code int, msg string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": msg,
		},
	})
}

func (h *HTTPHandler) mcpAPI(w http.ResponseWriter, r *http.Request, token string) {
	switch r.Method {
	case http.MethodGet:
		tools := mcpToolDefinitions()
		writeJSON(w, http.StatusOK, map[string]any{
			"name":            "fased-mcp",
			"version":         Version,
			"protocolVersion": mcpProtocolVersion,
			"scopes":          []string{"read", "draft"},
			"endpoint":        "/api/v1/mcp",
			"toolCount":       len(tools),
			"resourceCount":   len(mcpResources),
		})
		return

	case http.MethodPost:
		if !h.allowRate(r, "mcp", 60) {
			writeError(w, http.StatusTooManyRequests, ErrRateLimited.Error())
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "JSON content type required")
			return
		}
		origin := r.Header.Get("Origin")
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		if origin != "" || authHeader == "" {
			if !h.origins[origin] {
				writeError(w, http.StatusForbidden, "same-origin request required")
				return
			}
			if !h.csrf(w, r, token) {
				return
			}
		} else {
			if h.instanceLocation == "local" {
				if !h.checkLoopbackPeerUID(r, false) {
					writeError(w, http.StatusForbidden, "local loopback peer identity required")
					return
				}
			} else if h.instanceLocation == "remote" {
				if !strings.HasPrefix(authHeader, "Bearer ") || r.Header.Get("Sec-Fetch-Site") != "" || !h.ownerSession(r, token) {
					writeError(w, http.StatusForbidden, "owner Bearer authorization required for remote MCP")
					return
				}
			} else {
				writeError(w, http.StatusForbidden, "local loopback peer identity required")
				return
			}
		}

		r.Body = http.MaxBytesReader(w, r.Body, mcpMaxRequestBytes)
		defer r.Body.Close()
		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			writeMCPRPCError(w, nil, -32700, "invalid JSON-RPC payload")
			return
		}
		var req mcpRPCRequest
		if err := json.Unmarshal(rawBody, &req); err != nil {
			writeMCPRPCError(w, nil, -32700, "parse error")
			return
		}
		if req.JSONRPC != "2.0" || strings.TrimSpace(req.Method) == "" {
			writeMCPRPCError(w, req.ID, -32600, "invalid JSON-RPC 2.0 request")
			return
		}

		switch req.Method {
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return

		case "ping":
			writeMCPRPCResult(w, req.ID, map[string]any{})
			return

		case "initialize":
			writeMCPRPCResult(w, req.ID, map[string]any{
				"protocolVersion": mcpProtocolVersion,
				"capabilities": map[string]any{
					"tools":     map[string]any{"listChanged": false},
					"resources": map[string]any{"listChanged": false},
				},
				"serverInfo": map[string]any{
					"name":    "fased-mcp",
					"version": Version,
				},
			})
			return

		case "tools/list":
			writeMCPRPCResult(w, req.ID, map[string]any{
				"tools": mcpToolDefinitions(),
			})
			return

		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if len(req.Params) == 0 || json.Unmarshal(req.Params, &params) != nil || strings.TrimSpace(params.Name) == "" {
				writeMCPRPCError(w, req.ID, -32602, "tools/call requires a valid tool name")
				return
			}
			isRead := mcpReadTools[params.Name]
			isDraft := mcpDraftTools[params.Name]
			if !isRead && !isDraft {
				writeMCPRPCError(w, req.ID, -32602, "tool not exposed over scoped MCP")
				return
			}
			if isDraft {
				if o, _, uErr := readApplicationUpdate(r.Context(), h.store.db); uErr != nil || oneOf(o.Phase, "applying", "recovery") {
					writeMCPRPCError(w, req.ID, -32000, "Update in progress. Draft operations are unavailable.")
					return
				}
				if phase, _, pErr := readPause(r.Context(), h.store.db); pErr != nil || phase == "paused" {
					writeMCPRPCError(w, req.ID, -32000, ErrCorePaused.Error())
					return
				}
			}
			entry, known := lookupAgentV2Tool(params.Name)
			if !known || entry.execute == nil {
				writeMCPRPCError(w, req.ID, -32602, "tool unavailable")
				return
			}
			args := params.Arguments
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			profileID := defaultMCPProfileID(r.Context(), h.store)
			callCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			res, execErr := entry.execute(callCtx, h.store, profileID, args)
			if execErr != nil {
				writeMCPRPCResult(w, req.ID, map[string]any{
					"isError": true,
					"content": []map[string]any{
						{"type": "text", "text": encodeToolResultJSON(map[string]any{"error": execErr.Error()})},
					},
				})
				return
			}
			writeMCPRPCResult(w, req.ID, map[string]any{
				"isError": false,
				"content": []map[string]any{
					{"type": "text", "text": encodeToolResultJSON(res)},
				},
			})
			return

		case "resources/list":
			writeMCPRPCResult(w, req.ID, map[string]any{
				"resources": mcpResources,
			})
			return

		case "resources/read":
			var params struct {
				URI string `json:"uri"`
			}
			if len(req.Params) == 0 || json.Unmarshal(req.Params, &params) != nil || strings.TrimSpace(params.URI) == "" {
				writeMCPRPCError(w, req.ID, -32602, "resources/read requires a valid uri")
				return
			}
			payload, err := readMCPResource(r.Context(), h.store, strings.TrimSpace(params.URI))
			if err != nil {
				writeMCPRPCError(w, req.ID, -32602, err.Error())
				return
			}
			encoded, err := json.Marshal(payload)
			if err != nil {
				writeMCPRPCError(w, req.ID, -32603, "resource encoding failed")
				return
			}
			writeMCPRPCResult(w, req.ID, map[string]any{
				"contents": []map[string]any{
					{
						"uri":      strings.TrimSpace(params.URI),
						"mimeType": "application/json",
						"text":     string(encoded),
					},
				},
			})
			return

		default:
			writeMCPRPCError(w, req.ID, -32601, "unsupported MCP method")
			return
		}

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func defaultMCPProfileID(ctx context.Context, s *Store) string {
	if s == nil || s.db == nil {
		return "operator"
	}
	if d, _, err := readAgents(ctx, s.db); err == nil && len(d.Profiles) > 0 {
		for _, p := range d.Profiles {
			if p.ID == "operator" {
				return p.ID
			}
		}
		return d.Profiles[0].ID
	}
	return "operator"
}

func readMCPResource(ctx context.Context, s *Store, uri string) (any, error) {
	if s == nil {
		return nil, errors.New("workspace store unavailable")
	}
	switch uri {
	case "fased://workspace/status":
		return toolWorkspaceStatus(ctx, s, "", json.RawMessage(`{}`))
	case "fased://wallets":
		return toolListWallets(ctx, s, "", json.RawMessage(`{}`))
	case "fased://tasks":
		return toolListTasks(ctx, s, "", json.RawMessage(`{}`))
	case "fased://memories":
		rows, err := s.SearchMemories(ctx, "", "", 50)
		if err != nil {
			return nil, err
		}
		filtered := make([]AgentV2Memory, 0, len(rows))
		for _, m := range rows {
			if isV2DraftScope(m.Scope) {
				continue
			}
			filtered = append(filtered, m)
		}
		return map[string]any{"memories": filtered, "count": len(filtered)}, nil
	case "fased://strategies":
		return toolListStrategies(ctx, s, "", json.RawMessage(`{}`))
	case "fased://market/watchlists":
		svc, err := s.marketDataService()
		if err != nil {
			return nil, err
		}
		lists, err := svc.ListWatchlists(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"watchlists": lists, "count": len(lists)}, nil
	case "fased://mcp/clients":
		svc, err := s.mcpClientService()
		if err != nil {
			return nil, err
		}
		clients, err := svc.ListServers(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"clients": clients, "count": len(clients)}, nil
	default:
		return nil, errors.New("unknown MCP resource uri")
	}
}
