package custody

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/vault"
)

const (
	ownerProofHeader   = "X-Fased-Signer-Proof"
	ownerProofTTL      = 90 * time.Second
	ownerMinPassChars  = 12
	ownerMaxPassBytes  = 1024
	maxOwnerProofs     = 64
	ownerMaxFailsPerMn = 5
)

type ownerProofCtxKey struct{}

// WithOwnerProof attaches a single-use, action-scoped signer owner proof to ctx.
func WithOwnerProof(ctx context.Context, proof string) context.Context {
	clean := strings.TrimSpace(proof)
	if clean == "" {
		return ctx
	}
	return context.WithValue(ctx, ownerProofCtxKey{}, clean)
}

// OwnerProofFromContext extracts the signer owner proof from ctx, if present.
func OwnerProofFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(ownerProofCtxKey{}).(string)
	return strings.TrimSpace(v)
}

type ownerProofEntry struct {
	action    string
	walletID  string
	expiresAt time.Time
}

func hashOwnerProof(proof string) string {
	sum := sha256.Sum256([]byte("fased-signer-proof:v1\x00" + proof))
	return hex.EncodeToString(sum[:])
}

func normalizeOwnerScope(action, walletID string) (string, string) {
	act := strings.ToLower(strings.TrimSpace(action))
	wid := strings.TrimPrefix(strings.TrimSpace(walletID), "@wallet:")
	if wid == "" {
		wid = "*"
	}
	return act, wid
}

// HasOwnerAuth reports whether an independent owner password verifier is enrolled inside the signer vault.
func (s *Service) HasOwnerAuth(ctx context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return false, errors.New("custody store closed")
	}
	return s.hasOwnerAuthLocked(ctx)
}

func (s *Service) hasOwnerAuthLocked(ctx context.Context) (bool, error) {
	var salt, hash []byte
	err := s.db.QueryRowContext(ctx, `SELECT owner_pw_salt, owner_pw_hash FROM signer_meta WHERE id = 1`).Scan(&salt, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return len(salt) == 16 && len(hash) == 32, nil
}

// ConfigureOwnerAuth enrolls an independent Argon2id owner password verifier inside signer-vault/.
// Once enrolled, it cannot be overwritten without direct vault reset so a compromised app cannot replace it.
func (s *Service) ConfigureOwnerAuth(ctx context.Context, password string) error {
	if len(password) > ownerMaxPassBytes {
		return errors.New("owner password exceeds maximum length")
	}
	if len(strings.TrimSpace(password)) < ownerMinPassChars {
		return fmt.Errorf("owner password must be at least %d characters", ownerMinPassChars)
	}
	if err := s.acquireKDFSlot(ctx); err != nil {
		return err
	}
	defer s.releaseKDFSlot()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return errors.New("custody store closed")
	}
	return s.enrollOwnerAuthLocked(ctx, password, false)
}

func (s *Service) enrollOwnerAuthLocked(ctx context.Context, password string, allowAlreadySet bool) error {
	enrolled, err := s.hasOwnerAuthLocked(ctx)
	if err != nil {
		return err
	}
	if enrolled {
		if allowAlreadySet {
			return nil
		}
		return fmt.Errorf("%w: signer owner authorization already configured", ErrOwnerAuthRequired)
	}
	salt, err := vault.RandomBytes(16)
	if err != nil {
		return err
	}
	derived := vault.DeriveKey(password, salt, s.kdfTime, s.kdfMemory, s.kdfThreads)
	defer clear(derived)
	_, err = s.db.ExecContext(ctx, `
UPDATE signer_meta
SET owner_pw_salt = ?, owner_pw_hash = ?, owner_pw_time = ?, owner_pw_memory = ?, owner_pw_threads = ?
WHERE id = 1`,
		salt, derived, s.kdfTime, s.kdfMemory, s.kdfThreads,
	)
	if err != nil {
		return err
	}
	_ = s.appendAuditExecLocked(ctx, s.db, "*", "signer_owner_auth_enrolled", "owner", "Enrolled independent Argon2id owner credential inside signer", "")
	return nil
}

