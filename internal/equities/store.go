package equities

import (
	"context"
	"database/sql"
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
	equitiesDirName = "equities"
	equitiesDBName  = "equities.sqlite"
	maxOrders       = 200
)

type store struct {
	dir string
	db  *sql.DB
	now func() time.Time
	mu  sync.Mutex
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
		return nil, errors.New("equities DataDir required")
	}
	dir := filepath.Join(dataDir, equitiesDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, equitiesDBName)
	dsn := sqliteFileURI(dbPath) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(FULL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const schemaSQL = `
CREATE TABLE IF NOT EXISTS equities_orders(
	id TEXT PRIMARY KEY,
	venue TEXT NOT NULL,
	account_id TEXT NOT NULL DEFAULT '',
	symbol TEXT NOT NULL,
	is_option INTEGER NOT NULL,
	side TEXT NOT NULL,
	order_type TEXT NOT NULL,
	quantity REAL NOT NULL,
	limit_price REAL NOT NULL DEFAULT 0,
	environment TEXT NOT NULL,
	status TEXT NOT NULL,
	simulated INTEGER NOT NULL,
	message TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_equities_orders_created ON equities_orders(created_at DESC);
`
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &store{dir: dir, db: db, now: nowFn}, nil
}

func (s *store) close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *store) appendOrder(ctx context.Context, rec OrderReceipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	isOpt, sim := 0, 0
	if rec.IsOption {
		isOpt = 1
	}
	if rec.Simulated {
		sim = 1
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO equities_orders(id, venue, account_id, symbol, is_option, side, order_type, quantity, limit_price, environment, status, simulated, message, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, rec.ID, rec.Venue, rec.AccountID, rec.Symbol, isOpt, rec.Side, rec.OrderType, rec.Quantity, rec.LimitPrice, rec.Environment, rec.Status, sim, rec.Message, rec.CreatedAt); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
DELETE FROM equities_orders WHERE id NOT IN (
	SELECT id FROM equities_orders ORDER BY created_at DESC LIMIT ?
)`, maxOrders)
	return err
}

func (s *store) listOrders(ctx context.Context, limit int) ([]OrderReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > maxOrders {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, venue, account_id, symbol, is_option, side, order_type, quantity, limit_price, environment, status, simulated, message, created_at
FROM equities_orders ORDER BY created_at DESC LIMIT ?
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OrderReceipt{}
	for rows.Next() {
		var rec OrderReceipt
		var isOpt, sim int
		if err := rows.Scan(&rec.ID, &rec.Venue, &rec.AccountID, &rec.Symbol, &isOpt, &rec.Side, &rec.OrderType, &rec.Quantity, &rec.LimitPrice, &rec.Environment, &rec.Status, &sim, &rec.Message, &rec.CreatedAt); err != nil {
			return nil, err
		}
		rec.IsOption = isOpt == 1
		rec.Simulated = sim == 1
		out = append(out, rec)
	}
	return out, rows.Err()
}
