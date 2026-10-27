package main

import (
	"io"
	"testing"
)

func TestInitialInstallerHasNoPublicOrImplicitExecutionDefaults(t *testing.T) {
	for _, args := range [][]string{nil, {"--install"}, {"--development"}, {"--development", "--seed-file", "absent", "--seed-sha256", "test", "--output", "/absent", "--prefix", "/app"}} {
		if err := run(args, nil, io.Discard, io.Discard); err == nil {
			t.Fatal("public/implicit bootstrap accepted", args)
		}
	}
}
