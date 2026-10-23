package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestWENSessionCommandModes(t *testing.T) {
	for _, flags := range [][]string{{}, {"--review-stdin", "--step-stdin"}, {"--create-stdin", "--profile", "wen-operator"}, {"--action-review-stdin", "--control-stdin"}, {"--step-stdin", "extra"}} {
		var out bytes.Buffer
		e := wenSessionCommand(flags, nil, &out, io.Discard)
		if e == nil || out.Len() != 0 || !strings.Contains(e.Error(), "select one") {
			t.Fatal(flags, e, out.String())
		}
	}
}
func TestWENFixtureRunCommandModes(t *testing.T) {
	for _, flags := range [][]string{{"--auto-review-stdin", "--auto-start-stdin"}, {"--auto-control-stdin", "--control-stdin"}, {"--auto-inspect", "--profile", "wen-operator", "--auto-reconcile-stdin"}, {"--auto-start-stdin", "extra"}, {"--auto-inspect"}} {
		var out bytes.Buffer
		e := wenSessionCommand(flags, nil, &out, io.Discard)
		if e == nil || out.Len() != 0 {
			t.Fatal("ambiguous automatic CLI operation", flags, e)
		}
	}
}
func TestWENFixtureRunStartupIsolation(t *testing.T) {
	for _, args := range [][]string{{"status", "--session-fixture"}, {"serve", "--session-fixture", "--ai-fixture"}, {"serve", "--session-fixture", "--custody-config", "/never-open-fixture"}, {"serve", "--session-fixture", "--require-sandbox"}, {"serve", "--session-fixture", "--public-origin", "https://example.invalid"}, {"serve", "--session-fixture", "--wen-research-origin", "https://example.invalid"}} {
		var out bytes.Buffer
		e := run(args, nil, &out, io.Discard)
		if e == nil || out.Len() != 0 || !strings.Contains(e.Error(), "standalone") {
			t.Fatal("fixture startup acquired another capability", args, e)
		}
	}
}
func TestWENSessionCommandStrictShapes(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"schema":"wen-session-step/1","profile":"wen-operator","revision":null,"poolRevision":0,"sessionId":"x","eventId":"slot:0"}`, `{"schema":"x","profile":"p","revision":0,"revision":1,"poolRevision":0,"sessionId":"x","eventId":"e"}`, `{"schema":"x","Profile":"p","revision":0,"poolRevision":0,"sessionId":"x","eventId":"e"}`} {
		var v core.WENSessionStep
		if core.DecodeWENSession([]byte(raw), &v) == nil {
			t.Fatal("unsafe complete command", raw)
		}
	}
}
