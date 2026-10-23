package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

func taskAIPlan(t *testing.T, s *Store, max int) AgentView {
	t.Helper()
	p := "trading-analyst"
	v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: p, Name: "Scheduled fixture context"})
	return agentApply(t, s, AgentCommand{Action: "task-ai-prepare", ProfileID: p, ID: v.Sessions[len(v.Sessions)-1].ID, Name: "Bounded AI reminder", Text: "Review fixed paper inputs", IntervalSeconds: 60, MaxRuns: max, Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 1, BudgetMicrousd: 30000}, TotalBudgetMicrousd: int64(max) * 30000, AuthorizationSeconds: 3600})
}
func taskAIAuthorize(t *testing.T, s *Store, v AgentView) AgentView {
	t.Helper()
	task := v.Tasks[len(v.Tasks)-1]
	n, e := s.changeAgents(t.Context(), AgentCommand{Action: "task-ai-authorize", ProfileID: v.ProfileID, Revision: v.Revision, ID: task.ID, ConsentDigest: task.AI.Digest}, true)
	if e != nil {
		t.Fatal(e)
	}
	return n
}
func taskAIRun(t *testing.T, s *Store, v AgentView) AgentView {
	t.Helper()
	n, e := s.changeAgents(t.Context(), AgentCommand{Action: "task-run", ProfileID: v.ProfileID, Revision: v.Revision, ID: v.Tasks[len(v.Tasks)-1].ID}, true)
	if e != nil {
		t.Fatal(e)
	}
	return n
}
func TestTaskAIConsentBudgetsAndDurableEvaluations(t *testing.T) {
	s := fixtureStore(t)
	v := taskAIPlan(t, s, 2)
	task := v.Tasks[0]
	if task.State != "review" || len(v.AIRequests) != 0 {
		t.Fatal("preview executed")
	}
	c := AgentCommand{Action: "task-ai-authorize", Revision: v.Revision, ProfileID: v.ProfileID, ID: task.ID, ConsentDigest: task.AI.Digest}
	if _, e := s.ChangeAgents(t.Context(), c); e == nil {
		t.Fatal("ordinary core activated AI")
	}
	c.ConsentDigest = "wrong"
	if _, e := s.changeAgents(t.Context(), c, true); e == nil {
		t.Fatal("missing task consent")
	}
	v = taskAIAuthorize(t, s, v)
	v = taskAIRun(t, s, v)
	if len(v.AIRequests) != 1 || v.Tasks[0].Runs != 0 || v.Tasks[0].State != "waiting-ai" || v.AIRequests[0].HeldMicrousd == 0 {
		t.Fatal("dispatch was not durably reserved")
	}
	calls := 0
	rt := aiTestTransport(func(r *http.Request) (*http.Response, error) { calls++; return (fixtureAITransport{}).RoundTrip(r) })
	if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
		t.Fatal(e)
	}
	if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if calls != 1 || v.Tasks[0].Runs != 1 || v.Tasks[0].State != "scheduled" || len(v.Runs) != 1 || len(v.Notices) != 1 || v.Runs[0].AIRequestID != v.AIRequests[0].ID {
		t.Fatal("missing/duplicate dependent evaluation", v.Tasks)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	n, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer n.Close()
	v, _ = n.Agents(t.Context(), v.ProfileID, "")
	v = taskAIRun(t, n, v)
	if e = n.advanceAgentAI(t.Context(), rt, true); e != nil {
		t.Fatal(e)
	}
	v, _ = n.Agents(t.Context(), v.ProfileID, "")
	if calls != 2 || v.Tasks[0].State != "completed" || v.Tasks[0].Runs != 2 || len(v.Runs) != 2 || v.AIRequests[0].ID == v.AIRequests[1].ID {
		t.Fatal("restart/second evaluation")
	}
	for _, r := range v.Runs {
		if r.AIDigest != task.AI.Digest {
			t.Fatal("lost strategy consent")
		}
	}
}
func TestTaskAIFailureAndLocalContinuity(t *testing.T) {
	for _, mode := range []string{"unknown", "deadline", "cancel", "disabled", "changed"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			v := taskAIAuthorize(t, s, taskAIPlan(t, s, 2))
			p := v.ProfileID
			taskID := v.Tasks[0].ID
			v = agentApply(t, s, AgentCommand{Action: "bot-create", ProfileID: p, Name: "Independent paper", Quantity: 1, PriceCents: 5, BudgetCents: 20})
			bot := v.Bots[0].ID
			v = agentApply(t, s, AgentCommand{Action: "task-create", ProfileID: p, Name: "Independent reminder", Text: "still works", IntervalSeconds: 60, MaxRuns: 2})
			v = agentApply(t, s, AgentCommand{Action: "task-create", ProfileID: p, Name: "Independent paper", BotID: bot, IntervalSeconds: 60, MaxRuns: 2})
			c := AgentCommand{Action: "task-run", ProfileID: p, Revision: v.Revision, ID: taskID}
			v, e := s.changeAgents(t.Context(), c, mode != "disabled")
			if e != nil {
				t.Fatal(e)
			}
			if mode == "cancel" {
				v = agentApply(t, s, AgentCommand{Action: "ai-cancel", ProfileID: p, ID: v.AIRequests[0].ID})
			}
			if mode == "changed" {
				v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: p, Text: "changed after consent", Source: "owner"})
			}
			rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
				if mode == "unknown" {
					return nil, errors.New("secret provider body")
				}
				if mode == "deadline" {
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				return (fixtureAITransport{}).RoundTrip(r)
			})
			if e = s.advanceAgentAI(t.Context(), rt, mode != "disabled"); e != nil {
				t.Fatal(e)
			}
			v, _ = s.Agents(t.Context(), p, "")
			for _, task := range v.Tasks[1:] {
				v = agentApply(t, s, AgentCommand{Action: "task-run", ProfileID: p, ID: task.ID})
			}
			if v.Tasks[0].State != "held" || len(v.Runs) != 3 || v.Bots[0].SpentCents != 5 || v.Tasks[0].Runs != 1 {
				t.Fatal("failure blocked local work or ran dependency", v.Tasks)
			}
			c = AgentCommand{Action: "task-resume", ProfileID: p, Revision: v.Revision, ID: taskID}
			if _, e = s.changeAgents(t.Context(), c, true); e == nil {
				t.Fatal("held task silently retried")
			}
		})
	}
}
func TestTaskAIRestartAndConcurrentTickNoReplay(t *testing.T) {
	s := fixtureStore(t)
	v := taskAIAuthorize(t, s, taskAIPlan(t, s, 1))
	task := v.Tasks[0]
	tx, e := s.db.BeginTx(t.Context(), nil)
	if e != nil {
		t.Fatal(e)
	}
	d, r, e := readAgents(t.Context(), tx)
	if e != nil {
		t.Fatal(e)
	}
	d.Tasks[0].NextAt = 0
	if e = saveAgents(t.Context(), tx, d, r); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if e := s.runAgentTasks(t.Context(), true); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if len(v.AIRequests) != 1 {
		t.Fatal("duplicate scheduled request")
	}
	tx, e = s.db.BeginTx(t.Context(), nil)
	if e != nil {
		t.Fatal(e)
	}
	d, r, e = readAgents(t.Context(), tx)
	if e != nil {
		t.Fatal(e)
	}
	d.AIRequests[0].State = "running"
	d.AIRequests[0].DispatchBoot = s.bootID
	if e = saveAgents(t.Context(), tx, d, r); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	n, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer n.Close()
	calls := 0
	rt := aiTestTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, context.Canceled })
	if e = n.advanceAgentAI(t.Context(), rt, true); e != nil {
		t.Fatal(e)
	}
	v, _ = n.Agents(t.Context(), v.ProfileID, "")
	if calls != 0 || v.Tasks[0].State != "held" || v.AIRequests[0].HeldMicrousd == 0 || len(v.Runs) != 1 || v.Runs[0].AIDigest != task.AI.Digest {
		t.Fatal("restart lost unknown hold")
	}
}

