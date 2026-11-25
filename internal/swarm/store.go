package swarm

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	swarmDirName = "swarm"
	swarmDBName  = "swarm.sqlite"
	maxSignals   = 200
)

type store struct {
	dir  string
	db   *sql.DB
	seed [32]byte
	now  func() time.Time
	mu   sync.Mutex
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

func openStore(dataDir string, nowFn func() time.Time) (*store, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("swarm DataDir required")
	}
	dir := filepath.Join(dataDir, swarmDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, swarmDBName)
	dsn := sqliteFileURI(dbPath) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(FULL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const schemaSQL = `
CREATE TABLE IF NOT EXISTS swarm_config(
	id INTEGER PRIMARY KEY CHECK(id=1),
	enabled INTEGER NOT NULL DEFAULT 0,
	copy_mode TEXT NOT NULL DEFAULT 'shadow',
	local_peer_id TEXT NOT NULL,
	local_seed BLOB NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS swarm_peers(
	peer_id TEXT PRIMARY KEY,
	alias TEXT NOT NULL,
	pubkey_hex TEXT NOT NULL,
	status TEXT NOT NULL,
	added_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS swarm_nonces(
	nonce TEXT PRIMARY KEY,
	seen_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS swarm_signals(
	envelope_id TEXT PRIMARY KEY,
	nonce TEXT NOT NULL,
	sender_pubkey_hex TEXT NOT NULL,
	kind TEXT NOT NULL,
	symbol TEXT NOT NULL,
	direction TEXT NOT NULL,
	thesis TEXT NOT NULL,
	confidence REAL NOT NULL,
	timestamp INTEGER NOT NULL,
	signature_hex TEXT NOT NULL,
	untrusted INTEGER NOT NULL CHECK(untrusted = 1),
	verified_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_swarm_signals_ts ON swarm_signals(verified_at DESC);
`
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		_ = db.Close()
		return nil, err
	}

	st := &store{dir: dir, db: db, now: nowFn}
	if err := st.ensureConfigRow(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return st, nil
}

func (s *store) ensureConfigRow(ctx context.Context) error {
	var seedBytes []byte
	err := s.db.QueryRowContext(ctx, `SELECT local_seed FROM swarm_config WHERE id = 1`).Scan(&seedBytes)
	if err == nil && len(seedBytes) == ed25519.SeedSize {
		copy(s.seed[:], seedBytes)
		clear(seedBytes)
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var seed [32]byte
	if _, rErr := io.ReadFull(rand.Reader, seed[:]); rErr != nil {
		return rErr
	}
	copy(s.seed[:], seed[:])
	localPeerID := "peer-" + hex.EncodeToString(seed[:6])
	nowUnix := s.now().UTC().Unix()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO swarm_config(id, enabled, copy_mode, local_peer_id, local_seed, updated_at)
VALUES(1, 0, 'shadow', ?, ?, ?)
`, localPeerID, seed[:], nowUnix)
	clear(seed[:])
	return err
}

func (s *store) close() error {
	clear(s.seed[:])
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *store) localKey() ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(s.seed[:])
}

func (s *store) getConfig(ctx context.Context) (SwarmConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var cfg SwarmConfig
	var enabled int
	if err := s.db.QueryRowContext(ctx, `SELECT enabled, copy_mode, local_peer_id, updated_at FROM swarm_config WHERE id = 1`).
		Scan(&enabled, &cfg.CopyMode, &cfg.LocalPeerID, &cfg.UpdatedAt); err != nil {
		return SwarmConfig{}, err
	}
	cfg.Enabled = enabled == 1
	cfg.LiveCopyLocked = !SwarmLiveCopyEnabled
	pub := s.localKey().Public().(ed25519.PublicKey)
	cfg.LocalPubKeyHex = hex.EncodeToString(pub)
	return cfg, nil
}

func (s *store) setConfig(ctx context.Context, enabled bool, copyMode string) (SwarmConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	en := 0
	if enabled {
		en = 1
	}
	nowUnix := s.now().UTC().Unix()
	if _, err := s.db.ExecContext(ctx, `
UPDATE swarm_config SET enabled = ?, copy_mode = ?, updated_at = ? WHERE id = 1
`, en, copyMode, nowUnix); err != nil {
		return SwarmConfig{}, err
	}
	pub := s.localKey().Public().(ed25519.PublicKey)
	var peerID string
	_ = s.db.QueryRowContext(ctx, `SELECT local_peer_id FROM swarm_config WHERE id = 1`).Scan(&peerID)
	return SwarmConfig{
		Enabled:        enabled,
		CopyMode:       copyMode,
		LocalPeerID:    peerID,
		LocalPubKeyHex: hex.EncodeToString(pub),
		LiveCopyLocked: !SwarmLiveCopyEnabled,
		UpdatedAt:      nowUnix,
	}, nil
}

func (s *store) savePeer(ctx context.Context, peer Peer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO swarm_peers(peer_id, alias, pubkey_hex, status, added_at)
VALUES(?, ?, ?, ?, ?)
ON CONFLICT(peer_id) DO UPDATE SET alias = excluded.alias, pubkey_hex = excluded.pubkey_hex, status = excluded.status
`, peer.PeerID, peer.Alias, peer.PubKeyHex, peer.Status, peer.AddedAt)
	return err
}

func (s *store) listPeers(ctx context.Context) ([]Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.QueryContext(ctx, `SELECT peer_id, alias, pubkey_hex, status, added_at FROM swarm_peers ORDER BY added_at DESC, peer_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Peer{}
	for rows.Next() {
		var p Peer
		if err := rows.Scan(&p.PeerID, &p.Alias, &p.PubKeyHex, &p.Status, &p.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *store) recordEnvelopeIfFreshNonce(ctx context.Context, env Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO swarm_nonces(nonce, seen_at) VALUES(?, ?)`, env.Nonce, env.VerifiedAt)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrReplayDetected
	}
	_, _ = tx.ExecContext(ctx, `DELETE FROM swarm_nonces WHERE seen_at < ?`, env.VerifiedAt-int64((2*MaxClockSkew).Seconds()))

	if _, err := tx.ExecContext(ctx, `
INSERT OR REPLACE INTO swarm_signals(envelope_id, nonce, sender_pubkey_hex, kind, symbol, direction, thesis, confidence, timestamp, signature_hex, untrusted, verified_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)
`, env.EnvelopeID, env.Nonce, env.SenderPubKeyHex, env.Kind, env.Symbol, env.Direction, env.Thesis, env.Confidence, env.Timestamp, env.SignatureHex, env.VerifiedAt); err != nil {
		return err
	}
	_, _ = tx.ExecContext(ctx, `
DELETE FROM swarm_signals WHERE envelope_id NOT IN (
	SELECT envelope_id FROM swarm_signals ORDER BY verified_at DESC LIMIT ?
)`, maxSignals)
	return tx.Commit()
}

func (s *store) listSignals(ctx context.Context, limit int) ([]Envelope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > maxSignals {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT envelope_id, nonce, sender_pubkey_hex, kind, symbol, direction, thesis, confidence, timestamp, signature_hex, untrusted, verified_at
FROM swarm_signals ORDER BY verified_at DESC, timestamp DESC LIMIT ?
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Envelope{}
	for rows.Next() {
		var env Envelope
		var untrusted int
		if err := rows.Scan(&env.EnvelopeID, &env.Nonce, &env.SenderPubKeyHex, &env.Kind, &env.Symbol, &env.Direction, &env.Thesis, &env.Confidence, &env.Timestamp, &env.SignatureHex, &untrusted, &env.VerifiedAt); err != nil {
			return nil, err
		}
		env.Untrusted = untrusted == 1
		out = append(out, env)
	}
	return out, rows.Err()
}
