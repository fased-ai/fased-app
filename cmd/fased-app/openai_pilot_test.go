package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestOpenAIPilotCLIClosedAndStrict(t *testing.T) {
	root := filepath.Join(".evidence", "pilot-cli")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	dir, e := os.MkdirTemp(root, "f-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := os.RemoveAll(dir); e != nil {
			t.Error(e)
		}
	})
	server, e := core.StartAgentFixture(t.Context(), dir, "127.0.0.1:0", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := server.Close(ctx); e != nil {
			t.Error(e)
		}
		if e := <-done; e != nil {
			t.Error(e)
		}
	})
	claim, e := server.Store.IssueClaimCode(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	token, e := server.Store.Claim(t.Context(), claim.Code, "disposable-pilot-cli-password")
	if e != nil {
		t.Fatal(e)
	}
	client, e := core.NewClient(t.Context(), dir)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	client.Token = token
	if e = client.SaveSession(dir); e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	if e = aiConnectionCommand([]string{"--data-dir", dir}, nil, &out, io.Discard); e != nil {
		t.Fatal(e)
	}
	var v core.AIConnectionView
	if json.Unmarshal(out.Bytes(), &v) != nil || v.PilotAvailable || v.Pilot != nil || v.LiveCallsEnabled {
		t.Fatal("CLI default activated pilot")
	}
	for _, body := range []string{
		`{"action":"pilot-review","ref":"","profileId":"trading-analyst","revision":0}`,
		`{"action":"pilot-allow","pilot":{"id":"review","unexpected":"disposable-pilot-secret"}}`,
	} {
		input, writer, e := os.Pipe()
		if e != nil {
			t.Fatal(e)
		}
		_, _ = writer.WriteString(body)
		_ = writer.Close()
		out.Reset()
		e = aiConnectionCommand([]string{"--data-dir", dir, "--request-stdin"}, input, &out, io.Discard)
		_ = input.Close()
		if e == nil || out.Len() != 0 || strings.Contains(e.Error(), "disposable-pilot-secret") {
			t.Fatal("CLI admitted or echoed untrusted pilot", e)
		}
	}
}
