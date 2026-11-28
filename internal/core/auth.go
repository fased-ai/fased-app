package core

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/fased-ai/fased-app/internal/auth"
	"github.com/fased-ai/fased-app/internal/vault"
)

const SessionLifetime = 12 * time.Hour
const SessionIdleTimeout = 2 * time.Hour
const ClaimLifetime = 15 * time.Minute
const (
	maxRemoteSessionsPerNode = 16
	maxLocalSessions         = 32
)

const (
	prodKDFTime    uint32 = vault.ProdKDFTime
	prodKDFMemory  uint32 = vault.ProdKDFMemory
	prodKDFThreads uint8  = vault.ProdKDFThreads
)

var (
	kdfTime    = prodKDFTime
	kdfMemory  = prodKDFMemory
	kdfThreads = prodKDFThreads
)

func passwordHash(password string, salt []byte) []byte {
	return vault.DeriveKey(password, salt, kdfTime, kdfMemory, kdfThreads)
}

func validPassword(password string) bool {
	return auth.ValidPassword(password)
}

func validLocalPassword(password string) bool {
	return utf8.ValidString(password) && utf8.RuneCountInString(strings.TrimSpace(password)) >= 6 && len(password) <= 1024
}

func (s *Store) compareAndSetUnclaimedCode(ctx context.Context, previous, next string, expires int64) (bool, error) {
	result, err := s.db.ExecContext(ctx, "UPDATE instance SET claim_hash=?,claim_expires=? WHERE id=1 AND password_hash IS NULL AND COALESCE(claim_hash,'')=?", next, expires, previous)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s *Store) IssueClaimCode(ctx context.Context) (ClaimCode, error) {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	code, err := randomToken()
	if err != nil {
		return ClaimCode{}, err
	}
	result := ClaimCode{Code: code, ExpiresAt: s.now().Add(ClaimLifetime)}
	updated := false
	for attempt := 0; attempt < 3; attempt++ {
		var previous string
		var claimed bool
		if err = s.db.QueryRowContext(ctx, "SELECT COALESCE(claim_hash,''),password_hash IS NOT NULL FROM instance WHERE id=1").Scan(&previous, &claimed); err != nil {
			return ClaimCode{}, err
		}
		if claimed {
			return ClaimCode{}, ErrClaimed
		}
		prefix := ""
		if strings.HasPrefix(previous, "remote-v1:") {
			parts := strings.Split(previous, ":")
			if len(parts) != 4 {
				return ClaimCode{}, ErrUnauthorized
			}
			prefix = strings.Join(parts[:3], ":") + ":"
		}
		updated, err = s.compareAndSetUnclaimedCode(ctx, previous, prefix+tokenHash(code), result.ExpiresAt.Unix())
		if err != nil {
			return ClaimCode{}, err
		}
		if updated {
			break
		}
	}
	if !updated {
		return ClaimCode{}, ErrConflict
	}
	if err = WritePrivateJSON(filepath.Join(s.dir, "claim-code.json"), result); err != nil {
		return ClaimCode{}, err
	}
	return result, nil
}
func (s *Store) Claim(ctx context.Context, code, password string) (string, error) {
	if !validPassword(password) {
		return "", errors.New("use a password of at least 12 characters and at most 1024 bytes")
	}
	s.authMu.Lock()
	var hash string
	var expires int64
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(claim_hash,''),COALESCE(claim_expires,0) FROM instance WHERE id=1 AND password_hash IS NULL").Scan(&hash, &expires)
	s.authMu.Unlock()
	if err != nil || expires <= s.now().Unix() || subtle.ConstantTimeCompare([]byte(hash), []byte(tokenHash(code))) != 1 {
		return "", ErrUnauthorized
	}
	salt := make([]byte, 16)
	if _, err = rand.Read(salt); err != nil {
		return "", err
	}
	derived := passwordHash(password, salt)
	defer clear(derived)
	s.authMu.Lock()
	defer s.authMu.Unlock()
	// Recheck the claim inside the conditional write; another process may claim first.
	result, err := s.db.ExecContext(ctx, "UPDATE instance SET password_salt=?,password_hash=?,claim_hash=NULL,claim_expires=NULL WHERE id=1 AND password_hash IS NULL AND claim_hash=? AND claim_expires>?", salt, derived, hash, s.now().Unix())
	if err != nil {
		return "", metadataWriteError(err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if count != 1 {
		return "", ErrUnauthorized
	}
	_ = os.Remove(filepath.Join(s.dir, "claim-code.json"))
	return s.NewSession(ctx)
}
func (s *Store) Login(ctx context.Context, password string) (string, error) {
	if len(password) > 1024 {
		return "", ErrUnauthorized
	}
	var salt, expected []byte
	if err := s.db.QueryRowContext(ctx, "SELECT password_salt,password_hash FROM instance WHERE id=1 AND password_hash IS NOT NULL").Scan(&salt, &expected); err != nil {
		return "", ErrUnauthorized
	}
	if passwordDisabled(salt) {
		return "", ErrUnauthorized
	}
	derived := passwordHash(password, salt)
	defer clear(derived)
	if subtle.ConstantTimeCompare(derived, expected) != 1 {
		return "", ErrUnauthorized
	}
	return s.NewSession(ctx)
}

// VerifyOwnerPassword verifies the instance owner password without creating a new session.
func (s *Store) VerifyOwnerPassword(ctx context.Context, password string) error {
	if len(password) == 0 || len(password) > 1024 {
		return ErrUnauthorized
	}
	var salt, expected []byte
	if err := s.db.QueryRowContext(ctx, "SELECT password_salt,password_hash FROM instance WHERE id=1 AND password_hash IS NOT NULL").Scan(&salt, &expected); err != nil {
		return ErrUnauthorized
	}
	if len(expected) == 0 || passwordDisabled(salt) {
		return ErrUnauthorized
	}
	derived := passwordHash(password, salt)
	defer clear(derived)
	if subtle.ConstantTimeCompare(derived, expected) != 1 {
		return ErrUnauthorized
	}
	return nil
}

// SetLocalOwnerPassword upgrades a passwordless Local instance to an Argon2id owner password,
// disabling future passwordless local browser auto-entry and revoking prior sessions.
func (s *Store) SetLocalOwnerPassword(ctx context.Context, password string) error {
	return s.setLocalOwnerPasswordKeeping(ctx, password, "")
}

func (s *Store) setLocalOwnerPasswordKeeping(ctx context.Context, password, keepTokenHash string) error {
	if !validLocalPassword(password) {
		return errors.New("use a password of at least 6 characters and at most 1024 bytes")
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()
	var salt, existing []byte
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(password_salt,X''),COALESCE(password_hash,X'') FROM instance WHERE id=1").Scan(&salt, &existing); err != nil {
		return err
	}
	if len(existing) > 0 && !passwordDisabled(salt) {
		return ErrClaimed
	}
	newSalt := make([]byte, 16)
	if _, err := rand.Read(newSalt); err != nil {
		return err
	}
	derived := passwordHash(password, newSalt)
	defer clear(derived)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "UPDATE instance SET password_salt=?,password_hash=?,claim_hash=NULL,claim_expires=NULL WHERE id=1", newSalt, derived); err != nil {
		return metadataWriteError(err)
	}
	// Revoke all other pre-existing passwordless sessions immediately upon setting owner password.
	if strings.TrimSpace(keepTokenHash) != "" {
		if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash != ?", keepTokenHash); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func pruneSessionsLocked(ctx context.Context, tx *sql.Tx, now int64, nodeID string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE expires<=?", now); err != nil {
		return err
	}
	if nodeID != "" {
		_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash IN (
			SELECT s.token_hash FROM sessions s
			JOIN remote_sessions r ON r.token_hash=s.token_hash
			WHERE r.node_id=?
			ORDER BY s.expires DESC, s.rowid DESC
			LIMIT -1 OFFSET ?
		)`, nodeID, maxRemoteSessionsPerNode-1)
		return err
	}
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version >= RemoteOwnerStateSchema {
		_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash NOT IN (SELECT token_hash FROM remote_sessions) AND rowid NOT IN (
			SELECT rowid FROM sessions WHERE token_hash NOT IN (SELECT token_hash FROM remote_sessions)
			ORDER BY expires DESC, rowid DESC LIMIT ?
		)`, maxLocalSessions-1)
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE rowid NOT IN (
		SELECT rowid FROM sessions ORDER BY expires DESC, rowid DESC LIMIT ?
	)`, maxLocalSessions-1)
	return err
}

func (s *Store) NewSession(ctx context.Context) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err = pruneSessionsLocked(ctx, tx, s.now().Unix(), ""); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO sessions(token_hash,expires) VALUES(?,?)", tokenHash(token), s.now().Add(SessionLifetime).Unix()); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	s.recordSessionActivity(ctx, tokenHash(token), s.now().Unix())
	s.recordTestCSRFSecret(token)
	s.restoreOSProtectedConnections(ctx)
	return token, nil
}
func (s *Store) Session(ctx context.Context, token string) bool {
	if len(token) != 43 {
		return false
	}
	nowUnix := s.now().Unix()
	th := tokenHash(token)
	var valid bool
	err := s.db.QueryRowContext(ctx, "SELECT expires>? FROM sessions WHERE token_hash=?", nowUnix, th).Scan(&valid)
	if err != nil || !valid {
		return false
	}
	if !s.touchSessionIdle(ctx, th, nowUnix) {
		_, _ = s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", th)
		_, _ = s.db.ExecContext(ctx, "DELETE FROM session_activity WHERE token_hash=?", th)
		return false
	}
	return true
}
func (s *Store) Logout(ctx context.Context, token string) error {
	s.lockVenueCredentials()
	defer s.lockVenueCredentials() // Close the admission window before session deletion completes.
	s.logoutAIConnections()
	th := tokenHash(token)
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", th)
	_, _ = s.db.ExecContext(ctx, "DELETE FROM session_activity WHERE token_hash=?", th)
	if err == nil && len(token) == 43 {
		s.removePairedDeviceByTokenHash(th)
	}
	return err
}

func (s *Store) initCSRFSecret(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("core db unavailable")
	}
	if _, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS instance_security_meta (
	k TEXT PRIMARY KEY,
	v BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS session_activity (
	token_hash TEXT PRIMARY KEY,
	last_seen_at INTEGER NOT NULL
);
`); err != nil {
		return err
	}
	var secret []byte
	err := s.db.QueryRowContext(ctx, `SELECT v FROM instance_security_meta WHERE k = 'csrf_secret'`).Scan(&secret)
	if err == nil && len(secret) == 32 {
		s.csrfSecret = secret
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	secret = make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO instance_security_meta(k, v) VALUES('csrf_secret', ?)`, secret); err != nil {
		return err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT v FROM instance_security_meta WHERE k = 'csrf_secret'`).Scan(&secret); err != nil || len(secret) != 32 {
		return errors.New("failed to persist instance CSRF secret")
	}
	s.csrfSecret = secret
	return nil
}

