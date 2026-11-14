package core

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

func settleLoopAI(t *WENResearchTurn, q AgentAIRequest, now int64) {
	t.State, t.StopReason = q.State, q.Failure
	if q.State == "unknown" {
		t.State = "held"
	}
	if q.State == "completed" && q.researchFrame != nil {
		m := &t.Models[q.ResearchStep-1]
		m.Frame = q.researchFrame
		m.FrameSHA256 = researchHash(m.Frame)
		if m.Frame.Decision == "read" {
			t.State = "reads_queued"
		} else {
			t.State = "completed"
		}
	}
	if now > t.LastObservedAt {
		t.LastObservedAt = now
	}
}
func loopNotRun(t *WENResearchTurn, reason string) {
	seen := map[string]bool{}
	for _, v := range t.Tools {
		seen[v.ref()] = true
	}
	for _, m := range t.Models {
		if m.Frame != nil && m.Frame.Decision == "read" {
			for _, id := range m.Frame.CallRefs {
				if !seen[id] {
					t.Tools = append(t.Tools, researchToolRecord{NotRun: &researchReadNotRun{"wen-research-call-not-run/1", id, m.Step, "not_run", reason}})
					seen[id] = true
				}
			}
		}
	}
}

// Reserve a complete bounded error result even when cached data plus provenance
// does not fit. Never truncate a successful data envelope.
func boundedLoopResult(t WENResearchTurn, a researchReadAttempt, r researchReadResult, now int64) researchReadResult {
	r.ResultSHA256 = readResultHash(r)
	b, e := json.Marshal(r)
	if e != nil || len(b) > wenReadResultLimit {
		return loopErrorResult(t, a, "too_large", now, false)
	}
	return r
}

func loopErrorResult(t WENResearchTurn, a researchReadAttempt, reason string, now int64, recovered bool) researchReadResult {
	f, _ := loopFact(t, a.CallRef)
	r := researchReadResult{Schema: "wen-research-call-result/1", AttemptID: a.AttemptID, CallRef: a.CallRef, ModelStep: a.ModelStep, Status: "error", Sources: f.Sources, StartedAt: a.StartedAt, EndedAt: &now, Error: reason}
	if recovered {
		r.EndedAt = nil
		r.RecoveredAt = now
	}
	r.ResultSHA256 = readResultHash(r)
	return r
}

// Called at the beginning of the existing serialized worker, never as a new
// scheduler. A stored queued/running read from an earlier invocation is held.
func recoverLoopReads(d *agentDocument, now int64) bool {
	changed := false
	for i := range d.ResearchTurns {
		t := &d.ResearchTurns[i]
		if t.Schema != researchLoopTurn {
			continue
		}
		interrupted := t.State == "reads_queued" || t.State == "reads_running"
		for j := range t.Tools {
			a := t.Tools[j].Attempt
			if a != nil && a.Status == "running" {
				at := now
				if at < a.StartedAt {
					at = a.StartedAt
				}
				r := loopErrorResult(*t, *a, "interrupted", at, true)
				a.Status, a.Result = "error", &r
				interrupted = true
			}
		}
		if interrupted {
			loopNotRun(t, "interrupted")
			if t.State != "cancelled" {
				t.State = "held"
			}
			t.StopReason = "Interrupted local research reads; no replay."
			if now > t.LastObservedAt {
				t.LastObservedAt = now
			}
			changed = true
		}
	}
	return changed
}

func (s *Store) loopReadActive(ctx context.Context, turn, id string) bool {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return false
	}
	defer tx.Rollback()
	d, rev, e := readAgents(ctx, tx)
	if e != nil {
		return false
	}
	var phase string
	if tx.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase) != nil || phase == "paused" {
		return false
	}
	for _, t := range d.ResearchTurns {
		if t.ID != turn || t.State != "reads_running" {
			continue
		}
		q, ok := loopRequest(d, t.CurrentChildID)
		if !ok || researchSourceState(ctx, tx, d, rev, t, q, s.now().Unix()) != "unchanged" {
			return false
		}
		for _, v := range t.Tools {
			if v.Attempt != nil && v.Attempt.AttemptID == id && v.Attempt.Status == "running" {
				return true
			}
		}
	}
	return false
}

