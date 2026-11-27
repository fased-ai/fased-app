package core

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"runtime/debug"
	"strings"
)

var (
	logSecretPattern = regexp.MustCompile(`(?i)(bearer\s+[A-Za-z0-9._~+/=-]{8,}|fased_mcp_[A-Za-z0-9._~+/=-]{8,}|(?:sk|pk|xai|ghp|gho|ghu|ghs|glpat|AIza)[A-Za-z0-9_-]{8,}|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}|/bot\d+:[A-Za-z0-9_-]{16,}|/v[23]/[A-Za-z0-9_-]{16,}|(?:api[-_]?key|access_token|refresh_token|id_token|client_secret|password|passphrase|mnemonic|seed|sessionToken|fased_session|__Host-fased_session)=[^\s&"';]+|#(?:pair|local|setup)=[A-Za-z0-9_-]{8,}|\b[0-9a-f]{64}\b)`)
)

func isSensitiveLogKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	for _, needle := range []string{
		"token", "secret", "password", "passphrase", "authorization",
		"cookie", "mnemonic", "seed", "privatekey", "private_key", "apikey", "api_key",
	} {
		if strings.Contains(k, needle) {
			return true
		}
	}
	return false
}

// redactLogString removes credential-like substrings and control characters
// before structured log emission.
func redactLogString(raw string) string {
	cleaned := logSecretPattern.ReplaceAllString(raw, "[REDACTED]")
	cleaned = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(cleaned)
	if len(cleaned) > 512 {
		cleaned = cleaned[:512] + "..."
	}
	return strings.TrimSpace(cleaned)
}

func newRedactingLogger(w io.Writer) *slog.Logger {
	if w == nil {
		w = os.Stderr
	}
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if isSensitiveLogKey(a.Key) {
				return slog.String(a.Key, "[REDACTED]")
			}
			if a.Value.Kind() == slog.KindString {
				return slog.String(a.Key, redactLogString(a.Value.String()))
			}
			if a.Value.Kind() == slog.KindAny {
				if v := a.Value.Any(); v != nil {
					return slog.String(a.Key, redactLogString(fmt.Sprint(v)))
				}
			}
			return a
		},
	})
	return slog.New(handler)
}

var coreLogger = newRedactingLogger(os.Stderr)

// recoverBackgroundPanic prevents a panic inside a background daemon goroutine
// from terminating the entire process and records a redacted diagnostic entry.
func recoverBackgroundPanic(component string) {
	if r := recover(); r != nil {
		stack := redactLogString(string(debug.Stack()))
		coreLogger.Error("background panic recovered",
			"component", component,
			"panic", redactLogString(panicMessage(r)),
			"stack", stack,
		)
	}
}

func panicMessage(r any) string {
	switch v := r.(type) {
	case error:
		return v.Error()
	case string:
		return v
	default:
		return "unexpected panic"
	}
}
