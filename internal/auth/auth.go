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

// CSRFTokenWithSecret derives a server-keyed HMAC-SHA256 CSRF token bound to both
// a persistent per-instance secret and the active session token.
func CSRFTokenWithSecret(secret []byte, token string) string {
	if len(secret) == 0 {
		return CSRFToken(token)
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("fased-core-csrf-v2:"))
	_, _ = mac.Write([]byte(token))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyCSRFWithSecret validates a CSRF header using the persistent per-instance secret.
func VerifyCSRFWithSecret(secret []byte, provided, token string) bool {
	if token == "" || provided == "" {
		return false
	}
	if len(secret) == 0 {
		return VerifyCSRF(provided, token)
	}
	return ConstantTimeEqual([]byte(provided), []byte(CSRFTokenWithSecret(secret, token)))
}

// ConstantTimeEqual reports whether a and b contain identical bytes in constant time.
func ConstantTimeEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

type rateWindow struct {
	start    time.Time
	lastSeen time.Time
	attempts int
	locked   bool
}

// RateLimiter enforces per-key rate limits with bounded memory (1,024 active buckets).
// Expired buckets are purged first; unlocked idle buckets are evicted LRU so IP spray
// cannot DoS ordinary traffic, while actively locked-out buckets are never evicted.
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
			// Evict oldest idle UNLOCKED bucket (LRU) so active lockouts can never be
			// cleared by bucket flooding; if all 1,024 buckets are actively locked, fail closed.
			var evictKey string
			var oldest time.Time
			for k, existing := range l.buckets {
				if existing.locked {
					continue
				}
				if evictKey == "" || existing.lastSeen.Before(oldest) {
					evictKey = k
					oldest = existing.lastSeen
				}
			}
			if evictKey == "" {
				return false
			}
			delete(l.buckets, evictKey)
		}
		w = &rateWindow{start: now, lastSeen: now}
		l.buckets[key] = w
	}
	if now.Sub(w.start) >= time.Minute {
		w.start = now
		w.attempts = 0
		w.locked = false
	}
	w.lastSeen = now
	if w.attempts >= limit {
		w.locked = true
		return false
	}
	w.attempts++
	if w.attempts >= limit {
		w.locked = true
	}
	return true
}

// GlobalBackoff tracks instance-wide authentication failures across all source IPs
// and enforces progressive backoff against distributed credential stuffing.
type GlobalBackoff struct {
	mu          sync.Mutex
	windowStart time.Time
	failures    int
	blockedTill time.Time
}

// Allow reports whether an unauthenticated login/claim attempt is permitted at now.
func (g *GlobalBackoff) Allow(now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.blockedTill.IsZero() && now.Before(g.blockedTill) {
		return false
	}
	if !g.windowStart.IsZero() && now.Sub(g.windowStart) >= 5*time.Minute {
		g.failures = 0
		g.windowStart = time.Time{}
	}
	return true
}

// RecordFailure increments the global failure counter and applies progressive cooldowns
// after sustained distributed failures (30+ in 5m -> 15s, 60+ -> 60s, 100+ -> 5m).
func (g *GlobalBackoff) RecordFailure(now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.windowStart.IsZero() || now.Sub(g.windowStart) >= 5*time.Minute {
		g.windowStart = now
		g.failures = 0
	}
	g.failures++
	switch {
	case g.failures >= 100:
		g.blockedTill = now.Add(5 * time.Minute)
	case g.failures >= 60:
		g.blockedTill = now.Add(60 * time.Second)
	case g.failures >= 30:
		g.blockedTill = now.Add(15 * time.Second)
	}
}

// Reset clears the failure counter upon a successful owner login/claim.
func (g *GlobalBackoff) Reset() {
	g.mu.Lock()
	g.failures = 0
	g.windowStart = time.Time{}
	g.blockedTill = time.Time{}
	g.mu.Unlock()
}
