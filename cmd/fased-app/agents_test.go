package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/core"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAgentAIRequestDraftCLIArguments(t *testing.T) {
	for _, args := range [][]string{
		{"--ai-draft", "id"}, {"--profile", "trading-analyst", "--ai-draft", ""},
		{"--profile", "trading-analyst", "--ai-draft", "id", "--search", ""},
		{"--profile", "trading-analyst", "--ai-draft", "id", "--memory-search", ""},
		{"--profile", "trading-analyst", "--ai-draft", "id", "--memory-source", "note"},
		{"--profile", "trading-analyst", "--ai-draft", "id", "--request-stdin"},
		{"--profile", "trading-analyst", "--ai-draft", "id", "extra"},
	} {
		var out bytes.Buffer
		err := agentsCommand(args, nil, &out, io.Discard)
		if err == nil || out.Len() != 0 || strings.Contains(err.Error(), "core is not running") {
			t.Fatal("draft accepted conflicting/missing flags", args, err)
		}
	}
}

func TestAgentAIRequestDraftCLIPrivateRoundTrip(t *testing.T) {
	root := filepath.Join(".evidence", "agent-draft-cli")
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
	token, err := server.Store.Claim(t.Context(), claim.Code, "disposable-cli-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	v, err := server.Store.Agents(t.Context(), "trading-analyst", "")
	if err != nil {
		t.Fatal(err)
	}
	v, err = server.Store.ChangeAgents(t.Context(), core.AgentCommand{Action: "session-create", ProfileID: v.ProfileID, Revision: v.Revision, Name: "CLI draft"})
	if err != nil {
		t.Fatal(err)
	}
	v, err = server.Store.ChangeAgents(t.Context(), core.AgentCommand{Action: "ai-prepare", ProfileID: v.ProfileID, Revision: v.Revision, ID: v.Sessions[0].ID, Text: "CLI saved question", MemorySelection: &core.AIMemorySelection{Mode: "none"}, Limits: &core.AILimits{MaxInputTokens: 24000, MaxOutputTokens: 256, DeadlineSeconds: 3, BudgetMicrousd: 30000}})
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--data-dir", dir, "--profile", v.ProfileID, "--ai-draft", v.AIRequests[0].ID}
	var out bytes.Buffer
	if err := agentsCommand(args, nil, &out, io.Discard); err == nil || out.Len() != 0 {
		t.Fatal("unauthenticated draft read")
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
	if err = agentsCommand(args, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var got core.AgentAIRequestDraft
	if err = json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want, err := server.Store.AgentAIRequestDraft(t.Context(), v.ProfileID, v.AIRequests[0].ID)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("CLI differs from shared projection", got, err)
	}
	after, err := server.Store.Agents(t.Context(), v.ProfileID, "")
	if err != nil || !reflect.DeepEqual(after, v) {
		t.Fatal("CLI read wrote state", err)
	}
}
