package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// SecurityAuditEntry is an immutable, hash-chained security audit event persisted in core.sqlite.
type SecurityAuditEntry struct {
	ID        int64  `json:"id"`
	CreatedAt int64  `json:"createdAt"`
	EventType string `json:"eventType"`
	Actor     string `json:"actor"`
	Subject   string `json:"subject,omitempty"`
	Outcome   string `json:"outcome"`
	Detail    string `json:"detail,omitempty"`
	PrevHash  string `json:"prevHash"`
	EntryHash string `json:"entryHash"`
}

var securityAuditMu sync.Mutex

func ensureSecurityAuditTable(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("core db unavailable")
	}
	_, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS security_audit_log (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	created_at INTEGER NOT NULL,
	event_type TEXT NOT NULL,
	actor TEXT NOT NULL,
	subject TEXT NOT NULL DEFAULT '',
	outcome TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT '',
	prev_hash TEXT NOT NULL,
	entry_hash TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_security_audit_created ON security_audit_log(created_at DESC);
`)
	return err
}

func computeSecurityAuditHash(prevHash string, createdAt int64, eventType, actor, subject, outcome, detail string) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "fased-core-audit:v1|%s|%d|%s|%s|%s|%s|%s",
		prevHash, createdAt, eventType, actor, subject, outcome, detail)
	return hex.EncodeToString(h.Sum(nil))
}

// RecordSecurityAudit appends a redacted, hash-chained security audit entry to core.sqlite
// and emits a corresponding structured log entry via coreLogger.
func (s *Store) RecordSecurityAudit(ctx context.Context, eventType, actor, subject, outcome, detail string) {
	if s == nil || s.db == nil {
		return
	}
	cleanType := redactLogString(eventType)
	cleanActor := redactLogString(actor)
	cleanSubject := redactLogString(subject)
	cleanOutcome := redactLogString(outcome)
	cleanDetail := redactLogString(detail)
	if cleanType == "" {
		cleanType = "security.event"
	}
	if cleanActor == "" {
		cleanActor = "system"
	}
	if cleanOutcome == "" {
		cleanOutcome = "recorded"
	}
	nowUnix := s.now().UTC().Unix()
	coreLogger.Info("security audit",
		"event", cleanType,
		"actor", cleanActor,
		"subject", cleanSubject,
		"outcome", cleanOutcome,
		"detail", cleanDetail,
	)

	securityAuditMu.Lock()
	defer securityAuditMu.Unlock()
	if err := ensureSecurityAuditTable(ctx, s.db); err != nil {
		return
	}
	var prevHash string
	err := s.db.QueryRowContext(ctx, `SELECT entry_hash FROM security_audit_log ORDER BY id DESC LIMIT 1`).Scan(&prevHash)
	if errors.Is(err, sql.ErrNoRows) {
		prevHash = strings.Repeat("0", 64)
	} else if err != nil {
		return
	}
	entryHash := computeSecurityAuditHash(prevHash, nowUnix, cleanType, cleanActor, cleanSubject, cleanOutcome, cleanDetail)
	_, _ = s.db.ExecContext(ctx, `
INSERT INTO security_audit_log(created_at, event_type, actor, subject, outcome, detail, prev_hash, entry_hash)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)
`, nowUnix, cleanType, cleanActor, cleanSubject, cleanOutcome, cleanDetail, prevHash, entryHash)
}

// VerifySecurityAuditChain verifies the cryptographic hash chain of security_audit_log.
func (s *Store) VerifySecurityAuditChain(ctx context.Context) (int, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("core db unavailable")
	}
	securityAuditMu.Lock()
	defer securityAuditMu.Unlock()
	if err := ensureSecurityAuditTable(ctx, s.db); err != nil {
		return 0, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, created_at, event_type, actor, subject, outcome, detail, prev_hash, entry_hash
FROM security_audit_log ORDER BY id ASC
`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	expectedPrev := strings.Repeat("0", 64)
	count := 0
	for rows.Next() {
		var e SecurityAuditEntry
		if err := rows.Scan(&e.ID, &e.CreatedAt, &e.EventType, &e.Actor, &e.Subject, &e.Outcome, &e.Detail, &e.PrevHash, &e.EntryHash); err != nil {
			return count, err
		}
		if e.PrevHash != expectedPrev {
			return count, fmt.Errorf("security audit chain break at id=%d", e.ID)
		}
		want := computeSecurityAuditHash(e.PrevHash, e.CreatedAt, e.EventType, e.Actor, e.Subject, e.Outcome, e.Detail)
		if e.EntryHash != want {
			return count, fmt.Errorf("security audit hash mismatch at id=%d", e.ID)
		}
		expectedPrev = e.EntryHash
		count++
	}
	return count, rows.Err()
}
