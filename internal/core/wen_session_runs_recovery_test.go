package core

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/wenstrategy"
)

func TestWENFixtureRunStopBeforeUncertainty(t *testing.T) {
	s, h, in := runFixture(t, "fixture-4h-unknown", nil)
	v, _ := startRun(t, h, in)
	v = runWake(t, s)
	v = runControl(t, h, v, "stop")
	id := v.Runs[0].Record.SessionID
	for _, phase := range []string{"uncertain:", "resolve:"} {
		c := WENFixtureRunReconcile{Schema: "wen-fixture-run-reconcile/1", Profile: v.Profile, Revision: v.Revision, PoolRevision: v.Pools[0].Revision, RunID: id, EventID: phase + v.Runs[0].Operations[0].ID}
		var e error
		v, e = h.ReconcileWENFixtureRun(t.Context(), c)
		if e != nil {
			t.Fatal("stopped original operation cannot be reconciled", phase, e)
		}
		if phase == "uncertain:" && (v.Runs[0].Record.Status != "held" || v.Pools[0].Budget.UnsettledLamports != "200000000") {
			t.Fatal("uncertainty classification released a hold")
		}
	}
	if v.Runs[0].Record.Status != "complete" || len(v.Runs[0].Operations) != 1 || v.Pools[0].Budget.AvailableLamports != "20000000000" {
		t.Fatal("stopped fixture was replayed or stranded")
	}
}

func TestWENFixtureRunSourceAndClockFences(t *testing.T) {
	for _, cause := range []string{"source-missing", "clock-backward"} {
		t.Run(cause, func(t *testing.T) {
			s, h, in := runFixture(t, "fixture-4h", nil)
			v, _ := startRun(t, h, in)
			v = runWake(t, s)
			hold := v.Pools[0].Budget
			last := v.Runs[0].LastWallAt
			if cause == "source-missing" {
				if _, e := s.db.Exec("UPDATE wen_research SET document='{}', revision=revision+1 WHERE id=1"); e != nil {
					t.Fatal(e)
				}
			} else {
				s.now = func() time.Time { return time.Unix(last-1, 0) }
			}
			v = runWake(t, s)
			if v.Runs[0].Record.Status != "held" || !reflect.DeepEqual(v.Pools[0].Budget, hold) {
				t.Fatal("admission fence changed capital")
			}
			v = runControl(t, h, v, "stop")
			if v.Runs[0].LastWallAt < last {
				t.Fatal("stop lowered backward-clock fence")
			}
			c := WENFixtureRunReconcile{Schema: "wen-fixture-run-reconcile/1", Profile: v.Profile, Revision: v.Revision, PoolRevision: v.Pools[0].Revision, RunID: v.Runs[0].Record.SessionID, EventID: "resolve:" + v.Runs[0].Operations[0].ID}
			v, e := h.ReconcileWENFixtureRun(t.Context(), c)
			if e != nil || v.Pools[0].Budget.UnsettledLamports != "0" || v.Runs[0].Record.Status != "complete" {
				t.Fatal("retained original recovery failed", e)
			}
		})
	}
}

func TestWENFixtureRunManualContention(t *testing.T) {
	s, h, in := runFixture(t, "fixture-4h", func(d *wenstrategy.Document) {
		d.Limits.PerRoundLamports = "12000000000"
		d.Limits.MaxUnsettledLamports = "20000000000"
	})
	v, automatic := startRun(t, h, in)
	var doc wenstrategy.Document
	json.Unmarshal([]byte(automatic.Source.Raw), &doc)
	doc.Execution.Mode = "review"
	doc.Name = "Manual contention"
	raw, _ := json.Marshal(doc)
	lib, e := s.ChangeWENResearch(t.Context(), WENResearchCommand{Profile: in.Profile, Revision: in.LibraryRevision, Action: "save", Raw: string(raw)})
	if e != nil {
		t.Fatal(e)
	}
	manual, e := h.ReviewWENSession(t.Context(), WENSessionPrepare{Schema: "wen-session-prepare/1", Profile: in.Profile, Revision: v.Revision, LibraryRevision: lib.Revision, StrategyDigest: lib.Versions[len(lib.Versions)-1].Digest, Name: doc.Name, Mode: "review", ScenarioID: in.ScenarioID, ScenarioDigest: in.ScenarioDigest, FixtureWalletID: in.FixtureWalletID})
	if e != nil {
		t.Fatal(e)
	}
	m, e := h.CreateWENSession(t.Context(), manual)
	if e != nil {
		t.Fatal(e)
	}
	m = sessionStep(t, h, m, m.Sessions[0].Record.SessionID)
	offer := m.Sessions[0].Pending
	a, e := h.ReviewWENSessionAction(t.Context(), WENSessionActionInput{Schema: "wen-session-action-review/1", Profile: m.Profile, Revision: m.Revision, PoolRevision: m.Pools[0].Revision, SessionID: m.Sessions[0].Record.SessionID, DecisionID: offer.DecisionID})
	if e != nil {
		t.Fatal(e)
	}
	c := WENSessionControl{Schema: "wen-session-control/1", Profile: m.Profile, Revision: m.Revision, PoolRevision: m.Pools[0].Revision, SessionID: m.Sessions[0].Record.SessionID, Action: "simulate-accept", DecisionID: &offer.DecisionID, ReviewID: &a.ReviewID, Witness: &a.Witness}
	results := make(chan error, 2)
	go func() { results <- s.advanceWENFixtureRuns(t.Context()) }()
	go func() { _, err := h.ControlWENSession(t.Context(), c); results <- err }()
	for range 2 {
		if e := <-results; e != nil && !errors.Is(e, ErrConflict) {
			t.Fatal(e)
		}
	}
	v = runView(t, s)
	m, e = s.WENSessions(t.Context(), m.Profile)
	if e != nil {
		t.Fatal(e)
	}
	if len(m.Sessions[0].Operations)+len(v.Runs[0].Operations) != 1 || v.Pools[0].Budget.TotalTurnoverLamports != "12000000000" || v.Pools[0].Budget.AvailableLamports != "7999999000" {
		t.Fatal("manual/automatic pool double spent", v.Pools)
	}
}

