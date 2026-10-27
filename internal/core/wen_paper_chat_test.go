package core

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWENPaperContextCapture(t *testing.T) {
	s := fixtureStore(t)
	now := time.Unix(1800000000, 0)
	s.now = func() time.Time { return now }
	v := agentApply(t, s, AgentCommand{Action: "wen-paper-prepare", Name: "Explain range C", WEN: &WENPaperInput{BudgetSOL: "0.25", Selection: "2", ValidForSeconds: 300}})
	task := v.Tasks[0]
	v = agentApply(t, s, AgentCommand{Action: "wen-paper-explain", ID: task.ID})
	if len(v.Sessions) != 1 || len(v.Messages) != 2 || len(v.AIRequests) != 0 || len(v.Runs) != 0 || v.Tasks[0].State != "review" {
		t.Fatal("capture executed or lost records", v)
	}
	message := v.Messages[0]
	for _, part := range []string{"Frozen WEN paper context v1", "0.25 SOL", "range C", "review", "Snapshot SHA-256:", task.ID, task.WEN.Digest, "not current state", "No AI"} {
		if !strings.Contains(message.Text, part) {
			t.Fatal("missing", part, message.Text)
		}
	}
	session := v.Sessions[0].ID
	context, err := aiContext(mustAgents(t, s), v.ProfileID, session, "Explain the captured entry", now.Unix())
	if err != nil || !strings.Contains(context, task.WEN.Digest) {
		t.Fatal("manual AI disclosure lost snapshot", err)
	}
	agentApply(t, s, AgentCommand{Action: "wen-paper-cancel", ID: task.ID})
	v = fixtureProfileProvider(t, s, AgentCommand{Action: "profile-update", Name: "Changed preference", Provider: "xai"})
	if v.Messages[0].Text != message.Text {
		t.Fatal("snapshot changed with task/provider")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	v, err = n.Agents(t.Context(), "", "")
	if err != nil || v.Messages[0].Text != message.Text || v.Tasks[0].State != "cancelled" || len(v.AIRequests) != 0 {
		t.Fatal("restart changed context", err)
	}
}
func mustAgents(t *testing.T, s *Store) agentDocument {
	t.Helper()
	d, _, err := readAgents(t.Context(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestWENPaperContextReceiptIsolationRevisionAndBounds(t *testing.T) {
	s := fixtureStore(t)
	now := time.Unix(1800000000, 0)
	s.now = func() time.Time { return now }
	v := agentApply(t, s, AgentCommand{Action: "wen-paper-prepare", Name: "Receipt context", WEN: &WENPaperInput{BudgetSOL: "0.010000001", Selection: "0", ValidForSeconds: 300}})
	task := v.Tasks[0]
	v = agentApply(t, s, AgentCommand{Action: "wen-paper-approve", ID: task.ID, ConsentDigest: task.WEN.Digest})
	v = agentApply(t, s, AgentCommand{Action: "task-run", ID: task.ID})
	original := v.Tasks[0]
	receipt := v.Runs[0].WEN.ReceiptSHA256
	stale := v.Revision
	agentApply(t, s, AgentCommand{Action: "memory-put", Text: "Independent note", Source: "owner"})
	if _, err := s.ChangeAgents(t.Context(), AgentCommand{Action: "wen-paper-explain", ID: task.ID, Revision: stale}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale capture accepted", err)
	}
	other, _ := s.Agents(t.Context(), "news-researcher", "")
	if _, err := s.ChangeAgents(t.Context(), AgentCommand{Action: "wen-paper-explain", ID: task.ID, ProfileID: "news-researcher", Revision: other.Revision}); err == nil {
		t.Fatal("foreign profile capture accepted")
	}
	now = now.Add(301 * time.Second)
	v = agentApply(t, s, AgentCommand{Action: "wen-paper-explain", ID: task.ID, Text: "spoofed client data must not replace retained facts"})
	if len(v.Messages) != 2 || !strings.Contains(v.Messages[0].Text, receipt) || !strings.Contains(v.Messages[0].Text, "10000001 lamports") || !strings.Contains(v.Messages[0].Text, "expired at capture") || strings.Contains(v.Messages[0].Text, "spoofed client") {
		t.Fatal(v.Messages)
	}
	if !reflect.DeepEqual(original, v.Tasks[0]) || len(v.Runs) != 1 || len(v.Notices) != 1 || v.ProviderCallsEnabled || v.FinancialExecution {
		t.Fatal("capture changed execution")
	}
	body, digest, ok := strings.Cut(v.Messages[0].Text, "Snapshot SHA-256: ")
	if !ok || wenHash([]byte(body)) != digest || len(v.Messages[0].Text) > 4000 {
		t.Fatal("snapshot evidence invalid")
	}
	other, _ = s.Agents(t.Context(), "news-researcher", "")
	if len(other.Sessions) != 0 || len(other.Messages) != 0 {
		t.Fatal("foreign context leaked")
	}
	d := mustAgents(t, s)
	for _, mode := range []string{"sessions", "messages", "missing-result", "bad-digest", "bad-receipt", "duplicate-result"} {
		t.Run(mode, func(t *testing.T) {
			b, _ := json.Marshal(d)
			var copy agentDocument
			json.Unmarshal(b, &copy)
			switch mode {
			case "sessions":
				copy.Sessions = make([]AgentSession, 100)
			case "messages":
				copy.Messages = make([]AgentMessage, 198)
			case "missing-result":
				copy.Runs = nil
			case "bad-digest":
				copy.Tasks[0].WEN.Digest = "wrong"
			case "bad-receipt":
				copy.Runs[0].WEN.Receipt.Amount++
			case "duplicate-result":
				copy.Runs = append(copy.Runs, copy.Runs[0])
			}
			sessions, messages := len(copy.Sessions), len(copy.Messages)
			if captureWENPaperContext(&copy, v.ProfileID, task.ID, "capture", v.Revision, now.Unix()) == nil {
				t.Fatal("unsafe capture accepted")
			}
			if len(copy.Sessions) != sessions || len(copy.Messages) != messages {
				t.Fatal("partial context appended on refusal")
			}
		})
	}
}

func TestWENPaperContextFixtureAIConsentAndCancellation(t *testing.T) {
	s := fixtureStore(t)
	v := agentApply(t, s, AgentCommand{Action: "wen-paper-prepare", Name: "Manual explanation", WEN: &WENPaperInput{BudgetSOL: "0.01", Selection: "4", ValidForSeconds: 300}})
	task := v.Tasks[0]
	v = agentApply(t, s, AgentCommand{Action: "wen-paper-explain", ID: task.ID})
	session := v.Sessions[0].ID
	message := v.Messages[0].Text
	prepare := func() AgentView {
		return agentApply(t, s, AgentCommand{Action: "ai-prepare", ID: session, Text: "Explain the frozen paper facts and what is unknown", Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 3, BudgetMicrousd: 30000}})
	}
	v = prepare()
	q := v.AIRequests[0]
	if !strings.Contains(q.Context, "Frozen WEN paper context v1") || !strings.Contains(q.Context, task.WEN.Digest) || q.HeldMicrousd != 0 {
		t.Fatal("wrong disclosure", q)
	}
	if _, err := s.ChangeAgents(t.Context(), AgentCommand{Action: "ai-start", Revision: v.Revision, ID: q.ID, ConsentDigest: q.Digest}); err == nil {
		t.Fatal("ordinary core enabled AI")
	}
	if _, err := s.changeAgents(t.Context(), AgentCommand{Action: "ai-start", Revision: v.Revision, ID: q.ID, ConsentDigest: "wrong"}, true); err == nil {
		t.Fatal("wrong consent")
	}
	agentApply(t, s, AgentCommand{Action: "ai-cancel", ID: q.ID})
	v = prepare()
	v = aiStart(t, s, v)
	calls := 0
	rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), task.WEN.Digest) {
			t.Fatal("fixture wire lost snapshot")
		}
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		return (fixtureAITransport{}).RoundTrip(r)
	})
	if err := s.advanceAgentAI(t.Context(), rt, true); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), "", "")
	if calls != 1 || v.AIRequests[0].State != "cancelled" || v.AIRequests[1].State != "completed" || v.Tasks[0].State != "review" || v.Tasks[0].WEN.Approved || len(v.Runs) != 0 || len(v.Notices) != 0 || v.Messages[0].Text != message {
		t.Fatal("explanation executed or mutated paper", v)
	}
}
