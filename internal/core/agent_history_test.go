package core

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestTaskHistoryArchiveWirePreservesResult(t *testing.T) {
	s := fixtureStore(t)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	v := agentApply(t, s, AgentCommand{Action: "task-create", ProfileID: "trading-analyst", Name: "Retained result", Text: "Review the journal", IntervalSeconds: 60, MaxRuns: 1})
	id := v.Tasks[0].ID
	v = agentApply(t, s, AgentCommand{Action: "task-run", ProfileID: v.ProfileID, ID: id})
	v, e := s.ChangeAgents(t.Context(), AgentCommand{Action: "task-archive", ProfileID: v.ProfileID, Revision: v.Revision, ID: id})
	if e != nil {
		t.Fatalf("completed history archive rejected: %v", e)
	}
	if len(v.Tasks) != 0 || len(v.Runs) != 0 || len(v.Notices) != 0 {
		t.Fatal("active capacity not released")
	}
}

// Older-schema fixtures omit later tables; never discard an archived record.
func dropEmptyHistoryFixture(t *testing.T, s *Store) {
	t.Helper()
	var exists, n int
	if e := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='agent_history'").Scan(&exists); e != nil {
		t.Fatal(e)
	}
	if exists == 0 {
		return
	}
	if e := s.db.QueryRow("SELECT count(*) FROM agent_history").Scan(&n); e != nil || n != 0 {
		t.Fatal("refuse to discard fixture archive", n, e)
	}
	if _, e := s.db.Exec("DROP TABLE agent_history"); e != nil {
		t.Fatal(e)
	}
}

