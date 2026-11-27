package custody

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fased-ai/fased-app/internal/providersecrets"
	"github.com/fased-ai/fased-app/internal/vault"
)

const (
	sealedMasterKeyName = "signer-master.key.sealed"
	systemdKEKCredName  = "fased-signer-kek"
	masterKeyWrapAAD    = "fased-signer-master-kek:v1"
	KEKSystemdCreds     = "systemd-creds"
	KEKOSKeychain       = "os-keychain"
	KEKFileDevOnly      = "file-dev-only"
)

type sealedMasterKeyEnvelope struct {
	Version       int    `json:"version"`
	KEKProtection string `json:"kekProtection"`
	Nonce         []byte `json:"nonce"`
	Ciphertext    []byte `json:"ciphertext"`
}

// custodyKEKInstanceID derives a canonical, path-separator-free identifier safe for
// Linux Secret Service, macOS Keychain, and Windows DPAPI.
func custodyKEKInstanceID(dir string) string {
	sum := sha256.Sum256([]byte("fased-signer-kek-instance:v1\x00" + filepath.Clean(dir)))
	return "fased-signer-" + hex.EncodeToString(sum[:16])
}

func allowedCredDir(credDir string) bool {
	if !filepath.IsAbs(credDir) || filepath.Clean(credDir) != credDir {
		return false
	}
	if strings.HasPrefix(credDir, "/run/credentials/") {
		return true
	}
	if runtime.GOOS == "linux" {
		userPrefix := fmt.Sprintf("/run/user/%d/credentials/", os.Geteuid())
		if strings.HasPrefix(credDir, userPrefix) {
			return true
		}
	}
	return false
}

func resolveSigningKEK(ctx context.Context, dir string, cfg Config) ([]byte, string, error) {
	if credDir := strings.TrimSpace(os.Getenv("CREDENTIALS_DIRECTORY")); credDir != "" {
		if !allowedCredDir(credDir) {
			return nil, "", errors.New("CREDENTIALS_DIRECTORY outside trusted systemd credential path")
		}
		credPath := filepath.Join(credDir, systemdKEKCredName)
		raw, err := readCredentialRegularFile(credPath, 4096)
		if err != nil || len(raw) == 0 {
			if cfg.RequireOSKEK {
				return nil, "", fmt.Errorf("systemd credential %s unreadable: %w", systemdKEKCredName, err)
			}
			// Fail closed whenever CREDENTIALS_DIRECTORY is explicitly populated.
			return nil, "", fmt.Errorf("systemd credential %s unavailable: %w", systemdKEKCredName, err)
		}
		defer clear(raw)
		h := sha256.New()
		h.Write([]byte("fased-signer-kek:systemd-v1\x00"))
		h.Write(raw)
		kek := h.Sum(nil)
		return kek, KEKSystemdCreds, nil
	}

	if !cfg.FixtureMode || cfg.KEKSource != nil {
		src := cfg.KEKSource
		if src == nil {
			src = providersecrets.Native{}
		}
		instID := custodyKEKInstanceID(dir)
		// On Linux, if a legacy sealed vault exists created under the raw path key, check it read-only first
		// so existing local installations keep unlocking without disruption.
		if runtime.GOOS == "linux" && cfg.KEKSource == nil {
			if _, statErr := os.Stat(filepath.Join(dir, sealedMasterKeyName)); statErr == nil {
				if legacyKey, lErr := src.Key(ctx, dir, false); lErr == nil && len(legacyKey) == masterKeyBytes {
					return legacyKey, KEKOSKeychain, nil
				}
			}
		}
		if key, err := src.Key(ctx, instID, true); err == nil && len(key) == masterKeyBytes {
			return key, KEKOSKeychain, nil
		} else if err != nil && cfg.RequireOSKEK {
			return nil, "", fmt.Errorf("protected KEK required but unavailable: %w", err)
		}
	}

	if cfg.RequireOSKEK {
		return nil, "", errors.New("protected KEK required (systemd-creds or OS keychain unavailable)")
	}
	return nil, KEKFileDevOnly, nil
}

