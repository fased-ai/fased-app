package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/wenstrategy"
)

func migrateWENSessions(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	phase, _, err := readPause(ctx, tx)
	if err != nil {
		return err
	}
	if phase == "paused" {
		return ErrCorePaused
	}
	var raw string
	if err = tx.QueryRowContext(ctx, "SELECT document FROM agent_workspace WHERE id=1").Scan(&raw); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &fields) != nil {
		return errors.New("invalid original workspace; preserve state")
	}
	if _, exists := fields["wenRehearsal"]; exists {
		return errors.New("rehearsal data without schema protection; preserve state")
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA user_version=14"); err != nil {
		return err
	}
	return tx.Commit() // Format-only: no workspace/library byte or revision rewrite.
}
func wenZeroBudget() wenstrategy.BudgetState {
	return wenstrategy.BudgetState{TotalTurnoverLamports: "0", DailyTurnoverLamports: "0", UnsettledLamports: "0", UserFeesPaidLamports: "0", UserFeeHoldsLamports: "0", ActualDeductionsLamports: "0", DeductionHoldsLamports: "0", AvailableLamports: "0"}
}
func wenPool(id string) WENSessionPool {
	b := wenZeroBudget()
	b.AvailableLamports = "20000000000"
	return WENSessionPool{ID: id, Budget: b, Daily: map[string]string{}}
}
func wenSessionData(d *agentDocument) *wenSessionWorkspace {
	if d.WENRehearsal == nil {
		d.WENRehearsal = &wenSessionWorkspace{Sessions: []WENSession{}, Pools: []WENSessionPool{}}
	}
	return d.WENRehearsal
}
func wenPoolIndex(w *wenSessionWorkspace, id string) int {
	for i, p := range w.Pools {
		if p.ID == id {
			return i
		}
	}
	return -1
}
func wenSessionIndex(w *wenSessionWorkspace, id, profile string) int {
	for i, s := range w.Sessions {
		if s.Record.SessionID == id && s.Profile == profile {
			return i
		}
	}
	return -1
}
func wenNative(s string) (uint64, error) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, errors.New("noncanonical shadow amount")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("noncanonical shadow amount")
		}
	}
	return strconv.ParseUint(s, 10, 64)
}
func wenAdd(a, b string) (string, error) {
	x, e := wenNative(a)
	if e != nil {
		return "", e
	}
	y, e := wenNative(b)
	if e != nil {
		return "", e
	}
	z := new(big.Int).Add(new(big.Int).SetUint64(x), new(big.Int).SetUint64(y))
	if !z.IsUint64() {
		return "", errors.New("shadow amount overflow")
	}
	return z.String(), nil
}
func wenSub(a, b string) (string, error) {
	x, e := wenNative(a)
	if e != nil {
		return "", e
	}
	y, e := wenNative(b)
	if e != nil || x < y {
		return "", errors.New("shadow hold underflow; preserve state")
	}
	return strconv.FormatUint(x-y, 10), nil
}
func wenBudgetAt(p WENSessionPool, at string) wenstrategy.BudgetState {
	b := p.Budget
	b.DailyTurnoverLamports = p.Daily[at[:10]]
	if b.DailyTurnoverLamports == "" {
		b.DailyTurnoverLamports = "0"
	}
	return b
}
func wenSessionReservation(doc wenstrategy.Document) wenstrategy.Reservation {
	p, _ := wenNative(doc.Limits.PerRoundLamports)
	return wenstrategy.Reservation{PrincipalLamports: doc.Limits.PerRoundLamports, UserFeeLamports: "1000", WorstCaseDeductionLamports: strconv.FormatUint(p/10, 10)}
}
func wenMove(target *string, amount string, add bool) error {
	var e error
	if add {
		*target, e = wenAdd(*target, amount)
	} else {
		*target, e = wenSub(*target, amount)
	}
	return e
}
func wenReserve(p *WENSessionPool, day string, r wenstrategy.Reservation) error {
	b := &p.Budget
	for _, v := range []struct {
		p   *string
		a   string
		add bool
	}{{&b.AvailableLamports, r.PrincipalLamports, false}, {&b.AvailableLamports, r.UserFeeLamports, false}, {&b.UnsettledLamports, r.PrincipalLamports, true}, {&b.UserFeeHoldsLamports, r.UserFeeLamports, true}, {&b.DeductionHoldsLamports, r.WorstCaseDeductionLamports, true}, {&b.TotalTurnoverLamports, r.PrincipalLamports, true}} {
		if e := wenMove(v.p, v.a, v.add); e != nil {
			return e
		}
	}
	old := p.Daily[day]
	if old == "" {
		old = "0"
	}
	n, e := wenAdd(old, r.PrincipalLamports)
	if e != nil {
		return e
	}
	p.Daily[day] = n
	b.DailyTurnoverLamports = n
	p.Revision++
	return nil
}
func wenReconcile(p *WENSessionPool, op *WENSessionOperation, unspent bool) error {
	r := op.Reservation
	principal, e := wenNative(r.PrincipalLamports)
	if e != nil {
		return e
	}
	deduction, fee := strconv.FormatUint(principal/20, 10), r.UserFeeLamports
	if unspent {
		deduction = "0"
		fee = "0"
	}
	back, e := wenSub(r.PrincipalLamports, deduction)
	if e != nil {
		return e
	}
	unusedFee, e := wenSub(r.UserFeeLamports, fee)
	if e != nil {
		return e
	}
	if _, e = wenSub(r.WorstCaseDeductionLamports, deduction); e != nil {
		return e
	}
	b := &p.Budget
	for _, v := range []struct {
		p   *string
		a   string
		add bool
	}{{&b.UnsettledLamports, r.PrincipalLamports, false}, {&b.UserFeeHoldsLamports, r.UserFeeLamports, false}, {&b.DeductionHoldsLamports, r.WorstCaseDeductionLamports, false}, {&b.ActualDeductionsLamports, deduction, true}, {&b.UserFeesPaidLamports, fee, true}, {&b.AvailableLamports, back, true}, {&b.AvailableLamports, unusedFee, true}} {
		if e = wenMove(v.p, v.a, v.add); e != nil {
			return e
		}
	}
	op.ConfirmedDeduction = deduction
	op.ConfirmedFee = fee
	op.Status = "reconciled"
	p.Revision++
	return nil
}
func wenSessionSource(ctx context.Context, r snapshotReader, s WENSession) (wenstrategy.Document, error) {
	all, _, e := readWENLibraries(ctx, r)
	if e != nil {
		return wenstrategy.Document{}, e
	}
	for _, v := range all[s.Profile].Versions {
		if v.Digest == s.Source.Digest && v.Raw == s.Source.Raw {
			a, e := wenstrategy.Decode([]byte(v.Raw))
			if e != nil {
				return wenstrategy.Document{}, e
			}
			if a.Digest != s.Record.StrategyDigest || a.Document.Execution.Runner != "fased" || a.Document.Execution.Mode != s.Mode || a.Document.Execution.MandateID != nil {
				return wenstrategy.Document{}, errors.New("session source/authority changed")
			}
			return a.Document, nil
		}
	}
	return wenstrategy.Document{}, errors.New("original saved strategy unavailable; preserve rehearsal")
}
func wenSessionStorage(d agentDocument) (int, error) {
	w := d.WENRehearsal
	if w == nil {
		return 0, nil
	}
	if len(w.Sessions)+len(w.Runs) > 8 || len(w.Pools) > 2 {
		return 0, errors.New("rehearsal capacity exceeded; preserve state")
	}
	reserved, active := 0, 0
	seen := map[string]bool{}
	poolIDs := map[string]bool{}
	for _, p := range w.Pools {
		if (p.ID != "fixture-wallet:alpha" && p.ID != "fixture-wallet:beta") || poolIDs[p.ID] || p.Revision < 0 || p.Daily == nil {
			return 0, errors.New("invalid shadow pool; preserve state")
		}
		poolIDs[p.ID] = true
		b, _ := json.Marshal(p.Budget)
		var fields map[string]string
		if json.Unmarshal(b, &fields) != nil {
			return 0, errors.New("invalid shadow amounts")
		}
		for _, value := range fields {
			if _, e := wenNative(value); e != nil {
				return 0, e
			}
		}
		for day, value := range p.Daily {
			if _, e := time.Parse("2006-01-02", day); e != nil {
				return 0, e
			}
			if _, e := wenNative(value); e != nil {
				return 0, e
			}
		}
	}
	for _, s := range w.Sessions {
		if seen[s.Record.SessionID] || s.Schema != "wen-session-rehearsal/1" || s.EvidenceClass != "fixture" || s.Executable || s.Record.MandateID != nil || s.Record.Runner != "fased" || s.Source.Digest != s.Record.StrategyDigest || s.NextSlot < 0 || len(s.Decisions) > 288 {
			return 0, errors.New("invalid rehearsal identity; preserve state")
		}
		seen[s.Record.SessionID] = true
		if _, e := time.Parse(time.RFC3339, s.ClockAt); e != nil {
			return 0, errors.New("invalid rehearsal clock; preserve state")
		}
		if !poolIDs[s.Record.WalletID] || (s.Mode != "notify" && s.Mode != "review") || !validAgentText(s.Name, 80, true) || s.DailyAccepted == nil || s.Schedule.AcceptedRounds < 0 {
			return 0, errors.New("invalid rehearsal source/counters")
		}
		switch s.Record.Status {
		case "waiting", "active", "paused", "held", "complete":
		default:
			return 0, errors.New("invalid rehearsal state")
		}
		book, e := wenScenario(s.ScenarioID)
		if e != nil {
			return 0, e
		}
		if s.NextSlot > len(book.Slots) {
			return 0, errors.New("invalid rehearsal cursor")
		}
		for _, row := range s.Decisions {
			b, e := json.Marshal(row)
			if e != nil || len(b) > 2048 {
				return 0, errors.New("complete rehearsal decision exceeds 2048 bytes")
			}
		}
		if s.Record.Status != "complete" {
			active++
			a, e := wenstrategy.Decode([]byte(s.Source.Raw))
			if e != nil {
				return 0, e
			}
			remaining := len(book.Slots) - s.NextSlot
			accepts := a.Document.Schedule.MaxRoundsTotal - s.Schedule.AcceptedRounds
			if accepts < 0 {
				return 0, errors.New("invalid rehearsal counters")
			}
			if accepts > remaining+1 {
				accepts = remaining + 1
			}
			reserved += remaining*2048 + accepts*3072 + 8192
			for _, op := range s.Operations {
				if op.Status != "reconciled" {
					reserved += 512
				}
			}
		}
	}
	runReserved, runActive, e := wenRunStorage(w, seen, poolIDs)
	if e != nil {
		return 0, e
	}
	reserved += runReserved
	active += runActive
	if active > 4 {
		return 0, errors.New("four nonterminal rehearsals allowed")
	}
	return reserved, nil
}
func wenSessionNotice(d *agentDocument, s *WENSession, now int64) {
	id := "wen-fixture:" + s.Record.SessionID
	text := fmt.Sprintf("Fixture %s: %s · %s", s.Name, s.Record.Status, s.Reason)
	read := s.AcknowledgedDecision >= len(s.Decisions)-1
	for i, n := range d.Notices {
		if n.ID == id {
			d.Notices[i].Text = text
			d.Notices[i].At = now
			d.Notices[i].Read = read
			return
		}
	}
	d.Notices = append(d.Notices, AgentNotice{ID: id, ProfileID: s.Profile, Text: text, RunID: id, At: now, Read: read})
}
func (s *Store) recoverWENSessions(ctx context.Context) error {
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
	for i := range d.WENRehearsal.Sessions {
		r := &d.WENRehearsal.Sessions[i]
		if r.Record.Status != "complete" && r.BootID != s.bootID {
			r.Record.Status = "held"
			r.Reason = "restart-review-required"
			r.Pending = nil
			r.BootID = s.bootID
			wenSessionNotice(&d, r, s.now().Unix())
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
func wenSlotTimes(book WENSessionScenario, n int) (time.Time, time.Time, time.Time) {
	open, _ := time.Parse(time.RFC3339, book.Slots[n].OpensAt)
	eval, cutoff := open.Add(-70*time.Second), open.Add(-60*time.Second)
	if strings.HasSuffix(book.ID, "-cutoff") {
		eval = cutoff
	}
	return open, eval, cutoff
}
