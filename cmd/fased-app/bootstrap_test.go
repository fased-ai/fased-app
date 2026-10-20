//go:build linux

package main

import (
	"io"
	"testing"
)

func TestBootstrapRequiresExplicitProtectedInputs(t *testing.T) {
	for _, args := range [][]string{{"custody", "bootstrap"}, {"owner-bootstrap"}, {"custody", "bootstrap", "--disposable-fixture", "--prepare-host", "--intent-id", "invalid"}} {
		if e := run(args, nil, io.Discard, io.Discard); e == nil {
			t.Fatal("unprotected bootstrap accepted", args)
		}
	}
}
