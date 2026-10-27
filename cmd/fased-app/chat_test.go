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

func TestChatAllowanceCLIPrivateRoundTrip(t *testing.T) {
	root := filepath.Join(".evidence", "chat-cli")
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
	token, e := server.Store.Claim(t.Context(), claim.Code, "disposable-cli-chat-password")
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
	v, e := server.Store.Agents(t.Context(), "trading-analyst", "")
	if e != nil {
		t.Fatal(e)
	}
	change := func(c core.AgentCommand) core.AgentView {
		t.Helper()
		c.Revision = v.Revision
		c.ProfileID = v.ProfileID
		raw, _ := json.Marshal(c)
		r, w, e := os.Pipe()
		if e != nil {
			t.Fatal(e)
		}
		go func() { _, _ = w.Write(raw); _ = w.Close() }()
		defer r.Close()
		var out bytes.Buffer
		if e = agentsCommand([]string{"--data-dir", dir, "--request-stdin"}, r, &out, io.Discard); e != nil {
			t.Fatal(e)
		}
		var next core.AgentView
		if e = json.Unmarshal(out.Bytes(), &next); e != nil {
			t.Fatal(e)
		}
		v = next
		return next
	}
	v = change(core.AgentCommand{Action: "session-create", Name: "CLI bounded Chat"})
	id := v.Sessions[0].ID
	v = change(core.AgentCommand{Action: "chat-review", ID: id, MemorySelection: &core.AIMemorySelection{Mode: "none"}, Limits: &core.AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 5, BudgetMicrousd: 30000}, MaxRuns: 2, AuthorizationSeconds: 900, TotalBudgetMicrousd: 60000})
	if v.ChatReview == nil || len(v.AIRequests) != 0 {
		t.Fatal("CLI preview dispatched")
	}
	p := *v.ChatReview
	change(core.AgentCommand{Action: "chat-allow", ID: id, ChatPermission: &p, ConsentDigest: p.ID})
	change(core.AgentCommand{Action: "chat-send", ID: id, Text: "CLI normal reply", ConsentDigest: p.ID})
	until := time.Now().Add(8 * time.Second)
	for time.Now().Before(until) {
		v, e = server.Store.Agents(t.Context(), v.ProfileID, "")
		if e != nil {
			t.Fatal(e)
		}
		if len(v.AIRequests) == 1 && v.AIRequests[0].State == "completed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(v.AIRequests) != 1 || v.AIRequests[0].State != "completed" || v.AIRequests[0].HeldMicrousd != 0 || v.AIRequests[0].ChatPermissionID != p.ID {
		t.Fatal("CLI normal reply incomplete", v.AIRequests)
	}
	change(core.AgentCommand{Action: "chat-revoke", ID: id, ConsentDigest: p.ID})
	if v.ChatReadiness[id] != "revoked" {
		t.Fatal("CLI did not revoke shared permission")
	}
}