// The caller owns aiMu. Commit each attempt before even a cached read; close the
// DB transaction during evaluation so cancellation/pause can still be recorded.
func (s *Store) runLoopReadsLocked(ctx context.Context, turnID string) error {
	for {
		tx, e := s.db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		d, rev, e := readAgents(ctx, tx)
		if e != nil {
			tx.Rollback()
			return e
		}
		ti := -1
		for i, t := range d.ResearchTurns {
			if t.ID == turnID {
				ti = i
			}
		}
		if ti < 0 {
			tx.Rollback()
			return errors.New("research turn unavailable")
		}
		t := &d.ResearchTurns[ti]
		if t.State != "reads_queued" && t.State != "reads_running" {
			tx.Rollback()
			return nil
		}
		q, _ := loopRequest(d, t.CurrentChildID)
		now := s.now().Unix()
		source := researchSourceState(ctx, tx, d, rev, *t, q, now)
		if ctx.Err() != nil || source != "unchanged" {
			reason := "limit"
			if ctx.Err() != nil {
				reason = "cancelled"
			} else if source == "deadline_expired" {
				reason = "deadline"
			} else if source != "unchanged" {
				reason = "source_unavailable"
			}
			t.State, t.StopReason = "held", reason
			loopNotRun(t, reason)
			if now > t.LastObservedAt {
				t.LastObservedAt = now
			}
			e = saveAgents(ctx, tx, d, rev)
			if e == nil {
				e = tx.Commit()
			} else {
				tx.Rollback()
			}
			return e
		}
		m := t.Models[t.CurrentStep-1]
		seen := map[string]bool{}
		for _, v := range t.Tools {
			seen[v.ref()] = true
		}
		ref := ""
		for _, id := range m.Frame.CallRefs {
			if !seen[id] {
				ref = id
				break
			}
		}
		if ref == "" {
			t.State = "awaiting_review"
			e = saveAgents(ctx, tx, d, rev)
			if e == nil {
				e = tx.Commit()
			} else {
				tx.Rollback()
			}
			return e
		}
		pending := 0
		for _, id := range m.Frame.CallRefs {
			if !seen[id] {
				pending++
			}
		}
		if !loopFit(*t, pending*(wenReadResultLimit+1024)) || loopReadCount(*t, 0)+pending > t.Bounds.MaxToolAttempts {
			t.State, t.StopReason = "held", "limit"
			loopNotRun(t, "limit")
			e = saveAgents(ctx, tx, d, rev)
			if e == nil {
				e = tx.Commit()
			} else {
				tx.Rollback()
			}
			return e
		}
		id, e := randomToken()
		if e != nil {
			tx.Rollback()
			return e
		}
		call, _ := loopCall(*t, ref)
		fact, _ := loopFact(*t, ref)
		a := researchReadAttempt{"wen-research-read-attempt/1", id, t.CurrentStep, q.ID, m.FrameSHA256, ref, call, now, "running", nil}
		// Error metadata must fit too; do not invoke even the cache if a
		// complete bounded record cannot be retained.
		metadata, err := json.Marshal(loopErrorResult(*t, a, "too_large", now, false))
		if err != nil || len(metadata) > wenReadResultLimit {
			t.State, t.StopReason = "held", "too_large"
			loopNotRun(t, "too_large")
			e = saveAgents(ctx, tx, d, rev)
			if e == nil {
				e = tx.Commit()
			} else {
				tx.Rollback()
			}
			return e
		}
		t.Tools = append(t.Tools, researchToolRecord{Attempt: &a})
		t.State, t.LastObservedAt = "reads_running", now
		deadline := t.DeadlineAt
		e = saveAgents(ctx, tx, d, rev)
		if e == nil {
			e = tx.Commit()
		} else {
			tx.Rollback()
		}
		if e != nil {
			return e
		}
		limit := 3 * time.Second
		if remain := time.Unix(deadline, 0).Sub(s.now()); remain < limit {
			limit = remain
		}
		readCtx, cancel := context.WithTimeout(ctx, limit)
		done := make(chan struct{})
		go func() {
			defer close(done)
			tick := time.NewTicker(50 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-readCtx.Done():
					return
				case <-tick.C:
					if !s.loopReadActive(readCtx, turnID, id) {
						cancel()
						return
					}
				}
			}
		}()
		data := append(json.RawMessage(nil), fact.Data...)
		var readErr error
		if s.researchRead != nil {
			data, readErr = s.researchRead(readCtx, fact)
		}
		if readCtx.Err() != nil {
			readErr = readCtx.Err()
		}
		cancel()
		<-done
		finish, stop := context.WithTimeout(context.Background(), 2*time.Second)
		tx, e = s.db.BeginTx(finish, nil)
		if e != nil {
			stop()
			return e
		}
		d, rev, e = readAgents(finish, tx)
		if e != nil {
			tx.Rollback()
			stop()
			return e
		}
		ti = -1
		for i, t := range d.ResearchTurns {
			if t.ID == turnID {
				ti = i
			}
		}
		if ti < 0 {
			tx.Rollback()
			stop()
			return errors.New("research turn disappeared")
		}
		t = &d.ResearchTurns[ti]
		q, _ = loopRequest(d, t.CurrentChildID)
		now = s.now().Unix()
		source = researchSourceState(finish, tx, d, rev, *t, q, now)
		if now < t.LastObservedAt {
			now = t.LastObservedAt
		}
		t.LastObservedAt = now
		for j := range t.Tools {
			v := t.Tools[j].Attempt
			if v == nil || v.AttemptID != id || v.Status != "running" {
				continue
			}
			r := loopErrorResult(*t, *v, "source_unavailable", now, false)
			halt := false
			switch {
			case t.State == "cancelled":
				r.Error = "cancelled"
				halt = true
			case source != "unchanged":
				if source == "deadline_expired" {
					r.Error = "deadline"
				}
				halt = true
			case readErr != nil:
				if errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) {
					r.Error = "deadline"
					halt = true
				} else if stableReadError(readErr.Error()) {
					r.Error = readErr.Error()
				}
			case string(data) != string(fact.Data):
				r.Error = "source_unavailable"
				halt = true
			default:
				r.Status, r.Data, r.Error = "ok", data, ""
			}
			r = boundedLoopResult(*t, *v, r, now)
			v.Status, v.Result = r.Status, &r
			if halt {
				if t.State != "cancelled" {
					t.State = "held"
				}
				t.StopReason = r.Error
				loopNotRun(t, r.Error)
			}
		}
		e = saveAgents(finish, tx, d, rev)
		if e == nil {
			e = tx.Commit()
		} else {
			tx.Rollback()
		}
		stop()
		if e != nil {
			return e
		}
		if t.State == "held" || t.State == "cancelled" {
			return nil
		}
	}
}
