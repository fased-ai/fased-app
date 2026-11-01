package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestClientAvailabilityIsNotAuthenticationOrUnsafeFiles(t *testing.T) {
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := NewClient(t.Context(), dir); !errors.Is(e, ErrCoreUnavailable) {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "endpoint.json")
	if e := os.WriteFile(path, []byte("{}"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := NewClient(t.Context(), dir); e == nil || errors.Is(e, ErrCoreUnavailable) {
		t.Fatal("unsafe file classified as availability", e)
	}
	for _, e := range []error{ErrUnauthorized, context.Canceled, errors.New("instance mismatch"), errors.New("redirects are not allowed")} {
		if errors.Is(clientTransportError(e), ErrCoreUnavailable) {
			t.Fatal("terminal error became retryable", e)
		}
	}
	for _, e := range []error{syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.ENOENT} {
		if !errors.Is(clientTransportError(e), ErrCoreUnavailable) {
			t.Fatal(e)
		}
	}
}
