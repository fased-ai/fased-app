//go:build windows

package providersecrets

import (
	"bytes"
	"errors"
	"testing"
)

func TestWindowsDPAPIKeyLifecycle(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	var native Native
	if _, err := native.Key(t.Context(), "", true); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable for empty instance, got %v", err)
	}
	if _, err := native.Key(t.Context(), "inst-win-1", false); !errors.Is(err, ErrMissing) {
		t.Fatalf("expected ErrMissing before create, got %v", err)
	}
	created, err := native.Key(t.Context(), "inst-win-1", true)
	if err != nil || len(created) != 32 {
		t.Fatalf("DPAPI create failed: len=%d err=%v", len(created), err)
	}
	loaded, err := native.Key(t.Context(), "inst-win-1", false)
	if err != nil || !bytes.Equal(created, loaded) {
		t.Fatalf("DPAPI reload mismatch: err=%v", err)
	}
	other, err := native.Key(t.Context(), "inst-win-2", true)
	if err != nil || len(other) != 32 || bytes.Equal(created, other) {
		t.Fatalf("separate instance should receive independent key: err=%v", err)
	}
}
