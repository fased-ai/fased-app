package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/wenstrategy"
	"time"
)

func migrateWENFixtureRuns(ctx context.Context, db *sql.DB) error {
	tx, e := db.BeginTx(ctx, nil)
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
	var raw string
	if e = tx.QueryRowContext(ctx, "SELECT document FROM agent_workspace WHERE id=1").Scan(&raw); e != nil {
		return e
	}
	var root map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &root) != nil {
		return errors.New("invalid original workspace; preserve state")
	}
	if data, ok := root["wenRehearsal"]; ok && string(data) != "null" {
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil {
			return errors.New("invalid original rehearsal; preserve state")
		}
		if _, exists := fields["runs"]; exists {
			return errors.New("automatic run data without schema protection; preserve state")
		}
	}
	if _, e = tx.ExecContext(ctx, "PRAGMA user_version=15"); e != nil {
		return e
	}
	return tx.Commit()
}
func wenRunIndex(w *wenSessionWorkspace, id, profile string) int {
	for i, r := range w.Runs {
		if r.Record.SessionID == id && r.Profile == profile {
			return i
		}
	}
	return -1
}

// A transient adapter reuses the same F1 records and ledger. It is never saved as
// a manual session, and it cannot create an owner Review witness.
func wenRunSession(r WENFixtureRun) WENSession {
	state := wenstrategy.ScheduleState{AcceptedRounds: len(r.Operations)}
	if len(r.Operations) > 0 {
		last := r.Operations[len(r.Operations)-1].RoundID
		state.LastAcceptedRound = &last
	}
	if len(r.ClockAt) >= 10 {
		state.AcceptedRoundsToday = r.DailyAccepted[r.ClockAt[:10]]
	}
	return WENSession{Schema: "wen-session-rehearsal/1", EvidenceClass: r.EvidenceClass, Executable: r.Executable, Profile: r.Profile, Name: r.Name, Mode: r.Mode, Record: r.Record, Source: r.Source, ScenarioID: r.ScenarioID, ScenarioDigest: r.ScenarioDigest, InventoryDigest: r.InventoryDigest, PinnedIDs: r.PinnedIDs, NextSlot: r.Cursor, ClockAt: r.ClockAt, LastWallAt: r.LastWallAt, BootID: r.BootID, Schedule: state, DailyAccepted: r.DailyAccepted, Operations: r.Operations, Decisions: r.Decisions, Receipts: r.Receipts, Reason: r.Reason, CompletionReason: r.CompletionReason, StopRequested: r.StopRequested, AcknowledgedDecision: r.AcknowledgedDecision, CreateHash: r.CreateHash}
}
func wenStoreRunSession(r *WENFixtureRun, s WENSession) {
	r.Record = s.Record
	r.Cursor = s.NextSlot
	r.ClockAt = s.ClockAt
	r.LastWallAt = s.LastWallAt
	r.BootID = s.BootID
	r.DailyAccepted = s.DailyAccepted
	r.Operations = s.Operations
	r.Decisions = s.Decisions
	r.Receipts = s.Receipts
	r.Reason = s.Reason
	r.CompletionReason = s.CompletionReason
	r.StopRequested = s.StopRequested
	r.AcknowledgedDecision = s.AcknowledgedDecision
}
func wenRunOutstanding(r WENFixtureRun) bool {
	for _, op := range r.Operations {
		if op.Status != "reconciled" {
			return true
		}
	}
	return false
}
func wenRevokeRun(r *WENFixtureRun, now int64) {
	if now < r.LastWallAt {
		now = r.LastWallAt
	}
	for i := range r.ConsentHistory {
		if r.ConsentHistory[i].RevokedAt == nil {
			n := now
			r.ConsentHistory[i].RevokedAt = &n
		}
	}
}
func wenRunStorage(w *wenSessionWorkspace, seen, pools map[string]bool) (int, int, error) {
	reserved, active := 0, 0
	for _, r := range w.Runs {
		if r.Schema != "wen-fixture-run/1" || r.EvidenceClass != "fixture" || r.Executable || r.Mode != "bounded-auto" || r.Record.MandateID != nil || r.Record.Runner != "fased" || r.Source.Digest != r.Record.StrategyDigest || seen[r.Record.SessionID] || !validAgentText(r.Record.SessionID, 80, true) || !validAgentText(r.Name, 80, true) || !pools[r.Record.WalletID] || r.DailyAccepted == nil {
			return 0, 0, errors.New("invalid fixture-run identity; preserve state")
		}
		seen[r.Record.SessionID] = true
		book, e := wenScenario(r.ScenarioID)
		if e != nil {
			return 0, 0, e
		}
		if r.ScenarioDigest != book.Digest || r.InventoryDigest != wenSessionHash(book.Slots) || r.Cursor < 0 || r.Cursor > len(book.Slots) || r.FirstStartedAt <= 0 || r.Deadline != r.FirstStartedAt+300 || r.AdmissionOrder < 1 || len(r.ConsentHistory) < 1 || len(r.ConsentHistory) > 8 || len(r.Decisions) > 288 || r.AcknowledgedDecision < -1 || r.AcknowledgedDecision >= len(r.Decisions) {
			return 0, 0, errors.New("invalid fixture-run bounds; preserve state")
		}
		if _, e = time.Parse(time.RFC3339, r.ClockAt); e != nil {
			return 0, 0, e
		}
		a, e := wenstrategy.Decode([]byte(r.Source.Raw))
		if e != nil {
			return 0, 0, e
		}
		if a.Digest != r.Source.Digest || a.Document.Execution.Mode != "bounded-auto" || a.Document.Execution.Runner != "fased" || a.Document.Execution.MandateID != nil || a.Document.Schedule.StartsAt != book.StartsAt || a.Document.Schedule.ExpiresAt != book.ExpiresAt {
			return 0, 0, errors.New("fixture-run source changed")
		}
		switch r.Record.Status {
		case "waiting", "active", "paused", "held", "complete":
		default:
			return 0, 0, errors.New("invalid fixture-run state")
		}
		if r.Record.Status == "complete" && wenRunOutstanding(r) {
			return 0, 0, errors.New("completed fixture run has unresolved holds")
		}
		ids := map[string]bool{}
		for _, op := range r.Operations {
			if ids[op.ID] {
				return 0, 0, errors.New("duplicate fixture operation")
			}
			ids[op.ID] = true
			switch op.Status {
			case "reserved", "uncertain", "reconciled":
			default:
				return 0, 0, errors.New("invalid fixture operation")
			}
		}
		for _, c := range r.ConsentHistory {
			b, e := json.Marshal(c)
			if e != nil || len(b) > 4096 || c.Profile != r.Profile || c.SourceDigest != r.Source.Digest || c.ScenarioDigest != r.ScenarioDigest || c.InventoryDigest != r.InventoryDigest || c.PoolID != r.Record.WalletID || c.Deadline != r.Deadline || c.ConsentedAt < c.PreparedAt || c.ConsentedAt >= c.ReviewExpiresAt {
				return 0, 0, errors.New("invalid or oversized retained fixture consent")
			}
		}
		for _, row := range r.Decisions {
			b, e := json.Marshal(row)
			if e != nil || len(b) > 2048 {
				return 0, 0, errors.New("complete fixture decision exceeds 2048 bytes")
			}
		}
		if len(r.Operations) > a.Document.Schedule.MaxRoundsTotal {
			return 0, 0, errors.New("fixture accepted count exceeds source")
		}
		if r.Record.Status != "complete" {
			active++
			remaining := len(book.Slots) - r.Cursor
			accepts := a.Document.Schedule.MaxRoundsTotal - len(r.Operations)
			if accepts > remaining {
				accepts = remaining
			}
			reserved += remaining*2048 + accepts*3072 + 8192 + (8-len(r.ConsentHistory))*4096
			for _, op := range r.Operations {
				if op.Status != "reconciled" {
					reserved += 512
				}
			}
		}
	}
	return reserved, active, nil
}
func (s *Store) recoverWENFixtureRuns(ctx context.Context) error {
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
		return nil
	}
	d, rev, e := readAgents(ctx, tx)
	if e != nil {
		return e
	}
	if d.WENRehearsal == nil {
		return nil
	}
	changed := false
	for i := range d.WENRehearsal.Runs {
		r := &d.WENRehearsal.Runs[i]
		if r.Record.Status != "complete" && r.BootID != s.bootID {
			wenRevokeRun(r, s.now().Unix())
			r.Record.Status = "held"
			r.Reason = "restart-consent-required"
			r.BootID = s.bootID
			temp := wenRunSession(*r)
			wenSessionNotice(&d, &temp, s.now().Unix())
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if e = saveAgents(ctx, tx, d, rev); e != nil {
		return e
	}
	return tx.Commit()
}

func wenstrategyRunSource(r WENFixtureRun) (wenstrategy.Document, error) {
	a, e := wenstrategy.Decode([]byte(r.Source.Raw))
	if e != nil {
		return wenstrategy.Document{}, e
	}
	if a.Digest != r.Source.Digest || a.Digest != r.Record.StrategyDigest || a.Document.Execution.Mode != "bounded-auto" || a.Document.Execution.Runner != "fased" || a.Document.Execution.MandateID != nil {
		return wenstrategy.Document{}, errors.New("original fixture source invalid; preserve holds")
	}
	return a.Document, nil
}
