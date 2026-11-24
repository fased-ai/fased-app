//go:build darwin

package providersecrets

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

const macKeychainService = "org.fased.app.provider-task-access"

// macSecurityRunner executes /usr/bin/security; overridden in unit tests.
var macSecurityRunner = runMacSecurity

func validKeychainInstance(instance string) bool {
	if len(instance) == 0 || len(instance) > 128 {
		return false
	}
	for i := 0; i < len(instance); i++ {
		c := instance[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func runMacSecurity(ctx context.Context, stdin []byte, args ...string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/security", args...)
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return out, exitErr.ExitCode(), err
		}
		return out, -1, err
	}
	return out, 0, nil
}

// Key retrieves or creates the per-instance 32-byte encryption key in the
// current user's macOS login Keychain. Secrets are written via `security -i`
// over stdin so key material never appears in process arguments.
func (Native) Key(ctx context.Context, instance string, create bool) ([]byte, error) {
	if os.Geteuid() == 0 || !validKeychainInstance(instance) {
		return nil, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	out, code, err := macSecurityRunner(ctx, nil, "find-generic-password", "-s", macKeychainService, "-a", instance, "-w")
	if err == nil {
		rawHex := strings.TrimSpace(string(out))
		clear(out)
		key, decErr := hex.DecodeString(rawHex)
		if decErr != nil || len(key) != 32 {
			clear(key)
			return nil, ErrUnavailable
		}
		return key, nil
	}
	clear(out)
	// Exit status 44 is errSecItemNotFound from /usr/bin/security.
	if code != 44 {
		return nil, ErrUnavailable
	}
	if !create {
		return nil, ErrMissing
	}

	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return nil, err
	}
	hexKey := make([]byte, hex.EncodedLen(len(key)))
	hex.Encode(hexKey, key)
	defer clear(hexKey)

	// Feed add-generic-password over stdin to `security -i` so the secret
	// never enters argv / ps listings.
	var cmdBuf bytes.Buffer
	cmdBuf.WriteString("add-generic-password -s ")
	cmdBuf.WriteString(macKeychainService)
	cmdBuf.WriteString(" -a ")
	cmdBuf.WriteString(instance)
	cmdBuf.WriteString(` -l "Fased background access" -w `)
	cmdBuf.Write(hexKey)
	cmdBuf.WriteString(" -U\n")
	stdinBytes := cmdBuf.Bytes()
	defer clear(stdinBytes)

	out, _, err = macSecurityRunner(ctx, stdinBytes, "-i")
	clear(out)
	if err != nil {
		clear(key)
		return nil, ErrUnavailable
	}
	return key, nil
}
