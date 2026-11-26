package trade

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	tradeDirName = "trade"
	tradeDBName  = "trade.sqlite"
)

type store struct {
	dir    string
	db     *sql.DB
	now    func() time.Time
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

func openStore(dataDir string, nowFn func() time.Time) (*store, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("trade DataDir required")
	}
	dir := filepath.Join(dataDir, tradeDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, tradeDBName)
	dsn := sqliteFileURI(dbPath) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(FULL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const schemaSQL = `
CREATE TABLE IF NOT EXISTS trade_plans(
	id TEXT PRIMARY KEY,
	venue TEXT NOT NULL,
	symbol TEXT NOT NULL,
	interval TEXT NOT NULL,
	side TEXT NOT NULL,
	mode TEXT NOT NULL,
	strategy TEXT NOT NULL,
	status TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_trade_plans_status ON trade_plans (status, updated_at DESC);

CREATE TABLE IF NOT EXISTS trade_policy(
	id INTEGER PRIMARY KEY CHECK(id=1),
	payload_json TEXT NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS trade_kill(
	id INTEGER PRIMARY KEY CHECK(id=1),
	active INTEGER NOT NULL DEFAULT 0,
	reason TEXT NOT NULL DEFAULT '',
	updated_by TEXT NOT NULL DEFAULT 'system',
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS trade_bots(
	id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	status TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_trade_bots_status ON trade_bots (status, updated_at DESC);
`
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := os.Chmod(dbPath, 0o600); err != nil && !os.IsNotExist(err) {
		_ = db.Close()
		return nil, err
	}
	nowSec := nowFn().UTC().Unix()
	defPolicyBytes, _ := json.Marshal(DefaultRiskPolicy())
	if _, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO trade_policy(id, payload_json, updated_at) VALUES(1, ?, ?)`,
		string(defPolicyBytes), nowSec,
	); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO trade_kill(id, active, reason, updated_by, updated_at) VALUES(1, 0, '', 'system', ?)`,
		nowSec,
	); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &store{dir: dir, db: db, now: nowFn}, nil
}

func (s *store) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.db != nil {
		err := s.db.Close()
		s.db = nil
		return err
	}
	return nil
}

func (s *store) savePlan(ctx context.Context, p OrderPlan) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return errors.New("trade store closed")
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO trade_plans (id, venue, symbol, interval, side, mode, strategy, status, payload_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	status = excluded.status,
	payload_json = excluded.payload_json,
	updated_at = excluded.updated_at
`, p.ID, p.Venue, p.Symbol, p.Interval, p.Side, p.Mode, p.Strategy, p.Status, string(raw), p.CreatedAt, p.UpdatedAt)
	return err
}

func (s *store) getPlan(ctx context.Context, id string) (OrderPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return OrderPlan{}, errors.New("trade store closed")
	}
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM trade_plans WHERE id = ?`, strings.TrimSpace(id)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return OrderPlan{}, ErrPlanNotFound
	}
	if err != nil {
		return OrderPlan{}, err
	}
	var p OrderPlan
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return OrderPlan{}, err
	}
	return p, nil
}

func (s *store) listPlans(ctx context.Context, status string, limit int) ([]OrderPlan, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return nil, errors.New("trade store closed")
	}
	var (
		rows *sql.Rows
		err  error
	)
	if strings.TrimSpace(status) != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT payload_json FROM trade_plans WHERE status = ? ORDER BY updated_at DESC, id ASC LIMIT ?`,
			strings.TrimSpace(status), limit,
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT payload_json FROM trade_plans ORDER BY updated_at DESC, id ASC LIMIT ?`,
			limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]OrderPlan, 0)
	for rows.Next() {
		var raw string
		if scanErr := rows.Scan(&raw); scanErr != nil {
			return nil, scanErr
		}
		var p OrderPlan
		if uErr := json.Unmarshal([]byte(raw), &p); uErr != nil {
			continue
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *store) getPolicy(ctx context.Context) (RiskPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return RiskPolicy{}, errors.New("trade store closed")
	}
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM trade_policy WHERE id = 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultRiskPolicy(), nil
	}
	if err != nil {
		return RiskPolicy{}, err
	}
	var p RiskPolicy
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return DefaultRiskPolicy(), nil
	}
	return p, nil
}

func (s *store) savePolicy(ctx context.Context, p RiskPolicy) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return errors.New("trade store closed")
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE trade_policy SET payload_json = ?, updated_at = ? WHERE id = 1`,
		string(raw), s.now().UTC().Unix(),
	)
	return err
}

func (s *store) getKill(ctx context.Context) (KillState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return KillState{}, errors.New("trade store closed")
	}
	var k KillState
	var activeInt int
	err := s.db.QueryRowContext(ctx, `SELECT active, reason, updated_by, updated_at FROM trade_kill WHERE id = 1`).Scan(
		&activeInt, &k.Reason, &k.UpdatedBy, &k.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return KillState{}, nil
	}
	if err != nil {
		return KillState{}, err
	}
	k.Active = activeInt != 0
	return k, nil
}

func (s *store) setKill(ctx context.Context, k KillState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return errors.New("trade store closed")
	}
	activeInt := 0
	if k.Active {
		activeInt = 1
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE trade_kill SET active = ?, reason = ?, updated_by = ?, updated_at = ? WHERE id = 1`,
		activeInt, k.Reason, k.UpdatedBy, k.UpdatedAt,
	)
	return err
}
