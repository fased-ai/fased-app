package quantlab

import (
	"context"
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

	"github.com/fased-ai/fased-app/internal/fasedscript"
	_ "modernc.org/sqlite"
)

const (
	quantLabDirName = "quantlab"
	labDBName       = "lab.sqlite"
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
		return nil, errors.New("quantlab DataDir required")
	}
	dir := filepath.Join(dataDir, quantLabDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, labDBName)
	dsn := sqliteFileURI(dbPath) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(FULL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const schemaSQL = `
CREATE TABLE IF NOT EXISTS scripts(
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	category TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	source TEXT NOT NULL,
	built_in INTEGER NOT NULL DEFAULT 0,
	inputs_json TEXT NOT NULL DEFAULT '[]',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS validation_runs(
	id TEXT PRIMARY KEY,
	script_id TEXT NOT NULL,
	script_name TEXT NOT NULL,
	venue TEXT NOT NULL,
	symbol TEXT NOT NULL,
	interval TEXT NOT NULL,
	grade TEXT NOT NULL,
	gates_passed INTEGER NOT NULL,
	promotion_ready INTEGER NOT NULL,
	total_return_pct REAL NOT NULL,
	sharpe REAL NOT NULL,
	duration_ms INTEGER NOT NULL,
	config_json TEXT NOT NULL,
	report_json TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_validation_runs_created ON validation_runs(created_at DESC);
`
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		_ = db.Close()
		return nil, err
	}

	st := &store{dir: dir, db: db, now: nowFn}
	if err := st.seedBuiltIns(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return st, nil
}

