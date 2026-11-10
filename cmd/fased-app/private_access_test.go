//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
)

// Exercise the actual status command and protected core IPC; no network device
// or test-only application binary supplies an owner session.
func TestPrivateProofStatusUsesCoreIPC(t *testing.T) {
	dir, err := os.MkdirTemp("", "fased-proof-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s, err := core.Start(t.Context(), dir, "127.0.0.1:0", "https://node.owner.ts.net:8443", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ConfigureConnection("remote"); err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve() }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Close(ctx)
	}()
	token := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("p", 32)))
	call := func(action string) core.PrivateAccessProof {
		r, w, e := os.Pipe()
		if e != nil {
			t.Fatal(e)
		}
		payload, _ := json.Marshal(core.PrivateAccessProofRequest{Action: action, Token: token})
		if _, e = w.Write(payload); e != nil {
			t.Fatal(e)
		}
		w.Close()
		defer r.Close()
		var output, diagnostic bytes.Buffer
		if e = run([]string{"status", "--data-dir", dir, "--private-proof-stdin"}, r, &output, &diagnostic); e != nil {
			t.Fatal(e, diagnostic.String())
		}
		var result core.PrivateAccessProof
		if json.Unmarshal(output.Bytes(), &result) != nil {
			t.Fatal(output.String())
		}
		return result
	}
	if v := call("begin"); v.State != "pending" || v.InstanceID == "" {
		t.Fatal(v)
	}
	if v := call("check"); v.State != "pending" || v.InstanceID == "" {
		t.Fatal(v)
	}
}
