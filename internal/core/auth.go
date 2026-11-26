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
	"time"

	"github.com/fased-ai/fased-app/internal/auth"
	"github.com/fased-ai/fased-app/internal/vault"
)

const SessionLifetime = 12 * time.Hour
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
// disabling future passwordless local browser auto-entry.
func (s *Store) SetLocalOwnerPassword(ctx context.Context, password string) error {
	if !validPassword(password) {
		return errors.New("use a password of at least 12 characters and at most 1024 bytes")
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
	_, err := s.db.ExecContext(ctx, "UPDATE instance SET password_salt=?,password_hash=?,claim_hash=NULL,claim_expires=NULL WHERE id=1", newSalt, derived)
	if err != nil {
		return metadataWriteError(err)
	}
	return nil
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
	s.restoreOSProtectedConnections(ctx)
	return token, nil
}
func (s *Store) Session(ctx context.Context, token string) bool {
	if len(token) != 43 {
		return false
	}
	var valid bool
	err := s.db.QueryRowContext(ctx, "SELECT expires>? FROM sessions WHERE token_hash=?", s.now().Unix(), tokenHash(token)).Scan(&valid)
	return err == nil && valid
}
func (s *Store) Logout(ctx context.Context, token string) error {
	s.lockVenueCredentials()
	defer s.lockVenueCredentials() // Close the admission window before session deletion completes.
	s.logoutAIConnections()
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", tokenHash(token))
	if err == nil && len(token) == 43 {
		s.removePairedDeviceByTokenHash(tokenHash(token))
	}
	return err
}
func csrfToken(token string) string {
	return auth.CSRFToken(token)
}