func TestTaskAIReviewExpiryCapacityAndMigration(t *testing.T) {
	s := fixtureStore(t)
	v := taskAIPlan(t, s, 2)
	task := v.Tasks[0]
	for _, action := range []string{"task-run", "task-resume", "task-pause"} {
		if _, e := s.changeAgents(t.Context(), AgentCommand{Revision: v.Revision, ProfileID: v.ProfileID, Action: action, ID: task.ID}, true); e == nil {
			t.Fatal("unapproved plan control", action)
		}
	}
	if _, e := s.changeAgents(t.Context(), AgentCommand{Revision: v.Revision, ProfileID: "news-researcher", Action: "task-ai-authorize", ID: task.ID, ConsentDigest: task.AI.Digest}, true); e == nil {
		t.Fatal("cross-profile consent")
	}
	v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, Text: "context changed", Source: "owner"})
	if _, e := s.changeAgents(t.Context(), AgentCommand{Revision: v.Revision, ProfileID: v.ProfileID, Action: "task-ai-authorize", ID: task.ID, ConsentDigest: task.AI.Digest}, true); e == nil {
		t.Fatal("stale consent")
	}
	v = agentApply(t, s, AgentCommand{Action: "task-ai-cancel", ProfileID: v.ProfileID, ID: task.ID})
	if v.Tasks[0].State != "cancelled" {
		t.Fatal("review cancellation")
	}
	v = taskAIAuthorize(t, s, taskAIPlan(t, s, 2))
	task = v.Tasks[len(v.Tasks)-1]
	s.now = func() time.Time { return time.Unix(task.AI.ExpiresAt, 0) }
	v = taskAIRun(t, s, v)
	if v.Tasks[len(v.Tasks)-1].State != "held" || len(v.AIRequests) != 0 {
		t.Fatal("expired consent dispatched")
	}
	for _, paused := range []bool{false, true} {
		t.Run(fmt.Sprint("schema5-", paused), func(t *testing.T) {
			s := fixtureStore(t)
			v := taskAIPlan(t, s, 1)
			if _, e := s.db.Exec("PRAGMA user_version=5"); e != nil {
				t.Fatal(e)
			}
			if paused {
				_, _ = s.db.Exec("UPDATE update_pause SET phase='paused'")
			}
			if e := s.Close(); e != nil {
				t.Fatal(e)
			}
			n, e := OpenStore(s.dir)
			if paused {
				if e == nil {
					n.Close()
					t.Fatal("migrated paused schema5")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			defer n.Close()
			got, e := n.Agents(t.Context(), v.ProfileID, "")
			if e != nil || got.Tasks[0].AI.Digest != v.Tasks[0].AI.Digest {
				t.Fatal("lost plan")
			}
		})
	}
	t.Run("future-slots", func(t *testing.T) {
		s := fixtureStore(t)
		v := taskAIPlan(t, s, 8)
		for range 2 {
			v = agentApply(t, s, AgentCommand{Action: "ai-prepare", ProfileID: v.ProfileID, ID: v.Sessions[0].ID, Text: "manual preview", Limits: &v.Tasks[0].AI.Limits})
		}
		if _, e := s.ChangeAgents(t.Context(), AgentCommand{Revision: v.Revision, Action: "ai-prepare", ProfileID: v.ProfileID, ID: v.Sessions[0].ID, Text: "steal future slot", Limits: &v.Tasks[0].AI.Limits}); e == nil {
			t.Fatal("manual request stole reserved slot")
		}
	})
}
func TestTaskAIKnownFailureAndActiveStop(t *testing.T) {
	for _, mode := range []string{"incomplete", "malformed", "stop", "pause", "update-pause"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			v := taskAIRun(t, s, taskAIAuthorize(t, s, taskAIPlan(t, s, 1)))
			id := v.Tasks[0].ID
			rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
				if mode == "stop" || mode == "pause" {
					action := "task-ai-cancel"
					if mode == "pause" {
						action = "task-pause"
					}
					agentApply(t, s, AgentCommand{Action: action, ProfileID: v.ProfileID, ID: id})
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				if mode == "update-pause" {
					if _, e := s.db.Exec("UPDATE update_pause SET phase='paused'"); e != nil {
						t.Fatal(e)
					}
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				res, e := (fixtureAITransport{}).RoundTrip(r)
				if e != nil {
					return nil, e
				}
				defer res.Body.Close()
				var packet map[string]any
				_ = json.NewDecoder(res.Body).Decode(&packet)
				if mode == "incomplete" {
					packet["status"] = "incomplete"
				} else {
					packet["output"] = []any{map[string]string{"type": "function_call", "name": "sign_transaction"}}
				}
				b, _ := json.Marshal(packet)
				res.Body = io.NopCloser(strings.NewReader(string(b)))
				return res, nil
			})
			e := s.advanceAgentAI(t.Context(), rt, true)
			if e != nil && !errors.Is(e, ErrCorePaused) {
				t.Fatal(e)
			}
			if mode == "update-pause" {
				if _, e = s.db.Exec("UPDATE update_pause SET phase='idle'"); e != nil {
					t.Fatal(e)
				}
				s.bootID = "disposable-reboot"
				if e = s.advanceAgentAI(t.Context(), rt, false); e != nil {
					t.Fatal(e)
				}
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			if v.Tasks[0].State != "held" || len(v.Runs) != 1 || v.Runs[0].Outcome == "reminded" {
				t.Fatal("failed AI dependency ran")
			}
			q := v.AIRequests[0]
			known := mode == "incomplete" || mode == "malformed"
			if known && (q.SpentMicrousd == 0 || q.HeldMicrousd != 0) {
				t.Fatal("known usage not settled")
			}
			if !known && q.HeldMicrousd == 0 {
				t.Fatal("active unknown reservation released")
			}
		})
	}
}
func TestTaskAIFixedPaperInputsAndDefaultOffRestart(t *testing.T) {
	s := fixtureStore(t)
	p := "trading-analyst"
	v := agentApply(t, s, AgentCommand{Action: "bot-create", ProfileID: p, Name: "Fixed dependent", Quantity: 2, PriceCents: 5, BudgetCents: 20})
	bot := v.Bots[0].ID
	v = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: p, Name: "Paper context"})
	v = agentApply(t, s, AgentCommand{Action: "task-ai-prepare", ProfileID: p, ID: v.Sessions[0].ID, BotID: bot, Name: "Paper gated by fixture", Text: "Compare inputs", IntervalSeconds: 60, MaxRuns: 2, Limits: &AILimits{24000, 1024, 1, 30000}, TotalBudgetMicrousd: 60000, AuthorizationSeconds: 3600})
	v = taskAIRun(t, s, taskAIAuthorize(t, s, v))
	if v.Bots[0].SpentCents != 0 {
		t.Fatal("paper step ran before AI")
	}
	if e := s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), p, "")
	if v.Bots[0].SpentCents != 10 || v.Runs[0].Quantity != 2 || v.Runs[0].PriceCents != 5 {
		t.Fatal("AI changed fixed inputs")
	}
	v = taskAIRun(t, s, v)
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	n, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer n.Close()
	if e = n.advanceAgentAI(t.Context(), fixtureAITransport{}, false); e != nil {
		t.Fatal(e)
	}
	v, _ = n.Agents(t.Context(), p, "")
	if v.Tasks[0].State != "held" || v.Bots[0].SpentCents != 10 || v.AIRequests[1].HeldMicrousd != 0 {
		t.Fatal("default-off restart dispatched queued AI")
	}
}

