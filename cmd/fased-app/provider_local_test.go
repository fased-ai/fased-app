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

func TestLocalProviderCLIPrivateRoundTrip(t *testing.T) {
	for preset, model := range map[string]string{"ollama": "gemma4:e2b", "lmstudio": "google/gemma-4-e2b", "vllm": "google/gemma-4-E2B-it"} {
		t.Run(preset, func(t *testing.T) { localProviderCLIPrivateRoundTrip(t, preset, model) })
	}
}
func localProviderCLIPrivateRoundTrip(t *testing.T, preset, expectedModel string) {
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

	connectionRaw, _ := json.Marshal(core.AIConnectionCommand{Action: "add", Provider: "local", Server: preset, Passphrase: "disposable-agent-vault-password"})
	connectionIn, connectionOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer connectionIn.Close()
	go func() { _, _ = connectionOut.Write(connectionRaw); _ = connectionOut.Close() }()
	var connectionResult bytes.Buffer
	if err = aiConnectionCommand([]string{"--data-dir", dir, "--request-stdin"}, connectionIn, &connectionResult, io.Discard); err != nil {
		t.Fatal(err)
	}
	var account core.AIConnectionView
	if err = json.Unmarshal(connectionResult.Bytes(), &account); err != nil || account.Provider != "local" || account.Locked || account.LiveCallsEnabled {
		t.Fatal("CLI account setup", err)
	}
	model := ""
	for _, m := range account.Catalog.Models {
		if m.Selectable && m.ID == expectedModel {
			model = m.ID
		}
	}
	if model == "" {
		t.Fatal("named server model missing")
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
