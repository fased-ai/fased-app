package auth

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestValidPasswordAndTokens(t *testing.T) {
	t.Parallel()
	if ValidPassword("short") {
		t.Fatal("admitted short password")
	}
	if !ValidPassword("eleven123456") {
		t.Fatal("rejected 12-rune ASCII password")
	}
	if !ValidPassword(strings.Repeat("é", 12)) {
		t.Fatal("rejected 12-rune UTF-8 password")
	}
	if ValidPassword(strings.Repeat("a", 1025)) {
		t.Fatal("admitted oversized password")
	}

	t1, err := RandomToken()
	if err != nil || len(t1) != 43 {
		t.Fatalf("unexpected token len=%d err=%v", len(t1), err)
	}
	t2, err := RandomToken()
	if err != nil || t1 == t2 {
		t.Fatal("expected distinct tokens")
	}
	if len(TokenHash(t1)) != 64 || TokenHash(t1) == TokenHash(t2) {
		t.Fatal("unexpected token hash")
	}

	csrf := CSRFToken(t1)
	if !VerifyCSRF(csrf, t1) {
		t.Fatal("VerifyCSRF failed for matching token")
	}
	if VerifyCSRF(csrf, t2) || VerifyCSRF("wrong", t1) || VerifyCSRF("", "") {
		t.Fatal("VerifyCSRF accepted mismatched or empty token")
	}
}

func TestRateLimiterWindowEvictionAndConcurrency(t *testing.T) {
	t.Parallel()
	var limiter RateLimiter
	now := time.Unix(1795400000, 0)

	if !limiter.Allow(now, "auth|1.2.3.4", 2) || !limiter.Allow(now, "auth|1.2.3.4", 2) {
		t.Fatal("expected initial attempts within limit to pass")
	}
	if limiter.Allow(now, "auth|1.2.3.4", 2) {
		t.Fatal("expected third attempt in same minute to be denied")
	}
	if !limiter.Allow(now.Add(time.Minute), "auth|1.2.3.4", 2) {
		t.Fatal("expected reset after one minute")
	}

	// Verify 1024-bucket cap eviction under concurrent access.
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				key := fmt.Sprintf("bucket|%d", base*200+i)
				_ = limiter.Allow(now, key, 5)
			}
		}(g)
	}
	wg.Wait()
	if len(limiter.buckets) > 1024 {
		t.Fatalf("limiter exceeded max capacity 1024: %d", len(limiter.buckets))
	}
}
