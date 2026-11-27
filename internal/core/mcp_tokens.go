package core

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/httpapi"
)

const (
	mcpTokenPrefix     = "fased_mcp_"
	mcpTokenDefaultTTL = 30 * 24 * time.Hour
	mcpTokenMaxTTL     = 90 * 24 * time.Hour
)

// MCPTokenView is the non-secret projection of a dedicated, read/draft-scoped MCP token.
type MCPTokenView struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	Scopes    []string `json:"scopes"`
	CreatedAt int64    `json:"createdAt"`
	ExpiresAt int64    `json:"expiresAt"`
}

func (s *Store) ensureMCPTokensTable(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("workspace store unavailable")
	}
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS mcp_tokens (
	id TEXT PRIMARY KEY,
	label TEXT NOT NULL,
	token_hash TEXT UNIQUE NOT NULL,
	scopes TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL
);`)
	return err
}

// CreateMCPToken mints a dedicated, revocable MCP-only bearer token (`fased_mcp_...`)
// restricted strictly to `/api/v1/mcp` read+draft scopes and rejected on all owner `/api/v1/*` routes.
func (s *Store) CreateMCPToken(ctx context.Context, label string, ttl time.Duration) (string, MCPTokenView, error) {
	return s.CreateMCPTokenWithScopes(ctx, label, ttl, []string{"read", "draft"})
}

// CreateMCPTokenWithScopes mints a dedicated MCP token with explicit scopes ("read" or "read,draft").
func (s *Store) CreateMCPTokenWithScopes(ctx context.Context, label string, ttl time.Duration, scopes []string) (string, MCPTokenView, error) {
	if err := s.ensureMCPTokensTable(ctx); err != nil {
		return "", MCPTokenView{}, err
	}
	cleanLabel := strings.TrimSpace(label)
	if cleanLabel == "" {
		cleanLabel = "External MCP Client"
	}
	if len(cleanLabel) > 64 {
		cleanLabel = cleanLabel[:64]
	}
	if ttl <= 0 {
		ttl = mcpTokenDefaultTTL
	}
	if ttl > mcpTokenMaxTTL {
		ttl = mcpTokenMaxTTL
	}
	normScopes := []string{}
	seenScope := map[string]bool{}
	for _, sc := range scopes {
		sc = strings.ToLower(strings.TrimSpace(sc))
		if (sc == "read" || sc == "draft") && !seenScope[sc] {
			seenScope[sc] = true
			normScopes = append(normScopes, sc)
		}
	}
	if len(normScopes) == 0 {
		normScopes = []string{"read"}
	}
	raw, err := randomToken()
	if err != nil {
		return "", MCPTokenView{}, err
	}
	idRaw, err := randomToken()
	if err != nil {
		return "", MCPTokenView{}, err
	}
	fullTok := mcpTokenPrefix + raw
	id := "mcptok-" + idRaw[:12]
	nowSec := s.now().UTC().Unix()
	expSec := s.now().UTC().Add(ttl).Unix()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO mcp_tokens (id, label, token_hash, scopes, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, cleanLabel, tokenHash(fullTok), strings.Join(normScopes, ","), nowSec, expSec,
	); err != nil {
		return "", MCPTokenView{}, err
	}
	return fullTok, MCPTokenView{
		ID:        id,
		Label:     cleanLabel,
		Scopes:    normScopes,
		CreatedAt: nowSec,
		ExpiresAt: expSec,
	}, nil
}

// ValidateMCPToken checks whether raw is a valid, non-expired dedicated MCP token.
func (s *Store) ValidateMCPToken(ctx context.Context, raw string) bool {
	ok, _ := s.ValidateMCPTokenWithScopes(ctx, raw)
	return ok
}

// ValidateMCPTokenWithScopes checks whether raw is valid and returns its granted scope set.
func (s *Store) ValidateMCPTokenWithScopes(ctx context.Context, raw string) (bool, map[string]bool) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, mcpTokenPrefix) || len(trimmed) < len(mcpTokenPrefix)+32 {
		return false, nil
	}
	if err := s.ensureMCPTokensTable(ctx); err != nil {
		return false, nil
	}
	var exp int64
	var scopeStr string
	err := s.db.QueryRowContext(ctx, `SELECT expires_at, scopes FROM mcp_tokens WHERE token_hash = ?`, tokenHash(trimmed)).Scan(&exp, &scopeStr)
	if err != nil || s.now().UTC().Unix() >= exp {
		return false, nil
	}
	out := map[string]bool{}
	for _, sc := range strings.Split(scopeStr, ",") {
		sc = strings.ToLower(strings.TrimSpace(sc))
		if sc != "" {
			out[sc] = true
		}
	}
	return true, out
}

type mcpTokenScopesKey struct{}

func mcpScopesFromContext(ctx context.Context) map[string]bool {
	if ctx == nil {
		return nil
	}
	m, _ := ctx.Value(mcpTokenScopesKey{}).(map[string]bool)
	return m
}

// ListMCPTokens returns active dedicated MCP tokens without exposing secrets.
func (s *Store) ListMCPTokens(ctx context.Context) ([]MCPTokenView, error) {
	if err := s.ensureMCPTokensTable(ctx); err != nil {
		return nil, err
	}
	nowSec := s.now().UTC().Unix()
	rows, err := s.db.QueryContext(ctx, `SELECT id, label, scopes, created_at, expires_at FROM mcp_tokens WHERE expires_at > ? ORDER BY created_at DESC`, nowSec)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MCPTokenView, 0)
	for rows.Next() {
		var v MCPTokenView
		var scopeStr string
		if err := rows.Scan(&v.ID, &v.Label, &scopeStr, &v.CreatedAt, &v.ExpiresAt); err == nil {
			v.Scopes = strings.Split(scopeStr, ",")
			out = append(out, v)
		}
	}
	return out, nil
}

// RevokeMCPToken deletes a dedicated MCP token immediately.
func (s *Store) RevokeMCPToken(ctx context.Context, id string) (bool, error) {
	if err := s.ensureMCPTokensTable(ctx); err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM mcp_tokens WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (h *HTTPHandler) mcpAPIWithScopedToken(w http.ResponseWriter, r *http.Request) {
	// Dedicated MCP tokens are strictly non-browser Bearer credentials confined to /api/v1/mcp.
	if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
		writeError(w, http.StatusForbidden, "browser requests cannot use MCP bearer tokens")
		return
	}
	if r.Method == http.MethodGet {
		h.mcpAPI(w, r, "")
		return
	}
	// Delegate JSON-RPC execution directly without requiring owner session token.
	rCopy := r.Clone(r.Context())
	rCopy.Header = r.Header.Clone()
	rCopy.Header.Del("Origin")
	h.mcpAPI(w, rCopy, mcpScopedSessionSentinel)
}

const mcpScopedSessionSentinel = "__fased_mcp_scoped_token__"

func (h *HTTPHandler) mcpTokensAPI(w http.ResponseWriter, r *http.Request, token string) {
	rel := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/mcp/tokens"), "/")
	switch {
	case rel == "" && r.Method == http.MethodGet:
		list, err := h.store.ListMCPTokens(r.Context())
		if err != nil {
			writeError(w, 500, "MCP tokens unavailable")
			return
		}
		writeJSON(w, 200, map[string]any{"tokens": list, "count": len(list)})
	case rel == "" && r.Method == http.MethodPost:
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		if !h.allowAuthRate(r, "mcp-tokens", 10) {
			writeError(w, 429, ErrRateLimited.Error())
			return
		}
		var req struct {
			Label      string `json:"label"`
			TTLSeconds int64  `json:"ttlSeconds"`
		}
		if !httpapi.DecodeStrictJSON(w, r, 4*1024, &req) {
			return
		}
		var ttl time.Duration
		if req.TTLSeconds > 0 {
			ttl = time.Duration(req.TTLSeconds) * time.Second
		}
		fullTok, view, err := h.store.CreateMCPToken(r.Context(), req.Label, ttl)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"token": fullTok, "meta": view})
	case rel != "" && !strings.Contains(rel, "/") && r.Method == http.MethodDelete:
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		revoked, err := h.store.RevokeMCPToken(r.Context(), rel)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if !revoked {
			writeError(w, 404, "MCP token not found")
			return
		}
		writeJSON(w, 200, map[string]any{"revoked": true, "id": rel})
	default:
		writeError(w, 405, "method not allowed")
	}
}
