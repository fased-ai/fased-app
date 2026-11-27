package providersecrets

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	sealedKeyMagic = "FASED-SEALED-KEY:v1\n"
	sealNonceSize  = 12
)

// DeriveSubkey derives a deterministic 32-byte domain-separated subkey from a 32-byte master key
// using RFC 5869 HKDF-SHA256 (extract + single-block expand).
func DeriveSubkey(master []byte, purpose string) ([]byte, error) {
	if len(master) != 32 {
		return nil, ErrUnavailable
	}
	info := strings.TrimSpace(purpose)
	if info == "" {
		return nil, errors.New("HKDF purpose required")
	}
	// HKDF-Extract(salt="fased-providersecrets-hkdf:v1", IKM=master)
	ext := hmac.New(sha256.New, []byte("fased-providersecrets-hkdf:v1"))
	ext.Write(master)
	prk := ext.Sum(nil)
	defer clear(prk)

	// HKDF-Expand(PRK, info, 32) => T(1) = HMAC(PRK, info || 0x01)
	exp := hmac.New(sha256.New, prk)
	exp.Write([]byte(info))
	exp.Write([]byte{0x01})
	out := exp.Sum(nil)
	return out, nil
}

func resolveDerivedSubkey(ctx context.Context, src Source, purpose string) []byte {
	if src == nil {
		return nil
	}
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
	}
	master, err := src.Key(ctx, CredentialName, true)
	if err != nil || len(master) != 32 {
		if master != nil {
			clear(master)
		}
		return nil
	}
	defer clear(master)
	sub, err := DeriveSubkey(master, purpose)
	if err != nil {
		return nil
	}
	return sub
}

func sealKeyEnvelope(kek, rawKey []byte, purpose string) ([]byte, error) {
	nonce, ct, err := Seal(kek, rawKey, []byte(purpose))
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(sealedKeyMagic)+len(nonce)+len(ct))
	out = append(out, sealedKeyMagic...)
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}

func writeKeyAtomic(keyPath string, data []byte) error {
	dir := filepath.Dir(keyPath)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(keyPath)+".tmp.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, keyPath); err != nil {
		return err
	}
	_ = os.Chmod(keyPath, 0o600)
	return nil
}

// LoadOrCreateSealedKey loads or initializes a 32-byte data encryption key at keyPath,
// sealing it under an HKDF-SHA256 subkey derived from src (fased-provider-task-key) whenever
// an OS credential store or systemd-creds key is available. Legacy 32-byte plaintext keys are
// transparently migrated to the sealed envelope format in place.
func LoadOrCreateSealedKey(ctx context.Context, src Source, keyPath, purpose string, requireOS bool) ([]byte, string, error) {
	if strings.TrimSpace(keyPath) == "" || strings.TrimSpace(purpose) == "" {
		return nil, "", errors.New("key path and purpose required")
	}
	kek := resolveDerivedSubkey(ctx, src, purpose)
	defer func() {
		if kek != nil {
			clear(kek)
		}
	}()

	raw, err := os.ReadFile(keyPath)
	if err == nil {
		defer clear(raw)
		if bytes.HasPrefix(raw, []byte(sealedKeyMagic)) {
			if kek == nil {
				return nil, "", fmt.Errorf("%w: sealed key %s requires OS credential store", ErrUnavailable, filepath.Base(keyPath))
			}
			body := raw[len(sealedKeyMagic):]
			if len(body) <= sealNonceSize {
				return nil, "", fmt.Errorf("corrupted sealed key %s", filepath.Base(keyPath))
			}
			plain, openErr := Open(kek, body[:sealNonceSize], body[sealNonceSize:], []byte(purpose))
			if openErr != nil {
				return nil, "", fmt.Errorf("open sealed key %s: %w", filepath.Base(keyPath), openErr)
			}
			if len(plain) != 32 {
				clear(plain)
				return nil, "", fmt.Errorf("invalid sealed key length in %s", filepath.Base(keyPath))
			}
			return plain, "os_hkdf", nil
		}
		if len(raw) == 32 {
			key := make([]byte, 32)
			copy(key, raw)
			if kek != nil {
				sealed, sealErr := sealKeyEnvelope(kek, key, purpose)
				if sealErr != nil {
					clear(key)
					return nil, "", sealErr
				}
				if wErr := writeKeyAtomic(keyPath, sealed); wErr != nil {
					clear(key)
					return nil, "", wErr
				}
				return key, "os_hkdf", nil
			}
			if requireOS {
				clear(key)
				return nil, "", fmt.Errorf("%w: OS credential store required for %s", ErrUnavailable, filepath.Base(keyPath))
			}
			return key, "file_0600", nil
		}
		return nil, "", fmt.Errorf("corrupted key length in %s", filepath.Base(keyPath))
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}

	if kek == nil && requireOS {
		return nil, "", fmt.Errorf("%w: OS credential store required to create %s", ErrUnavailable, filepath.Base(keyPath))
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, "", err
	}
	payload := key
	mode := "file_0600"
	if kek != nil {
		sealed, sealErr := sealKeyEnvelope(kek, key, purpose)
		if sealErr != nil {
			clear(key)
			return nil, "", sealErr
		}
		payload = sealed
		mode = "os_hkdf"
	}

	// Atomically create without clobbering a concurrent writer.
	f, openErr := os.OpenFile(keyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if openErr == nil {
		_, writeErr := f.Write(payload)
		closeErr := f.Close()
		if writeErr != nil {
			_ = os.Remove(keyPath)
			clear(key)
			return nil, "", writeErr
		}
		if closeErr != nil {
			clear(key)
			return nil, "", closeErr
		}
		return key, mode, nil
	}
	clear(key)
	if errors.Is(openErr, os.ErrExist) {
		for attempt := 0; attempt < 5; attempt++ {
			loaded, loadedMode, lErr := LoadOrCreateSealedKey(ctx, src, keyPath, purpose, requireOS)
			if lErr == nil {
				return loaded, loadedMode, nil
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	return nil, "", openErr
}
