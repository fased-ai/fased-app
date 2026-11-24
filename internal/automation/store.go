package automation

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
	automationDirName = "automation"
	keyFileName       = "automation.key"
	automationDBName  = "automation.sqlite"
	secretAAD         = "fased-automation/v1"
)

type store struct {
	dir string
	key []byte
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

func loadOrCreateKey(dir string) ([]byte, error) {
	keyPath := filepath.Join(dir, keyFileName)
	if raw, err := os.ReadFile(keyPath); err == nil {
		if len(raw) != 32 {
			return nil, errors.New("corrupted automation key length")
		}
		return raw, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, ".automation.key.*")
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
			return nil, errors.New("failed to read initialized automation key")
		}
		return raw, nil
	}

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
		return nil, errors.New("failed to read initialized automation key")
	}
	return nil, err
}

func sealSecret(key []byte, plaintext string) ([]byte, error) {
	if plaintext == "" {
		return nil, nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	sealed := aead.Seal(nonce, nonce, []byte(plaintext), []byte(secretAAD))
	return sealed, nil
}

func openSecret(key, blob []byte) (string, error) {
	if len(blob) == 0 {
		return "", nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ns := aead.NonceSize()
	if len(blob) < ns {
		return "", errors.New("corrupted encrypted secret")
	}
	plain, err := aead.Open(nil, blob[:ns], blob[ns:], []byte(secretAAD))
	if err != nil {
		return "", errors.New("failed to decrypt automation secret")
	}
	return string(plain), nil
}

func maskSecret(secret string) string {
	trimmed := strings.TrimSpace(secret)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) <= 4 {
		return "****"
	}
	return "****" + trimmed[len(trimmed)-4:]
}

func openStore(dataDir string, nowFn func() time.Time) (*store, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("automation DataDir required")
	}
	dir := filepath.Join(dataDir, automationDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	key, err := loadOrCreateKey(dir)
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, automationDBName)
	dsn := sqliteFileURI(dbPath) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(FULL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const schemaSQL = `
CREATE TABLE IF NOT EXISTS control_state(
	id INTEGER PRIMARY KEY CHECK(id=1),
	paused INTEGER NOT NULL DEFAULT 0,
	kill_switch_active INTEGER NOT NULL DEFAULT 0,
	kill_switch_reason TEXT NOT NULL DEFAULT '',
	updated_at INTEGER NOT NULL DEFAULT 0,
	updated_by TEXT NOT NULL DEFAULT ''
);
INSERT OR IGNORE INTO control_state(id, paused, kill_switch_active, kill_switch_reason, updated_at, updated_by)
VALUES(1, 0, 0, '', 0, 'init');

CREATE TABLE IF NOT EXISTS triggers(
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	kind TEXT NOT NULL,
	enabled INTEGER NOT NULL,
	cooldown_seconds INTEGER NOT NULL,
	condition_json TEXT NOT NULL,
	action_json TEXT NOT NULL,
	last_evaluated_at INTEGER NOT NULL DEFAULT 0,
	last_fired_at INTEGER NOT NULL DEFAULT 0,
	last_value REAL NOT NULL DEFAULT 0,
	last_fingerprint TEXT NOT NULL DEFAULT '',
	fire_count INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS notifications(
	id TEXT PRIMARY KEY,
	trigger_id TEXT NOT NULL DEFAULT '',
	trigger_name TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL,
	severity TEXT NOT NULL,
	title TEXT NOT NULL,
	body TEXT NOT NULL,
	ticker TEXT NOT NULL DEFAULT '',
	venue TEXT NOT NULL DEFAULT '',
	urgency REAL NOT NULL DEFAULT 0,
	is_read INTEGER NOT NULL DEFAULT 0,
	untrusted INTEGER NOT NULL DEFAULT 0,
	metadata_json TEXT NOT NULL DEFAULT '{}',
	delivery_status TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_notifications_created ON notifications(created_at DESC);

CREATE TABLE IF NOT EXISTS kv_secrets(
	key TEXT PRIMARY KEY,
	value_enc BLOB,
	updated_at INTEGER NOT NULL
);
`
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &store{
		dir: dir,
		key: key,
		db:  db,
		now: nowFn,
	}, nil
}

func (st *store) close() error {
	if st == nil || st.db == nil {
		return nil
	}
	return st.db.Close()
}

func (st *store) getState(ctx context.Context) (ControlState, error) {
	var state ControlState
	var pausedInt, killInt int
	err := st.db.QueryRowContext(ctx,
		`SELECT paused, kill_switch_active, kill_switch_reason, updated_at, updated_by FROM control_state WHERE id=1`,
	).Scan(&pausedInt, &killInt, &state.KillSwitchReason, &state.UpdatedAt, &state.UpdatedBy)
	if err != nil {
		return ControlState{}, err
	}
	state.Paused = pausedInt != 0
	state.KillSwitchActive = killInt != 0
	return state, nil
}

func (st *store) setState(ctx context.Context, action, reason, actor string) (ControlState, error) {
	st.mu.Lock()
	defer st.mu.Unlock()

	cur, err := st.getState(ctx)
	if err != nil {
		return ControlState{}, err
	}
	trimmedReason := strings.TrimSpace(reason)
	if len(trimmedReason) > 240 {
		trimmedReason = trimmedReason[:240]
	}
	trimmedActor := strings.TrimSpace(actor)
	if trimmedActor == "" {
		trimmedActor = "owner"
	}
	if len(trimmedActor) > 64 {
		trimmedActor = trimmedActor[:64]
	}

	switch strings.ToLower(strings.TrimSpace(action)) {
	case StateActionPause:
		cur.Paused = true
	case StateActionResume:
		if cur.KillSwitchActive {
			return ControlState{}, errors.New("kill switch is active; clear kill switch before resuming automation")
		}
		cur.Paused = false
	case StateActionKill:
		cur.KillSwitchActive = true
		cur.Paused = true
		if trimmedReason != "" {
			cur.KillSwitchReason = trimmedReason
		} else if cur.KillSwitchReason == "" {
			cur.KillSwitchReason = "owner kill switch triggered"
		}
	case StateActionUnkill:
		cur.KillSwitchActive = false
		cur.Paused = false
		cur.KillSwitchReason = ""
	default:
		return ControlState{}, fmt.Errorf("unsupported automation control action %q", action)
	}

	cur.UpdatedAt = st.now().UTC().Unix()
	cur.UpdatedBy = trimmedActor
	if _, err := st.db.ExecContext(ctx,
		`UPDATE control_state SET paused=?, kill_switch_active=?, kill_switch_reason=?, updated_at=?, updated_by=? WHERE id=1`,
		boolToInt(cur.Paused), boolToInt(cur.KillSwitchActive), cur.KillSwitchReason, cur.UpdatedAt, cur.UpdatedBy,
	); err != nil {
		return ControlState{}, err
	}
	return cur, nil
}

func (st *store) listTriggers(ctx context.Context) ([]Trigger, error) {
	rows, err := st.db.QueryContext(ctx,
		`SELECT id, name, kind, enabled, cooldown_seconds, condition_json, action_json,
		        last_evaluated_at, last_fired_at, last_value, last_fingerprint, fire_count, created_at, updated_at
		 FROM triggers ORDER BY updated_at DESC, id ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Trigger{}
	for rows.Next() {
		var tr Trigger
		var enabledInt int
		var condJSON, actJSON string
		if err := rows.Scan(
			&tr.ID, &tr.Name, &tr.Kind, &enabledInt, &tr.CooldownSeconds,
			&condJSON, &actJSON, &tr.LastEvaluatedAt, &tr.LastFiredAt,
			&tr.LastValue, &tr.LastFingerprint, &tr.FireCount, &tr.CreatedAt, &tr.UpdatedAt,
		); err != nil {
			return nil, err
		}
		tr.Enabled = enabledInt != 0
		_ = json.Unmarshal([]byte(condJSON), &tr.Condition)
		_ = json.Unmarshal([]byte(actJSON), &tr.Action)
		out = append(out, tr)
	}
	return out, rows.Err()
}

func (st *store) getTrigger(ctx context.Context, id string) (Trigger, bool, error) {
	safeID, err := ValidateSafeID(id)
	if err != nil {
		return Trigger{}, false, err
	}
	var tr Trigger
	var enabledInt int
	var condJSON, actJSON string
	err = st.db.QueryRowContext(ctx,
		`SELECT id, name, kind, enabled, cooldown_seconds, condition_json, action_json,
		        last_evaluated_at, last_fired_at, last_value, last_fingerprint, fire_count, created_at, updated_at
		 FROM triggers WHERE id=?`, safeID,
	).Scan(
		&tr.ID, &tr.Name, &tr.Kind, &enabledInt, &tr.CooldownSeconds,
		&condJSON, &actJSON, &tr.LastEvaluatedAt, &tr.LastFiredAt,
		&tr.LastValue, &tr.LastFingerprint, &tr.FireCount, &tr.CreatedAt, &tr.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Trigger{}, false, nil
	}
	if err != nil {
		return Trigger{}, false, err
	}
	tr.Enabled = enabledInt != 0
	_ = json.Unmarshal([]byte(condJSON), &tr.Condition)
	_ = json.Unmarshal([]byte(actJSON), &tr.Action)
	return tr, true, nil
}

func (st *store) saveTrigger(ctx context.Context, in TriggerInput) (Trigger, error) {
	tr, err := NormalizeTriggerInput(in)
	if err != nil {
		return Trigger{}, err
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	var exists bool
	var existingCreated int64
	var lastEval, lastFire, fireCount int
	var lastVal float64
	var lastFP string
	err = st.db.QueryRowContext(ctx,
		`SELECT created_at, last_evaluated_at, last_fired_at, last_value, last_fingerprint, fire_count FROM triggers WHERE id=?`,
		tr.ID,
	).Scan(&existingCreated, &lastEval, &lastFire, &lastVal, &lastFP, &fireCount)
	if err == nil {
		exists = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Trigger{}, err
	}

	if !exists {
		var count int
		if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM triggers`).Scan(&count); err != nil {
			return Trigger{}, err
		}
		if count >= maxTriggers {
			return Trigger{}, fmt.Errorf("maximum of %d event triggers reached", maxTriggers)
		}
	}

	nowSec := st.now().UTC().Unix()
	if exists {
		tr.CreatedAt = existingCreated
		tr.LastEvaluatedAt = int64(lastEval)
		tr.LastFiredAt = int64(lastFire)
		tr.LastValue = lastVal
		tr.LastFingerprint = lastFP
		tr.FireCount = fireCount
	} else {
		tr.CreatedAt = nowSec
	}
	tr.UpdatedAt = nowSec

	condJSON, _ := json.Marshal(tr.Condition)
	actJSON, _ := json.Marshal(tr.Action)
	_, err = st.db.ExecContext(ctx,
		`INSERT INTO triggers(id, name, kind, enabled, cooldown_seconds, condition_json, action_json,
		                      last_evaluated_at, last_fired_at, last_value, last_fingerprint, fire_count, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET
		   name=excluded.name,
		   kind=excluded.kind,
		   enabled=excluded.enabled,
		   cooldown_seconds=excluded.cooldown_seconds,
		   condition_json=excluded.condition_json,
		   action_json=excluded.action_json,
		   updated_at=excluded.updated_at`,
		tr.ID, tr.Name, tr.Kind, boolToInt(tr.Enabled), tr.CooldownSeconds,
		string(condJSON), string(actJSON),
		tr.LastEvaluatedAt, tr.LastFiredAt, tr.LastValue, tr.LastFingerprint, tr.FireCount,
		tr.CreatedAt, tr.UpdatedAt,
	)
	if err != nil {
		return Trigger{}, err
	}
	return tr, nil
}