func (s *Store) recordSessionActivity(ctx context.Context, th string, nowUnix int64) {
	if s == nil || s.db == nil || th == "" {
		return
	}
	_, _ = s.db.ExecContext(ctx, `INSERT OR REPLACE INTO session_activity(token_hash, last_seen_at) VALUES(?, ?)`, th, nowUnix)
}

func (s *Store) touchSessionIdle(ctx context.Context, th string, nowUnix int64) bool {
	if s == nil || s.db == nil || th == "" {
		return false
	}
	var lastSeen int64
	err := s.db.QueryRowContext(ctx, `SELECT last_seen_at FROM session_activity WHERE token_hash = ?`, th).Scan(&lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		s.recordSessionActivity(ctx, th, nowUnix)
		return true
	}
	if err != nil {
		return true
	}
	if nowUnix-lastSeen > int64(SessionIdleTimeout.Seconds()) {
		return false
	}
	if nowUnix-lastSeen >= 60 {
		s.recordSessionActivity(ctx, th, nowUnix)
	}
	return true
}

func (s *Store) hardenSQLiteFilePermissions() {
	if s == nil || s.dir == "" {
		return
	}
	candidates := append([]string{"core.sqlite", "agents.sqlite"}, OptionalStateSQLitePaths()...)
	for _, rel := range candidates {
		base := filepath.Join(s.dir, filepath.FromSlash(rel))
		for _, suffix := range []string{"", "-wal", "-shm"} {
			p := base + suffix
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				_ = os.Chmod(p, 0o600)
			}
		}
	}
}

func (s *Store) CSRFToken(token string) string {
	if s == nil {
		return auth.CSRFToken(token)
	}
	return auth.CSRFTokenWithSecret(s.csrfSecret, token)
}

func (s *Store) VerifyCSRF(provided, token string) bool {
	if s == nil {
		return false
	}
	return auth.VerifyCSRFWithSecret(s.csrfSecret, provided, token)
}

var testCSRFSecrets sync.Map // testing.Testing() only: tokenHash -> []byte

func (s *Store) recordTestCSRFSecret(token string) {
	if !testing.Testing() || s == nil || len(s.csrfSecret) == 0 || token == "" {
		return
	}
	cp := append([]byte(nil), s.csrfSecret...)
	testCSRFSecrets.Store(tokenHash(token), cp)
}

func csrfToken(token string) string {
	if testing.Testing() {
		if v, ok := testCSRFSecrets.Load(tokenHash(token)); ok {
			if sec, ok2 := v.([]byte); ok2 && len(sec) == 32 {
				return auth.CSRFTokenWithSecret(sec, token)
			}
		}
	}
	return auth.CSRFToken(token)
}