// AuthorizeOwnerAction verifies the owner password directly inside fased-signer and mints a
// single-use, 90-second, action-and-wallet-scoped proof that is consumed on first use.
func (s *Service) AuthorizeOwnerAction(ctx context.Context, password, action, walletID string) (string, time.Time, error) {
	act, wid := normalizeOwnerScope(action, walletID)
	if act == "" {
		return "", time.Time{}, errors.New("owner authorization action required")
	}
	if wid == "*" && act != "restore" {
		return "", time.Time{}, fmt.Errorf("%w: explicit walletId required for %s authorization", ErrOwnerAuthRequired, act)
	}
	if len(password) > ownerMaxPassBytes || len(strings.TrimSpace(password)) < ownerMinPassChars {
		return "", time.Time{}, ErrWrongPassword
	}

	now := s.now().UTC()
	if err := s.checkOwnerAuthRateLocked(now); err != nil {
		return "", time.Time{}, err
	}

	s.mu.Lock()
	if s.closed || s.db == nil {
		s.mu.Unlock()
		return "", time.Time{}, errors.New("custody store closed")
	}
	var (
		salt, hash []byte
		tParam     uint32
		mParam     uint32
		pParam     uint8
	)
	err := s.db.QueryRowContext(ctx, `
SELECT owner_pw_salt, owner_pw_hash, owner_pw_time, owner_pw_memory, owner_pw_threads
FROM signer_meta WHERE id = 1`).Scan(&salt, &hash, &tParam, &mParam, &pParam)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.mu.Unlock()
		return "", time.Time{}, err
	}
	enrolled := len(salt) == 16 && len(hash) == 32
	s.mu.Unlock()
	if !enrolled {
		// Fail closed: never auto-enroll arbitrary caller passwords on authorize.
		return "", time.Time{}, fmt.Errorf("%w: signer owner authorization not configured", ErrOwnerAuthRequired)
	}

	if err := s.acquireKDFSlot(ctx); err != nil {
		return "", time.Time{}, err
	}
	// Run Argon2id outside DB mutex so concurrent reads do not stall.
	derived := vault.DeriveKey(password, salt, tParam, mParam, pParam)
	s.releaseKDFSlot()
	ok := subtle.ConstantTimeCompare(derived, hash) == 1
	clear(derived)
	if !ok {
		s.recordOwnerAuthFailure(s.now().UTC())
		return "", time.Time{}, ErrWrongPassword
	}
	s.resetOwnerAuthFailures()

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	proof := base64.RawURLEncoding.EncodeToString(raw)
	clear(raw)

	now = s.now().UTC()
	exp := now.Add(ownerProofTTL)
	s.proofMu.Lock()
	if s.ownerProofs == nil {
		s.ownerProofs = make(map[string]ownerProofEntry)
	}
	for k, v := range s.ownerProofs {
		if !now.Before(v.expiresAt) {
			delete(s.ownerProofs, k)
		}
	}
	if len(s.ownerProofs) >= maxOwnerProofs {
		s.proofMu.Unlock()
		return "", time.Time{}, errors.New("too many pending signer owner authorizations; retry shortly")
	}
	s.ownerProofs[hashOwnerProof(proof)] = ownerProofEntry{
		action:    act,
		walletID:  wid,
		expiresAt: exp,
	}
	s.proofMu.Unlock()
	return proof, exp, nil
}

func (s *Service) acquireKDFSlot(ctx context.Context) error {
	if s.kdfSlots == nil {
		return nil
	}
	select {
	case s.kdfSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return errors.New("signer KDF busy; retry shortly")
	}
}

func (s *Service) releaseKDFSlot() {
	if s.kdfSlots == nil {
		return
	}
	select {
	case <-s.kdfSlots:
	default:
	}
}

func (s *Service) checkOwnerAuthRateLocked(now time.Time) error {
	s.authFailMu.Lock()
	defer s.authFailMu.Unlock()
	if now.Before(s.authLockUntil) {
		return fmt.Errorf("%w: signer owner authorization temporarily rate-limited", ErrWrongPassword)
	}
	cutoff := now.Add(-time.Minute)
	kept := s.authFailTimes[:0]
	for _, ts := range s.authFailTimes {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	s.authFailTimes = kept
	if len(s.authFailTimes) >= ownerMaxFailsPerMn {
		return fmt.Errorf("%w: signer owner authorization rate limit exceeded", ErrWrongPassword)
	}
	return nil
}

func (s *Service) recordOwnerAuthFailure(now time.Time) {
	s.authFailMu.Lock()
	defer s.authFailMu.Unlock()
	s.authFailTimes = append(s.authFailTimes, now)
	s.authConsecFails++
	if s.authConsecFails >= ownerMaxFailsPerMn {
		shift := min(s.authConsecFails-ownerMaxFailsPerMn, 4)
		backoff := (5 * time.Second) << shift
		if backoff > 60*time.Second {
			backoff = 60 * time.Second
		}
		s.authLockUntil = now.Add(backoff)
	}
}

func (s *Service) resetOwnerAuthFailures() {
	s.authFailMu.Lock()
	defer s.authFailMu.Unlock()
	s.authConsecFails = 0
	s.authLockUntil = time.Time{}
}

// ConsumeOwnerProof validates and atomically consumes a single-use signer owner proof for (action, walletID).
func (s *Service) ConsumeOwnerProof(action, walletID, proof string) error {
	clean := strings.TrimSpace(proof)
	if clean == "" {
		return fmt.Errorf("%w: missing signer owner proof for %s", ErrOwnerAuthRequired, action)
	}
	act, wid := normalizeOwnerScope(action, walletID)
	key := hashOwnerProof(clean)
	now := s.now().UTC()

	s.proofMu.Lock()
	defer s.proofMu.Unlock()
	for k, v := range s.ownerProofs {
		if !now.Before(v.expiresAt) {
			delete(s.ownerProofs, k)
		}
	}
	entry, ok := s.ownerProofs[key]
	if !ok {
		return fmt.Errorf("%w: signer owner proof expired, already used, or invalid", ErrOwnerAuthRequired)
	}
	if entry.action != act || (entry.walletID != wid && entry.walletID != "*") {
		return fmt.Errorf("%w: signer owner proof scope mismatch (want %s/%s)", ErrOwnerAuthRequired, act, wid)
	}
	// Single-use: consume immediately.
	delete(s.ownerProofs, key)
	return nil
}
