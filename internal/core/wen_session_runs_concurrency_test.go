package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWENFixtureRunStopCommitOrder(t *testing.T) {
	for _, order := range []string{"stop-first", "accept-first", "concurrent"} {
		t.Run(order, func(t *testing.T) {
			s, h, in := runFixture(t, "fixture-4h", nil)
			v, _ := startRun(t, h, in)
			stop := WENFixtureRunControl{Schema: "wen-fixture-run-control/1", Profile: v.Profile, Revision: v.Revision, PoolRevision: v.Pools[0].Revision, RunID: v.Runs[0].Record.SessionID, Action: "stop"}
			var controlErr error
			switch order {
			case "stop-first":
				_, controlErr = h.ControlWENFixtureRun(t.Context(), stop)
				if e := s.advanceWENFixtureRuns(t.Context()); e != nil {
					t.Fatal(e)
				}
			case "accept-first":
				if e := s.advanceWENFixtureRuns(t.Context()); e != nil {
					t.Fatal(e)
				}
				_, controlErr = h.ControlWENFixtureRun(t.Context(), stop)
			case "concurrent":
				wakeDone, stopDone := make(chan error, 1), make(chan error, 1)
				go func() { wakeDone <- s.advanceWENFixtureRuns(t.Context()) }()
				go func() { _, e := h.ControlWENFixtureRun(t.Context(), stop); stopDone <- e }()
				if e := <-wakeDone; e != nil {
					t.Fatal(e)
				}
				controlErr = <-stopDone
			}
			v = runView(t, s)
			accepted := len(v.Runs[0].Operations)
			if accepted > 1 || (order == "stop-first" && accepted != 0) || (order == "accept-first" && accepted != 1) {
				t.Fatal("control/commit order admitted an extra entry", order, accepted)
			}
			if accepted == 0 {
				if controlErr != nil || !v.Runs[0].StopRequested || v.Pools[0].Budget.TotalTurnoverLamports != "0" {
					t.Fatal("winning stop failed", controlErr, v)
				}
			} else {
				if !errors.Is(controlErr, ErrConflict) || v.Runs[0].StopRequested || !wenRunOutstanding(v.Runs[0]) {
					t.Fatal("earlier acceptance was erased or stale control was accepted", controlErr)
				}
				// A losing command is not silently retried. The owner explicitly
				// reloads and submits a new stop with current revisions.
				v = runControl(t, h, v, "stop")
				if !wenRunOutstanding(v.Runs[0]) || v.Pools[0].Budget.UnsettledLamports == "0" {
					t.Fatal("stop released an accepted operation")
				}
			}
			before := retainedRevisionState(t, s)
			if e := s.advanceWENFixtureRuns(t.Context()); e != nil {
				t.Fatal(e)
			}
			if before != retainedRevisionState(t, s) {
				t.Fatal("winning stop allowed another discretionary event")
			}
			if accepted == 1 {
				r, p := v.Runs[0], v.Pools[0]
				c := WENFixtureRunReconcile{Schema: "wen-fixture-run-reconcile/1", Profile: v.Profile, Revision: v.Revision, PoolRevision: p.Revision, RunID: r.Record.SessionID, EventID: "resolve:" + r.Operations[0].ID}
				out, e := h.ReconcileWENFixtureRun(t.Context(), c)
				if e != nil || out.Runs[0].Record.Status != "complete" || out.Pools[0].Budget.UnsettledLamports != "0" {
					t.Fatal("protected original did not reconcile", e)
				}
				before = retainedRevisionState(t, s)
				retry, e := h.ReconcileWENFixtureRun(t.Context(), c)
				if e != nil || retry.Receipt == nil || retry.Receipt.ID != out.Receipt.ID || before != retainedRevisionState(t, s) {
					t.Fatal("exact reconciliation retry changed state", e)
				}
			}
		})
	}
}

func TestWENFixtureRunCancelledPreparation(t *testing.T) {
	s, h, in := runFixture(t, "fixture-4h", nil)
	_, _ = startRun(t, h, in)
	before := retainedRevisionState(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	now, original := s.now(), s.now
	calls := 0
	s.now = func() time.Time {
		calls++
		if calls >= 6 {
			cancel()
		}
		return now
	}
	e := s.advanceWENFixtureRuns(ctx)
	s.now = original
	if e == nil || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("cancelled preparation was admitted", e)
	}
	if before != retainedRevisionState(t, s) {
		t.Fatal("cancelled preparation left partial decision, hold, cursor or receipt")
	}
}
