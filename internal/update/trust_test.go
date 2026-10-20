package update

import (
	"context"
	"strings"
	"testing"
)

func TestTrustRehearsalRequiresExplicitFixtureTrust(t *testing.T) {
	for _, o := range []TrustRehearsalOptions{{}, {Development: true}, {Development: true, Initial: true, Seed: []byte(`{}`), SeedSHA256: strings.Repeat("0", 64), Now: 1800000000}} {
		if _, err := RehearseTrust(context.Background(), o); err == nil {
			t.Fatal("implicit or unpinned trust accepted")
		}
	}
}
