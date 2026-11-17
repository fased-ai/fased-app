package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestOrdinaryCustodyRefusesFixturePasswordBeforeConfigOrSecretRead(t *testing.T) {
	for _, operation := range []string{"serve", "owner", "app"} {
		t.Run(operation, func(t *testing.T) {
			var out, errOut bytes.Buffer
			// Missing configuration and nil input make accidental reads observable.
			err := run([]string{operation, "--ordinary-custody", "--config", filepath.Join(t.TempDir(), "absent.json"), "--action", "import", "--password-stdin"}, nil, &out, &errOut)
			if err == nil || !strings.Contains(err.Error(), "fixture passwords") || out.Len() != 0 {
				t.Fatalf("fixture input crossed the owner boundary: %v %q", err, out.String())
			}
		})
	}
}
