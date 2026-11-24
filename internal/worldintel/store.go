package worldintel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

const (
	worldIntelDirName = "worldintel"
	intelDBName       = "intel.sqlite"
)

type store struct {
	db     *sql.DB
	mu     sync.RWMutex
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

func openStore(dataDir string) (*store, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("worldintel requires non-empty DataDir")
	}
	dir := filepath.Join(dataDir, worldIntelDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, intelDBName)
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
CREATE TABLE IF NOT EXISTS intel_sources (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	kind TEXT NOT NULL,
	endpoint TEXT NOT NULL,
	category TEXT NOT NULL,
	region TEXT NOT NULL,
	enabled INTEGER NOT NULL,
	allow_localhost INTEGER NOT NULL,
	built_in INTEGER NOT NULL,
	last_sync_at INTEGER NOT NULL,
	last_error TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS intel_events (
	id TEXT PRIMARY KEY,
	source_id TEXT NOT NULL,
	source_name TEXT NOT NULL,
	category TEXT NOT NULL,
	title TEXT NOT NULL,
	summary TEXT NOT NULL,
	url TEXT NOT NULL,
	region TEXT NOT NULL,
	country_code TEXT NOT NULL,
	lat REAL NOT NULL,
	lon REAL NOT NULL,
	tickers_json TEXT NOT NULL,
	published_at INTEGER NOT NULL,
	ingested_at INTEGER NOT NULL,
	urgency REAL NOT NULL,
	sentiment TEXT NOT NULL,
	confidence REAL NOT NULL,
	score_provider TEXT NOT NULL,
	verified INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_intel_events_pub ON intel_events(published_at DESC, urgency DESC);

CREATE TABLE IF NOT EXISTS intel_predictions (
	id TEXT PRIMARY KEY,
	venue TEXT NOT NULL,
	slug TEXT NOT NULL,
	question TEXT NOT NULL,
	category TEXT NOT NULL,
	yes_prob REAL NOT NULL,
	no_prob REAL NOT NULL,
	volume_24h REAL NOT NULL,
	liquidity REAL NOT NULL,
	change_24h REAL NOT NULL,
	url TEXT NOT NULL,
	end_date TEXT NOT NULL,
	tickers_json TEXT NOT NULL,
	region TEXT NOT NULL,
	lat REAL NOT NULL,
	lon REAL NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_intel_predictions_vol ON intel_predictions(volume_24h DESC);
`
	if _, err := db.Exec(ddl); err != nil {
		_ = db.Close()
		return nil, err
	}

	st := &store{db: db}
	if err := st.seedDefaults(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return st, nil
}

func (s *store) seedDefaults() error {
	for _, src := range DefaultSources() {
		enabledInt, allowInt := 0, 0
		if src.Enabled {
			enabledInt = 1
		}
		if src.AllowLocalhost {
			allowInt = 1
		}
		if _, err := s.db.Exec(`
INSERT OR IGNORE INTO intel_sources
	(id, name, kind, endpoint, category, region, enabled, allow_localhost, built_in, last_sync_at, last_error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, 0, '')
`, src.ID, src.Name, src.Kind, src.Endpoint, src.Category, src.Region, enabledInt, allowInt); err != nil {
			return err
		}
	}
	return nil
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

func (s *store) listSources(ctx context.Context) ([]SourceConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.db == nil {
		return nil, errors.New("worldintel store closed")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, kind, endpoint, category, region, enabled, allow_localhost, built_in, last_sync_at, last_error
FROM intel_sources
ORDER BY built_in DESC, id ASC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []SourceConfig{}
	for rows.Next() {
		var cfg SourceConfig
		var en, allow, built int
		if err := rows.Scan(
			&cfg.ID, &cfg.Name, &cfg.Kind, &cfg.Endpoint, &cfg.Category, &cfg.Region,
			&en, &allow, &built, &cfg.LastSyncAt, &cfg.LastError,
		); err != nil {
			return nil, err
		}
		cfg.Enabled = en != 0
		cfg.AllowLocalhost = allow != 0
		cfg.BuiltIn = built != 0
		out = append(out, cfg)
	}
	return out, rows.Err()
}

func (s *store) getSourceLocked(ctx context.Context, id string) (SourceConfig, error) {
	var cfg SourceConfig
	var en, allow, built int
	err := s.db.QueryRowContext(ctx, `
SELECT id, name, kind, endpoint, category, region, enabled, allow_localhost, built_in, last_sync_at, last_error
FROM intel_sources WHERE id = ?
`, id).Scan(
		&cfg.ID, &cfg.Name, &cfg.Kind, &cfg.Endpoint, &cfg.Category, &cfg.Region,
		&en, &allow, &built, &cfg.LastSyncAt, &cfg.LastError,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceConfig{}, fmt.Errorf("intel source %q: %w", id, os.ErrNotExist)
	}
	if err != nil {
		return SourceConfig{}, err
	}
	cfg.Enabled = en != 0
	cfg.AllowLocalhost = allow != 0
	cfg.BuiltIn = built != 0
	return cfg, nil
}

func (s *store) getSource(ctx context.Context, id string) (SourceConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.db == nil {
		return SourceConfig{}, errors.New("worldintel store closed")
	}
	return s.getSourceLocked(ctx, id)
}

func (s *store) saveSource(ctx context.Context, in SourceInput) (SourceConfig, error) {
	norm, err := ValidateSourceInput(in)
	if err != nil {
		return SourceConfig{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.db == nil {
		return SourceConfig{}, errors.New("worldintel store closed")
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM intel_sources WHERE id != ?`, norm.ID).Scan(&count); err != nil {
		return SourceConfig{}, err
	}
	if count >= maxSources {
		return SourceConfig{}, fmt.Errorf("intel source limit (%d) reached", maxSources)
	}

	var builtInInt int
	_ = s.db.QueryRowContext(ctx, `SELECT built_in FROM intel_sources WHERE id = ?`, norm.ID).Scan(&builtInInt)

	enInt, allowInt := 0, 0
	if *norm.Enabled {
		enInt = 1
	}
	if norm.AllowLocalhost {
		allowInt = 1
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO intel_sources
	(id, name, kind, endpoint, category, region, enabled, allow_localhost, built_in, last_sync_at, last_error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, '')
ON CONFLICT(id) DO UPDATE SET
	name = excluded.name,
	kind = excluded.kind,
	endpoint = excluded.endpoint,
	category = excluded.category,
	region = excluded.region,
	enabled = excluded.enabled,
	allow_localhost = excluded.allow_localhost
`, norm.ID, norm.Name, norm.Kind, norm.Endpoint, norm.Category, norm.Region, enInt, allowInt, builtInInt); err != nil {
		return SourceConfig{}, err
	}
	return s.getSourceLocked(ctx, norm.ID)
}

func (s *store) deleteSource(ctx context.Context, id string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.db == nil {
		return false, errors.New("worldintel store closed")
	}
	cfg, err := s.getSourceLocked(ctx, strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if cfg.BuiltIn {
		return false, fmt.Errorf("built-in intel source %q cannot be deleted", cfg.ID)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM intel_sources WHERE id = ? AND built_in = 0`, cfg.ID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *store) markSourceSync(ctx context.Context, id string, ts int64, syncErr string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.db == nil {
		return errors.New("worldintel store closed")
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE intel_sources SET last_sync_at = ?, last_error = ? WHERE id = ?
`, ts, trimText(syncErr, 280), id)
	return err
}

func (s *store) upsertEvents(ctx context.Context, events []IntelEvent) error {
	if len(events) == 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.db == nil {
		return errors.New("worldintel store closed")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO intel_events
	(id, source_id, source_name, category, title, summary, url, region, country_code,
	 lat, lon, tickers_json, published_at, ingested_at, urgency, sentiment, confidence, score_provider, verified)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	summary = excluded.summary,
	urgency = excluded.urgency,
	sentiment = excluded.sentiment,
	confidence = excluded.confidence,
	score_provider = excluded.score_provider,
	verified = excluded.verified,
	ingested_at = excluded.ingested_at
`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, ev := range events {
		tickBytes, _ := json.Marshal(ev.Tickers)
		verifiedInt := 0
		if ev.Verified {
			verifiedInt = 1
		}
		if _, err := stmt.ExecContext(ctx,
			ev.ID, ev.SourceID, ev.SourceName, ev.Category, ev.Title, ev.Summary, ev.URL,
			ev.Region, ev.CountryCode, ev.Lat, ev.Lon, string(tickBytes),
			ev.PublishedAt, ev.IngestedAt, ev.Urgency, ev.Sentiment, ev.Confidence, ev.ScoreProvider, verifiedInt,
		); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `
DELETE FROM intel_events
WHERE id NOT IN (
	SELECT id FROM intel_events
	ORDER BY published_at DESC, ingested_at DESC
	LIMIT ?
)`, maxEventsRetained); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *store) upsertPredictions(ctx context.Context, markets []PredictionMarket) error {
	if len(markets) == 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.db == nil {
		return errors.New("worldintel store closed")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO intel_predictions
	(id, venue, slug, question, category, yes_prob, no_prob, volume_24h, liquidity,
	 change_24h, url, end_date, tickers_json, region, lat, lon, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	question = excluded.question,
	category = excluded.category,
	yes_prob = excluded.yes_prob,
	no_prob = excluded.no_prob,
	volume_24h = excluded.volume_24h,
	liquidity = excluded.liquidity,
	change_24h = excluded.change_24h,
	url = excluded.url,
	end_date = excluded.end_date,
	tickers_json = excluded.tickers_json,
	updated_at = excluded.updated_at
`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, m := range markets {
		tickBytes, _ := json.Marshal(m.Tickers)
		if _, err := stmt.ExecContext(ctx,
			m.ID, m.Venue, m.Slug, m.Question, m.Category, m.YesProb, m.NoProb,
			m.Volume24h, m.Liquidity, m.Change24h, m.URL, m.EndDate, string(tickBytes),
			m.Region, m.Lat, m.Lon, m.UpdatedAt,
		); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `
DELETE FROM intel_predictions
WHERE id NOT IN (
	SELECT id FROM intel_predictions
	ORDER BY updated_at DESC, volume_24h DESC
	LIMIT ?
)`, maxPredictionsRetained); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *store) listEvents(ctx context.Context, filter EventFilter) ([]IntelEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.db == nil {
		return nil, errors.New("worldintel store closed")
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	clauses := []string{"1=1"}
	args := []any{}

	if cat := NormalizeCategory(filter.Category); cat != "" {
		clauses = append(clauses, "category = ?")
		args = append(args, cat)
	}
	if reg := NormalizeRegion(filter.Region); reg != "" && reg != RegionGlobal {
		clauses = append(clauses, "region = ?")
		args = append(args, reg)
	}
	if filter.MinUrgency > 0 {
		clauses = append(clauses, "urgency >= ?")
		args = append(args, filter.MinUrgency)
	}
	if filter.Since > 0 {
		clauses = append(clauses, "published_at >= ?")
		args = append(args, filter.Since)
	}
	if tk := NormalizeTicker(filter.Ticker); tk != "" {
		clauses = append(clauses, "tickers_json LIKE ?")
		args = append(args, "%\""+tk+"\"%")
	}
	if q := strings.ToLower(strings.TrimSpace(filter.Query)); q != "" {
		clauses = append(clauses, "(lower(title) LIKE ? OR lower(summary) LIKE ? OR lower(source_name) LIKE ?)")
		pat := "%" + q + "%"
		args = append(args, pat, pat, pat)
	}
	args = append(args, limit)

	query := `
SELECT id, source_id, source_name, category, title, summary, url, region, country_code,
       lat, lon, tickers_json, published_at, ingested_at, urgency, sentiment, confidence, score_provider, verified
FROM intel_events
WHERE ` + strings.Join(clauses, " AND ") + `
ORDER BY published_at DESC, urgency DESC, id ASC
LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []IntelEvent{}
	for rows.Next() {
		var ev IntelEvent
		var tickJSON string
		var verifiedInt int
		if err := rows.Scan(
			&ev.ID, &ev.SourceID, &ev.SourceName, &ev.Category, &ev.Title, &ev.Summary, &ev.URL,
			&ev.Region, &ev.CountryCode, &ev.Lat, &ev.Lon, &tickJSON, &ev.PublishedAt, &ev.IngestedAt,
			&ev.Urgency, &ev.Sentiment, &ev.Confidence, &ev.ScoreProvider, &verifiedInt,
		); err != nil {
			return nil, err
		}
		ev.Verified = verifiedInt != 0
		ev.Untrusted = true
		_ = json.Unmarshal([]byte(tickJSON), &ev.Tickers)
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (s *store) listPredictions(ctx context.Context, filter PredictionFilter) ([]PredictionMarket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.db == nil {
		return nil, errors.New("worldintel store closed")
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	clauses := []string{"1=1"}
	args := []any{}
	if v := strings.ToLower(strings.TrimSpace(filter.Venue)); v == VenuePolymarket || v == VenueKalshi {
		clauses = append(clauses, "venue = ?")
		args = append(args, v)
	}
	if cat := NormalizeCategory(filter.Category); cat != "" {
		clauses = append(clauses, "category = ?")
		args = append(args, cat)
	}
	if q := strings.ToLower(strings.TrimSpace(filter.Query)); q != "" {
		clauses = append(clauses, "(lower(question) LIKE ? OR lower(slug) LIKE ?)")
		pat := "%" + q + "%"
		args = append(args, pat, pat)
	}
	args = append(args, limit)

	query := `
SELECT id, venue, slug, question, category, yes_prob, no_prob, volume_24h, liquidity,
       change_24h, url, end_date, tickers_json, region, lat, lon, updated_at
FROM intel_predictions
WHERE ` + strings.Join(clauses, " AND ") + `
ORDER BY volume_24h DESC, updated_at DESC, id ASC
LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PredictionMarket{}
	for rows.Next() {
		var m PredictionMarket
		var tickJSON string
		if err := rows.Scan(
			&m.ID, &m.Venue, &m.Slug, &m.Question, &m.Category, &m.YesProb, &m.NoProb,
			&m.Volume24h, &m.Liquidity, &m.Change24h, &m.URL, &m.EndDate, &tickJSON,
			&m.Region, &m.Lat, &m.Lon, &m.UpdatedAt,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(tickJSON), &m.Tickers)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *store) countEvents(ctx context.Context) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.db == nil {
		return 0
	}
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM intel_events`).Scan(&n)
	return n
}

func (s *store) countPredictions(ctx context.Context) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.db == nil {
		return 0
	}
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM intel_predictions`).Scan(&n)
	return n
}
