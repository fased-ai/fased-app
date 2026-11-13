//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/maintenance"
)

func TestMaintenanceEnrollmentFramePreservesPrivateStream(t *testing.T) {
	head := `{"version":1,"action":"custody-enrollment-image"}` + "\n"
	body := []byte("private image\nCHECK lock\x00tail")
	input := append([]byte(maintenance.EnrollmentStreamPrefix+head), body...)
	got, stream, err := readMaintenanceFrame(t.Context(), bytes.NewReader(input))
	if err != nil || string(got) != head || stream == nil {
		t.Fatalf("stream header: %q %v", got, err)
	}
	rest, err := io.ReadAll(stream)
	if err != nil || !bytes.Equal(rest, body) {
		t.Fatal("private stream bytes changed")
	}
	normal := []byte(`{"version":1,"action":"remote-status"}`)
	got, stream, err = readMaintenanceFrame(t.Context(), bytes.NewReader(normal))
	if err != nil || stream != nil || !bytes.Equal(got, normal) {
		t.Fatal("ordinary request framing changed")
	}
	oversized := maintenance.EnrollmentStreamPrefix + string(bytes.Repeat([]byte("x"), (32<<10)+1)) + "\n"
	if _, _, err = readMaintenanceFrame(t.Context(), bytes.NewBufferString(oversized)); err == nil {
		t.Fatal("oversized stream header accepted")
	}
}

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
