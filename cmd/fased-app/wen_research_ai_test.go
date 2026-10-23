package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestWENResearchAICommandModes(t *testing.T) {
	for _, flags := range [][]string{
		{"--ai-prepare-stdin", "--ai-inspect-stdin"},
		{"--ai-start-stdin", "--ai-inspect-stdin"},
		{"--ai-start-stdin", "--profile", "wen-operator"},
		{"--ai-cancel-stdin", "--read-stdin"},
		{"--ai-inspect-stdin", "--profile", "wen-operator"},
		{"--ai-prepare-stdin", "extra"},
	} {
		var out bytes.Buffer
		err := wenResearchCommand(flags, nil, &out, io.Discard)
		if err == nil || out.Len() != 0 || !strings.Contains(err.Error(), "select exactly one") {
			t.Fatal(flags, err, out.String())
		}
	}
}
