package core

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/wenstrategy"
)

func (s *Store) finishWENSession(ctx context.Context, tx *sql.Tx, d agentDocument, revision int64, profile string, receipt WENSessionReceipt) (WENSessionView, error) {
	if e := saveAgents(ctx, tx, d, revision); e != nil {
		return WENSessionView{}, e
	}
	v, e := s.wenSessionViewTx(ctx, tx, profile)
	if e != nil {
		return v, e
	}
	if e = tx.Commit(); e != nil {
		return WENSessionView{}, e
	}
	if receipt.ID != "" {
		v.Receipt = &receipt
	}
	return v, nil
}

type wenSessionMutation func(context.Context, *sql.Tx, *agentDocument, *WENSession, *WENSessionPool, wenstrategy.Document, WENSessionScenario) (WENSessionReceipt, error)

func (h *HTTPHandler) changeWENSession(ctx context.Context, profile, id string, revision, poolRevision int64, receiptID, hash string, allowInert bool, mutate wenSessionMutation) (WENSessionView, error) {
	if !validAgentText(profile, 80, true) || !validAgentText(id, 80, true) || revision < 0 || poolRevision < 0 {
		return WENSessionView{}, errors.New("invalid rehearsal identity")
	}
	tx, e := h.store.db.BeginTx(ctx, nil)
	if e != nil {
		return WENSessionView{}, e
	}
	defer tx.Rollback()
	d, rev, e := readAgents(ctx, tx)
	if e != nil {
		return WENSessionView{}, e
	}
	if agentProfile(d, profile) < 0 {
		return WENSessionView{}, errors.New("profile not found")
	}
	w := wenSessionData(&d)
	si := wenSessionIndex(w, id, profile)
	if si < 0 {
		return WENSessionView{}, errors.New("session not found")
	}
	s := &w.Sessions[si]
	pi := wenPoolIndex(w, s.Record.WalletID)
	if pi < 0 {
		return WENSessionView{}, errors.New("shadow pool unavailable")
	}
	p := &w.Pools[pi]
	if receiptID != "" {
		for _, old := range s.Receipts {
			if old.ID == receiptID {
				if old.CommandHash != hash {
					return WENSessionView{}, errors.New("committed identity has different command bytes")
				}
				v, e := h.store.wenSessionViewTx(ctx, tx, profile)
				v.Receipt = &old
				return v, e
			}
		}
	}
	phase, _, e := readPause(ctx, tx)
	if e != nil {
		return WENSessionView{}, e
	}
	if phase == "paused" {
		return WENSessionView{}, ErrCorePaused
	}
	if rev != revision || p.Revision != poolRevision {
		return WENSessionView{}, ErrConflict
	}
	if e = h.store.checkWENSession(ctx, tx, *s); e != nil && !allowInert {
		return WENSessionView{}, e
	}
	doc, e := wenSessionSource(ctx, tx, *s)
	if e != nil && !allowInert {
		return WENSessionView{}, e
	}
	if e != nil {
		original, de := wenstrategy.Decode([]byte(s.Source.Raw))
		if de != nil {
			return WENSessionView{}, de
		}
		doc = original.Document
	}
	book, _ := wenScenario(s.ScenarioID)
	receipt, e := mutate(ctx, tx, &d, s, p, doc, book)
	if e != nil {
		return WENSessionView{}, e
	}
	// Safe stop/ack/pause remain available during clock rollback, but must not
	// lower the admission fence and revive a proposal or permit a fresh resume.
	if now := h.store.now().Unix(); now > s.LastWallAt {
		s.LastWallAt = now
	}
	if receipt.ID != "" {
		receipt.CommandHash = hash
		s.Receipts = append(s.Receipts, receipt)
	}
	wenSessionNotice(&d, s, h.store.now().Unix())
	return h.store.finishWENSession(ctx, tx, d, rev, profile, receipt)
}
func wenSettleStatus(s *WENSession) {
	for _, op := range s.Operations {
		if op.Status != "reconciled" {
			return
		}
	}
	if s.StopRequested {
		s.Record.Status = "complete"
		s.CompletionReason = "stopped"
		s.Reason = "stopped"
		return
	}
	book, e := wenScenario(s.ScenarioID)
	if e == nil && s.NextSlot >= len(book.Slots) && s.Pending == nil {
		s.Record.Status = "complete"
		s.CompletionReason = "schedule-complete"
		s.Reason = "schedule-complete"
	}
}
func wenAppendDecision(s *WENSession, slot wenstrategy.Slot, at, cutoff string, p WENSessionPool, result wenstrategy.RehearsalEvaluation, reason string, pending bool) string {
	id := s.Record.SessionID + ":" + slot.RoundID + ":e"
	before := wenBudgetAt(p, at)
	s.Decisions = append(s.Decisions, WENSessionDecision{Record: wenstrategy.DecisionRecord{DecisionID: id, SessionID: s.Record.SessionID, StrategyDigest: s.Source.Digest, RoundID: slot.RoundID, At: at, Cutoff: cutoff, InputIDs: []string{s.ScenarioDigest}, Branch: result.Branch, WeightsBps: result.WeightsBps, AmountsLamports: append([]string{}, result.AmountsLamports[:]...), Action: result.Action, Reason: reason, Before: before, After: before, OperationIDs: []string{}, Pending: pending}, Phase: "evaluation", EvaluationReason: result.Reason, InputDigest: wenSessionHash(struct {
		Scenario string
		Round    string
		At       string
	}{s.ScenarioDigest, slot.RoundID, at})})
	return id
}
func (h *HTTPHandler) StepWENSession(ctx context.Context, c WENSessionStep) (WENSessionView, error) {
	if c.Schema != "wen-session-step/1" || !validAgentText(c.EventID, 140, true) {
		return WENSessionView{}, errors.New("invalid session step")
	}
	return h.changeWENSession(ctx, c.Profile, c.SessionID, c.Revision, c.PoolRevision, "step:"+c.EventID, wenSessionHash(c), false, func(ctx context.Context, tx *sql.Tx, d *agentDocument, s *WENSession, p *WENSessionPool, doc wenstrategy.Document, book WENSessionScenario) (WENSessionReceipt, error) {
		var out WENSessionReceipt
		if c.EventID != wenSessionNext(*s) {
			return out, errors.New("only the next frozen event is available")
		}
		clock, e := time.Parse(time.RFC3339, s.ClockAt)
		if e != nil {
			return out, e
		}
		eventAt := clock
		opID, decisionID := "", ""
		if strings.HasPrefix(c.EventID, "resolve:") || strings.HasPrefix(c.EventID, "uncertain:") {
			for i := range s.Operations {
				op := &s.Operations[i]
				if !strings.HasSuffix(c.EventID, op.ID) {
					continue
				}
				n, _ := strconv.Atoi(op.RoundID)
				open, _, _ := wenSlotTimes(book, n)
				eventAt = open.Add(30 * time.Second)
				opID = op.ID
				if strings.HasPrefix(c.EventID, "uncertain:") {
					eventAt = open.Add(10 * time.Second)
					op.Status = "uncertain"
					s.Reason = "fixture-operation-uncertain"
				} else {
					if e = wenReconcile(p, op, s.ScenarioID == "fixture-4h-unknown"); e != nil {
						return out, e
					}
					s.Reason = "shadow-reconciled; BTC/SAT unavailable"
				}
				break
			}
		} else {
			if s.BootID != h.store.bootID || s.Record.Status != "waiting" || s.StopRequested {
				return out, errors.New("explicit resume required before future fixture decisions")
			}
			if c.EventID == "finish" {
				eventAt, _ = time.Parse(time.RFC3339, doc.Schedule.ExpiresAt)
				s.Reason = "schedule-complete"
			} else if strings.HasPrefix(c.EventID, "cutoff:") {
				if s.Pending == nil {
					return out, errors.New("no pending review")
				}
				eventAt, _ = time.Parse(time.RFC3339, s.Pending.Cutoff)
				s.Pending = nil
				s.Reason = "review-expired"
			} else {
				n := s.NextSlot
				slot := book.Slots[n]
				_, eval, cutoff := wenSlotTimes(book, n)
				eventAt = eval
				state := s.Schedule
				state.AcceptedRoundsToday = s.DailyAccepted[eval.Format("2006-01-02")]
				result := wenstrategy.RehearsalEvaluation{Action: "skip", Branch: -1, Reason: "missed-cutoff", AmountsLamports: [5]string{"0", "0", "0", "0", "0"}}
				if eval.Before(cutoff) {
					result, e = wenstrategy.Rehearse(doc, slot, state, s.PinnedIDs, wenFixtureMetrics(doc, book, n, eval), eval.Format(time.RFC3339), wenBudgetAt(*p, eval.Format(time.RFC3339)), wenSessionReservation(doc))
					if e != nil {
						return out, e
					}
				}
				reason := result.Reason
				pending := false
				if result.Action == "offer" {
					if s.Mode == "notify" {
						result.Action = "skip"
						reason = "notify-only"
					} else {
						result.Action = "hold"
						reason = "review-pending"
						pending = true
					}
				}
				decisionID = wenAppendDecision(s, slot, eval.Format(time.RFC3339), cutoff.Format(time.RFC3339), *p, result, reason, pending)
				if pending {
					s.Pending = &WENSessionOffer{ID: "review:" + decisionID, DecisionID: decisionID, RoundID: slot.RoundID, At: eval.Format(time.RFC3339), Cutoff: cutoff.Format(time.RFC3339), OfferedAt: h.store.now().Unix(), ExpiresAt: h.store.now().Unix() + 120, Reservation: wenSessionReservation(doc)}
				}
				s.NextSlot++
				s.Reason = reason
			}
		}
		if eventAt.Before(clock) {
			return out, errors.New("fixture clock cannot move backward")
		}
		s.ClockAt = eventAt.Format(time.RFC3339)
		wenSettleStatus(s)
		return wenEmptyReceipt(*s, "step:"+c.EventID, "", "step", decisionID, opID, p.Revision), nil
	})
}
func (h *HTTPHandler) ReviewWENSessionAction(ctx context.Context, c WENSessionActionInput) (WENSessionActionReview, error) {
	tx, e := h.store.db.BeginTx(ctx, nil)
	if e != nil {
		return WENSessionActionReview{}, e
	}
	defer tx.Rollback()
	phase, _, e := readPause(ctx, tx)
	if e != nil {
		return WENSessionActionReview{}, e
	}
	if phase == "paused" {
		return WENSessionActionReview{}, ErrCorePaused
	}
	r, e := h.prepareWENSessionAction(ctx, tx, c)
	if e == nil {
		r.Witness = h.wenSessionWitness("wen-session-action-review/1", r)
	}
	return r, e
}
func pointerValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func (h *HTTPHandler) ControlWENSession(ctx context.Context, c WENSessionControl) (WENSessionView, error) {
	if c.Schema != "wen-session-control/1" {
		return WENSessionView{}, errors.New("invalid control schema")
	}
	key := ""
	if c.Action == "simulate-accept" {
		key = "accept:" + pointerValue(c.DecisionID)
	}
	return h.changeWENSession(ctx, c.Profile, c.SessionID, c.Revision, c.PoolRevision, key, wenSessionHash(c), c.Action == "pause" || c.Action == "stop" || c.Action == "acknowledge" || c.Action == "dismiss", func(ctx context.Context, tx *sql.Tx, d *agentDocument, s *WENSession, p *WENSessionPool, doc wenstrategy.Document, book WENSessionScenario) (WENSessionReceipt, error) {
		var receipt WENSessionReceipt
		decision := pointerValue(c.DecisionID)
		switch c.Action {
		case "simulate-accept":
			if c.ReviewID == nil || c.Witness == nil || c.DecisionID == nil {
				return receipt, errors.New("inspect the exact pending review first")
			}
			in := WENSessionActionInput{Schema: "wen-session-action-review/1", Profile: c.Profile, Revision: c.Revision, PoolRevision: c.PoolRevision, SessionID: c.SessionID, DecisionID: decision}
			r, e := h.prepareWENSessionAction(ctx, tx, in)
			if e != nil {
				return receipt, e
			}
			if r.ReviewID != *c.ReviewID || !hmac.Equal([]byte(*c.Witness), []byte(h.wenSessionWitness("wen-session-action-review/1", r))) {
				return receipt, errors.New("action review changed or belongs to another core boot")
			}
			before := wenBudgetAt(*p, s.ClockAt)
			if e = wenReserve(p, s.ClockAt[:10], r.Offer.Reservation); e != nil {
				return receipt, e
			}
			row := r.Decision
			row.Record.DecisionID = s.Record.SessionID + ":" + r.Offer.RoundID + ":a"
			row.Record.Action = "enter"
			row.Record.Reason = "simulated-acceptance"
			row.Record.Pending = false
			row.Record.Before = before
			row.Record.After = wenBudgetAt(*p, s.ClockAt)
			opID := "fixture:" + s.Record.SessionID + ":" + r.Offer.RoundID
			row.Record.OperationIDs = []string{opID}
			row.Phase = "acceptance"
			s.Decisions = append(s.Decisions, row)
			s.Operations = append(s.Operations, WENSessionOperation{ID: opID, RoundID: r.Offer.RoundID, Day: s.ClockAt[:10], Status: "reserved", Reservation: r.Offer.Reservation, ConfirmedDeduction: "0", ConfirmedFee: "0"})
			s.Record.OperationIDs = append(s.Record.OperationIDs, opID)
			s.Schedule.AcceptedRounds++
			s.DailyAccepted[s.ClockAt[:10]]++
			s.Schedule.AcceptedRoundsToday = s.DailyAccepted[s.ClockAt[:10]]
			round := r.Offer.RoundID
			s.Schedule.LastAcceptedRound = &round
			s.Pending = nil
			s.Reason = "shadow-capital-reserved"
			receipt = wenEmptyReceipt(*s, key, "", c.Action, row.Record.DecisionID, opID, p.Revision)
		case "acknowledge":
			if c.DecisionID == nil || c.ReviewID != nil || c.Witness != nil {
				return receipt, errors.New("acknowledgement names one displayed decision")
			}
			found := false
			for i, row := range s.Decisions {
				if row.Record.DecisionID == decision {
					found = true
					if i > s.AcknowledgedDecision {
						s.AcknowledgedDecision = i
					}
				}
			}
			if !found {
				return receipt, errors.New("displayed decision not found")
			}
		case "dismiss":
			if s.Pending == nil || decision != s.Pending.DecisionID || pointerValue(c.ReviewID) != s.Pending.ID || c.Witness != nil {
				return receipt, errors.New("current review required")
			}
			s.Pending = nil
			s.Reason = "review-dismissed"
		case "pause", "resume", "stop":
			if c.DecisionID != nil || c.ReviewID != nil || c.Witness != nil {
				return receipt, errors.New("control has no decision/grant fields")
			}
			if s.Record.Status == "complete" {
				return receipt, errors.New("session is complete")
			}
			if c.Action == "resume" {
				end, _ := time.Parse(time.RFC3339, doc.Schedule.ExpiresAt)
				clock, _ := time.Parse(time.RFC3339, s.ClockAt)
				if s.StopRequested || !clock.Before(end) {
					return receipt, errors.New("original session cannot resume")
				}
				for _, op := range s.Operations {
					if op.Status != "reconciled" {
						return receipt, errors.New("reconcile the original operation first")
					}
				}
				if s.Record.Status != "held" && s.Record.Status != "paused" && s.BootID == h.store.bootID {
					return receipt, errors.New("session is not paused/held")
				}
				s.Record.Status = "waiting"
				s.BootID = h.store.bootID
				s.Pending = nil
				s.Reason = "manual-step-required"
			} else {
				s.Pending = nil
				s.Record.Status = "paused"
				s.Reason = "paused"
				if c.Action == "stop" {
					s.StopRequested = true
					s.Record.Status = "held"
					s.Reason = "stop-awaiting-reconciliation"
					wenSettleStatus(s)
				}
			}
		default:
			return receipt, fmt.Errorf("unsupported rehearsal control %q", c.Action)
		}
		wenSettleStatus(s)
		return receipt, nil
	})
}