func completedHistoryTask(t *testing.T, s *Store, name string) (AgentView, string) {
	t.Helper()
	v := agentApply(t, s, AgentCommand{Action: "task-create", ProfileID: "trading-analyst", Name: name, Text: "Review SOL budget", IntervalSeconds: 60, MaxRuns: 1})
	id := v.Tasks[len(v.Tasks)-1].ID
	return agentApply(t, s, AgentCommand{Action: "task-run", ProfileID: v.ProfileID, ID: id}), id
}
func TestTaskHistoryPagingIsolationImmutabilityAndPause(t *testing.T) {
	s := fixtureStore(t)
	for i := range 21 {
		v, id := completedHistoryTask(t, s, fmt.Sprintf("Запись %d", i))
		agentApply(t, s, AgentCommand{Action: "task-archive", ProfileID: v.ProfileID, ID: id})
	}
	page, e := s.AgentTaskHistory(t.Context(), "trading-analyst", 0, "", "tasks", "ЗАПИСЬ")
	if e != nil || len(page.Records) != 20 || page.NextBefore == 0 {
		t.Fatal("history page", page, e)
	}
	next, e := s.AgentTaskHistory(t.Context(), "trading-analyst", page.NextBefore, "", "tasks", "запись")
	if e != nil || len(next.Records) != 1 || next.Records[0].ID >= page.NextBefore {
		t.Fatal("history cursor", next, e)
	}
	id := page.Records[0].TaskID
	detail, e := s.AgentTaskHistory(t.Context(), "trading-analyst", 0, id)
	if e != nil || detail.Entry == nil || len(detail.Entry.Runs) != 1 {
		t.Fatal(e)
	}
	other, e := s.AgentTaskHistory(t.Context(), "news-researcher", 0, "")
	if e != nil || len(other.Records) != 0 {
		t.Fatal("foreign archive visible")
	}
	if _, e = s.AgentTaskHistory(t.Context(), "news-researcher", 0, id); e == nil {
		t.Fatal("foreign archive inspected")
	}
	if _, e = s.db.Exec("DELETE FROM agent_history"); e == nil {
		t.Fatal("archive deleted")
	}
	if _, e = s.db.Exec("UPDATE agent_history SET sha256='changed'"); e == nil {
		t.Fatal("archive changed")
	}
	v, id := completedHistoryTask(t, s, "Paused archive")
	pause := pauseCommand(t, s, 1)
	if _, e = s.UpdatePause(t.Context(), pause); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeAgents(t.Context(), AgentCommand{Action: "task-archive", ProfileID: v.ProfileID, Revision: v.Revision, ID: id}); e == nil {
		t.Fatal("paused archive mutated")
	}
	if _, e = s.AgentTaskHistory(t.Context(), "trading-analyst", 0, ""); e != nil {
		t.Fatal("paused history read", e)
	}
}
func TestHistoryPolicyArchivesOnlyReadUnreferencedCompletedReminders(t *testing.T) {
	s := fixtureStore(t)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	v, id := completedHistoryTask(t, s, "Automatic archive")
	agentApply(t, s, AgentCommand{Action: "history-policy", ProfileID: v.ProfileID, HistoryDays: 7})
	now = now.Add(8 * 24 * time.Hour)
	if e := s.RunAgentTasks(t.Context()); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if len(v.Tasks) != 1 {
		t.Fatal("unread result hidden")
	}
	agentApply(t, s, AgentCommand{Action: "notice-read", ProfileID: v.ProfileID, ID: v.Notices[0].ID})
	if e := s.RunAgentTasks(t.Context()); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if len(v.Tasks) != 0 {
		t.Fatal("completed capacity not released")
	}
	saved, e := s.AgentTaskHistory(t.Context(), v.ProfileID, 0, id)
	if e != nil || saved.Entry.Task.ID != id {
		t.Fatal(e)
	}
	v, id = completedHistoryTask(t, s, "Referenced result")
	v = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: v.ProfileID, Name: "Linked receipt"})
	v = agentApply(t, s, AgentCommand{Action: "message-add", ProfileID: v.ProfileID, ID: v.Sessions[0].ID, Text: "Saved receipt " + id})
	before := retainedRevisionState(t, s)
	if _, e = s.ChangeAgents(t.Context(), AgentCommand{Action: "task-archive", ProfileID: v.ProfileID, Revision: v.Revision, ID: id}); e == nil {
		t.Fatal("referenced evidence archived")
	}
	if retainedRevisionState(t, s) != before {
		t.Fatal("refusal changed records")
	}
	if _, e = s.ChangeAgents(t.Context(), AgentCommand{Action: "history-policy", ProfileID: v.ProfileID, Revision: v.Revision, HistoryDays: 1}); e == nil {
		t.Fatal("unknown policy accepted")
	}
}
func TestConversationHistoryPreservesAIJournalMemorySearchAndRestart(t *testing.T) {
	s := fixtureStore(t)
	v := aiStart(t, s, aiPrepare(t, s))
	if e := s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	q := v.AIRequests[0]
	if q.State != "completed" || q.HeldMicrousd != 0 {
		t.Fatal("fixture incomplete")
	}
	v = agentApply(t, s, AgentCommand{Action: "memory-chat-save", ProfileID: v.ProfileID, ID: q.ID, Source: "answer", Text: "Remember the bounded fixture review"})
	memory := v.Memories[0]
	v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: memory.ID, Text: "Corrected bounded preference", Source: memory.Source})
	v = agentApply(t, s, AgentCommand{Action: "session-archive", ProfileID: v.ProfileID, ID: q.SessionID})
	if len(v.Sessions) != 0 || len(v.AIRequests) != 0 || v.ArchivedAISpentMicrousd != q.SpentMicrousd {
		t.Fatal("archive lost journal accounting", v.ArchivedAISpentMicrousd, q.SpentMicrousd)
	}
	page, e := s.AgentTaskHistory(t.Context(), v.ProfileID, 0, "", "conversations", "PAPER STRATEGY")
	if e != nil || len(page.Records) != 1 {
		t.Fatal("archived question not searchable", e)
	}
	saved, e := s.AgentTaskHistory(t.Context(), v.ProfileID, 0, q.SessionID, "conversations")
	if e != nil || saved.Entry.AIRequests[0].Digest != q.Digest || saved.Entry.AIRequests[0].Answer != q.Answer || saved.Entry.AIRequests[0].SpentMicrousd != q.SpentMicrousd {
		t.Fatal("journal changed", e)
	}
	notes, e := s.AgentMemories(t.Context(), v.ProfileID, "", memory.ID)
	if e != nil || notes.Source.Status != "matched" || notes.Source.Text != q.Answer {
		t.Fatal("archived memory source lost", notes.Source, e)
	}
	restore, e := s.AgentMemoryRestore(t.Context(), v.ProfileID, memory.ID, 0)
	if e != nil || restore.Evidence.Status != "matched" {
		t.Fatal("archived correction source lost", e)
	}
	other, _ := s.Agents(t.Context(), "news-researcher", "")
	if other.ArchivedAISpentMicrousd != 0 {
		t.Fatal("other profile spending leaked")
	}
	dir := s.dir
	s.Close()
	n, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer n.Close()
	saved, e = n.AgentTaskHistory(t.Context(), v.ProfileID, 0, q.SessionID, "conversations")
	if e != nil || saved.SHA256 == "" {
		t.Fatal("archive lost on restart", e)
	}
	before := retainedRevisionState(t, n)
	view, _ := n.Agents(t.Context(), v.ProfileID, "")
	if _, e = n.changeAgents(t.Context(), AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, Revision: view.Revision, ID: q.ID, ConsentDigest: q.Digest}, true); e == nil {
		t.Fatal("archive became execution authority")
	}
	if retainedRevisionState(t, n) != before {
		t.Fatal("archive replay changed state")
	}
}
func TestConversationHistoryPreservesExplicitRememberIdentity(t *testing.T) {
	s := fixtureStore(t)
	p := "trading-analyst"
	v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: p, Name: "Remembered preferences"})
	session := v.Sessions[0].ID
	v = agentApply(t, s, AgentCommand{Action: "message-add", ProfileID: p, ID: session, Text: "/remember My SOL risk budget is 500 units"})
	note := v.Memories[0]
	v = agentApply(t, s, AgentCommand{Action: "session-archive", ProfileID: p, ID: session})
	if len(v.Sessions) != 0 || len(v.Memories) != 1 || v.Memories[0].ID != note.ID {
		t.Fatal("remembered identity changed during archive")
	}
	source, e := s.AgentMemories(t.Context(), p, "", note.ID)
	if e != nil || source.Source.Status != "matched" || source.Source.SessionID != session {
		t.Fatal("explicit memory source lost", e)
	}
}

