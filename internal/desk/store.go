package desk

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
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
	deskDirName        = "desk"
	deskDBName         = "desk.sqlite"
	maxCyclesRetained  = 200
	maxJournalRetained = 500
	maxLessonsRetained = 100
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
		return nil, errors.New("desk DataDir required")
	}
	dir := filepath.Join(dataDir, deskDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, deskDBName)
	dsn := sqliteFileURI(dbPath) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(FULL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const schemaSQL = `
CREATE TABLE IF NOT EXISTS desk_control(
	id INTEGER PRIMARY KEY CHECK(id=1),
	paused INTEGER NOT NULL DEFAULT 0,
	kill_switch_active INTEGER NOT NULL DEFAULT 0,
	reason TEXT NOT NULL DEFAULT '',
	updated_by TEXT NOT NULL DEFAULT 'system',
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS desk_limits(
	id INTEGER PRIMARY KEY CHECK(id=1),
	limits_json TEXT NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS paper_account(
	id INTEGER PRIMARY KEY CHECK(id=1),
	initial_capital REAL NOT NULL,
	cash_usd REAL NOT NULL,
	realized_pnl REAL NOT NULL DEFAULT 0,
	daily_pnl REAL NOT NULL DEFAULT 0,
	daily_day TEXT NOT NULL,
	total_fees REAL NOT NULL DEFAULT 0,
	consecutive_losses INTEGER NOT NULL DEFAULT 0,
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS desk_cycles(
	id TEXT PRIMARY KEY,
	venue TEXT NOT NULL,
	symbol TEXT NOT NULL,
	interval TEXT NOT NULL,
	script_id TEXT NOT NULL,
	mode TEXT NOT NULL,
	final_status TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_desk_cycles_created ON desk_cycles(created_at DESC);

CREATE TABLE IF NOT EXISTS desk_tickets(
	id TEXT PRIMARY KEY,
	cycle_id TEXT NOT NULL,
	venue TEXT NOT NULL,
	symbol TEXT NOT NULL,
	interval TEXT NOT NULL,
	script_id TEXT NOT NULL,
	side TEXT NOT NULL,
	mode TEXT NOT NULL,
	entry_price REAL NOT NULL,
	stop_price REAL NOT NULL,
	target_price REAL NOT NULL,
	quantity REAL NOT NULL,
	notional_usd REAL NOT NULL,
	risk_usd REAL NOT NULL,
	risk_reward REAL NOT NULL,
	expected_avg_r REAL NOT NULL,
	chart_score REAL NOT NULL,
	status TEXT NOT NULL,
	reason TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_desk_tickets_created ON desk_tickets(created_at DESC);

CREATE TABLE IF NOT EXISTS paper_positions(
	id TEXT PRIMARY KEY,
	ticket_id TEXT NOT NULL,
	venue TEXT NOT NULL,
	symbol TEXT NOT NULL,
	script_id TEXT NOT NULL,
	side TEXT NOT NULL,
	quantity REAL NOT NULL,
	entry_price REAL NOT NULL,
	mark_price REAL NOT NULL,
	stop_price REAL NOT NULL,
	target_price REAL NOT NULL,
	initial_risk_usd REAL NOT NULL,
	entry_fee_usd REAL NOT NULL DEFAULT 0,
	exit_price REAL NOT NULL DEFAULT 0,
	exit_fee_usd REAL NOT NULL DEFAULT 0,
	unrealized_pnl REAL NOT NULL DEFAULT 0,
	realized_pnl REAL NOT NULL DEFAULT 0,
	pnl_r REAL NOT NULL DEFAULT 0,
	status TEXT NOT NULL,
	exit_reason TEXT NOT NULL DEFAULT '',
	opened_at INTEGER NOT NULL,
	closed_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_paper_positions_opened ON paper_positions(opened_at DESC);

CREATE TABLE IF NOT EXISTS desk_journal(
	id TEXT PRIMARY KEY,
	cycle_id TEXT NOT NULL,
	ticket_id TEXT NOT NULL DEFAULT '',
	seat TEXT NOT NULL,
	venue TEXT NOT NULL,
	symbol TEXT NOT NULL,
	script_id TEXT NOT NULL,
	side TEXT NOT NULL,
	mode TEXT NOT NULL,
	outcome TEXT NOT NULL,
	score REAL NOT NULL DEFAULT 0,
	entry_price REAL NOT NULL DEFAULT 0,
	stop_price REAL NOT NULL DEFAULT 0,
	target_price REAL NOT NULL DEFAULT 0,
	exit_price REAL NOT NULL DEFAULT 0,
	quantity REAL NOT NULL DEFAULT 0,
	risk_usd REAL NOT NULL DEFAULT 0,
	pnl_usd REAL NOT NULL DEFAULT 0,
	pnl_r REAL NOT NULL DEFAULT 0,
	expected_avg_r REAL NOT NULL DEFAULT 0,
	risk_check TEXT NOT NULL DEFAULT '',
	skeptic_verdict TEXT NOT NULL DEFAULT '',
	reason TEXT NOT NULL DEFAULT '',
	lesson TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_desk_journal_created ON desk_journal(created_at DESC);

CREATE TABLE IF NOT EXISTS desk_lessons(
	id TEXT PRIMARY KEY,
	category TEXT NOT NULL,
	symbol TEXT NOT NULL DEFAULT '',
	rule TEXT NOT NULL,
	source_trade_id TEXT NOT NULL DEFAULT '',
	active INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS desk_bench(
	id TEXT PRIMARY KEY,
	scope_key TEXT NOT NULL UNIQUE,
	reason TEXT NOT NULL DEFAULT '',
	losses INTEGER NOT NULL DEFAULT 0,
	benched_until INTEGER NOT NULL,
	created_at INTEGER NOT NULL
);
`
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		_ = db.Close()
		return nil, err
	}

	s := &store{dir: dir, db: db, now: nowFn}
	if err := s.seedDefaults(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *store) close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func newID(prefix string) string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(raw[:])
}

