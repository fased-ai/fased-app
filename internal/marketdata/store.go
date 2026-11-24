package marketdata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const marketDataDirName = "marketdata"

// Store manages isolated marketdata metadata and monthly SQLite partitions
// under <dataDir>/marketdata/ without touching core.sqlite or agents.sqlite.
type Store struct {
	dir        string
	now        func() time.Time
	mu         sync.Mutex
	metaDB     *sql.DB
	partitions map[string]*sql.DB
	closed     bool
}

func sqliteFileURI(path string) string {
	slashPath := filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		if !strings.HasPrefix(slashPath, "/") {
			slashPath = "/" + slashPath
		}
	}
	escaped := strings.ReplaceAll(slashPath, "?", "%3F")
	escaped = strings.ReplaceAll(escaped, "#", "%23")
	return "file://" + escaped
}

func openSQLiteDB(path string) (*sql.DB, error) {
	dsn := sqliteFileURI(path) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// NewStore opens (or initializes) the isolated <dataDir>/marketdata store.
func NewStore(dataDir string, nowFn func() time.Time) (*Store, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("marketdata store requires non-empty dataDir")
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	dir := filepath.Join(dataDir, marketDataDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create marketdata dir: %w", err)
	}
	metaPath := filepath.Join(dir, "meta.sqlite")
	metaDB, err := openSQLiteDB(metaPath)
	if err != nil {
		return nil, fmt.Errorf("open meta.sqlite: %w", err)
	}
	s := &Store{
		dir:        dir,
		now:        nowFn,
		metaDB:     metaDB,
		partitions: make(map[string]*sql.DB),
	}
	if err := s.initMetaSchema(); err != nil {
		_ = metaDB.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) initMetaSchema() error {
	const ddl = `
CREATE TABLE IF NOT EXISTS watchlists (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	items_json TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS instruments (
	venue TEXT NOT NULL,
	symbol TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (venue, symbol)
);
CREATE TABLE IF NOT EXISTS macro_cache (
	series_id TEXT PRIMARY KEY,
	payload_json TEXT NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sec_cache (
	cik TEXT PRIMARY KEY,
	payload_json TEXT NOT NULL,
	updated_at INTEGER NOT NULL
);
`
	_, err := s.metaDB.Exec(ddl)
	return err
}

func partitionKeyForTime(tsSec int64) string {
	return time.Unix(tsSec, 0).UTC().Format("2006_01")
}

func partitionFileName(monthKey string) string {
	return fmt.Sprintf("candles_%s.sqlite", monthKey)
}

func (s *Store) getPartitionDB(monthKey string, create bool) (*sql.DB, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("marketdata store closed")
	}
	if db, ok := s.partitions[monthKey]; ok {
		return db, nil
	}
	path := filepath.Join(s.dir, partitionFileName(monthKey))
	if !create {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil, nil
		}
	}
	db, err := openSQLiteDB(path)
	if err != nil {
		return nil, err
	}
	const ddl = `
CREATE TABLE IF NOT EXISTS candles (
	venue TEXT NOT NULL,
	symbol TEXT NOT NULL,
	interval TEXT NOT NULL,
	ts_sec INTEGER NOT NULL,
	open REAL NOT NULL,
	high REAL NOT NULL,
	low REAL NOT NULL,
	close REAL NOT NULL,
	volume REAL NOT NULL,
	buy_volume REAL NOT NULL,
	sell_volume REAL NOT NULL,
	trades INTEGER NOT NULL,
	PRIMARY KEY (venue, symbol, interval, ts_sec)
);
`
	if _, err := db.Exec(ddl); err != nil {
		_ = db.Close()
		return nil, err
	}
	s.partitions[monthKey] = db
	return db, nil
}