func TestConversationHistoryRefusesPendingAndProtectsSnapshot(t *testing.T) {
	s := fixtureStore(t)
	v := aiStart(t, s, aiPrepare(t, s))
	q := v.AIRequests[0]
	before := retainedRevisionState(t, s)
	if _, e := s.ChangeAgents(t.Context(), AgentCommand{Action: "session-archive", ProfileID: v.ProfileID, Revision: v.Revision, ID: q.SessionID}); e == nil {
		t.Fatal("pending/held journal archived")
	}
	if retainedRevisionState(t, s) != before {
		t.Fatal("failed archive lost hold")
	}
	n := fixtureStore(t)
	v, id := completedHistoryTask(t, n, "Snapshot history")
	agentApply(t, n, AgentCommand{Action: "task-archive", ProfileID: v.ProfileID, ID: id})
	first, e := n.UpdateSnapshot(t.Context(), strings.Repeat("a", 32))
	if e != nil {
		t.Fatal(e)
	}
	v, id = completedHistoryTask(t, n, "Another snapshot history")
	agentApply(t, n, AgentCommand{Action: "task-archive", ProfileID: v.ProfileID, ID: id})
	second, e := n.UpdateSnapshot(t.Context(), strings.Repeat("a", 32))
	if e != nil || first.State.Anchor == second.State.Anchor {
		t.Fatal("archive omitted from metadata inventory", e)
	}
}
