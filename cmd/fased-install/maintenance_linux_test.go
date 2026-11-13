//go:build linux

package main

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestMaintenanceInputDeadlineIncludesCallerWithholdingEOF(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := readMaintenanceInput(ctx, r); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unclosed caller pipe did not respect request deadline: %v", err)
	}
}
