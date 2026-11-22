package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestAccountsPortableCLIAndRemoteBoundary(t *testing.T) {
	var paths []string
	transport := accountsRemoteRoundTrip(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/portable":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"version":1,"exportedAt":100,"wallets":[{"name":"Devnet","network":"devnet","address":"11111111111111111111111111111111"}]}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		default:
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(`{"error":"forbidden"}`))}, nil
		}
	})
	dir := t.TempDir()
	prior := accountsRemoteClient
	accountsRemoteClient = func(_ context.Context, _ string) (*core.Client, error) {
		return &core.Client{
			Endpoint: core.Endpoint{URL: "https://fixture.invalid", InstanceID: "fixture"},
			HTTP:     &http.Client{Transport: transport},
			Token:    "fixture-device-session",
			CSRF:     "fixture-csrf",
		}, nil
	}
	defer func() { accountsRemoteClient = prior }()

	var out bytes.Buffer
	if err := accountsCommand([]string{"--remote", "--data-dir", dir, "--export-portable"}, nil, &out, io.Discard); err != nil {
		t.Fatalf("remote --export-portable failed: %v", err)
	}
	var exported core.PortableWalletsBundle
	if err := json.Unmarshal(out.Bytes(), &exported); err != nil || exported.Version != 1 || len(exported.Wallets) != 1 {
		t.Fatalf("unexpected exported bundle: %s err=%v", out.String(), err)
	}
	if len(paths) != 1 || paths[0] != "GET /api/v1/accounts/portable" {
		t.Fatalf("unexpected remote paths: %v", paths)
	}

	// Remote mutations remain strictly blocked.
	inPath := filepath.Join(dir, "import.json")
	if err := os.WriteFile(inPath, []byte(`{"revision":0,"bundle":{"version":1,"exportedAt":100,"wallets":[]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(inPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := accountsCommand([]string{"--remote", "--data-dir", dir, "--import-portable-stdin"}, f, &out, io.Discard); err == nil || len(paths) != 1 {
		t.Fatalf("remote --import-portable-stdin must remain blocked, err=%v", err)
	}
	if err := accountsCommand([]string{"--remote", "--data-dir", dir, "--venue-credential-stdin", "--venue-backup-out", filepath.Join(dir, "v.json")}, f, &out, io.Discard); err == nil || len(paths) != 1 {
		t.Fatalf("remote venue backup must remain blocked, err=%v", err)
	}
	if err := accountsCommand([]string{"--data-dir", dir, "--venue-backup-out", filepath.Join(dir, "v.json")}, nil, &out, io.Discard); err == nil {
		t.Fatal("--venue-backup-out without --venue-credential-stdin must fail")
	}
}