func readCredentialRegularFile(path string, maxBytes int64) ([]byte, error) {
	f, err := openNoFollowReadOnly(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("credential must be a regular non-symlink file")
	}
	if info.Size() <= 0 || info.Size() > maxBytes {
		return nil, errors.New("credential file empty or oversized")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o037 != 0 {
		return nil, errors.New("credential file permissions too permissive")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil || int64(len(raw)) > maxBytes {
		clear(raw)
		return nil, errors.New("credential read failed or oversized")
	}
	return raw, nil
}

func writePrivateAtomicFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".fsk-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if df, dErr := os.Open(dir); dErr == nil {
		_ = df.Sync()
		_ = df.Close()
	}
	cleanup = false
	return nil
}

func sealMasterKeyEnvelope(sealedPath, protection string, kek, masterKey []byte) error {
	nonce, err := vault.RandomBytes(12)
	if err != nil {
		return err
	}
	ct, err := vault.SealAESGCM(kek, nonce, masterKey, []byte(masterKeyWrapAAD))
	if err != nil {
		return err
	}
	env := sealedMasterKeyEnvelope{
		Version:       1,
		KEKProtection: protection,
		Nonce:         nonce,
		Ciphertext:    ct,
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return writePrivateAtomicFile(sealedPath, append(raw, '\n'))
}

func loadOrCreateMasterKeyWithKEK(ctx context.Context, dir string, cfg Config) ([]byte, string, error) {
	kek, protection, err := resolveSigningKEK(ctx, dir, cfg)
	if err != nil {
		return nil, "", err
	}
	defer func() {
		if kek != nil {
			clear(kek)
		}
	}()

	sealedPath := filepath.Join(dir, sealedMasterKeyName)
	legacyPath := filepath.Join(dir, masterKeyName)

	if rawSealed, sErr := os.ReadFile(sealedPath); sErr == nil {
		var env sealedMasterKeyEnvelope
		if jErr := json.Unmarshal(rawSealed, &env); jErr != nil || env.Version != 1 || len(env.Nonce) != 12 {
			return nil, "", errors.New("corrupt sealed signer master key")
		}
		if kek == nil {
			return nil, "", fmt.Errorf("signer master key is sealed by %s, but KEK is unavailable", firstStr(env.KEKProtection, "protected-kek"))
		}
		masterKey, oErr := vault.OpenAESGCM(kek, env.Nonce, env.Ciphertext, []byte(masterKeyWrapAAD))
		if oErr != nil || len(masterKey) != masterKeyBytes {
			return nil, "", errors.New("failed to unwrap sealed signer master key with current KEK")
		}
		_ = os.Chmod(sealedPath, 0o600)
		return masterKey, protection, nil
	} else if !errors.Is(sErr, os.ErrNotExist) {
		return nil, "", sErr
	}

	// Check for unsealed dev key (either reuse as dev-only or upgrade into sealed envelope if KEK is present).
	if rawLegacy, lErr := os.ReadFile(legacyPath); lErr == nil {
		if len(rawLegacy) != masterKeyBytes {
			return nil, "", errors.New("corrupt signer master key")
		}
		_ = os.Chmod(legacyPath, 0o600)
		if kek != nil {
			if sealErr := sealMasterKeyEnvelope(sealedPath, protection, kek, rawLegacy); sealErr != nil {
				clear(rawLegacy)
				return nil, "", sealErr
			}
			_ = os.Remove(legacyPath)
			return rawLegacy, protection, nil
		}
		return rawLegacy, KEKFileDevOnly, nil
	} else if !errors.Is(lErr, os.ErrNotExist) {
		return nil, "", lErr
	}

	// Brand-new vault master key.
	masterKey, err := vault.RandomBytes(masterKeyBytes)
	if err != nil {
		return nil, "", err
	}
	if kek != nil {
		if sealErr := sealMasterKeyEnvelope(sealedPath, protection, kek, masterKey); sealErr != nil {
			clear(masterKey)
			return nil, "", sealErr
		}
		return masterKey, protection, nil
	}
	f, err := os.OpenFile(legacyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		clear(masterKey)
		return nil, "", err
	}
	if _, err := f.Write(masterKey); err != nil {
		_ = f.Close()
		_ = os.Remove(legacyPath)
		clear(masterKey)
		return nil, "", err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(legacyPath)
		clear(masterKey)
		return nil, "", err
	}
	_ = f.Close()
	return masterKey, KEKFileDevOnly, nil
}
