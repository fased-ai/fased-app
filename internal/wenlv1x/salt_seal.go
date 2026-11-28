package wenlv1x

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/fased-ai/fased-app/internal/providersecrets"
)

const sealedSaltPrefix = "FASED-SEALED-V1:"

func saltSealAAD(walletID, cluster string, slot, nonce uint64) []byte {
	return []byte(fmt.Sprintf("lv1x-salt:%s:%s:%d:%d", strings.TrimSpace(walletID), normalizeCluster(cluster), slot, nonce))
}

func (e *Engine) sealSalt(walletID, cluster string, slot, nonce uint64, saltHex string) (string, error) {
	clean := strings.ToLower(strings.TrimSpace(saltHex))
	if clean == "" {
		return "", nil
	}
	raw, err := hex.DecodeString(clean)
	if err != nil || len(raw) != 32 {
		return "", errors.New("saltHex must be 64-char hex")
	}
	if len(e.saltKey) != 32 {
		return "", errors.New("salt encryption key unavailable")
	}
	gcmNonce, ct, err := providersecrets.Seal(e.saltKey, raw, saltSealAAD(walletID, cluster, slot, nonce))
	clear(raw)
	if err != nil {
		return "", err
	}
	combined := make([]byte, 0, len(gcmNonce)+len(ct))
	combined = append(combined, gcmNonce...)
	combined = append(combined, ct...)
	return sealedSaltPrefix + base64.StdEncoding.EncodeToString(combined), nil
}

func (e *Engine) openSalt(walletID, cluster string, slot, nonce uint64, stored string) (string, error) {
	trimmed := strings.TrimSpace(stored)
	if trimmed == "" {
		return "", nil
	}
	if !strings.HasPrefix(trimmed, sealedSaltPrefix) {
		raw, err := hex.DecodeString(trimmed)
		if err != nil || len(raw) != 32 {
			return "", errors.New("corrupt unsealed commit salt")
		}
		return strings.ToLower(trimmed), nil
	}
	if len(e.saltKey) != 32 {
		return "", errors.New("salt decryption key unavailable")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(trimmed, sealedSaltPrefix))
	if err != nil || len(decoded) <= 12 {
		return "", errors.New("corrupt sealed salt envelope")
	}
	plain, err := providersecrets.Open(e.saltKey, decoded[:12], decoded[12:], saltSealAAD(walletID, cluster, slot, nonce))
	if err != nil {
		return "", fmt.Errorf("failed to unseal commit salt: %w", err)
	}
	defer clear(plain)
	if len(plain) != 32 {
		return "", errors.New("invalid unsealed salt length")
	}
	return hex.EncodeToString(plain), nil
}
