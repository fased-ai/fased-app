package core

import (
	"bytes"
	"sync"
	"testing"
)

func TestPreDerivedKDFMatchesPasswordHashAndClears(t *testing.T) {
	t.Parallel()
	salt := []byte("0123456789abcdef")
	otherSalt := []byte("fedcba9876543210")

	if empty := derivePasswordKeyForSalt("short", salt); len(empty.key) != 0 || len(empty.salt) != 0 {
		t.Fatal("invalid short passphrase must not pre-derive key")
	}

	pre := derivePasswordKeyForSalt(testVaultPassword, salt)
	want := passwordHash(testVaultPassword, salt)
	if !bytes.Equal(pre.keyFor(testVaultPassword, salt), want) {
		t.Fatal("pre-derived key did not match direct passwordHash")
	}
	wantOther := passwordHash(testVaultPassword, otherSalt)
	if !bytes.Equal(pre.keyFor(testVaultPassword, otherSalt), wantOther) {
		t.Fatal("mismatched salt must fall back to computing passwordHash")
	}

	raw := pre.key
	pre.clear()
	for i, b := range raw {
		if b != 0 {
			t.Fatalf("expected cleared byte at %d, got %d", i, b)
		}
	}
	if len(pre.key) != 0 || len(pre.salt) != 0 {
		t.Fatal("clear must reset key and salt slices")
	}

	fresh, err := deriveNewPasswordKey(testVaultPassword)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.clear()
	if len(fresh.salt) != 16 || !bytes.Equal(fresh.keyFor(testVaultPassword, fresh.salt), passwordHash(testVaultPassword, fresh.salt)) {
		t.Fatal("deriveNewPasswordKey did not produce valid 16-byte salt and matching key")
	}
}

func TestConcurrentClaimWithUnlockedKDF(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	code, err := s.IssueClaimCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	tokens := make([]string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			tokens[idx], errs[idx] = s.Claim(t.Context(), code.Code, testVaultPassword)
		}(i)
	}
	close(start)
	wg.Wait()

	successes := 0
	unauthorized := 0
	for i := 0; i < 2; i++ {
		if errs[i] == nil && tokens[i] != "" {
			successes++
		} else if errs[i] == ErrUnauthorized {
			unauthorized++
		} else {
			t.Fatalf("unexpected Claim result[%d]: token=%q err=%v", i, tokens[i], errs[i])
		}
	}
	if successes != 1 || unauthorized != 1 {
		t.Fatalf("expected 1 claim success and 1 ErrUnauthorized, got successes=%d unauthorized=%d errs=%v", successes, unauthorized, errs)
	}
}

func TestAIConnectionAddUnlockRemoveWithPreDerivedKDF(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	added, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:     "add",
		Name:       "Primary",
		Key:        "fased-test-disposable-key-01",
		Passphrase: testVaultPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action: "lock",
		Ref:    added.Ref,
	}); err != nil {
		t.Fatal(err)
	}
	unlocked, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:     "unlock",
		Ref:        added.Ref,
		Passphrase: testVaultPassword,
	})
	if err != nil || unlocked.Locked {
		t.Fatalf("expected unlock success, got locked=%v err=%v", unlocked.Locked, err)
	}
	removed, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{
		Action:     "remove",
		Ref:        added.Ref,
		Passphrase: testVaultPassword,
	})
	if err != nil || len(removed.Connections) != 0 {
		t.Fatalf("expected clean remove, got connections=%d err=%v", len(removed.Connections), err)
	}
}