func (st *store) updateTriggerRuntime(ctx context.Context, tr Trigger) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	_, err := st.db.ExecContext(ctx,
		`UPDATE triggers SET last_evaluated_at=?, last_fired_at=?, last_value=?, last_fingerprint=?, fire_count=? WHERE id=?`,
		tr.LastEvaluatedAt, tr.LastFiredAt, tr.LastValue, tr.LastFingerprint, tr.FireCount, tr.ID,
	)
	return err
}

func (st *store) deleteTrigger(ctx context.Context, id string) (bool, error) {
	safeID, err := ValidateSafeID(id)
	if err != nil {
		return false, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	res, err := st.db.ExecContext(ctx, `DELETE FROM triggers WHERE id=?`, safeID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (st *store) insertNotification(ctx context.Context, n Notification) (Notification, error) {
	st.mu.Lock()
	defer st.mu.Unlock()

	if n.ID == "" {
		buf := make([]byte, 8)
		_, _ = rand.Read(buf)
		n.ID = fmt.Sprintf("ntf-%d-%x", st.now().UTC().UnixMilli(), buf[:4])
	}
	if n.Severity == "" {
		n.Severity = SeverityInfo
	}
	if n.Kind == "" {
		n.Kind = "system"
	}
	if n.MetadataJSON == "" {
		n.MetadataJSON = "{}"
	}
	if n.CreatedAt == 0 {
		n.CreatedAt = st.now().UTC().Unix()
	}

	_, err := st.db.ExecContext(ctx,
		`INSERT INTO notifications(id, trigger_id, trigger_name, kind, severity, title, body, ticker, venue, urgency, is_read, untrusted, metadata_json, delivery_status, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.TriggerID, n.TriggerName, n.Kind, n.Severity, n.Title, n.Body,
		n.Ticker, n.Venue, n.Urgency, boolToInt(n.Read), boolToInt(n.Untrusted),
		n.MetadataJSON, n.DeliveryStatus, n.CreatedAt,
	)
	if err != nil {
		return Notification{}, err
	}
	_, _ = st.db.ExecContext(ctx,
		`DELETE FROM notifications WHERE id NOT IN (SELECT id FROM notifications ORDER BY created_at DESC, id DESC LIMIT ?)`,
		maxNotificationsRetained,
	)
	return n, nil
}

func (st *store) updateNotificationDelivery(ctx context.Context, id, status string) error {
	if id == "" {
		return nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	_, err := st.db.ExecContext(ctx, `UPDATE notifications SET delivery_status=? WHERE id=?`, status, id)
	return err
}

func (st *store) listNotifications(ctx context.Context, filter NotificationFilter) (NotificationList, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var args []any
	var where []string
	if filter.UnreadOnly {
		where = append(where, "is_read = 0")
	}
	if sev := strings.ToLower(strings.TrimSpace(filter.Severity)); sev != "" {
		if !validSeverities[sev] {
			return NotificationList{}, fmt.Errorf("unsupported severity filter %q", filter.Severity)
		}
		where = append(where, "severity = ?")
		args = append(args, sev)
	}
	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}
	query := fmt.Sprintf(
		`SELECT id, trigger_id, trigger_name, kind, severity, title, body, ticker, venue, urgency, is_read, untrusted, metadata_json, delivery_status, created_at
		 FROM notifications %s ORDER BY created_at DESC, id DESC LIMIT ?`,
		whereClause,
	)
	args = append(args, limit)
	rows, err := st.db.QueryContext(ctx, query, args...)
	if err != nil {
		return NotificationList{}, err
	}
	defer rows.Close()

	items := []Notification{}
	for rows.Next() {
		var n Notification
		var readInt, untrustedInt int
		if err := rows.Scan(
			&n.ID, &n.TriggerID, &n.TriggerName, &n.Kind, &n.Severity, &n.Title, &n.Body,
			&n.Ticker, &n.Venue, &n.Urgency, &readInt, &untrustedInt,
			&n.MetadataJSON, &n.DeliveryStatus, &n.CreatedAt,
		); err != nil {
			return NotificationList{}, err
		}
		n.Read = readInt != 0
		n.Untrusted = untrustedInt != 0
		items = append(items, n)
	}
	if err := rows.Err(); err != nil {
		return NotificationList{}, err
	}

	var unread, total int
	_ = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE is_read=0`).Scan(&unread)
	_ = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications`).Scan(&total)
	return NotificationList{
		Notifications: items,
		UnreadCount:   unread,
		Total:         total,
	}, nil
}

func (st *store) markNotificationsRead(ctx context.Context, id string, all bool) (int, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	var res sql.Result
	var err error
	if all {
		res, err = st.db.ExecContext(ctx, `UPDATE notifications SET is_read=1 WHERE is_read=0`)
	} else {
		if strings.TrimSpace(id) == "" {
			return 0, errors.New("notification id or all=true required")
		}
		res, err = st.db.ExecContext(ctx, `UPDATE notifications SET is_read=1 WHERE id=?`, strings.TrimSpace(id))
	}
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (st *store) getSealedValue(ctx context.Context, key string) (string, bool, error) {
	if strings.TrimSpace(key) == "" {
		return "", false, errors.New("secret key required")
	}
	var blob []byte
	err := st.db.QueryRowContext(ctx, `SELECT value_enc FROM kv_secrets WHERE key=?`, key).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if len(blob) == 0 {
		return "", false, nil
	}
	plain, err := openSecret(st.key, blob)
	if err != nil {
		return "", false, err
	}
	return plain, true, nil
}

func (st *store) setSealedValue(ctx context.Context, key, plaintext string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("secret key required")
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	sealed, err := sealSecret(st.key, plaintext)
	if err != nil {
		return err
	}
	nowSec := st.now().UTC().Unix()
	_, err = st.db.ExecContext(ctx,
		`INSERT INTO kv_secrets(key, value_enc, updated_at) VALUES(?,?,?)
		 ON CONFLICT(key) DO UPDATE SET value_enc=excluded.value_enc, updated_at=excluded.updated_at`,
		key, sealed, nowSec,
	)
	return err
}

func (st *store) deleteSealedValue(ctx context.Context, key string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	_, err := st.db.ExecContext(ctx, `DELETE FROM kv_secrets WHERE key=?`, key)
	return err
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
