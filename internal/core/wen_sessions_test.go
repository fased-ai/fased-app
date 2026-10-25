package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/wenstrategy"
)

func TestWENSessionRehearsalAdmission(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("storage-protection", func(t *testing.T) {
		if StateSchema < 14 {
			t.Fatal("rehearsal writes require schema 14")
		}
	})
	t.Run("shared-private-view", func(t *testing.T) {
		w := apiRequest(h, "GET", "/api/v1/wen/sessions?profile=wen-operator", "", token, "http://127.0.0.1:7331", "")
		if w.Code != 200 {
			t.Fatalf("rehearsal view: %d %s", w.Code, w.Body.String())
		}
	})
}

func TestWENSessionLifecycleSharedPool(t *testing.T) {
	s, h, _, r := sessionFixture(t, "review", "fixture-4h")
	var doc wenstrategy.Document
	json.Unmarshal([]byte(r.Source.Raw), &doc)
	doc.Name = "Large fixture"
	doc.Limits.PerRoundLamports = "12000000000"
	doc.Limits.MaxUnsettledLamports = "20000000000"
	raw, _ := json.Marshal(doc)
	lib, e := s.ChangeWENResearch(t.Context(), WENResearchCommand{Revision: r.Input.LibraryRevision, Profile: r.Input.Profile, Action: "save", Raw: string(raw)})
	if e != nil {
		t.Fatal(e)
	}
	r.Input.LibraryRevision = lib.Revision
	r.Input.StrategyDigest = lib.Versions[len(lib.Versions)-1].Digest
	r, e = h.ReviewWENSession(t.Context(), r.Input)
	if e != nil {
		t.Fatal(e)
	}
	v, e := h.CreateWENSession(t.Context(), r)
	if e != nil {
		t.Fatal(e)
	}
	id1 := v.Sessions[0].Record.SessionID
	in := r.Input
	in.Revision = v.Revision
	in.Name = "Second large fixture"
	r2, e := h.ReviewWENSession(t.Context(), in)
	if e != nil {
		t.Fatal(e)
	}
	v, e = h.CreateWENSession(t.Context(), r2)
	if e != nil {
		t.Fatal(e)
	}
	id2 := v.Sessions[1].Record.SessionID
	v = sessionStep(t, h, v, id1)
	v = sessionStep(t, h, v, id2)
	p := sessionPool(t, v)
	commands := []WENSessionControl{}
	for _, id := range []string{id1, id2} {
		var session WENSession
		for _, ss := range v.Sessions {
			if ss.Record.SessionID == id {
				session = ss
			}
		}
		a, e := h.ReviewWENSessionAction(t.Context(), WENSessionActionInput{Schema: "wen-session-action-review/1", Profile: v.Profile, Revision: v.Revision, PoolRevision: p.Revision, SessionID: id, DecisionID: session.Pending.DecisionID})
		if e != nil {
			t.Fatal(e)
		}
		commands = append(commands, WENSessionControl{Schema: "wen-session-control/1", Profile: v.Profile, Revision: v.Revision, PoolRevision: p.Revision, SessionID: id, Action: "simulate-accept", DecisionID: &a.Offer.DecisionID, ReviewID: &a.ReviewID, Witness: &a.Witness})
	}
	results := make(chan error, 2)
	for _, c := range commands {
		go func(c WENSessionControl) { _, err := h.ControlWENSession(t.Context(), c); results <- err }(c)
	}
	ok := 0
	for range 2 {
		if e = <-results; e == nil {
			ok++
		} else if !errors.Is(e, ErrConflict) {
			t.Fatal(e)
		}
	}
	if ok != 1 {
		t.Fatal("shadow double admission", ok)
	}
	v, e = s.WENSessions(t.Context(), v.Profile)
	if e != nil {
		t.Fatal(e)
	}
	p = sessionPool(t, v)
	if p.Revision != 1 || p.Budget.TotalTurnoverLamports != "12000000000" || p.Budget.AvailableLamports != "7999999000" {
		t.Fatal("shared pool reset/double spending", p)
	}
	for _, ss := range v.Sessions {
		if ss.Pending != nil {
			if _, e = h.ReviewWENSessionAction(t.Context(), WENSessionActionInput{Schema: "wen-session-action-review/1", Profile: v.Profile, Revision: v.Revision, PoolRevision: p.Revision, SessionID: ss.Record.SessionID, DecisionID: ss.Pending.DecisionID}); e == nil {
				t.Fatal("insufficient remaining pool became executable")
			}
		}
	}
}