func (s *store) dayString(tsMillis int64) string {
	return time.UnixMilli(tsMillis).UTC().Format("2006-01-02")
}

func (s *store) seedDefaults(ctx context.Context) error {
	now := s.now().UnixMilli()
	day := s.dayString(now)
	defLimits := DefaultRiskLimits()
	limitsBytes, err := json.Marshal(defLimits)
	if err != nil {
		return err
	}

	if _, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO desk_control(id, paused, kill_switch_active, reason, updated_by, updated_at) VALUES(1, 0, 0, '', 'system', ?)`,
		now,
	); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO desk_limits(id, limits_json, updated_at) VALUES(1, ?, ?)`,
		string(limitsBytes), now,
	); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO paper_account(id, initial_capital, cash_usd, realized_pnl, daily_pnl, daily_day, total_fees, consecutive_losses, updated_at) VALUES(1, ?, ?, 0, 0, ?, 0, 0, ?)`,
		defLimits.InitialCapitalUSD, defLimits.InitialCapitalUSD, day, now,
	); err != nil {
		return err
	}

	starterLessons := []LessonRecord{
		{
			ID:        "lesson-kill-unsourced",
			Category:  "skeptic",
			Rule:      "Every claim, fair value anchor, and macro catalyst requires a primary source timestamp; UNVERIFIED setups are killed automatically.",
			Active:    true,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "lesson-paper-before-size",
			Category:  "risk",
			Rule:      "Run at least 20 closed trades in Shadow/Paper mode and compare Paper Avg R against backtest Avg R before promoting or resizing.",
			Active:    true,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "lesson-bench-streak",
			Category:  "risk",
			Rule:      "Three consecutive losses trigger an automatic 24h cooldown bench; never widen RiskLimits after a drawdown.",
			Active:    true,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	for _, l := range starterLessons {
		if _, err := s.db.ExecContext(ctx,
			`INSERT OR IGNORE INTO desk_lessons(id, category, symbol, rule, source_trade_id, active, created_at, updated_at) VALUES(?, ?, ?, ?, '', 1, ?, ?)`,
			l.ID, l.Category, l.Symbol, l.Rule, l.CreatedAt, l.UpdatedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *store) getControl(ctx context.Context) (ControlState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st ControlState
	var pausedInt, killInt int
	err := s.db.QueryRowContext(ctx,
		`SELECT paused, kill_switch_active, reason, updated_by, updated_at FROM desk_control WHERE id=1`,
	).Scan(&pausedInt, &killInt, &st.Reason, &st.UpdatedBy, &st.UpdatedAt)
	if err != nil {
		return ControlState{}, err
	}
	st.Paused = pausedInt != 0
	st.KillSwitchActive = killInt != 0
	st.EffectiveHalted = st.Paused || st.KillSwitchActive
	return st, nil
}

func (s *store) saveControl(ctx context.Context, paused, kill bool, reason, actor string, now int64) (ControlState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pausedInt := 0
	if paused {
		pausedInt = 1
	}
	killInt := 0
	if kill {
		killInt = 1
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE desk_control SET paused=?, kill_switch_active=?, reason=?, updated_by=?, updated_at=? WHERE id=1`,
		pausedInt, killInt, strings.TrimSpace(reason), strings.TrimSpace(actor), now,
	); err != nil {
		return ControlState{}, err
	}
	return ControlState{
		Paused:           paused,
		KillSwitchActive: kill,
		EffectiveHalted:  paused || kill,
		Reason:           strings.TrimSpace(reason),
		UpdatedBy:        strings.TrimSpace(actor),
		UpdatedAt:        now,
	}, nil
}

