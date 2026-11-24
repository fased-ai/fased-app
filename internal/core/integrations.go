package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/decision"
	"github.com/fased-ai/fased-app/internal/mcpclient"
)

func (s *Store) decisionService() (*decision.Service, error) {
	if s == nil {
		return nil, errors.New("workspace store unavailable")
	}
	s.integrationsMu.Lock()
	defer s.integrationsMu.Unlock()
	if s.decisionSvc != nil {
		return s.decisionSvc, nil
	}
	svc, err := decision.NewService(decision.Config{
		DataDir:       s.dir,
		FixtureMode:   s.providerFixture,
		Now:           s.now,
		HTTPTransport: s.decisionTransport,
	})
	if err != nil {
		return nil, err
	}
	s.decisionSvc = svc
	return svc, nil
}

func (s *Store) mcpClientService() (*mcpclient.Service, error) {
	if s == nil {
		return nil, errors.New("workspace store unavailable")
	}
	s.integrationsMu.Lock()
	defer s.integrationsMu.Unlock()
	if s.mcpClientSvc != nil {
		return s.mcpClientSvc, nil
	}
	svc, err := mcpclient.NewService(mcpclient.Config{
		DataDir:       s.dir,
		FixtureMode:   s.providerFixture,
		Now:           s.now,
		HTTPTransport: s.mcpClientTransport,
	})
	if err != nil {
		return nil, err
	}
	s.mcpClientSvc = svc
	return svc, nil
}

func (h *HTTPHandler) decisionAPI(w http.ResponseWriter, r *http.Request, token string) {
	if !h.allowRate(r, "decision", 120) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	svc, err := h.store.decisionService()
	if err != nil {
		writeError(w, 500, "decision service unavailable")
		return
	}

	rel := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/decision/"), "/")
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	switch rel {
	case "config":
		switch r.Method {
		case http.MethodGet:
			st, err := svc.Status(ctx)
			if err != nil {
				writeError(w, 500, "decision status unavailable")
				return
			}
			writeJSON(w, 200, st)
		case http.MethodPut:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var upd decision.ConfigUpdate
			if !decodeJSON(w, r, &upd) {
				return
			}
			st, err := svc.SaveConfig(ctx, upd)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, st)
		default:
			writeError(w, 405, "method not allowed")
		}

	case "evaluate":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var req decision.EvaluateRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		res, err := svc.Evaluate(ctx, req)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, res)

	default:
		writeError(w, 404, "unknown decision route")
	}
}

func (h *HTTPHandler) mcpClientsAPI(w http.ResponseWriter, r *http.Request, token string) {
	if !h.allowRate(r, "mcpclient", 120) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	svc, err := h.store.mcpClientService()
	if err != nil {
		writeError(w, 500, "external MCP client service unavailable")
		return
	}

	rel := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/mcp/clients"), "/")
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	switch {
	case rel == "":
		switch r.Method {
		case http.MethodGet:
			items, err := svc.ListServers(ctx)
			if err != nil {
				writeError(w, 500, "external MCP servers unavailable")
				return
			}
			writeJSON(w, 200, map[string]any{"clients": items, "count": len(items)})
		case http.MethodPut:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var in mcpclient.ServerInput
			if !decodeJSON(w, r, &in) {
				return
			}
			saved, err := svc.SaveServer(ctx, in)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, saved)
		default:
			writeError(w, 405, "method not allowed")
		}

	case strings.HasSuffix(rel, "/sync") && !strings.Contains(strings.TrimSuffix(rel, "/sync"), "/"):
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		id := strings.TrimSuffix(rel, "/sync")
		updated, err := svc.SyncServer(ctx, id)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeError(w, 404, "external MCP server not found")
				return
			}
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, updated)

	case strings.HasSuffix(rel, "/call") && !strings.Contains(strings.TrimSuffix(rel, "/call"), "/"):
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		id := strings.TrimSuffix(rel, "/call")
		var body struct {
			Tool      string          `json:"tool"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		res, err := svc.CallTool(ctx, id, body.Tool, body.Arguments)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeError(w, 404, "external MCP server not found")
				return
			}
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, res)

	case !strings.Contains(rel, "/"):
		if r.Method != http.MethodDelete {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		deleted, err := svc.DeleteServer(ctx, rel)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if !deleted {
			writeError(w, 404, "external MCP server not found")
			return
		}
		writeJSON(w, 200, map[string]any{"deleted": true, "id": rel})

	default:
		writeError(w, 404, "unknown external MCP client route")
	}
}

func toolJevEvaluate(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var args decision.EvaluateRequest
	if err := decodeToolArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	svc, err := s.decisionService()
	if err != nil {
		return nil, err
	}
	return svc.Evaluate(ctx, args)
}

func toolCallExternalMCP(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var args struct {
		ServerID  string          `json:"serverId"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := decodeToolArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.ServerID) == "" || strings.TrimSpace(args.Tool) == "" {
		return nil, errors.New("call_external_mcp_tool requires serverId and tool")
	}
	svc, err := s.mcpClientService()
	if err != nil {
		return nil, err
	}
	return svc.CallTool(ctx, args.ServerID, args.Tool, args.Arguments)
}
