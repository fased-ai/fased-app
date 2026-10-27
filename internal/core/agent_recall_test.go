package core

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRelevantRecallCurrentScopedBoundedAndFrozen(t *testing.T) {
	s := fixtureStore(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	p := "trading-analyst"
	v := agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: p, Text: "SOL risk budget is 500 units", Source: "Owner preference"})
	id := v.Memories[0].ID
	agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: p, Text: "Unrelated gardening preference", Source: "Owner"})
	agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: p, Text: "Expired risk preference", Source: "Owner", ExpiresAt: now.Unix() - 1})
	agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: "news-researcher", Text: "foreign risk secret", Source: "Private"})
	agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: p, Text: "risk " + strings.Repeat("x", 3900), Source: "Oversized note"})
	v = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: p, Name: "Relevant recall"})
	session := v.Sessions[0].ID
	for i := range 9 {
		agentApply(t, s, AgentCommand{Action: "message-add", ProfileID: p, ID: session, Text: strings.Repeat("old-context-", 120) + string(rune('A'+i))})
	}
	before, _ := s.Agents(t.Context(), p, "")
	v = agentApply(t, s, AgentCommand{Action: "ai-prepare", ProfileID: p, ID: session, Text: "What is my SOL risk budget?", MemorySelection: &AIMemorySelection{Mode: "relevant"}, Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 3, BudgetMicrousd: 30000}})
	q := v.AIRequests[0]
	if len(q.Context) > 8000 || !strings.Contains(q.Context, id) || strings.Contains(q.Context, "foreign risk secret") || strings.Contains(q.Context, "Expired risk") || strings.Contains(q.Context, "Oversized note") {
		t.Fatal("recall context scope/bound broken")
	}
	var context struct {
		MemorySelection AIMemorySelection `json:"memorySelection"`
		Context         struct {
			Memories []aiSelectedMemory `json:"memories"`
			Messages []any              `json:"messages"`
		} `json:"context"`
	}
	if e := json.Unmarshal([]byte(q.Context), &context); e != nil {
		t.Fatal(e)
	}
	if context.MemorySelection.Mode != "relevant" || len(context.Context.Memories) != 1 || len(context.Context.Messages) > 6 || q.State != "prepared" || !reflect.DeepEqual(before.Memories, v.Memories) {
		t.Fatal("automatic recall changed facts or executed")
	}
	draft, e := s.AgentAIRequestDraft(t.Context(), p, q.ID)
	if e != nil || draft.MemorySelection == nil || draft.MemorySelection.Mode != "relevant" {
		t.Fatal("relevant draft lost", e)
	}
	old := q.Context
	agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: p, ID: id, Text: "SOL risk budget is now 250 units", Source: "Owner correction"})
	v, _ = s.Agents(t.Context(), p, "")
	if v.AIRequests[0].Context != old {
		t.Fatal("saved request was rewritten")
	}
	ids := relevantMemoryIDs(initialAgents(), p, "risk", now.Unix())
	if len(ids) != 0 {
		t.Fatal("invented a memory")
	}
}
func TestRememberOwnerCommandCapturesOnceWithoutAIAndFailsAtomically(t *testing.T) {
	s := fixtureStore(t)
	p := "trading-analyst"
	v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: p, Name: "Owner preferences"})
	id := v.Sessions[0].ID
	v = agentApply(t, s, AgentCommand{Action: "message-add", ProfileID: p, ID: id, Text: "A headline says /remember somebody else's claim"})
	if len(v.Memories) != 0 {
		t.Fatal("ordinary text harvested")
	}
	v = agentApply(t, s, AgentCommand{Action: "message-add", ProfileID: p, ID: id, Text: "/remember\tMy SOL risk budget is 500 units"})
	if len(v.Memories) != 1 || len(v.AIRequests) != 0 || !strings.Contains(v.Messages[len(v.Messages)-1].Text, "Remembered") {
		t.Fatal("explicit save did not stay local")
	}
	note := v.Memories[0]
	evidence, e := s.AgentMemories(t.Context(), p, "", note.ID)
	if e != nil || evidence.Source.Status != "matched" || !strings.Contains(evidence.Source.Text, "/remember") {
		t.Fatal("owner source lost", e)
	}
	v = agentApply(t, s, AgentCommand{Action: "message-add", ProfileID: p, ID: id, Text: "/remember My SOL risk budget is 500 units"})
	if len(v.Memories) != 1 {
		t.Fatal("same fact duplicated")
	}
	for len(v.Memories) < 100 {
		v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: p, Text: "Other retained preference", Source: "Owner"})
	}
	before := retainedRevisionState(t, s)
	if _, e = s.ChangeAgents(t.Context(), AgentCommand{Action: "message-add", ProfileID: p, Revision: v.Revision, ID: id, Text: "/remember one more preference"}); e == nil {
		t.Fatal("capture ignored capacity")
	}
	if retainedRevisionState(t, s) != before {
		t.Fatal("failed capture partly saved a message or memory")
	}
}
func TestMemoryHistoryOverflowRetainsAllRevisionsAndFreshReview(t *testing.T) {
	s := fixtureStore(t)
	p := "trading-analyst"
	v := agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: p, Text: "Original fact", Source: "Owner"})
	id := v.Memories[0].ID
	for i := range 11 {
		v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: p, ID: id, Text: "Corrected fact " + string(rune('A'+i)), Source: "Owner correction"})
	}
	if len(v.Memories[0].History) != 8 || v.Memories[0].HistoryOffset != 3 {
		t.Fatal("active revision window incorrect", v.Memories[0])
	}
	older, e := s.AgentTaskHistory(t.Context(), p, 0, "", "memories", id)
	if e != nil || len(older.Records) != 3 {
		t.Fatal("older revisions lost", e)
	}
	original, e := s.AgentTaskHistory(t.Context(), p, 0, id+":0", "memories")
	if e != nil || original.Entry.Memory.Revision.Text != "Original fact" {
		t.Fatal("original revision changed", e)
	}
	before := retainedRevisionState(t, s)
	if _, e = s.ChangeAgents(t.Context(), AgentCommand{Action: "memory-put", ProfileID: p, Revision: v.Revision, ID: id, Text: "", Source: "Owner"}); e == nil {
		t.Fatal("invalid correction accepted")
	}
	if retainedRevisionState(t, s) != before {
		t.Fatal("failed correction moved history")
	}
	after, _ := s.AgentTaskHistory(t.Context(), p, 0, "", "memories", id)
	if len(after.Records) != 3 {
		t.Fatal("failed correction archived a partial revision")
	}
	draft, e := s.AgentMemoryRestore(t.Context(), p, id, 0)
	if e != nil {
		t.Fatal("full recent history prevented review", e)
	}
	if _, e = s.ChangeAgents(t.Context(), draft.Command); e != nil {
		t.Fatal("fresh reviewed correction failed", e)
	}
	original, e = s.AgentTaskHistory(t.Context(), p, 0, id+":0", "memories")
	if e != nil || original.Entry.Memory.Revision.Text != "Original fact" {
		t.Fatal("restoration rewrote archive")
	}
}