func (s *store) getLimits(ctx context.Context) (RiskLimits, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT limits_json FROM desk_limits WHERE id=1`).Scan(&raw); err != nil {
		return RiskLimits{}, err
	}
	var out RiskLimits
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return DefaultRiskLimits(), nil
	}
	if err := out.Validate(); err != nil {
		return DefaultRiskLimits(), nil
	}
	return out, nil
}

func (s *store) saveLimits(ctx context.Context, limits RiskLimits, adjustIdleCapital bool, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.Marshal(limits)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE desk_limits SET limits_json=?, updated_at=? WHERE id=1`,
		string(raw), now,
	); err != nil {
		return err
	}
	if adjustIdleCapital {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE paper_account SET initial_capital=?, cash_usd=?, updated_at=? WHERE id=1`,
			limits.InitialCapitalUSD, limits.InitialCapitalUSD, now,
		); err != nil {
			return err
		}
	}
	return nil
}

type accountRow struct {
	InitialCapital    float64
	CashUSD           float64
	RealizedPnL       float64
	DailyPnL          float64
	DailyDay          string
	TotalFees         float64
	ConsecutiveLosses int
	UpdatedAt         int64
}

func (s *store) getAccountRow(ctx context.Context) (accountRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getAccountRowLocked(ctx)
}

func (s *store) getAccountRowLocked(ctx context.Context) (accountRow, error) {
	var row accountRow
	err := s.db.QueryRowContext(ctx,
		`SELECT initial_capital, cash_usd, realized_pnl, daily_pnl, daily_day, total_fees, consecutive_losses, updated_at FROM paper_account WHERE id=1`,
	).Scan(&row.InitialCapital, &row.CashUSD, &row.RealizedPnL, &row.DailyPnL, &row.DailyDay, &row.TotalFees, &row.ConsecutiveLosses, &row.UpdatedAt)
	if err != nil {
		return accountRow{}, err
	}
	today := s.dayString(s.now().UnixMilli())
	if row.DailyDay != today {
		row.DailyDay = today
		row.DailyPnL = 0
		_, _ = s.db.ExecContext(ctx, `UPDATE paper_account SET daily_pnl=0, daily_day=? WHERE id=1`, today)
	}
	return row, nil
}

func (s *store) updateAccountRow(ctx context.Context, row accountRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`UPDATE paper_account SET initial_capital=?, cash_usd=?, realized_pnl=?, daily_pnl=?, daily_day=?, total_fees=?, consecutive_losses=?, updated_at=? WHERE id=1`,
		row.InitialCapital, row.CashUSD, row.RealizedPnL, row.DailyPnL, row.DailyDay, row.TotalFees, row.ConsecutiveLosses, row.UpdatedAt,
	)
	return err
}

func (s *store) saveCycle(ctx context.Context, c CycleResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO desk_cycles(id, venue, symbol, interval, script_id, mode, final_status, payload_json, created_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Venue, c.Symbol, c.Interval, c.ScriptID, c.Mode, c.FinalStatus, string(raw), c.CreatedAt,
	); err != nil {
		return err
	}
	_, _ = s.db.ExecContext(ctx,
		`DELETE FROM desk_cycles WHERE id IN (SELECT id FROM desk_cycles ORDER BY created_at DESC LIMIT -1 OFFSET ?)`,
		maxCyclesRetained,
	)
	return nil
}

func (s *store) listCycles(ctx context.Context, limit int) ([]CycleResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM desk_cycles ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CycleResult, 0, limit)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item CycleResult
		if err := json.Unmarshal([]byte(raw), &item); err == nil {
			out = append(out, item)
		}
	}
	return out, rows.Err()
}

func (s *store) saveTicket(ctx context.Context, t OrderTicket) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO desk_tickets(id, cycle_id, venue, symbol, interval, script_id, side, mode, entry_price, stop_price, target_price, quantity, notional_usd, risk_usd, risk_reward, expected_avg_r, chart_score, status, reason, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.CycleID, t.Venue, t.Symbol, t.Interval, t.ScriptID, t.Side, t.Mode, t.EntryPrice, t.StopPrice, t.TargetPrice, t.Quantity, t.NotionalUSD, t.RiskUSD, t.RiskReward, t.ExpectedAvgR, t.ChartScore, t.Status, t.Reason, t.CreatedAt, t.UpdatedAt,
	)
	return err
}

func (s *store) getTicket(ctx context.Context, id string) (OrderTicket, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var t OrderTicket
	err := s.db.QueryRowContext(ctx,
		`SELECT id, cycle_id, venue, symbol, interval, script_id, side, mode, entry_price, stop_price, target_price, quantity, notional_usd, risk_usd, risk_reward, expected_avg_r, chart_score, status, reason, created_at, updated_at FROM desk_tickets WHERE id=?`,
		id,
	).Scan(&t.ID, &t.CycleID, &t.Venue, &t.Symbol, &t.Interval, &t.ScriptID, &t.Side, &t.Mode, &t.EntryPrice, &t.StopPrice, &t.TargetPrice, &t.Quantity, &t.NotionalUSD, &t.RiskUSD, &t.RiskReward, &t.ExpectedAvgR, &t.ChartScore, &t.Status, &t.Reason, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return OrderTicket{}, false, nil
	}
	if err != nil {
		return OrderTicket{}, false, err
	}
	return t, true, nil
}

func (s *store) listTickets(ctx context.Context, status string, limit int) ([]OrderTicket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `SELECT id, cycle_id, venue, symbol, interval, script_id, side, mode, entry_price, stop_price, target_price, quantity, notional_usd, risk_usd, risk_reward, expected_avg_r, chart_score, status, reason, created_at, updated_at FROM desk_tickets`
	var args []any
	if strings.TrimSpace(status) != "" {
		query += ` WHERE status=?`
		args = append(args, strings.ToUpper(strings.TrimSpace(status)))
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]OrderTicket, 0, limit)
	for rows.Next() {
		var t OrderTicket
		if err := rows.Scan(&t.ID, &t.CycleID, &t.Venue, &t.Symbol, &t.Interval, &t.ScriptID, &t.Side, &t.Mode, &t.EntryPrice, &t.StopPrice, &t.TargetPrice, &t.Quantity, &t.NotionalUSD, &t.RiskUSD, &t.RiskReward, &t.ExpectedAvgR, &t.ChartScore, &t.Status, &t.Reason, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *store) cancelPendingTickets(ctx context.Context, reason string, now int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx,
		`UPDATE desk_tickets SET status='CANCELLED_KILL_SWITCH', reason=?, updated_at=? WHERE status='PENDING_PM'`,
		reason, now,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *store) savePosition(ctx context.Context, p PaperPosition) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO paper_positions(id, ticket_id, venue, symbol, script_id, side, quantity, entry_price, mark_price, stop_price, target_price, initial_risk_usd, entry_fee_usd, exit_price, exit_fee_usd, unrealized_pnl, realized_pnl, pnl_r, status, exit_reason, opened_at, closed_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.TicketID, p.Venue, p.Symbol, p.ScriptID, p.Side, p.Quantity, p.EntryPrice, p.MarkPrice, p.StopPrice, p.TargetPrice, p.InitialRiskUSD, p.EntryFeeUSD, p.ExitPrice, p.ExitFeeUSD, p.UnrealizedPnL, p.RealizedPnL, p.PnLR, p.Status, p.ExitReason, p.OpenedAt, p.ClosedAt,
	)
	return err
}

func (s *store) getPosition(ctx context.Context, id string) (PaperPosition, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var p PaperPosition
	err := s.db.QueryRowContext(ctx,
		`SELECT id, ticket_id, venue, symbol, script_id, side, quantity, entry_price, mark_price, stop_price, target_price, initial_risk_usd, entry_fee_usd, exit_price, exit_fee_usd, unrealized_pnl, realized_pnl, pnl_r, status, exit_reason, opened_at, closed_at FROM paper_positions WHERE id=?`,
		id,
	).Scan(&p.ID, &p.TicketID, &p.Venue, &p.Symbol, &p.ScriptID, &p.Side, &p.Quantity, &p.EntryPrice, &p.MarkPrice, &p.StopPrice, &p.TargetPrice, &p.InitialRiskUSD, &p.EntryFeeUSD, &p.ExitPrice, &p.ExitFeeUSD, &p.UnrealizedPnL, &p.RealizedPnL, &p.PnLR, &p.Status, &p.ExitReason, &p.OpenedAt, &p.ClosedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PaperPosition{}, false, nil
	}
	if err != nil {
		return PaperPosition{}, false, err
	}
	return p, true, nil
}

func (s *store) listPositions(ctx context.Context, status string, limit int) ([]PaperPosition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `SELECT id, ticket_id, venue, symbol, script_id, side, quantity, entry_price, mark_price, stop_price, target_price, initial_risk_usd, entry_fee_usd, exit_price, exit_fee_usd, unrealized_pnl, realized_pnl, pnl_r, status, exit_reason, opened_at, closed_at FROM paper_positions`
	var args []any
	if strings.TrimSpace(status) != "" {
		query += ` WHERE status=?`
		args = append(args, strings.ToUpper(strings.TrimSpace(status)))
	}
	query += ` ORDER BY opened_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PaperPosition, 0, limit)
	for rows.Next() {
		var p PaperPosition
		if err := rows.Scan(&p.ID, &p.TicketID, &p.Venue, &p.Symbol, &p.ScriptID, &p.Side, &p.Quantity, &p.EntryPrice, &p.MarkPrice, &p.StopPrice, &p.TargetPrice, &p.InitialRiskUSD, &p.EntryFeeUSD, &p.ExitPrice, &p.ExitFeeUSD, &p.UnrealizedPnL, &p.RealizedPnL, &p.PnLR, &p.Status, &p.ExitReason, &p.OpenedAt, &p.ClosedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *store) appendJournal(ctx context.Context, j JournalEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO desk_journal(id, cycle_id, ticket_id, seat, venue, symbol, script_id, side, mode, outcome, score, entry_price, stop_price, target_price, exit_price, quantity, risk_usd, pnl_usd, pnl_r, expected_avg_r, risk_check, skeptic_verdict, reason, lesson, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.CycleID, j.TicketID, string(j.Seat), j.Venue, j.Symbol, j.ScriptID, j.Side, j.Mode, j.Outcome, j.Score, j.EntryPrice, j.StopPrice, j.TargetPrice, j.ExitPrice, j.Quantity, j.RiskUSD, j.PnLUSD, j.PnLR, j.ExpectedAvgR, j.RiskCheck, j.SkepticVerdict, j.Reason, j.Lesson, j.CreatedAt, j.UpdatedAt,
	); err != nil {
		return err
	}
	_, _ = s.db.ExecContext(ctx,
		`DELETE FROM desk_journal WHERE id IN (SELECT id FROM desk_journal ORDER BY created_at DESC LIMIT -1 OFFSET ?)`,
		maxJournalRetained,
	)
	return nil
}

func (s *store) updateJournalOutcomeByTicket(ctx context.Context, ticketID, outcome string, exitPrice, pnlUSD, pnlR float64, reason, lesson string, updatedAt int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`UPDATE desk_journal SET outcome=?, exit_price=?, pnl_usd=?, pnl_r=?, reason=CASE WHEN ?='' THEN reason ELSE ? END, lesson=CASE WHEN ?='' THEN lesson ELSE ? END, updated_at=? WHERE ticket_id=?`,
		outcome, exitPrice, pnlUSD, pnlR, reason, reason, lesson, lesson, updatedAt, ticketID,
	)
	return err
}

