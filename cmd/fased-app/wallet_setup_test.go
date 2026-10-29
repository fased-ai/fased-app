package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestWalletSetupCLIUsesSavedSessionAndRejectsSecrets(t *testing.T) {
	root := filepath.Join(".evidence", "wallet-setup-cli")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "f-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	server, err := core.Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Close(ctx); err != nil {
			t.Error(err)
		}
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	claim, err := server.Store.IssueClaimCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	token, err := server.Store.Claim(t.Context(), claim.Code, "disposable-wallet-cli-password")
	if err != nil {
		t.Fatal(err)
	}
	client, err := core.NewClient(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.Token = token
	if err = client.SaveSession(dir); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ body, want string }{
		{`{"revision":0,"action":"watch","name":"Watch","network":"devnet","address":"11111111111111111111111111111111"}`, "network account setup is disabled"},
		{`{"revision":0,"action":"watch","name":"Watch","network":"devnet","address":"11111111111111111111111111111111","seed":"refused"}`, "no key or password fields"},
	} {
		file := filepath.Join(dir, "public-request.json")
		if err = os.WriteFile(file, []byte(tc.body), 0600); err != nil {
			t.Fatal(err)
		}
		in, err := os.Open(file)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		err = accountsCommand([]string{"--data-dir", dir, "--setup-stdin"}, in, &out, io.Discard)
		in.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) || out.Len() != 0 {
			t.Fatal("CLI setup session/boundary", err)
		}
	}
}
