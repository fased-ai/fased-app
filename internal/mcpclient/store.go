package mcpclient

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	integrationsDirName = "integrations"
	keyFileName         = "integrations.key"
	mcpDBName           = "mcpclient.sqlite"
	secretAAD           = "fased-mcpclient/v1"
)

type store struct {
	key    []byte
	db     *sql.DB
	mu     sync.Mutex
	closed bool
}

func sqliteFileURI(path string) string {
	slashPath := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	escaped := strings.ReplaceAll(slashPath, "?", "%3F")
	escaped = strings.ReplaceAll(escaped, "#", "%23")
	return "file://" + escaped
}

func loadOrCreateKey(dir string) ([]byte, error) {
	keyPath := filepath.Join(dir, keyFileName)
	if raw, err := os.ReadFile(keyPath); err == nil {
		if len(raw) != 32 {
			return nil, errors.New("corrupted integrations key length")
		}
		return raw, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, ".integrations.key.*")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if _, err := tmp.Write(key); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	linkErr := os.Link(tmpPath, keyPath)
	if linkErr == nil {
		return key, nil
	}
	if os.IsExist(linkErr) {
		raw, readErr := os.ReadFile(keyPath)
		if readErr != nil || len(raw) != 32 {
			return nil, errors.New("failed to read initialized integrations key")
		}
		return raw, nil
	}

	// Fallback for filesystems that do not support hardlinks.
	f, err := os.OpenFile(keyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		_, writeErr := f.Write(key)
		closeErr := f.Close()
		if writeErr != nil {
			_ = os.Remove(keyPath)
			return nil, writeErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		return key, nil
	}
	if os.IsExist(err) {
		for attempt := 0; attempt < 5; attempt++ {
			raw, readErr := os.ReadFile(keyPath)
			if readErr == nil && len(raw) == 32 {
				return raw, nil
			}
			time.Sleep(10 * time.Millisecond)
		}
		return nil, errors.New("failed to read initialized integrations key")
	}
	return nil, err
}

func openStore(dataDir string) (*store, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("mcpclient requires non-empty DataDir")
	}
	dir := filepath.Join(dataDir, integrationsDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	key, err := loadOrCreateKey(dir)
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, mcpDBName)
	dsn := sqliteFileURI(dbPath) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	const ddl = `
CREATE TABLE IF NOT EXISTS mcp_servers (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	endpoint TEXT NOT NULL,
	allow_localhost INTEGER NOT NULL,
	enabled INTEGER NOT NULL,
	read_only INTEGER NOT NULL,
	token_ciphertext BLOB NOT NULL,
	allowed_tools_json TEXT NOT NULL,
	tools_json TEXT NOT NULL,
	resources_json TEXT NOT NULL,
	last_sync_at INTEGER NOT NULL,
	last_error TEXT NOT NULL
);`
	if _, err := db.Exec(ddl); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &store{key: key, db: db}, nil
}