func (s *store) listJournal(ctx context.Context, symbol, outcome string, limit int) ([]JournalEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > 250 {
		limit = 50
	}
	var conds []string
	var args []any
	if strings.TrimSpace(symbol) != "" {
		conds = append(conds, `UPPER(symbol)=?`)
		args = append(args, strings.ToUpper(strings.TrimSpace(symbol)))
	}
	if strings.TrimSpace(outcome) != "" {
		conds = append(conds, `UPPER(outcome)=?`)
		args = append(args, strings.ToUpper(strings.TrimSpace(outcome)))
	}
	query := `SELECT id, cycle_id, ticket_id, seat, venue, symbol, script_id, side, mode, outcome, score, entry_price, stop_price, target_price, exit_price, quantity, risk_usd, pnl_usd, pnl_r, expected_avg_r, risk_check, skeptic_verdict, reason, lesson, created_at, updated_at FROM desk_journal`
	if len(conds) > 0 {
		query += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]JournalEntry, 0, limit)
	for rows.Next() {
		var j JournalEntry
		var seatStr string
		if err := rows.Scan(&j.ID, &j.CycleID, &j.TicketID, &seatStr, &j.Venue, &j.Symbol, &j.ScriptID, &j.Side, &j.Mode, &j.Outcome, &j.Score, &j.EntryPrice, &j.StopPrice, &j.TargetPrice, &j.ExitPrice, &j.Quantity, &j.RiskUSD, &j.PnLUSD, &j.PnLR, &j.ExpectedAvgR, &j.RiskCheck, &j.SkepticVerdict, &j.Reason, &j.Lesson, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		j.Seat = SeatID(seatStr)
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *store) listLessons(ctx context.Context, limit int) ([]LessonRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > maxLessonsRetained {
		limit = maxLessonsRetained
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, category, symbol, rule, source_trade_id, active, created_at, updated_at FROM desk_lessons ORDER BY updated_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LessonRecord, 0, 16)
	for rows.Next() {
		var l LessonRecord
		var activeInt int
		if err := rows.Scan(&l.ID, &l.Category, &l.Symbol, &l.Rule, &l.SourceTradeID, &activeInt, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, err
		}
		l.Active = activeInt != 0
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *store) saveLesson(ctx context.Context, in LessonInput, now int64) (LessonRecord, error) {
	if err := in.Validate(); err != nil {
		return LessonRecord{}, err
	}
	id := in.ID
	if id == "" {
		id = newID("lesson")
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	activeInt := 0
	if active {
		activeInt = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var createdAt int64 = now
	_ = s.db.QueryRowContext(ctx, `SELECT created_at FROM desk_lessons WHERE id=?`, id).Scan(&createdAt)
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO desk_lessons(id, category, symbol, rule, source_trade_id, active, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		id, in.Category, in.Symbol, in.Rule, in.SourceTradeID, activeInt, createdAt, now,
	); err != nil {
		return LessonRecord{}, err
	}
	_, _ = s.db.ExecContext(ctx,
		`DELETE FROM desk_lessons WHERE id IN (SELECT id FROM desk_lessons ORDER BY updated_at DESC LIMIT -1 OFFSET ?)`,
		maxLessonsRetained,
	)
	return LessonRecord{
		ID:            id,
		Category:      in.Category,
		Symbol:        in.Symbol,
		Rule:          in.Rule,
		SourceTradeID: in.SourceTradeID,
		Active:        active,
		CreatedAt:     createdAt,
		UpdatedAt:     now,
	}, nil
}

func (s *store) deleteLesson(ctx context.Context, id string) (bool, error) {
	if !safeSlugPattern.MatchString(strings.TrimSpace(id)) {
		return false, fmt.Errorf("invalid lesson id %q", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, `DELETE FROM desk_lessons WHERE id=?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *store) setBench(ctx context.Context, scopeKey, reason string, losses int, benchedUntil, now int64) (BenchEntry, error) {
	id := newID("bench")
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO desk_bench(id, scope_key, reason, losses, benched_until, created_at) VALUES(?, ?, ?, ?, ?, ?)
		 ON CONFLICT(scope_key) DO UPDATE SET reason=excluded.reason, losses=excluded.losses, benched_until=excluded.benched_until`,
		id, scopeKey, reason, losses, benchedUntil, now,
	); err != nil {
		return BenchEntry{}, err
	}
	var out BenchEntry
	err := s.db.QueryRowContext(ctx,
		`SELECT id, scope_key, reason, losses, benched_until, created_at FROM desk_bench WHERE scope_key=?`,
		scopeKey,
	).Scan(&out.ID, &out.ScopeKey, &out.Reason, &out.Losses, &out.BenchedUntil, &out.CreatedAt)
	return out, err
}

func (s *store) listActiveBench(ctx context.Context, now int64) ([]BenchEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, scope_key, reason, losses, benched_until, created_at FROM desk_bench WHERE benched_until > ? ORDER BY benched_until DESC`,
		now,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]BenchEntry, 0, 8)
	for rows.Next() {
		var b BenchEntry
		if err := rows.Scan(&b.ID, &b.ScopeKey, &b.Reason, &b.Losses, &b.BenchedUntil, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *store) clearBench(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM desk_bench`)
	return err
}

func (s *store) isBenched(ctx context.Context, symbol, scriptID string, now int64) (bool, BenchEntry, error) {
	items, err := s.listActiveBench(ctx, now)
	if err != nil {
		return false, BenchEntry{}, err
	}
	symKey := "symbol:" + strings.ToUpper(strings.TrimSpace(symbol))
	scriptKey := "script:" + strings.TrimSpace(scriptID)
	for _, b := range items {
		if b.ScopeKey == symKey || (scriptID != "" && b.ScopeKey == scriptKey) {
			return true, b, nil
		}
	}
	return false, BenchEntry{}, nil
}