func TestWENFixtureRunMigration(t *testing.T) {
	for _, mode := range []string{"original", "paused", "unprotected-runs"} {
		t.Run(mode, func(t *testing.T) {
			s, h, _, r := sessionFixture(t, "review", "fixture-4h")
			if _, e := h.CreateWENSession(t.Context(), r); e != nil {
				t.Fatal(e)
			}
			if mode == "paused" {
				c := pauseCommand(t, s, 1)
				if _, e := s.UpdatePause(t.Context(), c); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "unprotected-runs" {
				if _, e := s.db.Exec("UPDATE agent_workspace SET document=json_set(document, '$.wenRehearsal.runs', json('[]')) WHERE id=1"); e != nil {
					t.Fatal(e)
				}
			}
			before := retainedRevisionState(t, s)
			if _, e := s.db.Exec("PRAGMA user_version=14"); e != nil {
				t.Fatal(e)
			}
			if mode == "original" {
				if e := migrateWENFixtureRuns(t.Context(), s.db); e != nil {
					t.Fatal(e)
				}
				if before != retainedRevisionState(t, s) {
					t.Fatal("format-only migration changed original records")
				}
				var version int
				s.db.QueryRow("PRAGMA user_version").Scan(&version)
				if version != 15 {
					t.Fatal("unprotected format")
				}
				return
			}
			dir := s.dir
			if e := s.Close(); e != nil {
				t.Fatal(e)
			}
			n, e := OpenStore(dir)
			if mode != "original" {
				if e == nil {
					n.Close()
					t.Fatal("unsafe migration accepted", mode)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			defer n.Close()
			if before != retainedRevisionState(t, n) {
				t.Fatal("format-only migration changed manual records or revisions")
			}
			var version int
			n.db.QueryRow("PRAGMA user_version").Scan(&version)
			if version != 15 {
				t.Fatal("automatic storage unprotected")
			}
		})
	}
}

func TestWENFixtureRunStrictWire(t *testing.T) {
	_, h, in := runFixture(t, "fixture-4h", nil)
	r, e := h.ReviewWENFixtureRun(t.Context(), in)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(r)
	var decoded WENFixtureRunReview
	if e := DecodeWENFixtureRun(raw, &decoded); e != nil {
		t.Fatal("complete native review rejected", e)
	}
	for _, missing := range []string{"source", "pool", "capability"} {
		var wire map[string]json.RawMessage
		json.Unmarshal(raw, &wire)
		var nested map[string]json.RawMessage
		json.Unmarshal(wire[missing], &nested)
		for key := range nested {
			delete(nested, key)
			break
		}
		wire[missing], _ = json.Marshal(nested)
		bad, _ := json.Marshal(wire)
		if DecodeWENFixtureRun(bad, &decoded) == nil {
			t.Fatal("partial nested evidence accepted", missing)
		}
	}
	for _, bad := range []string{strings.Replace(string(raw), `"executable":false`, `"executable":null`, 1), strings.Replace(string(raw), `"schema":`, `"Schema":`, 1), `null`, `{}`} {
		if DecodeWENFixtureRun([]byte(bad), &decoded) == nil {
			t.Fatal("unsafe exact wire accepted")
		}
	}
}

func TestWENFixtureRunStorageSnapshot(t *testing.T) {
	s, h, in := runFixture(t, "fixture-4h-unknown", nil)
	_, _ = startRun(t, h, in)
	_ = runWake(t, s)
	_ = runWake(t, s)
	proof := pausedBackup(t, s)
	if _, e := s.BackupMetadata(t.Context(), proof); e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("sqlite", "file:"+filepath.Join(s.dir, "lifecycle", proof.IntentID, "metadata.sqlite")+"?mode=ro&immutable=1")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var live, saved string
	s.db.QueryRow("SELECT document || ':' || revision FROM agent_workspace WHERE id=1").Scan(&live)
	db.QueryRow("SELECT document || ':' || revision FROM agent_workspace WHERE id=1").Scan(&saved)
	if live != saved {
		t.Fatal("backup lost original run, consent or native hold")
	}
	d, _, e := readAgents(t.Context(), db)
	if e != nil || d.WENRehearsal.Runs[0].Operations[0].Status != "uncertain" || d.WENRehearsal.Pools[0].Budget.UnsettledLamports != "200000000" {
		t.Fatal("snapshot omitted uncertain operation", e)
	}
}

func TestWENFixtureRunPrecommitFence(t *testing.T) {
	s, h, in := runFixture(t, "fixture-4h", nil)
	v, _ := startRun(t, h, in)
	before := retainedRevisionState(t, s)
	now := s.now()
	calls := 0
	s.now = func() time.Time {
		calls++
		if calls >= 5 {
			return time.Unix(v.Runs[0].Deadline, 0)
		}
		return now
	}
	if e := s.advanceWENFixtureRuns(t.Context()); e == nil {
		t.Fatal("deadline crossing committed an acceptance")
	}
	if before != retainedRevisionState(t, s) {
		t.Fatal("failed deadline fence left partial ledger/cursor/receipt changes")
	}
}

func TestWENFixtureRunCombinedCapacity(t *testing.T) {
	s, h, in := runFixture(t, "fixture-4h-stale", nil)
	lib, e := s.WENResearch(t.Context(), in.Profile)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		v := runView(t, s)
		r, e := h.ReviewWENSession(t.Context(), WENSessionPrepare{Schema: "wen-session-prepare/1", Profile: in.Profile, Revision: v.Revision, LibraryRevision: lib.Revision, StrategyDigest: lib.Versions[0].Digest, Name: fmt.Sprintf("Manual quota %d", i), Mode: "review", ScenarioID: in.ScenarioID, ScenarioDigest: in.ScenarioDigest, FixtureWalletID: in.FixtureWalletID})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = h.CreateWENSession(t.Context(), r); e != nil {
			t.Fatal(e)
		}
	}
	in.Revision = runView(t, s).Revision
	v, _ := startRun(t, h, in)
	in.Revision = v.Revision
	in.Name = "Fifth nonterminal"
	if _, e = h.ReviewWENFixtureRun(t.Context(), in); e == nil {
		t.Fatal("manual and automatic quotas were counted separately")
	}
	m, e := s.WENSessions(t.Context(), in.Profile)
	if e != nil {
		t.Fatal(e)
	}
	ids := []string{}
	for _, r := range m.Sessions {
		ids = append(ids, r.Record.SessionID)
	}
	for _, id := range ids {
		m, e = s.WENSessions(t.Context(), in.Profile)
		if e != nil {
			t.Fatal(e)
		}
		m = sessionControl(t, h, m, id, "stop")
	}
	v = runControl(t, h, runView(t, s), "stop")
	for i := 0; i < 4; i++ {
		in.Revision = v.Revision
		in.Name = fmt.Sprintf("Retained quota %d", i)
		v, _ = startRun(t, h, in)
		r := v.Runs[len(v.Runs)-1]
		v, e = h.ControlWENFixtureRun(t.Context(), WENFixtureRunControl{Schema: "wen-fixture-run-control/1", Profile: v.Profile, Revision: v.Revision, PoolRevision: v.Pools[0].Revision, RunID: r.Record.SessionID, Action: "stop"})
		if e != nil {
			t.Fatal(e)
		}
	}
	in.Revision = v.Revision
	in.Name = "Ninth retained"
	if _, e = h.ReviewWENFixtureRun(t.Context(), in); e == nil {
		t.Fatal("combined retained records were evicted or quota reset")
	}
	if len(v.Runs) != 5 || v.Pools[0].Budget.AvailableLamports != "20000000000" {
		t.Fatal("quota refusal changed history or shadow capital")
	}
}
