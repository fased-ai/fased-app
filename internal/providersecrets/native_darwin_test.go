//go:build darwin

package providersecrets

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestMacOSKeychainLifecycleAndNoSecretInArgv(t *testing.T) {
	prev := macSecurityRunner
	t.Cleanup(func() { macSecurityRunner = prev })

	stored := map[string]string{}
	macSecurityRunner = func(_ context.Context, stdin []byte, args ...string) ([]byte, int, error) {
		if len(args) > 0 && args[0] == "find-generic-password" {
			var account string
			for i := 0; i+1 < len(args); i++ {
				if args[i] == "-a" {
					account = args[i+1]
				}
			}
			if val, ok := stored[account]; ok {
				return []byte(val + "\n"), 0, nil
			}
			return nil, 44, errors.New("item not found")
		}
		if len(args) == 1 && args[0] == "-i" {
			line := strings.TrimSpace(string(stdin))
			if !strings.HasPrefix(line, "add-generic-password ") {
				t.Fatalf("unexpected interactive command: %q", line)
			}
			parts := strings.Fields(line)
			var account, secret string
			for i := 0; i+1 < len(parts); i++ {
				if parts[i] == "-a" {
					account = parts[i+1]
				}
				if parts[i] == "-w" {
					secret = parts[i+1]
				}
			}
			if account == "" || len(secret) != 64 {
				t.Fatalf("missing account or 64-char hex secret in stdin: %q", line)
			}
			stored[account] = secret
			return nil, 0, nil
		}
		// Never allow secrets passed via argv.
		for _, a := range args {
			if len(a) == 64 {
				if _, err := hex.DecodeString(a); err == nil {
					t.Fatalf("key material passed via argv: %v", args)
				}
			}
		}
		return nil, 1, errors.New("unexpected security call")
	}

	var native Native
	if _, err := native.Key(t.Context(), "bad instance!", true); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected invalid instance refusal, got %v", err)
	}
	if _, err := native.Key(t.Context(), "inst-mac-1", false); !errors.Is(err, ErrMissing) {
		t.Fatalf("expected ErrMissing before create, got %v", err)
	}
	created, err := native.Key(t.Context(), "inst-mac-1", true)
	if err != nil || len(created) != 32 {
		t.Fatalf("create failed: len=%d err=%v", len(created), err)
	}
	loaded, err := native.Key(t.Context(), "inst-mac-1", false)
	if err != nil || !bytes.Equal(created, loaded) {
		t.Fatalf("reload mismatch: err=%v", err)
	}
}
