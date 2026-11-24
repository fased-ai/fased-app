package hyperliquid

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	orderStoreDirName   = "hyperliquid"
	orderStoreDBName    = "orders.sqlite"
	defaultOrderLimit   = 64
	metaLastNonceKey    = "last_nonce"
	orderStoreBusyPragm = "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(FULL)"
)

// OrderStore durably persists Hyperliquid controlled order records and
// strictly monotonic signer nonces across process restarts.
type OrderStore struct {
	mu     sync.Mutex
	db     *sql.DB
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

// OpenOrderStore opens or creates `<dataDir>/hyperliquid/orders.sqlite` with
// strict WAL durability and 0700/0600 filesystem permissions.
func OpenOrderStore(dataDir string) (*OrderStore, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("hyperliquid order store dataDir required")
	}
	dir := filepath.Join(dataDir, orderStoreDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, orderStoreDBName)
	db, err := sql.Open("sqlite", sqliteFileURI(dbPath)+orderStoreBusyPragm)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const schemaSQL = `
CREATE TABLE IF NOT EXISTS hyperliquid_orders (
	id TEXT PRIMARY KEY,
	order_id INTEGER NOT NULL DEFAULT 0,
	client_order_id TEXT NOT NULL DEFAULT '',
	asset TEXT NOT NULL,
	is_buy INTEGER NOT NULL,
	is_cross INTEGER NOT NULL DEFAULT 0,
	limit_price TEXT NOT NULL,
	size TEXT NOT NULL,
	leverage INTEGER NOT NULL,
	reduce_only INTEGER NOT NULL DEFAULT 0,
	time_in_force TEXT NOT NULL,
	state TEXT NOT NULL,
	nonce INTEGER NOT NULL,
	signer_address TEXT NOT NULL,
	connection_id TEXT NOT NULL,
	typed_data_digest TEXT NOT NULL,
	signature_hex TEXT NOT NULL,
	filled_size TEXT NOT NULL DEFAULT '',
	avg_price TEXT NOT NULL DEFAULT '',
	message TEXT NOT NULL DEFAULT '',
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_hyperliquid_orders_oid ON hyperliquid_orders(order_id);
CREATE INDEX IF NOT EXISTS idx_hyperliquid_orders_cloid ON hyperliquid_orders(client_order_id);
CREATE INDEX IF NOT EXISTS idx_hyperliquid_orders_recent ON hyperliquid_orders(updated_at DESC, nonce DESC);
CREATE TABLE IF NOT EXISTS hyperliquid_meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);`
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		_ = db.Close()
		return nil, err
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(dbPath, 0o600)
	}
	return &OrderStore{db: db}, nil
}

// Close closes the underlying SQLite connection safely and idempotently.
func (s *OrderStore) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return nil
	}
	s.closed = true
	return s.db.Close()
}

