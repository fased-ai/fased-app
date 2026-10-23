package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/fased-ai/fased-app/internal/wenstrategy"
)

func wenSessionHash(v any) string { b, _ := json.Marshal(v); return wenHash(b) }
func (h *HTTPHandler) wenSessionWitness(domain string, v any) string {
	m := hmac.New(sha256.New, h.wenResearchKey[:])
	m.Write([]byte(domain + "\n"))
	b, _ := json.Marshal(v)
	m.Write(b)
	return domain + ":" + hex.EncodeToString(m.Sum(nil))
}
func wenEmptyReceipt(s WENSession, id, hash, action, decision, op string, poolRevision int64) WENSessionReceipt {
	return WENSessionReceipt{ID: id, CommandHash: hash, Action: action, DecisionID: decision, OperationID: op, PoolRevision: poolRevision}
}
func wenSessionNew(in WENSessionPrepare, source WENVersion, book WENSessionScenario, at int64, boot, instance string, pin []string) WENSession {
	_, eval, _ := wenSlotTimes(book, 0)
	id := "ws-" + wenSessionHash(struct {
		Input WENSessionPrepare
		At    int64
	}{in, at})[:24]
	return WENSession{Schema: "wen-session-rehearsal/1", EvidenceClass: "fixture", Profile: in.Profile, Name: in.Name, Mode: in.Mode, Record: wenstrategy.SessionRecord{SessionID: id, StrategyDigest: source.Digest, Runner: "fased", InstanceID: instance, WalletID: in.FixtureWalletID, Status: "waiting", OperationIDs: []string{}, Budget: wenZeroBudget()}, Source: source, ScenarioID: book.ID, ScenarioDigest: book.Digest, InventoryDigest: wenSessionHash(book.Slots), PinnedIDs: pin, ClockAt: eval.Add(-time.Second).Format(time.RFC3339), LastWallAt: at, BootID: boot, DailyAccepted: map[string]int{}, Operations: []WENSessionOperation{}, Decisions: []WENSessionDecision{}, Receipts: []WENSessionReceipt{}, AcknowledgedDecision: -1, Reason: "manual-step-required"}
}
func (h *HTTPHandler) prepareWENSession(ctx context.Context, r snapshotReader, in WENSessionPrepare, at int64) (WENSessionReview, error) {
	var out WENSessionReview
	if in.Schema != "wen-session-prepare/1" || !validAgentText(in.Profile, 80, true) || !validAgentText(in.Name, 80, true) || !researchDigest.MatchString(in.StrategyDigest) || (in.Mode != "notify" && in.Mode != "review") || (in.FixtureWalletID != "fixture-wallet:alpha" && in.FixtureWalletID != "fixture-wallet:beta") || in.Revision < 0 || in.LibraryRevision < 0 {
		return out, errors.New("invalid fixture session selection")
	}
	d, rev, e := readAgents(ctx, r)
	if e != nil {
		return out, e
	}
	if agentProfile(d, in.Profile) < 0 {
		return out, errors.New("profile not found")
	}
	if rev != in.Revision {
		return out, ErrConflict
	}
	all, libRev, e := readWENLibraries(ctx, r)
	if e != nil {
		return out, e
	}
	if libRev != in.LibraryRevision {
		return out, ErrConflict
	}
	var source WENVersion
	for _, v := range all[in.Profile].Versions {
		if v.Digest == in.StrategyDigest {
			source = v
		}
	}
	a, e := wenstrategy.Decode([]byte(source.Raw))
	if e != nil || a.Digest != in.StrategyDigest {
		return out, errors.New("saved immutable strategy unavailable")
	}
	doc := a.Document
	if doc.Execution.Runner != "fased" || doc.Execution.MandateID != nil || doc.Execution.Mode != in.Mode {
		return out, errors.New("save an explicitly reviewed Fased-targeted Notify/Review version first")
	}
	book, e := wenScenario(in.ScenarioID)
	if e != nil {
		return out, e
	}
	if book.Digest != in.ScenarioDigest || doc.Schedule.StartsAt != book.StartsAt || doc.Schedule.ExpiresAt != book.ExpiresAt {
		return out, errors.New("strategy window/scenario digest must match the compiled fixture")
	}
	pin := []string{}
	if doc.Schedule.RandomSelection != nil {
		pin, e = wenstrategy.PinRandom(doc.Schedule, book.Slots)
		if e != nil {
			return out, e
		}
	}
	// Deep copy: preparation is inert even when the original contains sessions.
	raw, _ := json.Marshal(d)
	var trial agentDocument
	if e = json.Unmarshal(raw, &trial); e != nil {
		return out, e
	}
	w := wenSessionData(&trial)
	if len(w.Sessions)+len(w.Runs) >= 8 || len(d.Notices) >= 200 {
		return out, errors.New("rehearsal/inbox capacity reached; preserve records")
	}
	pool := wenPool(in.FixtureWalletID)
	if pi := wenPoolIndex(w, in.FixtureWalletID); pi >= 0 {
		pool = w.Pools[pi]
	} else {
		w.Pools = append(w.Pools, pool)
	}
	n := wenSessionNew(in, source, book, at, h.store.bootID, h.store.instanceID, pin)
	w.Sessions = append(w.Sessions, n)
	wenSessionNotice(&trial, &w.Sessions[len(w.Sessions)-1], at)
	if e = agentCapacity(trial); e != nil {
		return out, e
	}
	reserved, e := wenSessionStorage(trial)
	if e != nil {
		return out, e
	}
	out = WENSessionReview{Schema: "wen-session-review/1", Input: in, Source: source, Scenario: book, InventoryDigest: wenSessionHash(book.Slots), PinnedIDs: pin, Pool: pool, PreparedAt: at, ExpiresAt: at + 120, ReservedBytes: reserved}
	return out, nil
}
func (h *HTTPHandler) ReviewWENSession(ctx context.Context, in WENSessionPrepare) (WENSessionReview, error) {
	tx, e := h.store.db.BeginTx(ctx, nil)
	if e != nil {
		return WENSessionReview{}, e
	}
	defer tx.Rollback()
	phase, _, e := readPause(ctx, tx)
	if e != nil {
		return WENSessionReview{}, e
	}
	if phase == "paused" {
		return WENSessionReview{}, ErrCorePaused
	}
	v, e := h.prepareWENSession(ctx, tx, in, h.store.now().Unix())
	if e == nil {
		v.Witness = h.wenSessionWitness("wen-session-review/1", v)
	}
	return v, e
}
func (s *Store) wenSessionViewTx(ctx context.Context, reader snapshotReader, profile string) (WENSessionView, error) {
	d, rev, e := readAgents(ctx, reader)
	if e != nil {
		return WENSessionView{}, e
	}
	if !validAgentText(profile, 80, true) || agentProfile(d, profile) < 0 {
		return WENSessionView{}, errors.New("profile not found")
	}
	_, libRev, e := readWENLibraries(ctx, reader)
	if e != nil {
		return WENSessionView{}, e
	}
	v := WENSessionView{Schema: "wen-session-view/1", Profile: profile, Revision: rev, LibraryRevision: libRev, EvidenceClass: "fixture", Sessions: []WENSession{}, Pools: []WENSessionPool{}, PoolOptions: []string{"fixture-wallet:alpha", "fixture-wallet:beta"}, Scenarios: wenSessionScenarios(), NextEvents: map[string]string{}}
	w := wenSessionData(&d)
	v.Pools = append(v.Pools, w.Pools...)
	for _, r := range w.Sessions {
		if r.Profile != profile {
			continue
		}
		if pi := wenPoolIndex(w, r.Record.WalletID); pi >= 0 {
			r.Record.Budget = wenBudgetAt(w.Pools[pi], r.ClockAt)
		}
		if r.Record.Status != "complete" {
			if e := s.checkWENSession(ctx, reader, r); e != nil {
				r.Record.Status = "held"
				r.Reason = e.Error()
			}
		}
		v.Sessions = append(v.Sessions, r)
		v.NextEvents[r.Record.SessionID] = wenSessionNext(r)
	}
	return v, nil
}
func (s *Store) WENSessions(ctx context.Context, profile string) (WENSessionView, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return WENSessionView{}, e
	}
	defer tx.Rollback()
	return s.wenSessionViewTx(ctx, tx, profile)
}
func (h *HTTPHandler) CreateWENSession(ctx context.Context, review WENSessionReview) (WENSessionView, error) {
	tx, e := h.store.db.BeginTx(ctx, nil)
	if e != nil {
		return WENSessionView{}, e
	}
	defer tx.Rollback()
	d, rev, e := readAgents(ctx, tx)
	if e != nil {
		return WENSessionView{}, e
	}
	if agentProfile(d, review.Input.Profile) < 0 {
		return WENSessionView{}, errors.New("profile not found")
	}
	command := wenSessionHash(review)
	w := wenSessionData(&d)
	for _, r := range w.Sessions {
		if r.Profile == review.Input.Profile && r.CreateHash == command {
			v, e := h.store.wenSessionViewTx(ctx, tx, r.Profile)
			if len(r.Receipts) > 0 {
				v.Receipt = &r.Receipts[0]
			}
			return v, e
		}
	}
	phase, _, e := readPause(ctx, tx)
	if e != nil {
		return WENSessionView{}, e
	}
	if phase == "paused" {
		return WENSessionView{}, ErrCorePaused
	}
	witness := review.Witness
	review.Witness = ""
	now := h.store.now().Unix()
	if review.Schema != "wen-session-review/1" || review.PreparedAt > now || review.ExpiresAt <= now || review.ExpiresAt != review.PreparedAt+120 || !hmac.Equal([]byte(witness), []byte(h.wenSessionWitness("wen-session-review/1", review))) {
		return WENSessionView{}, errors.New("session review expired or changed")
	}
	expected, e := h.prepareWENSession(ctx, tx, review.Input, review.PreparedAt)
	if e != nil {
		return WENSessionView{}, e
	}
	if !reflect.DeepEqual(review, expected) {
		return WENSessionView{}, errors.New("session source/budget/capacity changed")
	}
	poolIndex := wenPoolIndex(w, review.Input.FixtureWalletID)
	if poolIndex < 0 {
		w.Pools = append(w.Pools, review.Pool)
		poolIndex = len(w.Pools) - 1
	}
	r := wenSessionNew(review.Input, review.Source, review.Scenario, review.PreparedAt, h.store.bootID, h.store.instanceID, review.PinnedIDs)
	r.CreateHash = command
	r.Record.Budget = wenBudgetAt(w.Pools[poolIndex], r.ClockAt)
	r.Receipts = append(r.Receipts, wenEmptyReceipt(r, "create", command, "create", "", "", w.Pools[poolIndex].Revision))
	w.Sessions = append(w.Sessions, r)
	wenSessionNotice(&d, &w.Sessions[len(w.Sessions)-1], now)
	return h.store.finishWENSession(ctx, tx, d, rev, r.Profile, r.Receipts[0])
}
func wenSessionNext(r WENSession) string {
	if r.Record.Status == "complete" {
		return ""
	}
	for _, op := range r.Operations {
		if op.Status == "reserved" || op.Status == "uncertain" {
			if r.ScenarioID == "fixture-4h-unknown" && op.Status == "reserved" {
				return "uncertain:" + op.ID
			}
			return "resolve:" + op.ID
		}
	}
	if r.Record.Status == "paused" || r.Record.Status == "held" || r.StopRequested {
		return ""
	}
	if r.Pending != nil {
		return "cutoff:" + r.Pending.DecisionID
	}
	book, e := wenScenario(r.ScenarioID)
	if e != nil {
		return ""
	}
	if r.NextSlot >= len(book.Slots) {
		return "finish"
	}
	return "slot:" + strconv.Itoa(r.NextSlot)
}
func (h *HTTPHandler) prepareWENSessionAction(ctx context.Context, reader snapshotReader, in WENSessionActionInput) (WENSessionActionReview, error) {
	var out WENSessionActionReview
	if in.Schema != "wen-session-action-review/1" {
		return out, errors.New("invalid action review schema")
	}
	d, rev, e := readAgents(ctx, reader)
	if e != nil {
		return out, e
	}
	w := wenSessionData(&d)
	si := wenSessionIndex(w, in.SessionID, in.Profile)
	if si < 0 || agentProfile(d, in.Profile) < 0 {
		return out, errors.New("session not found")
	}
	s := w.Sessions[si]
	pi := wenPoolIndex(w, s.Record.WalletID)
	if pi < 0 {
		return out, errors.New("shadow pool missing")
	}
	p := w.Pools[pi]
	if rev != in.Revision || p.Revision != in.PoolRevision {
		return out, ErrConflict
	}
	if e = h.store.checkWENSession(ctx, reader, s); e != nil {
		return out, e
	}
	if s.Record.Status != "waiting" || s.Pending == nil || s.Pending.DecisionID != in.DecisionID || s.Mode != "review" || s.BootID != h.store.bootID {
		return out, errors.New("no current review proposal")
	}
	offer := *s.Pending
	clock, _ := time.Parse(time.RFC3339, s.ClockAt)
	cutoff, _ := time.Parse(time.RFC3339, offer.Cutoff)
	if !clock.Before(cutoff) || h.store.now().Unix() >= offer.ExpiresAt {
		return out, errors.New("original review deadline reached")
	}
	doc, e := wenSessionSource(ctx, reader, s)
	if e != nil {
		return out, e
	}
	if e = wenstrategy.CheckLimits(doc.Limits, wenBudgetAt(p, s.ClockAt), offer.Reservation); e != nil {
		return out, e
	}
	book, _ := wenScenario(s.ScenarioID)
	index, _ := strconv.Atoi(offer.RoundID)
	for _, row := range s.Decisions {
		if row.Record.DecisionID == offer.DecisionID {
			out = WENSessionActionReview{Schema: "wen-session-action-review/1", Input: in, ReviewID: offer.ID, Offer: offer, Decision: row, Source: s.Source, Scenario: book, Metrics: wenFixtureMetrics(doc, book, index, clock), Budget: wenBudgetAt(p, s.ClockAt)}
			return out, nil
		}
	}
	return out, errors.New("original decision unavailable")
}
func (s *Store) checkWENSession(ctx context.Context, r snapshotReader, v WENSession) error {
	if s.now().Unix() < v.LastWallAt {
		return errors.New("backward core clock; preserve rehearsal")
	}
	book, e := wenScenario(v.ScenarioID)
	if e != nil || book.Digest != v.ScenarioDigest || wenSessionHash(book.Slots) != v.InventoryDigest {
		return errors.New("scenario/inventory changed; rehearsal held")
	}
	_, e = wenSessionSource(ctx, r, v)
	return e
}