func (s *store) seal(plaintext string) ([]byte, error) {
	if plaintext == "" {
		return []byte{}, nil
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ct := gcm.Seal(nil, nonce, []byte(plaintext), []byte(secretAAD))
	out := make([]byte, len(nonce)+len(ct))
	copy(out, nonce)
	copy(out[len(nonce):], ct)
	return out, nil
}

func (s *store) open(blob []byte) (string, error) {
	if len(blob) == 0 {
		return "", nil
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(blob) < gcm.NonceSize() {
		return "", errors.New("corrupted sealed token")
	}
	pt, err := gcm.Open(nil, blob[:gcm.NonceSize()], blob[gcm.NonceSize():], []byte(secretAAD))
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

func (s *store) listServers(ctx context.Context) ([]ServerConfig, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, errors.New("mcpclient store closed")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, endpoint, allow_localhost, enabled, read_only, length(token_ciphertext) > 0,
       allowed_tools_json, tools_json, resources_json, last_sync_at, last_error
FROM mcp_servers
ORDER BY id ASC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ServerConfig{}
	for rows.Next() {
		var cfg ServerConfig
		var allowLocal, enabledInt, readOnlyInt int
		var allowedJSON, toolsJSON, resJSON string
		if err := rows.Scan(
			&cfg.ID, &cfg.Name, &cfg.Endpoint, &allowLocal, &enabledInt, &readOnlyInt, &cfg.HasAuthToken,
			&allowedJSON, &toolsJSON, &resJSON, &cfg.LastSyncAt, &cfg.LastError,
		); err != nil {
			return nil, err
		}
		cfg.AllowLocalhost = allowLocal != 0
		cfg.Enabled = enabledInt != 0
		cfg.ReadOnly = readOnlyInt != 0
		_ = json.Unmarshal([]byte(allowedJSON), &cfg.AllowedTools)
		_ = json.Unmarshal([]byte(toolsJSON), &cfg.Tools)
		_ = json.Unmarshal([]byte(resJSON), &cfg.Resources)
		cfg.ToolCount = len(cfg.Tools)
		cfg.ResourceCount = len(cfg.Resources)
		out = append(out, cfg)
	}
	return out, rows.Err()
}

func (s *store) getServer(ctx context.Context, id string) (ServerConfig, string, error) {
	var cfg ServerConfig
	var allowLocal, enabledInt, readOnlyInt int
	var tokenBlob []byte
	var allowedJSON, toolsJSON, resJSON string
	err := s.db.QueryRowContext(ctx, `
SELECT id, name, endpoint, allow_localhost, enabled, read_only, token_ciphertext,
       allowed_tools_json, tools_json, resources_json, last_sync_at, last_error
FROM mcp_servers WHERE id = ?
`, id).Scan(
		&cfg.ID, &cfg.Name, &cfg.Endpoint, &allowLocal, &enabledInt, &readOnlyInt, &tokenBlob,
		&allowedJSON, &toolsJSON, &resJSON, &cfg.LastSyncAt, &cfg.LastError,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ServerConfig{}, "", fmt.Errorf("external MCP server %q: %w", id, os.ErrNotExist)
	}
	if err != nil {
		return ServerConfig{}, "", err
	}
	cfg.AllowLocalhost = allowLocal != 0
	cfg.Enabled = enabledInt != 0
	cfg.ReadOnly = readOnlyInt != 0
	cfg.HasAuthToken = len(tokenBlob) > 0
	_ = json.Unmarshal([]byte(allowedJSON), &cfg.AllowedTools)
	_ = json.Unmarshal([]byte(toolsJSON), &cfg.Tools)
	_ = json.Unmarshal([]byte(resJSON), &cfg.Resources)
	cfg.ToolCount = len(cfg.Tools)
	cfg.ResourceCount = len(cfg.Resources)
	tok, err := s.open(tokenBlob)
	if err != nil {
		return ServerConfig{}, "", err
	}
	return cfg, tok, nil
}

func (s *store) saveServer(ctx context.Context, in ServerInput) (ServerConfig, error) {
	norm, err := NormalizeServerInput(in)
	if err != nil {
		return ServerConfig{}, err
	}

	var existingCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_servers WHERE id != ?`, norm.ID).Scan(&existingCount); err != nil {
		return ServerConfig{}, err
	}
	if existingCount >= maxServerCount {
		return ServerConfig{}, fmt.Errorf("external MCP server limit (%d) reached", maxServerCount)
	}

	var prevToken []byte
	var prevTools, prevRes, prevErr string
	var prevSync int64
	rowErr := s.db.QueryRowContext(ctx, `SELECT token_ciphertext, tools_json, resources_json, last_sync_at, last_error FROM mcp_servers WHERE id = ?`, norm.ID).
		Scan(&prevToken, &prevTools, &prevRes, &prevSync, &prevErr)
	if errors.Is(rowErr, sql.ErrNoRows) {
		prevTools = "[]"
		prevRes = "[]"
	} else if rowErr != nil {
		return ServerConfig{}, rowErr
	}

	tokenBlob := prevToken
	if norm.ClearAuthToken {
		tokenBlob = []byte{}
	} else if norm.AuthToken != "" {
		tokenBlob, err = s.seal(norm.AuthToken)
		if err != nil {
			return ServerConfig{}, err
		}
	}
	if tokenBlob == nil {
		tokenBlob = []byte{}
	}
	allowedBytes, _ := json.Marshal(norm.AllowedTools)

	_, err = s.db.ExecContext(ctx, `
INSERT INTO mcp_servers (id, name, endpoint, allow_localhost, enabled, read_only, token_ciphertext, allowed_tools_json, tools_json, resources_json, last_sync_at, last_error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	name = excluded.name,
	endpoint = excluded.endpoint,
	allow_localhost = excluded.allow_localhost,
	enabled = excluded.enabled,
	read_only = excluded.read_only,
	token_ciphertext = excluded.token_ciphertext,
	allowed_tools_json = excluded.allowed_tools_json
`, norm.ID, norm.Name, norm.Endpoint, boolInt(norm.AllowLocalhost), boolInt(*norm.Enabled), boolInt(*norm.ReadOnly), tokenBlob, string(allowedBytes), prevTools, prevRes, prevSync, prevErr)
	if err != nil {
		return ServerConfig{}, err
	}
	cfg, _, err := s.getServer(ctx, norm.ID)
	return cfg, err
}

func (s *store) updateCapabilities(ctx context.Context, id string, tools []RemoteTool, resources []RemoteResource, syncAt int64, syncErr string) (ServerConfig, error) {
	toolsBytes, _ := json.Marshal(tools)
	resBytes, _ := json.Marshal(resources)
	_, err := s.db.ExecContext(ctx, `
UPDATE mcp_servers
SET tools_json = ?, resources_json = ?, last_sync_at = ?, last_error = ?
WHERE id = ?
`, string(toolsBytes), string(resBytes), syncAt, syncErr, id)
	if err != nil {
		return ServerConfig{}, err
	}
	cfg, _, err := s.getServer(ctx, id)
	return cfg, err
}

func (s *store) deleteServer(ctx context.Context, id string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM mcp_servers WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *store) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	for i := range s.key {
		s.key[i] = 0
	}
	if s.db != nil {
		err := s.db.Close()
		s.db = nil
		return err
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
