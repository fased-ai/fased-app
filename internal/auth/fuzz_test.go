package auth

import (
	"testing"
	"time"
)

func FuzzAuthCSRFAndPasswordValidation(f *testing.F) {
	f.Add([]byte("server-secret-32-bytes-minimum!!"), "session-token-example", "provided-csrf", "valid-password-123", "192.0.2.1")
	f.Add([]byte{}, "", "", "short", "2001:db8::1")

	f.Fuzz(func(t *testing.T, secret []byte, token, provided, password, key string) {
		_ = ValidPassword(password)
		_ = TokenHash(token)
		if len(secret) > 0 && token != "" {
			tok := CSRFTokenWithSecret(secret, token)
			if !VerifyCSRFWithSecret(secret, tok, token) {
				t.Fatalf("valid server-keyed CSRF failed verification")
			}
			_ = VerifyCSRFWithSecret(secret, provided, token)
		}
		_ = VerifyCSRF(provided, token)

		var rl RateLimiter
		_ = rl.Allow(time.Unix(1_700_000_000, 0), key, 5)
	})
}