// LoadState returns up to `limit` most recent orders (ordered oldest-to-newest
// for chronological appending) alongside the highest persisted signer nonce.
func (s *OrderStore) LoadState(ctx context.Context, limit int) ([]OrderRecord, uint64, error) {
	if s == nil {
		return nil, 0, errors.New("hyperliquid order store unavailable")
	}
	if limit <= 0 {
		limit = defaultOrderLimit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return nil, 0, errors.New("hyperliquid order store closed")
	}

	var savedNonce uint64
	var metaVal string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM hyperliquid_meta WHERE key = ?`, metaLastNonceKey).Scan(&metaVal)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, 0, err
	}
	if err == nil && metaVal != "" {
		parsed, pErr := strconv.ParseUint(metaVal, 10, 64)
		if pErr != nil {
			return nil, 0, fmt.Errorf("corrupt hyperliquid last_nonce %q: %w", metaVal, pErr)
		}
		savedNonce = parsed
	}

	var maxOrderNonce sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(nonce) FROM hyperliquid_orders`).Scan(&maxOrderNonce); err != nil {
		return nil, 0, err
	}
	if maxOrderNonce.Valid && maxOrderNonce.Int64 > 0 && uint64(maxOrderNonce.Int64) > savedNonce {
		savedNonce = uint64(maxOrderNonce.Int64)
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT
	id, order_id, client_order_id, asset, is_buy, is_cross,
	limit_price, size, leverage, reduce_only, time_in_force,
	state, nonce, signer_address, connection_id, typed_data_digest,
	signature_hex, filled_size, avg_price, message, updated_at
FROM hyperliquid_orders
ORDER BY updated_at DESC, nonce DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var desc []OrderRecord
	for rows.Next() {
		var rec OrderRecord
		var oid int64
		var isBuy, isCross, reduceOnly int64
		var nonce int64
		if err := rows.Scan(
			&rec.ID, &oid, &rec.ClientOrderID, &rec.Asset, &isBuy, &isCross,
			&rec.LimitPrice, &rec.Size, &rec.Leverage, &reduceOnly, &rec.TimeInForce,
			&rec.State, &nonce, &rec.SignerAddress, &rec.ConnectionID, &rec.TypedDataDigest,
			&rec.SignatureHex, &rec.FilledSize, &rec.AvgPrice, &rec.Message, &rec.UpdatedAt,
		); err != nil {
			return nil, 0, err
		}
		if oid > 0 {
			rec.OrderID = uint64(oid)
		}
		if nonce > 0 {
			rec.Nonce = uint64(nonce)
		}
		rec.IsBuy = isBuy != 0
		rec.IsCross = isCross != 0
		rec.ReduceOnly = reduceOnly != 0
		desc = append(desc, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	// Reverse to chronological (oldest-to-newest) order so newest orders sit at the tail.
	orders := make([]OrderRecord, len(desc))
	for i := range desc {
		orders[len(desc)-1-i] = desc[i]
	}
	return orders, savedNonce, nil
}

// UpsertOrder inserts or updates a persisted controlled order record and
// guarantees `last_nonce` does not trail `rec.Nonce`.
func (s *OrderStore) UpsertOrder(ctx context.Context, rec OrderRecord) error {
	if s == nil {
		return errors.New("hyperliquid order store unavailable")
	}
	if strings.TrimSpace(rec.ID) == "" {
		return errors.New("hyperliquid order id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return errors.New("hyperliquid order store closed")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
INSERT INTO hyperliquid_orders (
	id, order_id, client_order_id, asset, is_buy, is_cross,
	limit_price, size, leverage, reduce_only, time_in_force,
	state, nonce, signer_address, connection_id, typed_data_digest,
	signature_hex, filled_size, avg_price, message, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	order_id = excluded.order_id,
	client_order_id = CASE WHEN excluded.client_order_id != '' THEN excluded.client_order_id ELSE hyperliquid_orders.client_order_id END,
	state = excluded.state,
	filled_size = CASE WHEN excluded.filled_size != '' THEN excluded.filled_size ELSE hyperliquid_orders.filled_size END,
	avg_price = CASE WHEN excluded.avg_price != '' THEN excluded.avg_price ELSE hyperliquid_orders.avg_price END,
	message = excluded.message,
	updated_at = excluded.updated_at`,
		rec.ID, int64(rec.OrderID), strings.ToLower(strings.TrimSpace(rec.ClientOrderID)),
		rec.Asset, boolToInt(rec.IsBuy), boolToInt(rec.IsCross),
		rec.LimitPrice, rec.Size, int64(rec.Leverage), boolToInt(rec.ReduceOnly), rec.TimeInForce,
		rec.State, int64(rec.Nonce), rec.SignerAddress, rec.ConnectionID, rec.TypedDataDigest,
		rec.SignatureHex, rec.FilledSize, rec.AvgPrice, rec.Message, rec.UpdatedAt,
	)
	if err != nil {
		return err
	}
	if rec.Nonce > 0 {
		if err := saveLastNonceTx(ctx, tx, rec.Nonce); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SaveLastNonce durably persists the monotonic signer nonce floor.
func (s *OrderStore) SaveLastNonce(ctx context.Context, nonce uint64) error {
	if s == nil {
		return errors.New("hyperliquid order store unavailable")
	}
	if nonce == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return errors.New("hyperliquid order store closed")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := saveLastNonceTx(ctx, tx, nonce); err != nil {
		return err
	}
	return tx.Commit()
}

func saveLastNonceTx(ctx context.Context, tx *sql.Tx, nonce uint64) error {
	var curStr string
	err := tx.QueryRowContext(ctx, `SELECT value FROM hyperliquid_meta WHERE key = ?`, metaLastNonceKey).Scan(&curStr)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && curStr != "" {
		if cur, pErr := strconv.ParseUint(curStr, 10, 64); pErr == nil && cur >= nonce {
			return nil
		}
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO hyperliquid_meta (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		metaLastNonceKey, strconv.FormatUint(nonce, 10),
	)
	return err
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
