package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func chatPermission(t *testing.T, s *Store, refs ...string) (AgentView, ChatPermission) {
	t.Helper()
	v := agentApply(t, s, AgentCommand{Action: "session-create", Name: "Normal Chat"})
	ref := ""
	if len(refs) > 0 {
		ref = refs[0]
	}
	v = agentApply(t, s, AgentCommand{Action: "chat-review", ConnectionRef: ref, ID: v.Sessions[len(v.Sessions)-1].ID, Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 5, BudgetMicrousd: 30000}, MemorySelection: &AIMemorySelection{Mode: "relevant"}, MaxRuns: 3, AuthorizationSeconds: 900, TotalBudgetMicrousd: 90000})
	if v.ChatReview == nil {
		t.Fatal("missing bounded review")
	}
	p := *v.ChatReview
	v = agentApply(t, s, AgentCommand{Action: "chat-allow", ID: p.SessionID, ChatPermission: &p, ConsentDigest: p.ID})
	return v, p
}
func chatSend(t *testing.T, s *Store, v AgentView, p ChatPermission, text string) AgentView {
	t.Helper()
	next, e := s.changeAgents(t.Context(), AgentCommand{Action: "chat-send", Revision: v.Revision, ProfileID: v.ProfileID, ID: p.SessionID, Text: text, ConsentDigest: p.ID}, true)
	if e != nil {
		t.Fatal(e)
	}
	return next
}
func TestNormalChatPermissionAndStreaming(t *testing.T) {
	s := fixtureStore(t)
	v, p := chatPermission(t, s)
	c := AgentCommand{Action: "chat-send", Revision: v.Revision, ProfileID: v.ProfileID, ID: p.SessionID, Text: "Explain a reminder", ConsentDigest: p.ID}
	if _, e := s.ChangeAgents(t.Context(), c); e == nil {
		t.Fatal("ordinary core dispatched")
	}
	bad := c
	bad.ProfileID = "trading-analyst"
	if _, e := s.changeAgents(t.Context(), bad, true); e == nil {
		t.Fatal("foreign profile reused grant")
	}
	v = chatSend(t, s, v, p, c.Text)
	q := v.AIRequests[len(v.AIRequests)-1]
	if q.State != "queued" || q.HeldMicrousd == 0 || !q.Stream || q.ChatPermissionID != p.ID {
		t.Fatal("Send did not atomically reserve one request")
	}
	done := make(chan error, 1)
	go func() { done <- s.advanceAgentAI(context.Background(), fixtureAITransport{}, true) }()
	sawPartial := false
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		view, e := s.Agents(t.Context(), v.ProfileID, "")
		if e != nil {
			t.Fatal(e)
		}
		last := view.AIRequests[len(view.AIRequests)-1]
		if last.State == "running" && last.PartialAnswer != "" {
			sawPartial = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	q = v.AIRequests[len(v.AIRequests)-1]
	if !sawPartial || q.State != "completed" || q.Answer == "" || q.PartialAnswer != "" || q.HeldMicrousd != 0 {
		t.Fatalf("stream/finalization: partial=%v result=%+v", sawPartial, q)
	}
	v = chatSend(t, s, v, p, "What did you just explain?")
	if !strings.Contains(v.AIRequests[len(v.AIRequests)-1].Context, q.Answer) {
		t.Fatal("normal Chat lost preceding reply")
	}
	if e := s.advanceAgentAI(t.Context(), aiTestTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("fixture drop") }), true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if _, e := s.changeAgents(t.Context(), AgentCommand{Action: "chat-send", Revision: v.Revision, ID: p.SessionID, Text: "Must not bypass unknown usage", ConsentDigest: p.ID}, true); e == nil {
		t.Fatal("unknown usage bypassed")
	}
}

