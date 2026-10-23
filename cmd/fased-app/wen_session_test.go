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
func TestWENSessionCommandStrictShapes(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"schema":"wen-session-step/1","profile":"wen-operator","revision":null,"poolRevision":0,"sessionId":"x","eventId":"slot:0"}`, `{"schema":"x","profile":"p","revision":0,"revision":1,"poolRevision":0,"sessionId":"x","eventId":"e"}`, `{"schema":"x","Profile":"p","revision":0,"poolRevision":0,"sessionId":"x","eventId":"e"}`} {
		var v core.WENSessionStep
		if core.DecodeWENSession([]byte(raw), &v) == nil {
			t.Fatal("unsafe complete command", raw)
		}
	}
}
