package core

import (
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/wenstrategy"
	"reflect"
	"strings"
	"testing"
	"time"
)

func runFixture(t *testing.T, scenario string, edit func(*wenstrategy.Document)) (*Store, *HTTPHandler, WENFixtureRunPrepare) {
	t.Helper()
	s, h, _, base := sessionFixture(t, "review", scenario)
	s.sessionFixture = true
	var doc wenstrategy.Document
	if e := json.Unmarshal([]byte(base.Source.Raw), &doc); e != nil {
		t.Fatal(e)
	}
	doc.Name = "Automatic fixture"
	doc.Execution.Mode = "bounded-auto"
	if edit != nil {
		edit(&doc)
	}
	raw, _ := json.Marshal(doc)
	lib, e := s.ChangeWENResearch(t.Context(), WENResearchCommand{Profile: base.Input.Profile, Revision: base.Input.LibraryRevision, Action: "save", Raw: string(raw)})
	if e != nil {
		t.Fatal(e)
	}
	v, e := s.WENFixtureRuns(t.Context(), base.Input.Profile)
	if e != nil {
		t.Fatal(e)
	}
	return s, h, WENFixtureRunPrepare{Schema: "wen-fixture-run-prepare/1", Profile: v.Profile, Revision: v.Revision, LibraryRevision: lib.Revision, StrategyDigest: lib.Versions[len(lib.Versions)-1].Digest, Name: "Automatic fixture", ScenarioID: base.Scenario.ID, ScenarioDigest: base.Scenario.Digest, FixtureWalletID: "fixture-wallet:alpha"}
}
func startRun(t *testing.T, h *HTTPHandler, in WENFixtureRunPrepare) (WENFixtureRunView, WENFixtureRunReview) {
	t.Helper()
	before := retainedRevisionState(t, h.store)
	r, e := h.ReviewWENFixtureRun(t.Context(), in)
	if e != nil {
		t.Fatal(e)
	}
	if before != retainedRevisionState(t, h.store) {
		t.Fatal("review wrote a run or grant")
	}
	v, e := h.StartWENFixtureRun(t.Context(), r)
	if e != nil {
		t.Fatal(e)
	}
	return v, r
}
func runView(t *testing.T, s *Store) WENFixtureRunView {
	t.Helper()
	v, e := s.WENFixtureRuns(t.Context(), "wen-operator")
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func runWake(t *testing.T, s *Store) WENFixtureRunView {
	t.Helper()
	if e := s.advanceWENFixtureRuns(t.Context()); e != nil {
		t.Fatal(e)
	}
	return runView(t, s)
}
func runControl(t *testing.T, h *HTTPHandler, v WENFixtureRunView, action string) WENFixtureRunView {
	t.Helper()
	r := v.Runs[0]
	p := v.Pools[0]
	out, e := h.ControlWENFixtureRun(t.Context(), WENFixtureRunControl{Schema: "wen-fixture-run-control/1", Profile: v.Profile, Revision: v.Revision, PoolRevision: p.Revision, RunID: r.Record.SessionID, Action: action})
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func TestWENFixtureRunSchedules(t *testing.T) {
	for _, scenario := range []string{"fixture-4h", "fixture-12h", "fixture-4h-stale", "fixture-4h-cutoff"} {
		t.Run(scenario, func(t *testing.T) {
			s, h, in := runFixture(t, scenario, func(d *wenstrategy.Document) {
				d.Schedule.EveryNthRound = 1
				d.Allocation.Mode = "conditional"
				d.Allocation.Rules = []wenstrategy.Rule{{When: []wenstrategy.Condition{{Metric: "returnBps", LookbackMinutes: 30, Operator: "gte", ValueBps: "10"}}, Preset: "custom", WeightsBps: []int{1000, 1000, 2000, 3000, 3000}}}
			})
			v, _ := startRun(t, h, in)
			if len(v.Runs[0].Operations) != 0 || v.Pools[0].Budget.TotalTurnoverLamports != "0" {
				t.Fatal("Start spent before the worker")
			}
			for n := 0; n < 170 && v.Runs[0].Record.Status != "complete"; n++ {
				v = runWake(t, s)
			}
			r := v.Runs[0]
			if r.Record.Status != "complete" || wenRunOutstanding(r) {
				t.Fatal("finite fixture did not complete", r.Record.Status, r.Reason)
			}
			b := v.Pools[0].Budget
			if scenario == "fixture-4h-stale" || scenario == "fixture-4h-cutoff" {
				if len(r.Operations) != 0 || b.TotalTurnoverLamports != "0" {
					t.Fatal("invalid/stale/cutoff fixture spent")
				}
			} else {
				if len(r.Operations) != 8 || b.TotalTurnoverLamports != "1600000000" || b.ActualDeductionsLamports != "80000000" || b.UserFeesPaidLamports != "8000" || b.UnsettledLamports != "0" {
					t.Fatal("native fixture accounting", len(r.Operations), b)
				}
				for _, row := range r.Decisions {
					if row.Phase == "acceptance" && !strings.HasPrefix(row.Record.Reason, "fixture-consented:") {
						t.Fatal("acceptance lacks consent identity")
					}
				}
			}
			original := retainedRevisionState(t, s)
			_ = runWake(t, s)
			if original != retainedRevisionState(t, s) {
				t.Fatal("terminal run replayed")
			}
			d, _, e := readAgents(t.Context(), s.db)
			if e != nil || len(d.Tasks) != 0 || len(d.Notices) != 1 {
				t.Fatal("second task engine or duplicated inbox", e)
			}
		})
	}
}
func TestWENFixtureRunNoGrantAndGuards(t *testing.T) {
	s, h, in := runFixture(t, "fixture-4h", nil)
	before := retainedRevisionState(t, s)
	s.sessionFixture = false
	if _, e := h.ReviewWENFixtureRun(t.Context(), in); e == nil {
		t.Fatal("ordinary core prepared automation")
	}
	if e := s.advanceWENFixtureRuns(t.Context()); e != nil {
		t.Fatal(e)
	}
	if before != retainedRevisionState(t, s) {
		t.Fatal("ordinary wake mutated fixture state")
	}
	s.sessionFixture = true
	r, e := h.ReviewWENFixtureRun(t.Context(), in)
	if e != nil {
		t.Fatal(e)
	}
	modified := r
	modified.Input.Name = "Changed"
	if _, e = h.StartWENFixtureRun(t.Context(), modified); e == nil {
		t.Fatal("changed wire accepted")
	}
	now := s.now()
	s.now = func() time.Time { return now.Add(120 * time.Second) }
	if _, e = h.StartWENFixtureRun(t.Context(), r); e == nil {
		t.Fatal("expired review accepted")
	}
	s.now = func() time.Time { return now }
	v, e := h.StartWENFixtureRun(t.Context(), r)
	if e != nil {
		t.Fatal(e)
	}
	first := retainedRevisionState(t, s)
	duplicate, e := h.StartWENFixtureRun(t.Context(), r)
	if e != nil || duplicate.Receipt.ID != v.Receipt.ID || first != retainedRevisionState(t, s) {
		t.Fatal("duplicate Start changed/requeued run", e)
	}
	v = runWake(t, s)
	s.now = func() time.Time { return now.Add(300 * time.Second) }
	v = runWake(t, s)
	if v.Runs[0].Record.Status != "held" || v.Pools[0].Budget.UnsettledLamports != "200000000" {
		t.Fatal("deadline released accepted hold", v.Runs[0].Reason)
	}
}
func TestWENFixtureRunSharedPool(t *testing.T) {
	s, h, in := runFixture(t, "fixture-4h", func(d *wenstrategy.Document) {
		d.Limits.PerRoundLamports = "12000000000"
		d.Limits.MaxUnsettledLamports = "20000000000"
		d.Schedule.EveryNthRound = 1
	})
	v, _ := startRun(t, h, in)
	in.Revision = v.Revision
	in.Name = "Second automatic fixture"
	v, _ = startRun(t, h, in)
	v = runWake(t, s)
	if len(v.Runs) != 2 || len(v.Runs[0].Operations) != 1 || len(v.Runs[1].Operations) != 0 || v.Pools[0].Budget.TotalTurnoverLamports != "12000000000" || v.Pools[0].Budget.AvailableLamports != "7999999000" {
		t.Fatal("automatic contention reset or double spent", v.Pools)
	}
	if v.Runs[1].Decisions[0].Record.Reason != "budget-unavailable" {
		t.Fatal("second run did not use the shared F1 limit")
	}
}
func TestWENFixtureRunUnknownRestart(t *testing.T) {
	s, h, in := runFixture(t, "fixture-4h-unknown", nil)
	v, review := startRun(t, h, in)
	v = runWake(t, s)
	v = runWake(t, s)
	if v.Runs[0].Record.Status != "held" || v.Runs[0].Operations[0].Status != "uncertain" {
		t.Fatal("uncertainty did not hold run")
	}
	old := v.Pools[0].Budget
	id := v.Runs[0].Record.SessionID
	dir := s.dir
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	next, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	nh, e := NewHTTPHandler(next, "http://127.0.0.1:7331", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	v = runView(t, next)
	if !reflect.DeepEqual(v.Pools[0].Budget, old) || v.Runs[0].Record.Status != "held" {
		t.Fatal("restart dropped original holds")
	}
	if _, e = nh.StartWENFixtureRun(t.Context(), review); e != nil {
		t.Fatal("stored exact Start receipt not readable", e)
	}
	before := retainedRevisionState(t, next)
	next.sessionFixture = true
	_ = runWake(t, next)
	if before != retainedRevisionState(t, next) {
		t.Fatal("restart replayed an old consent")
	}
	v = runControl(t, nh, runView(t, next), "stop")
	command := WENFixtureRunReconcile{Schema: "wen-fixture-run-reconcile/1", Profile: v.Profile, Revision: v.Revision, PoolRevision: v.Pools[0].Revision, RunID: id, EventID: "resolve:" + v.Runs[0].Operations[0].ID}
	v, e = nh.ReconcileWENFixtureRun(t.Context(), command)
	if e != nil {
		t.Fatal(e)
	}
	if v.Runs[0].Record.Status != "complete" || v.Pools[0].Budget.AvailableLamports != "20000000000" || v.Pools[0].Budget.UnsettledLamports != "0" {
		t.Fatal("original proven-unspent reconciliation", v.Pools)
	}
	original := retainedRevisionState(t, next)
	replay, e := nh.ReconcileWENFixtureRun(t.Context(), command)
	if e != nil || replay.Receipt.ID != v.Receipt.ID || retainedRevisionState(t, next) != original {
		t.Fatal("exact committed reconciliation retry lost its original receipt", e)
	}
	command.Revision++
	if _, e = nh.ReconcileWENFixtureRun(t.Context(), command); e == nil {
		t.Fatal("changed reconciliation command reused committed identity")
	}

}
func TestWENFixtureRunPauseEpochAndResume(t *testing.T) {
	s, h, in := runFixture(t, "fixture-4h", nil)
	v, _ := startRun(t, h, in)
	c := pauseCommand(t, s, 1)
	if _, e := s.UpdatePause(t.Context(), c); e != nil {
		t.Fatal(e)
	}
	if e := s.advanceWENFixtureRuns(t.Context()); !errors.Is(e, ErrCorePaused) {
		t.Fatal("pause not fenced", e)
	}
	if _, e := s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "release", ProbeID: update.NewProbe(), Aborted: aborted(c)}); e != nil {
		t.Fatal(e)
	}
	v = runWake(t, s)
	if v.Runs[0].Record.Status != "held" || len(v.Runs[0].Operations) != 0 {
		t.Fatal("release silently resumed old consent")
	}
	id := v.Runs[0].Record.SessionID
	in.RunID = &id
	in.Revision = v.Revision
	v, _ = startRun(t, h, in)
	if v.Runs[0].Deadline != v.Runs[0].FirstStartedAt+300 || len(v.Runs[0].ConsentHistory) != 2 {
		t.Fatal("resume reset deadline/history")
	}
	v = runWake(t, s)
	if len(v.Runs[0].Operations) != 1 {
		t.Fatal("explicit resumed consent did not admit one event")
	}
}
func TestWENFixtureRunConsentCapacity(t *testing.T) {
	_, h, in := runFixture(t, "fixture-4h", nil)
	v, _ := startRun(t, h, in)
	for i := 0; i < 7; i++ {
		v = runControl(t, h, v, "pause")
		id := v.Runs[0].Record.SessionID
		in.RunID = &id
		in.Revision = v.Revision
		v, _ = startRun(t, h, in)
	}
	v = runControl(t, h, v, "pause")
	in.Revision = v.Revision
	if _, e := h.ReviewWENFixtureRun(t.Context(), in); e == nil {
		t.Fatal("consent history pruned/reset")
	}
	if len(v.Runs[0].ConsentHistory) != 8 || v.Pools[0].Budget.AvailableLamports != "20000000000" {
		t.Fatal("consent capacity mutated capital")
	}
}