func TestChatPermissionRevocationRestartAndScope(t *testing.T) {
	for _, mode := range []string{"stop", "revoke", "lock", "logout", "expire", "persona", "rotation", "restart"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			ref := ""
			if mode == "rotation" {
				ref = saveTestConnection(t, s).Ref
			}
			v, p := chatPermission(t, s, ref)
			v = chatSend(t, s, v, p, "Do not replay this reply")
			switch mode {
			case "stop":
				v = agentApply(t, s, AgentCommand{Action: "ai-cancel", ID: v.AIRequests[0].ID})
			case "revoke":
				v = agentApply(t, s, AgentCommand{Action: "chat-revoke", ID: p.SessionID, ConsentDigest: p.ID})
			case "lock":
				s.lockAIConnection()
			case "logout":
				token, e := s.NewSession(t.Context())
				if e != nil {
					t.Fatal(e)
				}
				if e = s.Logout(t.Context(), token); e != nil {
					t.Fatal(e)
				}
			case "expire":
				now := time.Unix(p.ExpiresAt, 0)
				s.now = func() time.Time { return now }
			case "persona":
				v = agentApply(t, s, AgentCommand{Action: "profile-update", Name: "Changed", Text: "Changed disclosure", Provider: "none"})
			case "rotation":
				if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Ref: ref, Key: "fased-test-rotated-connection-key", Passphrase: testVaultPassword}); err != nil {
					t.Fatal(err)
				}
			case "restart":
				if e := s.Close(); e != nil {
					t.Fatal(e)
				}
				n, e := OpenStore(s.dir)
				if e != nil {
					t.Fatal(e)
				}
				defer n.Close()
				s = n
			}
			calls := 0
			rt := aiTestTransport(func(r *http.Request) (*http.Response, error) { calls++; return (fixtureAITransport{}).RoundTrip(r) })
			if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
				t.Fatal(e)
			}
			next, e := s.Agents(t.Context(), v.ProfileID, "")
			if e != nil {
				t.Fatal(e)
			}
			if calls != 0 || next.AIRequests[0].State != "cancelled" || next.AIRequests[0].HeldMicrousd != 0 {
				t.Fatal("queued permission was replayed", calls, next.AIRequests)
			}
		})
	}
}
func TestChatStopsDuringActualHTTPSStream(t *testing.T) {
	for _, mode := range []string{"stop", "revoke", "lock"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			connection := saveTestConnection(t, s)
			v, p := chatPermission(t, s, connection.Ref)
			v = chatSend(t, s, v, p, "Explain a reviewed reminder")
			calls := 0
			rt := httpsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				copy := r.Clone(r.Context())
				copy.URL.Scheme = "https"
				copy.URL.Host = "api.openai.com"
				res, e := (fixtureAITransport{}).RoundTrip(copy)
				if e != nil {
					t.Error(e)
					w.WriteHeader(400)
					return
				}
				defer res.Body.Close()
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				b := make([]byte, 32)
				for {
					n, e := res.Body.Read(b)
					if n > 0 {
						if _, err := w.Write(b[:n]); err != nil {
							return
						}
						w.(http.Flusher).Flush()
					}
					if e != nil {
						return
					}
				}
			})
			done := make(chan error, 1)
			go func() { done <- s.advanceAgentAI(context.Background(), rt, true) }()
			until := time.Now().Add(3 * time.Second)
			found := false
			for time.Now().Before(until) {
				next, e := s.Agents(t.Context(), v.ProfileID, "")
				if e == nil && next.AIRequests[0].PartialAnswer != "" {
					found = true
					v = next
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !found {
				t.Fatal("no actual incremental HTTPS output")
			}
			switch mode {
			case "stop":
				agentApply(t, s, AgentCommand{Action: "ai-cancel", ID: v.AIRequests[0].ID})
			case "revoke":
				agentApply(t, s, AgentCommand{Action: "chat-revoke", ID: p.SessionID, ConsentDigest: p.ID})
			case "lock":
				s.lockAIConnection()
			}
			if e := <-done; e != nil {
				t.Fatal(e)
			}
			next, e := s.Agents(t.Context(), v.ProfileID, "")
			if e != nil {
				t.Fatal(e)
			}
			q := next.AIRequests[0]
			if calls != 1 || q.HeldMicrousd == 0 || q.Answer != "" || q.PartialAnswer != "" || len(q.Cards) != 0 || (q.State != "unknown" && q.State != "cancelled") {
				t.Fatal("stopped stream became authoritative or lost hold", q)
			}
		})
	}
}
func TestChatCapacityPreservesSourcesAndJournals(t *testing.T) {
	s := fixtureStore(t)
	v, p := chatPermission(t, s)
	// Reserve all ten slots, including linked task and unknown-journal records.
	d, r, e := readAgents(t.Context(), s.db)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 10; i++ {
		d.AIRequests = append(d.AIRequests, AgentAIRequest{ID: strings.Repeat("x", 42) + string(rune('a'+i)), ProfileID: v.ProfileID, SessionID: p.SessionID, TaskID: "linked-task", State: "unknown", HeldMicrousd: 30000, Answer: "retained source"})
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
	before, rev, _ := readAgents(t.Context(), s.db)
	if _, e = s.changeAgents(t.Context(), AgentCommand{Action: "chat-send", Revision: v.Revision, ID: p.SessionID, Text: "Cannot bypass protected usage", ConsentDigest: p.ID}, true); e == nil {
		t.Fatal("protected source bypassed")
	}
	if _, e = s.ChangeAgents(t.Context(), AgentCommand{Action: "session-archive", Revision: v.Revision, ID: p.SessionID}); e == nil {
		t.Fatal("uncertain linked record archived")
	}
	after, revision, _ := readAgents(t.Context(), s.db)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if revision != rev || string(a) != string(b) {
		t.Fatal("backpressure pruned or rewrote records")
	}
}
