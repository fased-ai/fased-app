//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"testing"

	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestCurrentInstallationPreservesConfirmedAbsentHold(t *testing.T) {
	got, err := currentInstallation(t.Context(), InstallTransaction{}, nil, func(context.Context) (*update.Installation, error) {
		return nil, signer.ErrNoInstallationHold
	})
	if got != nil || !errors.Is(err, signer.ErrNoInstallationHold) {
		t.Fatalf("authenticated absence was changed: %v, %v", got, err)
	}
}
