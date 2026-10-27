package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestConfiguredAgentCLIPrivateRoundTrip(t *testing.T) {
	root := filepath.Join(".evidence", "connected-agent-cli")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "f-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	server, err := core.StartAgentFixture(t.Context(), dir, "127.0.0.1:0", "", nil)
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
	token, err := server.Store.Claim(t.Context(), claim.Code, "disposable-agent-cli-password")
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
	account, err := server.Store.ChangeAIConnection(t.Context(), core.AIConnectionCommand{Action: "save", Key: "fased-test-disposable-agent-cli-key", Passphrase: "disposable-agent-vault-password"})
	if err != nil {
		t.Fatal(err)
	}
	model := ""
	for _, m := range account.Catalog.Models {
		if m.Selectable && m.Provider == account.Provider {
			model = m.ID
			break
		}
	}
	if model == "" {
		t.Fatal("fixture account has no current model")
	}
	v, err := server.Store.Agents(t.Context(), "trading-analyst", "")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(core.AgentCommand{Action: "profile-create", Revision: v.Revision, Name: "CLI analyst", Text: "Keep sources", Provider: account.Provider, AccountRef: &account.Ref, Model: model})
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	go func() { _, _ = w.Write(raw); _ = w.Close() }()
	var out bytes.Buffer
	if err = agentsCommand([]string{"--data-dir", dir, "--request-stdin"}, r, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var created core.AgentView
	if err = json.Unmarshal(out.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range created.Profiles {
		if p.ID == created.ProfileID {
			found = p.Name == "CLI analyst" && p.Persona == "Keep sources" && p.ConnectionRef == account.Ref && p.Provider == account.Provider && p.Model == model
		}
	}
	if !found || len(created.Profiles) != len(v.Profiles)+1 || len(created.AIRequests) != 0 || len(created.Tasks) != 0 || len(created.Sessions) != 0 {
		t.Fatal("CLI did not create one configured agent without execution")
	}
}
