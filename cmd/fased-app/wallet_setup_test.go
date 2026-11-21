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

func TestWalletSetupCLIUsesSavedSessionAndRejectsSecrets(t *testing.T) {
	dir := shortFixtureDir(t, "fa-")
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
	validFile := filepath.Join(dir, "public-request.json")
	if err = os.WriteFile(validFile, []byte(`{"revision":0,"action":"watch","name":"Watch","network":"devnet","address":"11111111111111111111111111111111"}`), 0600); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(validFile)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = accountsCommand([]string{"--data-dir", dir, "--setup-stdin"}, in, &out, io.Discard)
	in.Close()
	if err != nil {
		t.Fatal("CLI setup valid watch account", err)
	}
	var view core.AccountsView
	if err = json.Unmarshal(out.Bytes(), &view); err != nil {
		t.Fatal("decode CLI watch setup view", err)
	}
	if view.Record.Revision != 1 || len(view.Record.Accounts) != 1 || view.Record.Accounts[0].Mode != "watch" || view.Record.Accounts[0].Network != "devnet" || view.Record.Accounts[0].PublicKey != strings.Repeat("0", 64) {
		t.Fatalf("unexpected CLI setup view: %+v", view.Record)
	}

	secretFile := filepath.Join(dir, "secret-request.json")
	if err = os.WriteFile(secretFile, []byte(`{"revision":1,"action":"watch","name":"Watch","network":"devnet","address":"11111111111111111111111111111111","seed":"refused"}`), 0600); err != nil {
		t.Fatal(err)
	}
	in, err = os.Open(secretFile)
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err = accountsCommand([]string{"--data-dir", dir, "--setup-stdin"}, in, &out, io.Discard)
	in.Close()
	if err == nil || !strings.Contains(err.Error(), "no key or password fields") || out.Len() != 0 {
		t.Fatal("CLI setup secret boundary", err)
	}
}
