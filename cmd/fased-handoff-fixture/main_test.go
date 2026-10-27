package main

import (
	"context"
	"testing"
)

func TestFixtureCommandRejectsExecution(t *testing.T) {
	for _, args := range [][]string{nil, {"owner", "inspect"}, {"install", "run", "--disposable-fixture"}, {"signer", "serve", "--real-wallet"}, {"owner", "inspect", "--disposable-fixture", "--config", "/missing", "--config-sha256", "x"}} {
		if run(context.Background(), args) == nil {
			t.Fatalf("unsafe args accepted %q", args)
		}
	}
}
