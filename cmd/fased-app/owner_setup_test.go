package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/core"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOwnerSetupLinkPreservesClaimedWorkspace(t *testing.T) {
	dir, err := os.MkdirTemp("", "fsl-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	server, err := core.Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve() }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Close(ctx)
	}()
	var output bytes.Buffer
	if err = ownerSetupLink(t.Context(), dir, "", &output); err != nil {
		t.Fatal(err)
	}
	link, err := url.Parse(strings.TrimSpace(output.String()))
	if err != nil || link.Query().Get("instance") == "" || !strings.HasPrefix(link.Fragment, "setup=") {
		t.Fatal("missing bound handoff", err)
	}
	var before, after core.ClaimCode
	data, _ := core.ReadPrivateFile(filepath.Join(dir, "claim-code.json"))
	_ = json.Unmarshal(data, &before)
	if err = ownerSetupLink(t.Context(), dir, "http://public.invalid", &output); err == nil {
		t.Fatal("unsafe origin accepted")
	}
	data, _ = core.ReadPrivateFile(filepath.Join(dir, "claim-code.json"))
	_ = json.Unmarshal(data, &after)
	if before != after {
		t.Fatal("invalid destination rotated proof")
	}
	session, err := server.Store.Claim(t.Context(), strings.TrimPrefix(link.Fragment, "setup="), "disposable-setup-test-password")
	if err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err = ownerSetupLink(t.Context(), dir, "", &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "#setup=") || !server.Store.Session(t.Context(), session) {
		t.Fatal("reopen reset an existing owner")
	}
}
