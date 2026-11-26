package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"sync"
	"time"
	"unicode/utf8"
)

// ValidPassword checks the minimum rune count and maximum byte bound for owner passwords.
func ValidPassword(password string) bool {
	return utf8.RuneCountInString(password) >= 12 && len(password) <= 1024
}

// RandomToken returns a 43-character base64url encoding of 32 random bytes.
func RandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// TokenHash returns the canonical lowercase SHA-256 hex digest of a bearer token or claim code.
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CSRFToken derives the domain-separated HMAC-SHA256 CSRF token bound to a session token.
func CSRFToken(token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write([]byte("fased-core-csrf-v1"))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyCSRF performs constant-time validation of a provided CSRF header against the session token.
func VerifyCSRF(provided, token string) bool {
	if token == "" || provided == "" {
		return false
	}
	return ConstantTimeEqual([]byte(provided), []byte(CSRFToken(token)))
}

// ConstantTimeEqual reports whether a and b contain identical bytes in constant time.
func ConstantTimeEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

type rateWindow struct {
	start    time.Time
	attempts int
}

// RateLimiter enforces fixed-window per-key rate limits with bounded memory (1,024 active buckets).
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*rateWindow
}

// Allow consumes one attempt for key at now and returns false when limit attempts in the current minute are exhausted.
func (l *RateLimiter) Allow(now time.Time, key string, limit int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil {
		l.buckets = map[string]*rateWindow{}
	}
	w := l.buckets[key]
	if w == nil {
		if len(l.buckets) >= 1024 {
			for k, existing := range l.buckets {
				if now.Sub(existing.start) >= time.Minute {
					delete(l.buckets, k)
				}
			}
		}
		if len(l.buckets) >= 1024 {
			// Fail closed when capacity is exhausted by active non-expired buckets instead of
			// randomly evicting active lockouts.
			return false
		}
		w = &rateWindow{start: now}
		l.buckets[key] = w
	}
	if now.Sub(w.start) >= time.Minute {
		w.start = now
		w.attempts = 0
	}
	if w.attempts >= limit {
		return false
	}
	w.attempts++
	return true
}
