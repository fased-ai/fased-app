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

func TestReminderTaskDraftCLIArguments(t *testing.T) {
	base := []string{"--profile", "trading-analyst", "--reminder-draft", "task"}
	cases := [][]string{{"--reminder-draft", "task"}, {"--profile", "trading-analyst", "--reminder-draft", ""}}
	for _, extra := range [][]string{{"--ai-draft", "request"}, {"--evidence", "memory", "--evidence-id", "id"}, {"--search", ""}, {"--memory-search", ""}, {"--memory-source", "note"}, {"--request-stdin"}, {"extra"}} {
		cases = append(cases, append(append([]string(nil), base...), extra...))
	}
	for _, args := range cases {
		var out bytes.Buffer
		if err := agentsCommand(args, nil, &out, io.Discard); err == nil || out.Len() != 0 || strings.Contains(err.Error(), "core is not running") {
			t.Fatal("invalid reminder draft arguments reached core", args, err)
		}
	}
}

func TestReminderTaskDraftCLIPrivateRoundTrip(t *testing.T) {
	root := filepath.Join(".evidence", "reminder-draft-cli")
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
	token, err := server.Store.Claim(t.Context(), claim.Code, "disposable-reminder-cli-password")
	if err != nil {
		t.Fatal(err)
	}
	profile := "trading-analyst"
	v, _ := server.Store.Agents(t.Context(), profile, "")
	v, err = server.Store.ChangeAgents(t.Context(), core.AgentCommand{Action: "task-create", ProfileID: profile, Revision: v.Revision, Name: "Saved review", Text: "Read retained results", IntervalSeconds: 60, MaxRuns: 1})
	if err != nil {
		t.Fatal(err)
	}
	v, err = server.Store.ChangeAgents(t.Context(), core.AgentCommand{Action: "task-run", ProfileID: profile, Revision: v.Revision, ID: v.Tasks[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--data-dir", dir, "--profile", profile, "--reminder-draft", v.Tasks[0].ID}
	var out bytes.Buffer
	if err = agentsCommand(args, nil, &out, io.Discard); err == nil || out.Len() != 0 {
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
	var draft core.TaskReminderReview
	if err = json.Unmarshal(out.Bytes(), &draft); err != nil {
		t.Fatal(err)
	}
	want, err := server.Store.AgentReminderDraft(t.Context(), profile, v.Tasks[0].ID)
	if err != nil || !reflect.DeepEqual(draft.Source, want.Source) || draft.SourceSHA256 != want.SourceSHA256 || draft.Revision != want.Revision {
		t.Fatal("CLI projection differs", err)
	}
	after, _ := server.Store.Agents(t.Context(), profile, "")
	if !reflect.DeepEqual(v, after) {
		t.Fatal("reading/abandoning CLI draft wrote state")
	}
	request := func(c core.AgentCommand) core.AgentView {
		t.Helper()
		f, err := os.CreateTemp(dir, "command-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(f.Name())
		defer f.Close()
		if err = json.NewEncoder(f).Encode(c); err != nil {
			t.Fatal(err)
		}
		if _, err = f.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		if err = agentsCommand([]string{"--data-dir", dir, "--request-stdin"}, f, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
		var got core.AgentView
		if err = json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	c := core.AgentCommand{Action: "task-reminder-review", ProfileID: profile, Revision: draft.Revision, ID: draft.Source.ID, Name: "Edited CLI copy", Text: "Review again", IntervalSeconds: 120, MaxRuns: 2}
	reviewed := request(c)
	if reviewed.TaskReminderReview == nil || reviewed.Revision != v.Revision || len(reviewed.Tasks) != 1 {
		t.Fatal("review scheduled")
	}
	c.Action, c.ConsentDigest, c.ExpiresAt = "task-reminder-create", reviewed.TaskReminderReview.Digest, reviewed.TaskReminderReview.ExpiresAt
	created := request(c)
	if len(created.Tasks) != 2 || created.Tasks[0] != v.Tasks[0] || created.Tasks[1].Name != c.Name || created.Tasks[1].Runs != 0 || len(created.Runs) != 1 || len(created.AIRequests) != 0 {
		t.Fatal("CLI copy lost bounds or changed original")
	}
	out.Reset()
	args[3] = "news-researcher"
	if err = agentsCommand(args, nil, &out, io.Discard); err == nil || out.Len() != 0 {
		t.Fatal("foreign CLI source leaked")
	}
}

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

func TestAgentEvidenceCLIPrivateRoundTrip(t *testing.T) {
	root := filepath.Join(".evidence", "agent-evidence-cli")
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
	v, err = server.Store.ChangeAgents(t.Context(), core.AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, Revision: v.Revision, Text: "CLI inspect note", Source: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	v, err = server.Store.ChangeAgents(t.Context(), core.AgentCommand{Action: "task-create", ProfileID: v.ProfileID, Revision: v.Revision, Name: "CLI reminder", Text: "Inspect saved receipt", IntervalSeconds: 60, MaxRuns: 1})
	if err != nil {
		t.Fatal(err)
	}
	v, err = server.Store.ChangeAgents(t.Context(), core.AgentCommand{Action: "task-run", ProfileID: v.ProfileID, Revision: v.Revision, ID: v.Tasks[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	args := []string{"--data-dir", dir, "--profile", v.ProfileID, "--evidence", "memory", "--evidence-id", v.Memories[0].ID}
	if err = agentsCommand(args, nil, &out, io.Discard); err == nil || out.Len() != 0 {
		t.Fatal("unauthenticated inspection")
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
	for _, q := range []struct{ kind, id string }{{"memory", v.Memories[0].ID}, {"reminder-run", v.Runs[0].ID}} {
		out.Reset()
		args = []string{"--data-dir", dir, "--profile", v.ProfileID, "--evidence", q.kind, "--evidence-id", q.id}
		if err = agentsCommand(args, nil, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
		var got core.AgentEvidenceView
		if err = json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		want, err := server.Store.AgentEvidence(t.Context(), v.ProfileID, q.kind, q.id, "")
		got.ObservedAt = want.ObservedAt
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("CLI differs from shared evidence", err)
		}
	}
	after, err := server.Store.Agents(t.Context(), v.ProfileID, "")
	if err != nil || !reflect.DeepEqual(after, v) {
		t.Fatal("CLI inspection wrote state", err)
	}
}

func TestAgentEvidenceCLIArguments(t *testing.T) {
	base := []string{"--profile", "trading-analyst", "--evidence", "memory", "--evidence-id", "note"}
	cases := [][]string{{"--evidence", "memory", "--evidence-id", "note"}, {"--profile", "trading-analyst", "--evidence", "memory"}, {"--profile", "trading-analyst", "--evidence-id", "note"}, {"--profile", "trading-analyst", "--evidence", "future", "--evidence-id", "note"}, {"--profile", "trading-analyst", "--evidence", "headline", "--evidence-id", "fetch"}}
	for _, extra := range [][]string{{"--evidence-item", ""}, {"--evidence-item", "item"}, {"--search", ""}, {"--memory-search", ""}, {"--memory-source", "note"}, {"--ai-draft", "request"}, {"--request-stdin"}, {"extra"}} {
		cases = append(cases, append(append([]string(nil), base...), extra...))
	}
	for _, args := range cases {
		var out bytes.Buffer
		err := agentsCommand(args, nil, &out, io.Discard)
		if err == nil || out.Len() != 0 || strings.Contains(err.Error(), "core is not running") {
			t.Fatal("invalid evidence CLI flags accepted", args, err)
		}
	}
}

func TestAgentAIEvidenceCLIPrivateRoundTrip(t *testing.T) {
	root := filepath.Join(".evidence", "ai-evidence-cli")
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
	change := func(c core.AgentCommand) core.AgentView {
		t.Helper()
		c.ProfileID = v.ProfileID
		c.Revision = v.Revision
		n, err := server.Store.ChangeAgents(t.Context(), c)
		if err != nil {
			t.Fatal(err)
		}
		v = n
		return n
	}
	change(core.AgentCommand{Action: "session-create", Name: "CLI evidence review"})
	session := v.Sessions[0].ID
	change(core.AgentCommand{Action: "memory-put", Text: "CLI inspected AI evidence", Source: "owner"})
	note := v.Memories[0]
	change(core.AgentCommand{Action: "task-create", Name: "CLI saved run", Text: "Read this reminder", IntervalSeconds: 60, MaxRuns: 1})
	change(core.AgentCommand{Action: "task-run", ID: v.Tasks[0].ID})
	run := v.Runs[0]
	client, err := core.NewClient(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.Token = token
	if err = client.SaveSession(dir); err != nil {
		t.Fatal(err)
	}
	input, err := os.CreateTemp(dir, "command-")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	request := func(c core.AgentCommand) (core.AgentView, error) {
		t.Helper()
		current, err := server.Store.Agents(t.Context(), v.ProfileID, "")
		if err != nil {
			return core.AgentView{}, err
		}
		c.ProfileID = v.ProfileID
		c.Revision = current.Revision
		b, _ := json.Marshal(c)
		if err = input.Truncate(0); err != nil {
			t.Fatal(err)
		}
		input.Seek(0, 0)
		if _, err = input.Write(b); err != nil {
			t.Fatal(err)
		}
		input.Seek(0, 0)
		var out bytes.Buffer
		err = agentsCommand([]string{"--data-dir", dir, "--request-stdin"}, input, &out, io.Discard)
		var result core.AgentView
		if err == nil {
			err = json.Unmarshal(out.Bytes(), &result)
		} else if out.Len() != 0 {
			t.Fatal("failed command leaked partial response")
		}
		return result, err
	}
	for _, source := range []struct{ kind, id string }{{"memory", note.ID}, {"reminder-run", run.ID}} {
		t.Run(source.kind, func(t *testing.T) {
			var out bytes.Buffer
			if err := agentsCommand([]string{"--data-dir", dir, "--profile", v.ProfileID, "--evidence", source.kind, "--evidence-id", source.id}, nil, &out, io.Discard); err != nil {
				t.Fatal(err)
			}
			var e core.AgentEvidenceView
			if err = json.Unmarshal(out.Bytes(), &e); err != nil {
				t.Fatal(err)
			}
			ref := &core.AIEvidenceSelection{Kind: e.Kind, ID: e.ID, ItemID: e.ItemID, SHA256: e.SHA256}
			c := core.AgentCommand{Action: "ai-prepare", ID: session, Text: "Review explicitly selected source", MemorySelection: &core.AIMemorySelection{Mode: "none"}, EvidenceSelection: ref, Limits: &core.AILimits{MaxInputTokens: 24000, MaxOutputTokens: 256, DeadlineSeconds: 3, BudgetMicrousd: 30000}}
			before, _ := server.Store.Agents(t.Context(), v.ProfileID, "")
			got, err := request(c)
			if err != nil {
				t.Fatal(err)
			}
			v = got
			q := v.AIRequests[len(v.AIRequests)-1]
			var context struct {
				Version   string                   `json:"version"`
				Selection core.AIEvidenceSelection `json:"evidenceSelection"`
				Evidence  core.AgentEvidenceView   `json:"evidence"`
			}
			if err = json.Unmarshal([]byte(q.Context), &context); err != nil || context.Version != "manual-ai-evidence-v1" || context.Selection != *ref || context.Evidence.SHA256 != e.SHA256 || !bytes.Equal(context.Evidence.Data, e.Data) || q.State != "prepared" || q.HeldMicrousd != 0 || v.ProviderCallsEnabled {
				t.Fatal("CLI preparation/disclosure differs", err)
			}
			after, _ := server.Store.Agents(t.Context(), v.ProfileID, "")
			if len(after.AIRequests) != len(before.AIRequests)+1 || !reflect.DeepEqual(after.Tasks, before.Tasks) || !reflect.DeepEqual(after.Runs, before.Runs) || !reflect.DeepEqual(after.Memories, before.Memories) {
				t.Fatal("CLI preparation changed source/task state")
			}
			out.Reset()
			if err = agentsCommand([]string{"--data-dir", dir, "--profile", v.ProfileID, "--ai-draft", q.ID}, nil, &out, io.Discard); err != nil {
				t.Fatal(err)
			}
			var draft core.AgentAIRequestDraft
			if err = json.Unmarshal(out.Bytes(), &draft); err != nil || draft.EvidenceSelection == nil || *draft.EvidenceSelection != *ref {
				t.Fatal("CLI draft lost evidence", err)
			}
			if _, err = request(core.AgentCommand{Action: "ai-start", ID: q.ID, ConsentDigest: q.Digest}); err == nil {
				t.Fatal("ordinary core enabled provider calls")
			}
			copy := *ref
			copy.SHA256 = "wrong"
			c.EvidenceSelection = &copy
			old, _ := server.Store.Agents(t.Context(), v.ProfileID, "")
			if _, err = request(c); err == nil {
				t.Fatal("wrong source hash accepted")
			}
			now, _ := server.Store.Agents(t.Context(), v.ProfileID, "")
			if !reflect.DeepEqual(old, now) {
				t.Fatal("CLI failed preparation wrote state")
			}
		})
	}
}