func (st *store) seedBuiltIns(ctx context.Context) error {
	nowSec := st.now().UTC().Unix()
	for _, tpl := range fasedscript.Templates() {
		prog, err := fasedscript.Compile(tpl.Source)
		if err != nil {
			return fmt.Errorf("compile built-in template %s: %w", tpl.ID, err)
		}
		inJSON, _ := json.Marshal(prog.Inputs)
		_, err = st.db.ExecContext(ctx,
			`INSERT INTO scripts(id, name, category, description, source, built_in, inputs_json, created_at, updated_at)
			 VALUES(?,?,?,?,?,1,?,?,?)
			 ON CONFLICT(id) DO UPDATE SET
			   name=excluded.name,
			   category=excluded.category,
			   description=excluded.description,
			   source=excluded.source,
			   built_in=1,
			   inputs_json=excluded.inputs_json,
			   updated_at=excluded.updated_at`,
			tpl.ID, tpl.Name, tpl.Category, tpl.Description, tpl.Source, string(inJSON), nowSec, nowSec,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (st *store) close() error {
	if st == nil || st.db == nil {
		return nil
	}
	return st.db.Close()
}

func (st *store) listScripts(ctx context.Context) ([]ScriptRecord, error) {
	rows, err := st.db.QueryContext(ctx,
		`SELECT id, name, category, description, source, built_in, inputs_json, created_at, updated_at
		 FROM scripts ORDER BY built_in DESC, updated_at DESC, id ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ScriptRecord{}
	for rows.Next() {
		var rec ScriptRecord
		var builtInInt int
		var inJSON string
		if err := rows.Scan(&rec.ID, &rec.Name, &rec.Category, &rec.Description, &rec.Source, &builtInInt, &inJSON, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return nil, err
		}
		rec.BuiltIn = builtInInt != 0
		_ = json.Unmarshal([]byte(inJSON), &rec.Inputs)
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (st *store) getScript(ctx context.Context, id string) (ScriptRecord, bool, error) {
	safeID, err := ValidateSafeID(id)
	if err != nil {
		return ScriptRecord{}, false, err
	}
	var rec ScriptRecord
	var builtInInt int
	var inJSON string
	err = st.db.QueryRowContext(ctx,
		`SELECT id, name, category, description, source, built_in, inputs_json, created_at, updated_at
		 FROM scripts WHERE id=?`, safeID,
	).Scan(&rec.ID, &rec.Name, &rec.Category, &rec.Description, &rec.Source, &builtInInt, &inJSON, &rec.CreatedAt, &rec.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ScriptRecord{}, false, nil
	}
	if err != nil {
		return ScriptRecord{}, false, err
	}
	rec.BuiltIn = builtInInt != 0
	_ = json.Unmarshal([]byte(inJSON), &rec.Inputs)
	return rec, true, nil
}

func (st *store) saveScript(ctx context.Context, in ScriptInput) (ScriptRecord, error) {
	id, err := ValidateSafeID(in.ID)
	if err != nil {
		return ScriptRecord{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 120 {
		return ScriptRecord{}, errors.New("script name must be 1-120 characters")
	}
	category := strings.ToLower(strings.TrimSpace(in.Category))
	if category == "" {
		category = "custom"
	}
	if len(category) > 32 {
		return ScriptRecord{}, errors.New("script category exceeds 32 characters")
	}
	desc := strings.TrimSpace(in.Description)
	if len(desc) > 240 {
		desc = desc[:240]
	}

	prog, err := fasedscript.Compile(in.Source)
	if err != nil {
		return ScriptRecord{}, err
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	var builtInInt int
	var existingCreated int64
	exists := false
	err = st.db.QueryRowContext(ctx, `SELECT built_in, created_at FROM scripts WHERE id=?`, id).Scan(&builtInInt, &existingCreated)
	if err == nil {
		exists = true
		if builtInInt != 0 {
			return ScriptRecord{}, errors.New("built-in FasedScript templates cannot be overwritten; save under a new ID")
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return ScriptRecord{}, err
	}

	if !exists {
		var customCount int
		if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scripts WHERE built_in=0`).Scan(&customCount); err != nil {
			return ScriptRecord{}, err
		}
		if customCount >= maxScripts {
			return ScriptRecord{}, fmt.Errorf("maximum of %d custom scripts reached", maxScripts)
		}
	}

	nowSec := st.now().UTC().Unix()
	created := nowSec
	if exists {
		created = existingCreated
	}
	inJSON, _ := json.Marshal(prog.Inputs)
	rec := ScriptRecord{
		ID:          id,
		Name:        name,
		Category:    category,
		Description: desc,
		Source:      in.Source,
		BuiltIn:     false,
		Inputs:      prog.Inputs,
		CreatedAt:   created,
		UpdatedAt:   nowSec,
	}
	_, err = st.db.ExecContext(ctx,
		`INSERT INTO scripts(id, name, category, description, source, built_in, inputs_json, created_at, updated_at)
		 VALUES(?,?,?,?,?,0,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET
		   name=excluded.name,
		   category=excluded.category,
		   description=excluded.description,
		   source=excluded.source,
		   inputs_json=excluded.inputs_json,
		   updated_at=excluded.updated_at`,
		rec.ID, rec.Name, rec.Category, rec.Description, rec.Source, string(inJSON), rec.CreatedAt, rec.UpdatedAt,
	)
	if err != nil {
		return ScriptRecord{}, err
	}
	return rec, nil
}

func (st *store) deleteScript(ctx context.Context, id string) (bool, error) {
	safeID, err := ValidateSafeID(id)
	if err != nil {
		return false, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	var builtInInt int
	err = st.db.QueryRowContext(ctx, `SELECT built_in FROM scripts WHERE id=?`, safeID).Scan(&builtInInt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if builtInInt != 0 {
		return false, errors.New("built-in FasedScript templates cannot be deleted")
	}
	res, err := st.db.ExecContext(ctx, `DELETE FROM scripts WHERE id=? AND built_in=0`, safeID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (st *store) insertRun(ctx context.Context, rec ValidationRunRecord) (ValidationRunRecord, error) {
	st.mu.Lock()
	defer st.mu.Unlock()

	if rec.ID == "" {
		buf := make([]byte, 8)
		_, _ = rand.Read(buf)
		rec.ID = fmt.Sprintf("run-%d-%x", st.now().UTC().UnixMilli(), buf[:4])
	}
	if rec.CreatedAt == 0 {
		rec.CreatedAt = st.now().UTC().Unix()
	}
	cfgJSON, err := json.Marshal(rec.Config)
	if err != nil {
		return ValidationRunRecord{}, err
	}
	repJSON, err := json.Marshal(rec.Report)
	if err != nil {
		return ValidationRunRecord{}, err
	}

	promoInt := 0
	if rec.Report.PromotionReady {
		promoInt = 1
	}

	_, err = st.db.ExecContext(ctx,
		`INSERT INTO validation_runs(id, script_id, script_name, venue, symbol, interval, grade, gates_passed, promotion_ready, total_return_pct, sharpe, duration_ms, config_json, report_json, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		rec.ID, rec.ScriptID, rec.ScriptName, rec.Config.Venue, rec.Config.Symbol, rec.Config.Interval,
		rec.Report.Grade, rec.Report.GatesPassed, promoInt,
		rec.Report.BaselineMetrics.TotalReturnPct, rec.Report.BaselineMetrics.AnnualizedSharpe,
		rec.DurationMs, string(cfgJSON), string(repJSON), rec.CreatedAt,
	)
	if err != nil {
		return ValidationRunRecord{}, err
	}

	_, _ = st.db.ExecContext(ctx,
		`DELETE FROM validation_runs WHERE id NOT IN (SELECT id FROM validation_runs ORDER BY created_at DESC, id DESC LIMIT ?)`,
		maxRunsRetained,
	)
	return rec, nil
}

func (st *store) listRuns(ctx context.Context, limit int) ([]ValidationRunSummary, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := st.db.QueryContext(ctx,
		`SELECT id, script_id, script_name, venue, symbol, interval, grade, gates_passed, promotion_ready, total_return_pct, sharpe, duration_ms, created_at
		 FROM validation_runs ORDER BY created_at DESC, id DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ValidationRunSummary{}
	for rows.Next() {
		var r ValidationRunSummary
		var promoInt int
		if err := rows.Scan(
			&r.ID, &r.ScriptID, &r.ScriptName, &r.Venue, &r.Symbol, &r.Interval,
			&r.Grade, &r.GatesPassed, &promoInt, &r.TotalReturnPct, &r.Sharpe,
			&r.DurationMs, &r.CreatedAt,
		); err != nil {
			return nil, err
		}
		r.PromotionReady = promoInt != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

func (st *store) getRun(ctx context.Context, id string) (ValidationRunRecord, bool, error) {
	safeID, err := ValidateSafeID(id)
	if err != nil {
		return ValidationRunRecord{}, false, err
	}
	var rec ValidationRunRecord
	var cfgJSON, repJSON string
	err = st.db.QueryRowContext(ctx,
		`SELECT id, script_id, script_name, duration_ms, config_json, report_json, created_at
		 FROM validation_runs WHERE id=?`, safeID,
	).Scan(&rec.ID, &rec.ScriptID, &rec.ScriptName, &rec.DurationMs, &cfgJSON, &repJSON, &rec.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ValidationRunRecord{}, false, nil
	}
	if err != nil {
		return ValidationRunRecord{}, false, err
	}
	_ = json.Unmarshal([]byte(cfgJSON), &rec.Config)
	_ = json.Unmarshal([]byte(repJSON), &rec.Report)
	return rec, true, nil
}
