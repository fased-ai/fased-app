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
	mu         sync.RWMutex
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
CREATE TABLE IF NOT EXISTS chart_drawings (
	id TEXT PRIMARY KEY,
	venue TEXT NOT NULL,
	symbol TEXT NOT NULL,
	interval TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL,
	p1_time INTEGER NOT NULL,
	p1_price REAL NOT NULL,
	p2_time INTEGER,
	p2_price REAL,
	label TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chart_drawings_venue_symbol ON chart_drawings (venue, symbol, updated_at DESC);
CREATE TABLE IF NOT EXISTS custom_feeds (
	symbol TEXT NOT NULL,
	interval TEXT NOT NULL,
	bar_count INTEGER NOT NULL,
	last_time INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (symbol, interval)
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errors.New("marketdata store closed")
	}
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
		if db, ok := s.partitions[key]; ok {
			_ = db.Close()
			delete(s.partitions, key)
		}

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
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.metaDB == nil {
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.metaDB == nil {
		return Watchlist{}, errors.New("marketdata store closed")
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.metaDB == nil {
		return false, errors.New("marketdata store closed")
	}
	res, err := s.metaDB.ExecContext(ctx, `DELETE FROM watchlists WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func scanDrawingRow(scan func(dest ...any) error) (ChartDrawing, error) {
	var d ChartDrawing
	var p2Time sql.NullInt64
	var p2Price sql.NullFloat64
	if err := scan(
		&d.ID,
		&d.Venue,
		&d.Symbol,
		&d.Interval,
		&d.Kind,
		&d.P1.Time,
		&d.P1.Price,
		&p2Time,
		&p2Price,
		&d.Label,
		&d.CreatedAt,
		&d.UpdatedAt,
	); err != nil {
		return ChartDrawing{}, err
	}
	if p2Time.Valid && p2Price.Valid {
		d.P2 = &DrawingPoint{Time: p2Time.Int64, Price: p2Price.Float64}
	}
	return d, nil
}

// ListDrawings lists saved chart drawings filtered optionally by venue, symbol, and interval.
func (s *Store) ListDrawings(ctx context.Context, venue, symbol, interval string) ([]ChartDrawing, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.metaDB == nil {
		return nil, errors.New("marketdata store closed")
	}
	var clauses []string
	var args []any
	if v := strings.TrimSpace(venue); v != "" {
		clauses = append(clauses, "venue = ?")
		args = append(args, v)
	}
	if sym := strings.TrimSpace(symbol); sym != "" {
		clauses = append(clauses, "symbol = ?")
		args = append(args, sym)
	}
	if iv := strings.TrimSpace(interval); iv != "" {
		clauses = append(clauses, "(interval = '' OR interval = ?)")
		args = append(args, iv)
	}
	query := `
SELECT id, venue, symbol, interval, kind, p1_time, p1_price, p2_time, p2_price, label, created_at, updated_at
FROM chart_drawings`
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY updated_at DESC, id ASC"

	rows, err := s.metaDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ChartDrawing, 0)
	for rows.Next() {
		d, scanErr := scanDrawingRow(rows.Scan)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetDrawing returns a single saved chart drawing by ID.
func (s *Store) GetDrawing(ctx context.Context, id string) (ChartDrawing, bool, error) {
	cleanID := strings.TrimSpace(id)
	if !idPattern.MatchString(cleanID) {
		return ChartDrawing{}, false, errors.New("drawing id must match [A-Za-z0-9._-] (1-64 chars)")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.metaDB == nil {
		return ChartDrawing{}, false, errors.New("marketdata store closed")
	}
	row := s.metaDB.QueryRowContext(ctx, `
SELECT id, venue, symbol, interval, kind, p1_time, p1_price, p2_time, p2_price, label, created_at, updated_at
FROM chart_drawings
WHERE id = ?
`, cleanID)
	d, err := scanDrawingRow(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ChartDrawing{}, false, nil
		}
		return ChartDrawing{}, false, err
	}
	return d, true, nil
}

// SaveDrawing validates and upserts a chart drawing subject to per-symbol and total caps.
func (s *Store) SaveDrawing(ctx context.Context, d ChartDrawing) (ChartDrawing, error) {
	clean, err := ValidateChartDrawing(d)
	if err != nil {
		return ChartDrawing{}, err
	}
	now := s.now().UTC().Unix()
	if clean.CreatedAt == 0 {
		clean.CreatedAt = now
	}
	clean.UpdatedAt = now

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.metaDB == nil {
		return ChartDrawing{}, errors.New("marketdata store closed")
	}

	var existingCreated int64
	exists := false
	err = s.metaDB.QueryRowContext(ctx, `SELECT created_at FROM chart_drawings WHERE id = ?`, clean.ID).Scan(&existingCreated)
	if err == nil {
		exists = true
		if existingCreated > 0 {
			clean.CreatedAt = existingCreated
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return ChartDrawing{}, err
	}

	if !exists {
		var symCount int
		if err := s.metaDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM chart_drawings WHERE venue = ? AND symbol = ?`, clean.Venue, clean.Symbol).Scan(&symCount); err != nil {
			return ChartDrawing{}, err
		}
		if symCount >= maxDrawingsPerSymbol {
			return ChartDrawing{}, fmt.Errorf("symbol drawings limit (%d) reached", maxDrawingsPerSymbol)
		}
		var totalCount int
		if err := s.metaDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM chart_drawings`).Scan(&totalCount); err != nil {
			return ChartDrawing{}, err
		}
		if totalCount >= maxDrawingsTotal {
			return ChartDrawing{}, fmt.Errorf("total drawings limit (%d) reached", maxDrawingsTotal)
		}
	}

	var p2Time sql.NullInt64
	var p2Price sql.NullFloat64
	if clean.P2 != nil {
		p2Time = sql.NullInt64{Int64: clean.P2.Time, Valid: true}
		p2Price = sql.NullFloat64{Float64: clean.P2.Price, Valid: true}
	}
	if _, err := s.metaDB.ExecContext(ctx, `
INSERT INTO chart_drawings (id, venue, symbol, interval, kind, p1_time, p1_price, p2_time, p2_price, label, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	venue = excluded.venue,
	symbol = excluded.symbol,
	interval = excluded.interval,
	kind = excluded.kind,
	p1_time = excluded.p1_time,
	p1_price = excluded.p1_price,
	p2_time = excluded.p2_time,
	p2_price = excluded.p2_price,
	label = excluded.label,
	updated_at = excluded.updated_at
`, clean.ID, clean.Venue, clean.Symbol, clean.Interval, clean.Kind, clean.P1.Time, clean.P1.Price, p2Time, p2Price, clean.Label, clean.CreatedAt, clean.UpdatedAt); err != nil {
		return ChartDrawing{}, err
	}
	return clean, nil
}

// DeleteDrawing removes a persisted chart drawing by ID.
func (s *Store) DeleteDrawing(ctx context.Context, id string) (bool, error) {
	cleanID := strings.TrimSpace(id)
	if !idPattern.MatchString(cleanID) {
		return false, errors.New("drawing id must match [A-Za-z0-9._-] (1-64 chars)")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.metaDB == nil {
		return false, errors.New("marketdata store closed")
	}
	res, err := s.metaDB.ExecContext(ctx, `DELETE FROM chart_drawings WHERE id = ?`, cleanID)
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.metaDB == nil {
		return errors.New("marketdata store closed")
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.metaDB == nil {
		return nil, 0, false, errors.New("marketdata store closed")
	}
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

// UpsertCustomFeedMeta persists summary metadata for an imported custom OHLCV feed (capped at 64).
func (s *Store) UpsertCustomFeedMeta(ctx context.Context, meta CustomFeedSummary) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.metaDB == nil {
		return errors.New("marketdata store closed")
	}
	var existing int
	err := s.metaDB.QueryRowContext(ctx, `SELECT 1 FROM custom_feeds WHERE symbol = ? AND interval = ?`, meta.Symbol, meta.Interval).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		if err := s.metaDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM custom_feeds`).Scan(&count); err != nil {
			return err
		}
		if count >= maxCustomFeeds {
			return fmt.Errorf("custom feeds limit (%d) reached", maxCustomFeeds)
		}
	} else if err != nil {
		return err
	}
	_, err = s.metaDB.ExecContext(ctx, `
INSERT INTO custom_feeds (symbol, interval, bar_count, last_time, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(symbol, interval) DO UPDATE SET
	bar_count = excluded.bar_count,
	last_time = excluded.last_time,
	updated_at = excluded.updated_at
`, meta.Symbol, meta.Interval, meta.BarCount, meta.LastTime, meta.UpdatedAt)
	return err
}

// ListCustomFeeds returns stored custom feed summaries sorted by updated_at descending.
func (s *Store) ListCustomFeeds(ctx context.Context) ([]CustomFeedSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.metaDB == nil {
		return nil, errors.New("marketdata store closed")
	}
	rows, err := s.metaDB.QueryContext(ctx, `
SELECT symbol, interval, bar_count, last_time, updated_at
FROM custom_feeds
ORDER BY updated_at DESC, symbol ASC, interval ASC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CustomFeedSummary, 0)
	for rows.Next() {
		var f CustomFeedSummary
		if err := rows.Scan(&f.Symbol, &f.Interval, &f.BarCount, &f.LastTime, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
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
