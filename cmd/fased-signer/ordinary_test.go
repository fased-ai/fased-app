package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"github.com/fased-ai/fased-app/internal/signer"
)

func TestOrdinaryCustodyHeldBeforeConfigOrSecretRead(t *testing.T) {
	for _, operation := range []string{"serve", "owner", "app"} {
		t.Run(operation, func(t *testing.T) {
			var out, errOut bytes.Buffer
			// Missing configuration and nil input make accidental reads observable.
			err := run([]string{operation, "--ordinary-custody", "--config", filepath.Join(t.TempDir(), "absent.json"), "--action", "import", "--password-stdin"}, nil, &out, &errOut)
			if !errors.Is(err, signer.ErrUnavailable) || out.Len() != 0 {
				t.Fatalf("ordinary startup crossed the admission gate: %v %q", err, out.String())
			}
		})
	}
}
