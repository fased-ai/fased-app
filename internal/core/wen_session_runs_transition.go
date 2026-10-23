package core

import (
	"context"
	"errors"
	"sort"
	"strings"
)

func (h *HTTPHandler) wenRunWitness(ctx context.Context, reader snapshotReader, v WENFixtureRunReview) (string, error) {
	_, pause, e := readPause(ctx, reader)
	if e != nil {
		return "", e
	}
	return h.wenSessionWitness("wen-fixture-run-review/1/"+pause.PauseID, v), nil
}
func (s *Store) checkWENFixtureRun(ctx context.Context, reader snapshotReader, r WENFixtureRun) error {
	if !s.sessionFixture {
		return errors.New("session-fixture capability disabled")
	}
	if s.now().Unix() < r.LastWallAt {
		return errors.New("fixture wall clock reversed")
	}
	if s.now().Unix() >= r.Deadline {
		return errors.New("original fixture run deadline reached")
	}
	if r.BootID != s.bootID || len(r.ConsentHistory) == 0 {
		return errors.New("fresh fixture consent required")
	}
	c := r.ConsentHistory[len(r.ConsentHistory)-1]
	if c.BootID != s.bootID || c.RevokedAt != nil || c.Deadline != r.Deadline {
		return errors.New("fresh fixture consent required")
	}
	phase, pause, e := readPause(ctx, reader)
	if e != nil {
		return e
	}
	if phase == "paused" || !strings.HasSuffix(c.ID, "@"+pause.PauseID) {
		return errors.New("update pause changed; fresh fixture consent required")
	}
	temp := wenRunSession(r)
	if _, e = wenSessionSource(ctx, reader, temp); e != nil {
		return e
	}
	book, e := wenScenario(r.ScenarioID)
	if e != nil {
		return e
	}
	if book.Digest != r.ScenarioDigest || wenSessionHash(book.Slots) != r.InventoryDigest {
		return errors.New("compiled scenario changed; preserve run")
	}
	return nil
}
func wenHoldRun(r *WENFixtureRun, reason string, now int64) {
	wenRevokeRun(r, now)
	r.Record.Status = "held"
	r.Reason = reason
	if now > r.LastWallAt {
		r.LastWallAt = now
	}
}
func (s *Store) advanceWENFixtureRuns(ctx context.Context) error {
	if !s.sessionFixture {
		return nil
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	phase, _, e := readPause(ctx, tx)
	if e != nil {
		return e
	}
	if phase == "paused" {
		return ErrCorePaused
	}
	d, rev, e := readAgents(ctx, tx)
	if e != nil {
		return e
	}
	if d.WENRehearsal == nil || len(d.WENRehearsal.Runs) == 0 {
		return nil
	}
	w := d.WENRehearsal
	order := []int{}
	for i, r := range w.Runs {
		if r.Record.Status == "waiting" || r.Record.Status == "active" {
			order = append(order, i)
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := w.Runs[order[i]], w.Runs[order[j]]
		if wenRunOutstanding(a) != wenRunOutstanding(b) {
			return wenRunOutstanding(a)
		}
		return a.AdmissionOrder < b.AdmissionOrder
	})
	if len(order) > 4 {
		order = order[:4]
	}
	changed := false
	accepted := map[int]bool{}
	for _, index := range order {
		if e = ctx.Err(); e != nil {
			return e
		}
		r := &w.Runs[index]
		now := s.now().Unix()
		if e = s.checkWENFixtureRun(ctx, tx, *r); e != nil {
			wenHoldRun(r, e.Error(), now)
			temp := wenRunSession(*r)
			wenSessionNotice(&d, &temp, now)
			changed = true
			continue
		}
		pi := wenPoolIndex(w, r.Record.WalletID)
		if pi < 0 {
			return errors.New("original pool missing; preserve run")
		}
		pool := &w.Pools[pi]
		temp := wenRunSession(*r)
		doc, e := wenSessionSource(ctx, tx, temp)
		if e != nil {
			return e
		}
		book, _ := wenScenario(r.ScenarioID)
		event := wenSessionNext(temp)
		if event == "" {
			continue
		}
		command := wenSessionHash(struct{ Run, Event, Consent string }{r.Record.SessionID, event, r.ConsentHistory[len(r.ConsentHistory)-1].ID})
		receipt, e := wenAdvanceFixtureEvent(&temp, pool, doc, book, event, now, s.bootID)
		if e != nil {
			return e
		}
		receipt.ID = "auto:" + event
		receipt.CommandHash = command
		receipt.Action = "fixture-event"
		if temp.Pending != nil {
			offer := *temp.Pending
			row := temp.Decisions[len(temp.Decisions)-1]
			// The offer is an internal evaluation, not an owner Review or grant.
			row.Record.Reason = "fixture-auto-eligible"
			row.Record.Pending = false
			temp.Decisions[len(temp.Decisions)-1] = row
			if s.now().Unix() < r.LastWallAt || s.now().Unix() >= r.Deadline || ctx.Err() != nil {
				return errors.New("fixture deadline/clock changed before acceptance")
			}
			acceptance, e := wenAcceptFixtureEntry(&temp, pool, doc, offer, row, "auto-accept:"+event, "fixture-consented")
			if e != nil {
				return e
			}
			accepted[index] = true
			last := &temp.Decisions[len(temp.Decisions)-1]
			last.Record.Reason = "fixture-consented:" + r.ConsentHistory[len(r.ConsentHistory)-1].ID
			receipt.DecisionID = acceptance.DecisionID
			receipt.OperationID = acceptance.OperationID
			receipt.PoolRevision = pool.Revision
		}
		temp.Receipts = append(temp.Receipts, receipt)
		if now > temp.LastWallAt {
			temp.LastWallAt = now
		}
		for _, op := range temp.Operations {
			if op.Status == "uncertain" {
				temp.Record.Status = "held"
				temp.Reason = "fixture-operation-uncertain"
			}
		}
		wenSettleStatus(&temp)
		wenStoreRunSession(r, temp)
		if r.Record.Status == "held" || r.Record.Status == "complete" {
			wenRevokeRun(r, now)
		}
		wenSessionNotice(&d, &temp, now)
		changed = true
	}
	if !changed {
		return nil
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	// The final fence prevents a slow preparation from crossing the original limit.
	for i := range accepted {
		r := w.Runs[i]
		if s.now().Unix() < r.LastWallAt || s.now().Unix() >= r.Deadline {
			return errors.New("fixture deadline/clock changed before commit")
		}
	}
	if e = saveAgents(ctx, tx, d, rev); e != nil {
		return e
	}
	return tx.Commit()
}

type wenRunMutation func(*agentDocument, *WENFixtureRun, *WENSessionPool) (*WENSessionReceipt, error)

func (h *HTTPHandler) changeWENFixtureRun(ctx context.Context, profile, id string, rev, poolRev int64, receiptID, commandHash string, mutate wenRunMutation) (WENFixtureRunView, error) {
	if !validAgentText(profile, 80, true) || !validAgentText(id, 80, true) || rev < 0 || poolRev < 0 {
		return WENFixtureRunView{}, errors.New("invalid fixture-run reference")
	}
	tx, e := h.store.db.BeginTx(ctx, nil)
	if e != nil {
		return WENFixtureRunView{}, e
	}
	defer tx.Rollback()
	d, current, e := readAgents(ctx, tx)
	if e != nil {
		return WENFixtureRunView{}, e
	}
	if agentProfile(d, profile) < 0 {
		return WENFixtureRunView{}, errors.New("profile not found")
	}
	w := wenSessionData(&d)
	index := wenRunIndex(w, id, profile)
	if index < 0 {
		return WENFixtureRunView{}, errors.New("fixture run not found")
	}
	r := &w.Runs[index]
	pi := wenPoolIndex(w, r.Record.WalletID)
	if pi < 0 {
		return WENFixtureRunView{}, errors.New("original shadow pool missing")
	}
	pool := &w.Pools[pi]
	if receiptID != "" {
		for _, old := range r.Receipts {
			if old.ID == receiptID {
				if old.CommandHash != commandHash {
					return WENFixtureRunView{}, errors.New("committed reconciliation has different command")
				}
				view, e := h.store.wenRunView(ctx, tx, profile)
				view.Receipt = &old
				return view, e
			}
		}
	}
	phase, _, e := readPause(ctx, tx)
	if e != nil {
		return WENFixtureRunView{}, e
	}
	if phase == "paused" {
		return WENFixtureRunView{}, ErrCorePaused
	}
	if current != rev || pool.Revision != poolRev {
		return WENFixtureRunView{}, ErrConflict
	}
	receipt, e := mutate(&d, r, pool)
	if e != nil {
		return WENFixtureRunView{}, e
	}
	if now := h.store.now().Unix(); now > r.LastWallAt {
		r.LastWallAt = now
	}
	temp := wenRunSession(*r)
	wenSessionNotice(&d, &temp, h.store.now().Unix())
	return h.store.finishWENFixtureRun(ctx, tx, d, current, profile, receipt)
}
func (h *HTTPHandler) ControlWENFixtureRun(ctx context.Context, c WENFixtureRunControl) (WENFixtureRunView, error) {
	if c.Schema != "wen-fixture-run-control/1" {
		return WENFixtureRunView{}, errors.New("invalid fixture control")
	}
	return h.changeWENFixtureRun(ctx, c.Profile, c.RunID, c.Revision, c.PoolRevision, "", "", func(d *agentDocument, r *WENFixtureRun, p *WENSessionPool) (*WENSessionReceipt, error) {
		switch c.Action {
		case "acknowledge":
			if c.DecisionID == nil {
				return nil, errors.New("exact displayed decision required")
			}
			found := false
			for i, row := range r.Decisions {
				if row.Record.DecisionID == *c.DecisionID {
					found = true
					if i > r.AcknowledgedDecision {
						r.AcknowledgedDecision = i
					}
				}
			}
			if !found {
				return nil, errors.New("displayed decision unavailable")
			}
		case "pause", "stop":
			if c.DecisionID != nil || r.Record.Status == "complete" {
				return nil, errors.New("run already complete or extra decision selector")
			}
			wenRevokeRun(r, h.store.now().Unix())
			r.Record.Status = "paused"
			r.Reason = "paused"
			if c.Action == "stop" {
				r.StopRequested = true
				r.Record.Status = "held"
				r.Reason = "stop-awaiting-reconciliation"
				temp := wenRunSession(*r)
				wenSettleStatus(&temp)
				wenStoreRunSession(r, temp)
			}
		default:
			return nil, errors.New("unsupported fixture-run control; resume needs fresh review/Start")
		}
		return nil, nil
	})
}
func (h *HTTPHandler) ReconcileWENFixtureRun(ctx context.Context, c WENFixtureRunReconcile) (WENFixtureRunView, error) {
	if c.Schema != "wen-fixture-run-reconcile/1" {
		return WENFixtureRunView{}, errors.New("invalid fixture reconciliation")
	}
	return h.changeWENFixtureRun(ctx, c.Profile, c.RunID, c.Revision, c.PoolRevision, "reconcile:"+c.EventID, wenSessionHash(c), func(d *agentDocument, r *WENFixtureRun, p *WENSessionPool) (*WENSessionReceipt, error) {
		hash := wenSessionHash(c)
		for _, old := range r.Receipts {
			if old.ID == "reconcile:"+c.EventID {
				if old.CommandHash != hash {
					return nil, errors.New("reconciliation identity has different command")
				}
				return &old, nil
			}
		}
		// Reconciliation uses retained immutable facts, even after a library source
		// disappears. It can release only its existing pinned fixture operation.
		temp := wenRunSession(*r)
		event := wenSessionNext(temp)
		if c.EventID != event || (!strings.HasPrefix(event, "resolve:") && !strings.HasPrefix(event, "uncertain:")) {
			return nil, errors.New("only the exact proven fixture reconciliation is available")
		}
		source, e := wenstrategyRunSource(*r)
		if e != nil {
			return nil, e
		}
		book, e := wenScenario(r.ScenarioID)
		if e != nil || book.Digest != r.ScenarioDigest {
			return nil, errors.New("original compiled scenario unavailable")
		}
		receipt, e := wenAdvanceFixtureEvent(&temp, p, source, book, event, h.store.now().Unix(), h.store.bootID)
		if e != nil {
			return nil, e
		}
		receipt.ID = "reconcile:" + event
		receipt.CommandHash = hash
		receipt.Action = "reconcile"
		temp.Receipts = append(temp.Receipts, receipt)
		wenSettleStatus(&temp)
		wenStoreRunSession(r, temp)
		return &receipt, nil
	})
}
