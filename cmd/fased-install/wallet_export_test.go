package main

import (
	"io"
	"strings"
	"testing"
)

func TestEmbeddedSignerExportRefusesExtraOptions(t *testing.T) {
	for _, args := range [][]string{
		{"--emit-embedded-signer", "--output", "/not-created"},
		{"--emit-embedded-signer", "--seed-file", "/not-read"},
	} {
		if err := run(args, strings.NewReader(""), io.Discard, io.Discard); err == nil {
			t.Fatal("signer export accepted caller overrides")
		}
	}
}
