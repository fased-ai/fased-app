package core

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const SessionLifetime = 12 * time.Hour
const ClaimLifetime = 15 * time.Minute

func passwordHash(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
}
func validPassword(password string) bool {
	return utf8.RuneCountInString(password) >= 12 && len(password) <= 1024
}
func (s *Store) IssueClaimCode(ctx context.Context) (ClaimCode, error) {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	code, err := randomToken()
	if err != nil {
		return ClaimCode{}, err
	}
	result := ClaimCode{Code: code, ExpiresAt: s.now().Add(ClaimLifetime)}
	updated, err := s.db.ExecContext(ctx, "UPDATE instance SET claim_hash=?,claim_expires=? WHERE id=1 AND password_hash IS NULL", tokenHash(code), result.ExpiresAt.Unix())
	if err != nil {
		return ClaimCode{}, err
	}
	count, err := updated.RowsAffected()
	if err != nil {
		return ClaimCode{}, err
	}
	if count != 1 {
		return ClaimCode{}, ErrClaimed
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
	defer s.authMu.Unlock()
	var hash string
	var expires int64
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(claim_hash,''),COALESCE(claim_expires,0) FROM instance WHERE id=1 AND password_hash IS NULL").Scan(&hash, &expires)
	if err != nil || expires <= s.now().Unix() || subtle.ConstantTimeCompare([]byte(hash), []byte(tokenHash(code))) != 1 {
		return "", ErrUnauthorized
	}
	salt := make([]byte, 16)
	if _, err = rand.Read(salt); err != nil {
		return "", err
	}
	derived := passwordHash(password, salt)
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
	derived := passwordHash(password, salt)
	if subtle.ConstantTimeCompare(derived, expected) != 1 {
		return "", ErrUnauthorized
	}
	return s.NewSession(ctx)
}
func (s *Store) NewSession(ctx context.Context) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	if _, err = s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires<=?", s.now().Unix()); err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO sessions(token_hash,expires) VALUES(?,?)", tokenHash(token), s.now().Add(SessionLifetime).Unix())
	return token, err
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
	s.lockAIConnection()
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", tokenHash(token))
	return err
}
func csrfToken(token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write([]byte("fased-core-csrf-v1"))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