func TestTaskAIActualWorkerWithoutBrowser(t *testing.T) {
	s := fixtureStore(t)
	v := taskAIAuthorize(t, s, taskAIPlan(t, s, 1))
	tx, e := s.db.BeginTx(t.Context(), nil)
	if e != nil {
		t.Fatal(e)
	}
	d, r, e := readAgents(t.Context(), tx)
	if e != nil {
		t.Fatal(e)
	}
	d.Tasks[0].NextAt = 0
	if e = saveAgents(t.Context(), tx, d, r); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	server, e := StartAgentFixture(t.Context(), s.dir, "127.0.0.1:0", "", fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("disposable")}})
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close(context.Background())
	go func() { _ = server.Serve() }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, e := server.Store.Agents(t.Context(), v.ProfileID, "")
		if e != nil {
			t.Fatal(e)
		}
		if got.Tasks[0].State == "completed" {
			if len(got.Runs) != 1 || len(got.AIRequests) != 1 || got.AIRequests[0].State != "completed" || got.ProviderCallsEnabled {
				t.Fatal("invalid worker result")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("fixture worker did not complete scheduled task")
}

func TestTaskAIRefusesChangedWireContract(t *testing.T) {
	s := fixtureStore(t)
	v := taskAIAuthorize(t, s, taskAIPlan(t, s, 1))
	d, _, e := readAgents(t.Context(), s.db)
	if e != nil {
		t.Fatal(e)
	}
	// Simulate a plan consented under a previous wire format, not a new grant.
	d.Tasks[0].AI.Disclosure = `{"instructions":"older compiled contract"}`
	d.Tasks[0].AI.Digest = taskAIDigest(d.Tasks[0])
	if validateTaskAI(d, d.Tasks[0], s.now().Unix()) == nil {
		t.Fatal("updated wire silently inherited old consent", v.ProfileID)
	}
}

func TestTaskAIProfileContentionDefersWithoutNewDispatch(t *testing.T) {
	s := fixtureStore(t)
	v := taskAIAuthorize(t, s, taskAIPlan(t, s, 1))
	v = taskAIAuthorize(t, s, taskAIPlan(t, s, 1))
	tx, e := s.db.BeginTx(t.Context(), nil)
	if e != nil {
		t.Fatal(e)
	}
	d, r, e := readAgents(t.Context(), tx)
	if e != nil {
		t.Fatal(e)
	}
	for i := range d.Tasks {
		d.Tasks[i].NextAt = 0
	}
	if e = saveAgents(t.Context(), tx, d, r); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if e = s.runAgentTasks(t.Context(), true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if len(v.AIRequests) != 1 || v.Tasks[1].State != "scheduled" || v.Tasks[1].Runs != 0 {
		t.Fatal("busy profile failed another task or dispatched twice")
	}
	if e = s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	v = taskAIRun(t, s, v)
	if e = s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if len(v.AIRequests) != 2 || len(v.Runs) != 2 || v.Tasks[1].State != "completed" {
		t.Fatal("deferred task did not continue")
	}
}

func TestAgentAIMemorySelectionScheduledRefusal(t *testing.T) {
	s := fixtureStore(t)
	v := taskAIPlan(t, s, 1)
	q := v.Tasks[0]
	c := memorySelectionCommand(t, q.AI.SessionID, map[string]any{"mode": "none"})
	c.Action = "task-ai-prepare"
	c.Name = "Manual selection must not schedule"
	c.IntervalSeconds = 60
	c.MaxRuns = 1
	c.AuthorizationSeconds = 3600
	c.TotalBudgetMicrousd = 30000
	selectionRefused(t, s, c, false)
	d, _, _ := readAgents(t.Context(), s.db)
	legacy, err := aiContext(d, q.ProfileID, q.AI.SessionID, q.Text, s.now().Unix())
	if err != nil || legacy != q.AI.Context || strings.Contains(q.AI.Context, "memorySelection") {
		t.Fatal("scheduled memory policy changed", err)
	}
	v = taskAIRun(t, s, taskAIAuthorize(t, s, v))
	if err = s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), q.ProfileID, "")
	if v.Tasks[0].State != "completed" || v.AIRequests[0].State != "completed" {
		t.Fatal("legacy scheduled consent failed")
	}
}
