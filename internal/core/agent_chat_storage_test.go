package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChatSchemaMigrationPreservesOriginalsAndRejectsUnprotectedAuthority(t *testing.T) {
	for _, mode := range []string{"plain", "paused", "chat", "case", "stream"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			v := agentApply(t, s, AgentCommand{Action: "session-create", Name: "Preserved conversation"})
			_ = v
			var raw string
			var rev int64
			if e := s.db.QueryRow("SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&raw, &rev); e != nil {
				t.Fatal(e)
			}
			if mode == "chat" || mode == "case" || mode == "stream" {
				var d map[string]any
				_ = json.Unmarshal([]byte(raw), &d)
				if mode == "stream" {
					d["aiRequests"] = []any{map[string]any{"stream": false}}
				} else {
					key := "chat"
					if mode == "case" {
						key = "Chat"
					}
					ss := d["sessions"].([]any)[0].(map[string]any)
					ss[key] = nil
				}
				b, _ := json.Marshal(d)
				raw = string(b)
				if _, e := s.db.Exec("UPDATE agent_workspace SET document=? WHERE id=1", raw); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "paused" {
				if _, e := s.db.Exec("UPDATE update_pause SET phase='paused' WHERE id=1"); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := s.db.Exec("PRAGMA user_version=18"); e != nil {
				t.Fatal(e)
			}
			if e := s.Close(); e != nil {
				t.Fatal(e)
			}
			n, e := OpenStore(s.dir)
			if mode == "plain" {
				if e != nil {
					t.Fatal(e)
				}
				defer n.Close()
				var got string
				var revision int64
				if e = n.db.QueryRow("SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&got, &revision); e != nil || got != raw || revision != rev {
					t.Fatal("format migration rewrote records/revision")
				}
			} else if e == nil {
				n.Close()
				t.Fatal("unprotected/paused authority migrated", mode)
			}
		})
	}
}
func TestChatSettledHistoryReleasesCapacityWithoutPruningSources(t *testing.T) {
	s := fixtureStore(t)
	v, p := chatPermission(t, s)
	v = chatSend(t, s, v, p, "Preserve this answer as a source")
	if e := s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	q := v.AIRequests[0]
	v = agentApply(t, s, AgentCommand{Action: "memory-chat-save", ID: q.ID, Source: "answer", Text: "A reviewed local preference"})
	d, r, _ := readAgents(t.Context(), s.db)
	for i := 1; i < 10; i++ {
		copy := q
		copy.ID = strings.Repeat("f", 42) + string(rune('a'+i))
		d.AIRequests = append(d.AIRequests, copy)
	}
	tx, e := s.db.BeginTx(t.Context(), nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = saveAgents(t.Context(), tx, d, r); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if _, e = s.changeAgents(t.Context(), AgentCommand{Action: "chat-send", Revision: v.Revision, ID: p.SessionID, Text: "At the active limit", ConsentDigest: p.ID}, true); e == nil || !strings.Contains(e.Error(), "archive") {
		t.Fatal("no actionable capacity barrier", e)
	}
	v = agentApply(t, s, AgentCommand{Action: "session-archive", ID: p.SessionID})
	if len(v.AIRequests) != 0 || v.ArchivedAISpentMicrousd != q.SpentMicrousd*10 {
		t.Fatal("journal capacity was pruned instead of archived")
	}
	history, e := s.AgentTaskHistory(t.Context(), v.ProfileID, 0, p.SessionID, "conversations")
	if e != nil || history.Entry == nil || len(history.Entry.AIRequests) != 10 {
		t.Fatal("history journal missing", e)
	}
	memory, e := s.AgentMemories(t.Context(), v.ProfileID, "", v.Memories[0].ID)
	if e != nil || (memory.Source == nil || memory.Source.Status != "matched") {
		t.Fatal("archived source lost", e)
	}
}