func TestWENSessionRehearsalF1Parity(t *testing.T) {
	var doc wenstrategy.Document
	json.Unmarshal([]byte(researchRaw(t)), &doc)
	doc.Execution = wenstrategy.Execution{Mode: "review", Runner: "fased"}
	doc.Schedule.EveryNthRound = 1
	doc.Limits.PerRoundLamports = "200000000"
	doc.Allocation.Mode = "conditional"
	doc.Allocation.Rules = []wenstrategy.Rule{{When: []wenstrategy.Condition{{Metric: "returnBps", LookbackMinutes: 30, Operator: "gte", ValueBps: "10"}}, Preset: "custom", WeightsBps: []int{1000, 1000, 2000, 3000, 3000}}}
	type vector struct {
		Raw         string                          `json:"raw"`
		Slot        wenstrategy.Slot                `json:"slot"`
		State       wenstrategy.ScheduleState       `json:"state"`
		Pin         []string                        `json:"pin"`
		Metrics     []wenstrategy.Metric            `json:"metrics"`
		At          string                          `json:"at"`
		Budget      wenstrategy.BudgetState         `json:"budget"`
		Reservation wenstrategy.Reservation         `json:"reservation"`
		Expected    wenstrategy.RehearsalEvaluation `json:"expected"`
	}
	cases := []vector{}
	for _, id := range []string{"fixture-4h", "fixture-4h-stale"} {
		book, _ := wenScenario(id)
		for i := range 3 {
			_, at, _ := wenSlotTimes(book, i)
			metrics := wenFixtureMetrics(doc, book, i, at)
			budget := wenPool("fixture-wallet:alpha").Budget
			reservation := wenSessionReservation(doc)
			expected, e := wenstrategy.Rehearse(doc, book.Slots[i], wenstrategy.ScheduleState{}, nil, metrics, at.Format(time.RFC3339), budget, reservation)
			if e != nil {
				t.Fatal(e)
			}
			raw, _ := json.Marshal(doc)
			cases = append(cases, vector{string(raw), book.Slots[i], wenstrategy.ScheduleState{}, []string{}, metrics, at.Format(time.RFC3339), budget, reservation, expected})
		}
	}
	root := filepath.Join("..", "..", ".evidence", "f4-rehearsal")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.MarshalIndent(cases, "", "  ")
	if e := os.WriteFile(filepath.Join(root, "f1-vectors.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
}

func sessionFixture(t *testing.T, mode, scenario string) (*Store, *HTTPHandler, string, WENSessionReview) {
	t.Helper()
	s := fixtureStore(t)
	token := claimFixture(t, s)
	h, e := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	var doc wenstrategy.Document
	if json.Unmarshal([]byte(researchRaw(t)), &doc) != nil {
		t.Fatal("fixture source")
	}
	book, e := wenScenario(scenario)
	if e != nil {
		t.Fatal(e)
	}
	doc.Execution = wenstrategy.Execution{Mode: mode, Runner: "fased"}
	doc.Schedule.ExpiresAt = book.ExpiresAt
	doc.Limits.PerRoundLamports = "200000000"
	raw, _ := json.Marshal(doc)
	lib, e := s.ChangeWENResearch(t.Context(), WENResearchCommand{Profile: "wen-operator", Action: "save", Raw: string(raw)})
	if e != nil {
		t.Fatal(e)
	}
	_, rev, e := readAgents(t.Context(), s.db)
	if e != nil {
		t.Fatal(e)
	}
	before := retainedRevisionState(t, s)
	review, e := h.ReviewWENSession(t.Context(), WENSessionPrepare{Schema: "wen-session-prepare/1", Profile: "wen-operator", Revision: rev, LibraryRevision: lib.Revision, StrategyDigest: lib.Versions[0].Digest, Name: "Fixture " + mode, Mode: mode, ScenarioID: book.ID, ScenarioDigest: book.Digest, FixtureWalletID: "fixture-wallet:alpha"})
	if e != nil {
		t.Fatal(e)
	}
	if before != retainedRevisionState(t, s) {
		t.Fatal("preparation wrote state")
	}
	return s, h, token, review
}
func sessionPool(t *testing.T, v WENSessionView) WENSessionPool {
	t.Helper()
	for _, p := range v.Pools {
		if p.ID == "fixture-wallet:alpha" {
			return p
		}
	}
	t.Fatal("missing pool")
	return WENSessionPool{}
}
func sessionStep(t *testing.T, h *HTTPHandler, v WENSessionView, id string) WENSessionView {
	t.Helper()
	p := sessionPool(t, v)
	out, e := h.StepWENSession(t.Context(), WENSessionStep{Schema: "wen-session-step/1", Profile: v.Profile, Revision: v.Revision, PoolRevision: p.Revision, SessionID: id, EventID: v.NextEvents[id]})
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func sessionControl(t *testing.T, h *HTTPHandler, v WENSessionView, id, action string) WENSessionView {
	t.Helper()
	p := sessionPool(t, v)
	r, e := h.ControlWENSession(t.Context(), WENSessionControl{Schema: "wen-session-control/1", Profile: v.Profile, Revision: v.Revision, PoolRevision: p.Revision, SessionID: id, Action: action})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func sessionAcceptance(t *testing.T, h *HTTPHandler, v WENSessionView, id string) (WENSessionView, WENSessionControl) {
	t.Helper()
	var selected WENSession
	for _, r := range v.Sessions {
		if r.Record.SessionID == id {
			selected = r
		}
	}
	if selected.Pending == nil {
		t.Fatal("no pending offer")
	}
	p := sessionPool(t, v)
	a, e := h.ReviewWENSessionAction(t.Context(), WENSessionActionInput{Schema: "wen-session-action-review/1", Profile: v.Profile, Revision: v.Revision, PoolRevision: p.Revision, SessionID: id, DecisionID: selected.Pending.DecisionID})
	if e != nil {
		t.Fatal(e)
	}
	c := WENSessionControl{Schema: "wen-session-control/1", Profile: v.Profile, Revision: v.Revision, PoolRevision: p.Revision, SessionID: id, Action: "simulate-accept", DecisionID: &a.Offer.DecisionID, ReviewID: &a.ReviewID, Witness: &a.Witness}
	out, e := h.ControlWENSession(t.Context(), c)
	if e != nil {
		t.Fatal(e)
	}
	return out, c
}
func TestWENSessionRehearsalSchedules(t *testing.T) {
	for _, hours := range []int{4, 12} {
		for _, mode := range []string{"notify", "review"} {
			t.Run(fmt.Sprintf("%dh-%s", hours, mode), func(t *testing.T) {
				s, h, _, review := sessionFixture(t, mode, fmt.Sprintf("fixture-%dh", hours))
				before, _ := s.UpdateSnapshot(t.Context(), update.NewProbe())
				v, e := h.CreateWENSession(t.Context(), review)
				if e != nil {
					t.Fatal(e)
				}
				id := v.Sessions[0].Record.SessionID
				duplicate, e := h.CreateWENSession(t.Context(), review)
				if e != nil || duplicate.Revision != v.Revision {
					t.Fatal("duplicate create", e)
				}
				for n := 0; n < hours*6*4+3 && v.Sessions[0].Record.Status != "complete"; n++ {
					if v.Sessions[0].Pending != nil && mode == "review" {
						v, _ = sessionAcceptance(t, h, v, id)
					} else {
						v = sessionStep(t, h, v, id)
					}
				}
				if v.Sessions[0].Record.Status != "complete" {
					t.Fatal("finite rehearsal did not complete")
				}
				p := sessionPool(t, v)
				if mode == "notify" {
					if p.Budget.AvailableLamports != "20000000000" || v.Sessions[0].Schedule.AcceptedRounds != 0 {
						t.Fatal("notification spent capital")
					}
				} else {
					if v.Sessions[0].Schedule.AcceptedRounds != 8 || p.Budget.TotalTurnoverLamports != "1600000000" || p.Budget.ActualDeductionsLamports != "80000000" || p.Budget.UserFeesPaidLamports != "8000" || p.Budget.UnsettledLamports != "0" {
						t.Fatalf("wrong canonical shadow accounting: %+v", p)
					}
				}
				rows := v.Sessions[0].Decisions
				for _, row := range rows {
					if row.Record.StrategyDigest != review.Source.Digest || row.Record.MandateID != nil {
						t.Fatal("lineage/grant")
					}
					if row.Phase == "evaluation" && row.Record.Action == "hold" && row.Record.Before != row.Record.After {
						t.Fatal("offer reserved")
					}
				}
				after, e := s.UpdateSnapshot(t.Context(), update.NewProbe())
				if e != nil || before.State.Anchor == after.State.Anchor {
					t.Fatal("snapshot omitted rehearsal", e)
				}
				d, _, _ := readAgents(t.Context(), s.db)
				if len(d.Tasks) != 0 || len(d.AIRequests) != 0 || len(d.Notices) != 1 {
					t.Fatal("session created task/model or duplicate notices")
				}
			})
		}
	}
}
func TestWENSessionLifecycleIdentityAndHolds(t *testing.T) {
	s, h, _, review := sessionFixture(t, "review", "fixture-4h-unknown")
	v, e := h.CreateWENSession(t.Context(), review)
	if e != nil {
		t.Fatal(e)
	}
	id := v.Sessions[0].Record.SessionID
	v = sessionStep(t, h, v, id)
	if v.Sessions[0].Schedule.AcceptedRounds != 0 || sessionPool(t, v).Revision != 0 {
		t.Fatal("review counted as acceptance")
	}
	v, accept := sessionAcceptance(t, h, v, id)
	original := *v.Receipt
	dup, e := h.ControlWENSession(t.Context(), accept)
	if e != nil || *dup.Receipt != original || dup.Revision != v.Revision {
		t.Fatal("acceptance duplicate", e)
	}
	changed := accept
	changed.Witness = new(string)
	if _, e = h.ControlWENSession(t.Context(), changed); e == nil {
		t.Fatal("changed bytes reused accepted identity")
	}
	v = sessionStep(t, h, v, id)
	if v.Sessions[0].Operations[0].Status != "uncertain" {
		t.Fatal("missing uncertainty")
	}
	holds := sessionPool(t, v).Budget
	dir := s.dir
	oldNow := s.now
	s.Close()
	n, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer n.Close()
	n.now = oldNow
	h, e = NewHTTPHandler(n, "http://127.0.0.1:7331", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	v, e = n.WENSessions(t.Context(), "wen-operator")
	if e != nil || v.Sessions[0].Record.Status != "held" || sessionPool(t, v).Budget != holds {
		t.Fatal("restart erased hold", e)
	}
	v = sessionControl(t, h, v, id, "stop")
	if v.Sessions[0].Record.Status == "complete" {
		t.Fatal("stop released unknown")
	}
	v = sessionStep(t, h, v, id)
	if v.Sessions[0].Record.Status != "complete" || v.Sessions[0].CompletionReason != "stopped" || sessionPool(t, v).Budget.AvailableLamports != "20000000000" {
		t.Fatal("unspent recovery", v.Sessions[0].Reason)
	}
	if sessionPool(t, v).Budget.TotalTurnoverLamports != "200000000" {
		t.Fatal("unspent proof reset consumed admission")
	}
	dup, e = h.ControlWENSession(t.Context(), accept)
	if e != nil || *dup.Receipt != original {
		t.Fatal("restart lost original receipt", e)
	}
}
func TestWENSessionStorageMigration(t *testing.T) {
	t.Run("format-only-and-paused", func(t *testing.T) {
		s := fixtureStore(t)
		dropEmptyHistoryFixture(t, s)
		_, e := s.db.Exec("PRAGMA user_version=13")
		if e != nil {
			t.Fatal(e)
		}
		before := retainedRevisionState(t, s)
		dir := s.dir
		s.Close()
		n, e := OpenStore(dir)
		if e != nil {
			t.Fatal(e)
		}
		defer n.Close()
		if before != retainedRevisionState(t, n) {
			t.Fatal("format-only migration rewrote records/revisions")
		}
		pause := pauseCommand(t, n, 1)
		if _, e = n.UpdatePause(t.Context(), pause); e != nil {
			t.Fatal(e)
		}
		n.db.Exec("PRAGMA user_version=13")
		n.Close()
		if _, e = OpenStore(dir); !errors.Is(e, ErrCorePaused) {
			t.Fatal("paused migration", e)
		}
	})
	t.Run("unprotected-new-data-refused", func(t *testing.T) {
		s, h, _, r := sessionFixture(t, "review", "fixture-4h")
		if _, e := h.CreateWENSession(t.Context(), r); e != nil {
			t.Fatal(e)
		}
		s.db.Exec("PRAGMA user_version=13")
		dir := s.dir
		s.Close()
		if _, e := OpenStore(dir); e == nil || !strings.Contains(e.Error(), "without schema protection") {
			t.Fatal("unprotected data admitted", e)
		}
	})
}

func TestWENSessionRehearsalGuards(t *testing.T) {
	for _, mode := range []string{"witness", "expired-create", "source", "pool-reset", "pause", "action-expiry", "backward-clock", "exact-cutoff", "foreign", "strict-null", "strict-unknown", "auth", "capacity"} {
		t.Run(mode, func(t *testing.T) {
			s, h, token, review := sessionFixture(t, "review", "fixture-4h")
			before := retainedRevisionState(t, s)
			switch mode {
			case "witness":
				review.Witness += "changed"
				if _, e := h.CreateWENSession(t.Context(), review); e == nil {
					t.Fatal("changed review")
				}
				return
			case "expired-create":
				s.now = func() time.Time { return time.Unix(review.ExpiresAt, 0) }
				if _, e := h.CreateWENSession(t.Context(), review); e == nil {
					t.Fatal("expiry")
				}
				return
			case "source":
				s.db.Exec("UPDATE wen_research SET revision=revision+1")
				if _, e := h.CreateWENSession(t.Context(), review); !errors.Is(e, ErrConflict) {
					t.Fatal("source revision", e)
				}
				return
			case "strict-null", "strict-unknown", "auth":
				raw, _ := json.Marshal(review.Input)
				body := string(raw)
				auth, csrf := token, csrfToken(token)
				if mode == "strict-null" {
					body = strings.Replace(body, `"revision":0`, `"revision":null`, 1)
				}
				if mode == "strict-unknown" {
					body = strings.TrimSuffix(body, "}") + `,"rpc":"https://not-admitted.example"}`
				}
				if mode == "auth" {
					auth = ""
					csrf = ""
				}
				w := apiRequest(h, "POST", "/api/v1/wen/sessions/review", body, auth, "http://127.0.0.1:7331", csrf)
				if w.Code == 200 || before != retainedRevisionState(t, s) {
					t.Fatal("unsafe input wrote", w.Code, w.Body.String())
				}
				return
			case "capacity":
				d, rev, _ := readAgents(t.Context(), s.db)
				for i := 0; i < 130; i++ {
					d.Messages = append(d.Messages, AgentMessage{ID: fmt.Sprint(i), ProfileID: "wen-operator", SessionID: "fixture", Role: "user", Text: strings.Repeat("x", 3500)})
				}
				tx, _ := s.db.Begin()
				if e := saveAgents(t.Context(), tx, d, rev); e != nil {
					t.Fatal(e)
				}
				tx.Commit()
				_, rev, _ = readAgents(t.Context(), s.db)
				book, _ := wenScenario("fixture-4h")
				in := review.Input
				in.Revision = rev
				in.ScenarioDigest = book.Digest
				if _, e := h.ReviewWENSession(t.Context(), in); e == nil {
					t.Fatal("capacity not reserved")
				}
				return
			}
			v, e := h.CreateWENSession(t.Context(), review)
			if e != nil {
				t.Fatal(e)
			}
			id := v.Sessions[0].Record.SessionID
			if mode == "pool-reset" {
				_, rev, _ := readAgents(t.Context(), s.db)
				in := review.Input
				in.Revision = rev
				in.Name = "Second fixture"
				r, e := h.ReviewWENSession(t.Context(), in)
				if e != nil {
					t.Fatal(e)
				}
				r.Pool.Budget.AvailableLamports = "99999999999"
				if _, e = h.CreateWENSession(t.Context(), r); e == nil {
					t.Fatal("pool reset accepted")
				}
				return
			}
			if mode == "pause" {
				pause := pauseCommand(t, s, 1)
				if _, e = s.UpdatePause(t.Context(), pause); e != nil {
					t.Fatal(e)
				}
				if _, e = h.StepWENSession(t.Context(), WENSessionStep{Schema: "wen-session-step/1", Profile: v.Profile, Revision: v.Revision, PoolRevision: 0, SessionID: id, EventID: v.NextEvents[id]}); !errors.Is(e, ErrCorePaused) {
					t.Fatal("pause", e)
				}
				return
			}
			if mode == "foreign" {
				if _, e = s.WENSessions(t.Context(), "news-researcher"); e != nil {
					t.Fatal(e)
				}
				if _, e = h.StepWENSession(t.Context(), WENSessionStep{Schema: "wen-session-step/1", Profile: "news-researcher", Revision: v.Revision, SessionID: id, EventID: v.NextEvents[id]}); e == nil {
					t.Fatal("foreign session admitted")
				}
				return
			}
			v = sessionStep(t, h, v, id)
			if mode == "backward-clock" {
				now := s.now()
				s.now = func() time.Time { return now.Add(-time.Second) }
			}
			if mode == "action-expiry" {
				now := s.now()
				s.now = func() time.Time { return now.Add(120 * time.Second) }
			}
			if mode == "exact-cutoff" {
				d, rev, _ := readAgents(t.Context(), s.db)
				d.WENRehearsal.Sessions[0].ClockAt = d.WENRehearsal.Sessions[0].Pending.Cutoff
				tx, _ := s.db.Begin()
				if e = saveAgents(t.Context(), tx, d, rev); e != nil {
					t.Fatal(e)
				}
				tx.Commit()
				v, _ = s.WENSessions(t.Context(), v.Profile)
			}
			p := sessionPool(t, v)
			_, e = h.ReviewWENSessionAction(t.Context(), WENSessionActionInput{Schema: "wen-session-action-review/1", Profile: v.Profile, Revision: v.Revision, PoolRevision: p.Revision, SessionID: id, DecisionID: v.Sessions[0].Pending.DecisionID})
			if e == nil {
				t.Fatal("unsafe expired/clock action admitted")
			}
		})
	}
}

func TestWENSessionLifecycleSourceHoldCanStop(t *testing.T) {
	s, h, _, review := sessionFixture(t, "review", "fixture-4h")
	v, e := h.CreateWENSession(t.Context(), review)
	if e != nil {
		t.Fatal(e)
	}
	id := v.Sessions[0].Record.SessionID
	v = sessionStep(t, h, v, id)
	v, _ = sessionAcceptance(t, h, v, id)
	held := sessionPool(t, v).Budget
	var original string
	s.db.QueryRow("SELECT document FROM wen_research WHERE id=1").Scan(&original)
	s.db.Exec("UPDATE wen_research SET document='{}',revision=revision+1 WHERE id=1")
	v, e = s.WENSessions(t.Context(), v.Profile)
	if e != nil || v.Sessions[0].Record.Status != "held" {
		t.Fatal("missing source not held", e)
	}
	v = sessionControl(t, h, v, id, "stop")
	if sessionPool(t, v).Budget != held || v.Sessions[0].Record.Status != "held" {
		t.Fatal("stop erased accepted source hold")
	}
	s.db.Exec("UPDATE wen_research SET document=?,revision=revision+1 WHERE id=1", original)
	v, e = s.WENSessions(t.Context(), v.Profile)
	if e != nil {
		t.Fatal(e)
	}
	v = sessionStep(t, h, v, id)
	if v.Sessions[0].Record.Status != "complete" {
		t.Fatal("explicit restored-source reconciliation failed")
	}
}

func TestWENSessionLifecycleBackwardAckDoesNotRenew(t *testing.T) {
	s, h, _, review := sessionFixture(t, "review", "fixture-4h")
	v, e := h.CreateWENSession(t.Context(), review)
	if e != nil {
		t.Fatal(e)
	}
	id := v.Sessions[0].Record.SessionID
	v = sessionStep(t, h, v, id)
	originalWall := s.now()
	s.now = func() time.Time { return originalWall.Add(-time.Second) }
	decision := v.Sessions[0].Pending.DecisionID
	v, e = h.ControlWENSession(t.Context(), WENSessionControl{Schema: "wen-session-control/1", Profile: v.Profile, Revision: v.Revision, PoolRevision: sessionPool(t, v).Revision, SessionID: id, Action: "acknowledge", DecisionID: &decision})
	if e != nil {
		t.Fatal("safe acknowledgement should remain available", e)
	}
	d, _, e := readAgents(t.Context(), s.db)
	if e != nil {
		t.Fatal(e)
	}
	if d.WENRehearsal.Sessions[0].LastWallAt != originalWall.Unix() {
		t.Fatal("safe control lowered original clock guard")
	}
	if _, e = h.ReviewWENSessionAction(t.Context(), WENSessionActionInput{Schema: "wen-session-action-review/1", Profile: v.Profile, Revision: v.Revision, PoolRevision: sessionPool(t, v).Revision, SessionID: id, DecisionID: decision}); e == nil {
		t.Fatal("acknowledgement renewed an old proposal under a backward clock")
	}
}
