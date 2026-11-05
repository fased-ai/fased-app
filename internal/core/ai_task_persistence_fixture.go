package core

import (
	"context"
	"crypto/sha256"
	"github.com/fased-ai/fased-app/internal/providersecrets"
	"runtime"
)

// Used only by the existing explicit standalone --ai-fixture constructor.
// Ordinary services never select this source or accept its synthetic accounts.
type fixtureTaskSecrets struct{}

func (fixtureTaskSecrets) Key(_ context.Context, instance string, _ bool) ([]byte, error) {
	if runtime.GOOS != "linux" {
		return nil, providersecrets.ErrUnavailable
	}
	key := sha256.Sum256([]byte("fased-disposable-task-secret-v1:" + instance))
	return append([]byte(nil), key[:]...), nil
}
