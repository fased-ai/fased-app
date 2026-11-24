package core

import (
	"bytes"
	"strings"
	"testing"
)

func TestRedactingLoggerScrubsSecretsAndKeys(t *testing.T) {
	var buf bytes.Buffer
	logger := newRedactingLogger(&buf)
	logger.Warn("provider check failed",
		"err", "Authorization: Bearer superSecretToken123456 sk-live1234567890 #pair=abcdef1234567890",
		"password", "my-vault-pass",
		"accessToken", "raw-token-value",
		"component", "scheduler",
	)
	out := buf.String()
	for _, forbidden := range []string{
		"superSecretToken123456",
		"sk-live1234567890",
		"abcdef1234567890",
		"my-vault-pass",
		"raw-token-value",
	} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("redacted log leaked %q: %s", forbidden, out)
		}
	}
	if !strings.Contains(out, "[REDACTED]") || !strings.Contains(out, "component=scheduler") {
		t.Fatalf("expected redacted markers and safe fields, got: %s", out)
	}
}

func TestRecoverBackgroundPanicCatchesPanic(t *testing.T) {
	var buf bytes.Buffer
	prev := coreLogger
	coreLogger = newRedactingLogger(&buf)
	t.Cleanup(func() { coreLogger = prev })

	func() {
		defer recoverBackgroundPanic("test-loop")
		panic("boom Bearer secretToken999999")
	}()

	out := buf.String()
	if !strings.Contains(out, "background panic recovered") || !strings.Contains(out, "component=test-loop") {
		t.Fatalf("expected structured panic recovery log, got: %s", out)
	}
	if strings.Contains(out, "secretToken999999") {
		t.Fatalf("panic log leaked bearer token: %s", out)
	}
}