// UpsertCandles persists standard OHLCV candles into their UTC monthly partitions.
func (s *Store) UpsertCandles(ctx context.Context, venue, symbol, interval string, candles []Candle) error {
	if len(candles) == 0 {
		return nil
	}
	clean, err := sanitizeCandles(candles)
	if err != nil {
		return err
	}
	grouped := make(map[string][]Candle)
	for _, c := range clean {
		key := partitionKeyForTime(c.Time)
		grouped[key] = append(grouped[key], c)
	}
	for monthKey, batch := range grouped {
		db, err := s.getPartitionDB(monthKey, true)
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, `
INSERT INTO candles (venue, symbol, interval, ts_sec, open, high, low, close, volume, buy_volume, sell_volume, trades)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(venue, symbol, interval, ts_sec) DO UPDATE SET
	open=excluded.open,
	high=excluded.high,
	low=excluded.low,
	close=excluded.close,
	volume=excluded.volume,
	buy_volume=excluded.buy_volume,
	sell_volume=excluded.sell_volume,
	trades=excluded.trades
`)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		for _, c := range batch {
			if _, execErr := stmt.ExecContext(ctx, venue, symbol, interval, c.Time, c.Open, c.High, c.Low, c.Close, c.Volume, c.BuyVolume, c.SellVolume, c.Trades); execErr != nil {
				_ = stmt.Close()
				_ = tx.Rollback()
				return execErr
			}
		}
		if err := stmt.Close(); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// QueryCandles loads cached candles across existing monthly partitions ordered ascending by time.
func (s *Store) QueryCandles(ctx context.Context, venue, symbol, interval string, fromSec, toSec int64, limit int) ([]Candle, error) {
	if limit <= 0 || limit > maxCandlesLimit {
		limit = maxCandlesLimit
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var monthKeys []string
	for _, ent := range entries {
		name := ent.Name()
		if !ent.IsDir() && strings.HasPrefix(name, "candles_") && strings.HasSuffix(name, ".sqlite") {
			key := strings.TrimSuffix(strings.TrimPrefix(name, "candles_"), ".sqlite")
			if _, parseErr := time.Parse("2006_01", key); parseErr == nil {
				monthKeys = append(monthKeys, key)
			}
		}
	}
	sort.Strings(monthKeys)

	var out []Candle
	for _, key := range monthKeys {
		if !partitionOverlapsWindow(key, fromSec, toSec) {
			continue
		}
		db, err := s.getPartitionDB(key, false)
		if err != nil {
			return nil, err
		}
		if db == nil {
			continue
		}
		var rows *sql.Rows
		if toSec > 0 {
			rows, err = db.QueryContext(ctx, `
SELECT ts_sec, open, high, low, close, volume, buy_volume, sell_volume, trades
FROM candles
WHERE venue = ? AND symbol = ? AND interval = ? AND ts_sec >= ? AND ts_sec <= ?
ORDER BY ts_sec ASC
`, venue, symbol, interval, fromSec, toSec)
		} else {
			rows, err = db.QueryContext(ctx, `
SELECT ts_sec, open, high, low, close, volume, buy_volume, sell_volume, trades
FROM candles
WHERE venue = ? AND symbol = ? AND interval = ? AND ts_sec >= ?
ORDER BY ts_sec ASC
`, venue, symbol, interval, fromSec)
		}
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var c Candle
			if scanErr := rows.Scan(&c.Time, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume, &c.BuyVolume, &c.SellVolume, &c.Trades); scanErr != nil {
				_ = rows.Close()
				return nil, scanErr
			}
			out = append(out, c)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func partitionOverlapsWindow(monthKey string, fromSec, toSec int64) bool {
	start, err := time.Parse("2006_01", monthKey)
	if err != nil {
		return false
	}
	end := start.AddDate(0, 1, 0).Add(-time.Second)
	if fromSec > 0 && end.Unix() < fromSec {
		return false
	}
	if toSec > 0 && start.Unix() > toSec {
		return false
	}
	return true
}

// PruneOldPartitions closes and removes monthly candle partition files whose end month
// precedes now() - maxAgeDays.
func (s *Store) PruneOldPartitions(maxAgeDays int) (int, error) {
	if maxAgeDays <= 0 {
		return 0, nil
	}
	cutoff := s.now().UTC().Add(-time.Duration(maxAgeDays) * 24 * time.Hour)
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0, err
	}
	pruned := 0
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasPrefix(name, "candles_") || !strings.HasSuffix(name, ".sqlite") {
			continue
		}
		key := strings.TrimSuffix(strings.TrimPrefix(name, "candles_"), ".sqlite")
		monthStart, parseErr := time.Parse("2006_01", key)
		if parseErr != nil {
			continue
		}
		monthEnd := monthStart.AddDate(0, 1, 0)
		if !monthEnd.Before(cutoff) {
			continue
		}
		s.mu.Lock()
		if db, ok := s.partitions[key]; ok {
			_ = db.Close()
			delete(s.partitions, key)
		}
		s.mu.Unlock()

		base := filepath.Join(s.dir, name)
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if remErr := os.Remove(base + suffix); remErr != nil && !os.IsNotExist(remErr) {
				return pruned, remErr
			}
		}
		pruned++
	}
	return pruned, nil
}

// ListWatchlists returns all persisted user watchlists sorted by updatedAt descending.
func (s *Store) ListWatchlists(ctx context.Context) ([]Watchlist, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, errors.New("marketdata store closed")
	}
	rows, err := s.metaDB.QueryContext(ctx, `
SELECT id, name, items_json, created_at, updated_at
FROM watchlists
ORDER BY updated_at DESC, id ASC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Watchlist{}
	for rows.Next() {
		var w Watchlist
		var rawItems string
		if err := rows.Scan(&w.ID, &w.Name, &rawItems, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(rawItems), &w.Items); err != nil {
			w.Items = []WatchlistItem{}
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// SaveWatchlist validates and persists a watchlist.
func (s *Store) SaveWatchlist(ctx context.Context, w Watchlist) (Watchlist, error) {
	clean, err := ValidateWatchlist(w)
	if err != nil {
		return Watchlist{}, err
	}
	now := s.now().UTC().Unix()
	if clean.CreatedAt == 0 {
		clean.CreatedAt = now
	}
	clean.UpdatedAt = now
	itemsBytes, err := json.Marshal(clean.Items)
	if err != nil {
		return Watchlist{}, err
	}
	_, err = s.metaDB.ExecContext(ctx, `
INSERT INTO watchlists (id, name, items_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	name=excluded.name,
	items_json=excluded.items_json,
	updated_at=excluded.updated_at
`, clean.ID, clean.Name, string(itemsBytes), clean.CreatedAt, clean.UpdatedAt)
	if err != nil {
		return Watchlist{}, err
	}
	return clean, nil
}

// DeleteWatchlist deletes a watchlist by ID.
func (s *Store) DeleteWatchlist(ctx context.Context, id string) (bool, error) {
	res, err := s.metaDB.ExecContext(ctx, `DELETE FROM watchlists WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SaveMacroCache caches fetched FRED points.
func (s *Store) SaveMacroCache(ctx context.Context, seriesID string, pts []MacroPoint) error {
	raw, err := json.Marshal(pts)
	if err != nil {
		return err
	}
	_, err = s.metaDB.ExecContext(ctx, `
INSERT INTO macro_cache (series_id, payload_json, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(series_id) DO UPDATE SET payload_json=excluded.payload_json, updated_at=excluded.updated_at
`, seriesID, string(raw), s.now().UTC().Unix())
	return err
}

// LoadMacroCache retrieves cached FRED points if stored.
func (s *Store) LoadMacroCache(ctx context.Context, seriesID string) ([]MacroPoint, int64, bool, error) {
	var raw string
	var updated int64
	err := s.metaDB.QueryRowContext(ctx, `SELECT payload_json, updated_at FROM macro_cache WHERE series_id = ?`, seriesID).Scan(&raw, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	var pts []MacroPoint
	if err := json.Unmarshal([]byte(raw), &pts); err != nil {
		return nil, 0, false, nil
	}
	return pts, updated, true, nil
}

// Close deterministically closes all SQLite partitions and meta.sqlite.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var errs []error
	for key, db := range s.partitions {
		if err := db.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close partition %s: %w", key, err))
		}
		delete(s.partitions, key)
	}
	if s.metaDB != nil {
		if err := s.metaDB.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close meta.sqlite: %w", err))
		}
		s.metaDB = nil
	}
	return errors.Join(errs...)
}
