package core

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/wenstrategy"
	"reflect"
	"strings"
)

func (s *Store) fixtureRunCapability() WENFixtureCapability {
	return WENFixtureCapability{ID: "session-fixture/1", Enabled: s.sessionFixture, Financial: false}
}
func wenNewRun(v WENFixtureRunReview, now, order int64) WENFixtureRun {
	in := v.Input
	s := wenSessionNew(WENSessionPrepare{Profile: in.Profile, Name: in.Name, Mode: "bounded-auto", FixtureWalletID: in.FixtureWalletID}, v.Source, v.Scenario, now, v.BootID, v.InstanceID, v.PinnedIDs)
	s.Record.SessionID = "wfr-" + wenSessionHash(struct {
		Input WENFixtureRunPrepare
		At    int64
	}{in, now})[:24]
	r := WENFixtureRun{Schema: "wen-fixture-run/1", EvidenceClass: "fixture", Profile: in.Profile, Name: in.Name, Mode: "bounded-auto", Source: v.Source, ScenarioID: in.ScenarioID, ScenarioDigest: in.ScenarioDigest, InventoryDigest: v.InventoryDigest, PinnedIDs: v.PinnedIDs, FirstStartedAt: now, Deadline: now + 300, AdmissionOrder: order, ConsentHistory: []WENFixtureConsent{}, CreateHash: wenSessionHash(v)}
	wenStoreRunSession(&r, s)
	r.Reason = "awaiting-fixture-wake"
	return r
}
func wenConsent(r WENFixtureRun, v WENFixtureRunReview, now int64) WENFixtureConsent {
	a, _ := wenstrategy.Decode([]byte(v.Source.Raw))
	hash := wenSessionHash(v)
	domain, _, _ := strings.Cut(v.Witness, ":")
	epoch := strings.TrimPrefix(domain, "wen-fixture-run-review/1/")
	return WENFixtureConsent{ID: "fc-" + hash[:24] + "@" + epoch, ReviewDigest: hash, Profile: r.Profile, SourceDigest: r.Source.Digest, ScenarioDigest: r.ScenarioDigest, InventoryDigest: r.InventoryDigest, PinnedIDs: r.PinnedIDs, PoolID: r.Record.WalletID, PoolRevision: v.Pool.Revision, BudgetBefore: wenBudgetAt(v.Pool, r.ClockAt), StrategyLimits: a.Document.Limits, BootID: v.BootID, PreparedAt: v.PreparedAt, ConsentedAt: now, ReviewExpiresAt: v.ExpiresAt, Deadline: r.Deadline, WitnessDigest: wenHash([]byte(v.Witness))}
}
func (h *HTTPHandler) prepareWENFixtureRun(ctx context.Context, tx snapshotReader, in WENFixtureRunPrepare, at int64) (WENFixtureRunReview, error) {
	var out WENFixtureRunReview
	if !h.store.sessionFixture {
		return out, errors.New("explicit session-fixture core required")
	}
	if in.Schema != "wen-fixture-run-prepare/1" || !validAgentText(in.Profile, 80, true) || !validAgentText(in.Name, 80, true) || !researchDigest.MatchString(in.StrategyDigest) || in.Revision < 0 || in.LibraryRevision < 0 || (in.FixtureWalletID != "fixture-wallet:alpha" && in.FixtureWalletID != "fixture-wallet:beta") {
		return out, errors.New("invalid fixture-run selection")
	}
	d, rev, e := readAgents(ctx, tx)
	if e != nil {
		return out, e
	}
	if agentProfile(d, in.Profile) < 0 {
		return out, errors.New("profile not found")
	}
	if rev != in.Revision {
		return out, ErrConflict
	}
	libraries, libRev, e := readWENLibraries(ctx, tx)
	if e != nil {
		return out, e
	}
	if libRev != in.LibraryRevision {
		return out, ErrConflict
	}
	var source WENVersion
	for _, v := range libraries[in.Profile].Versions {
		if v.Digest == in.StrategyDigest {
			source = v
		}
	}
	a, e := wenstrategy.Decode([]byte(source.Raw))
	if e != nil || a.Digest != in.StrategyDigest {
		return out, errors.New("saved immutable fixture strategy unavailable")
	}
	doc := a.Document
	if doc.Execution.Mode != "bounded-auto" || doc.Execution.Runner != "fased" || doc.Execution.MandateID != nil {
		return out, errors.New("save an explicitly reviewed bounded-auto Fased fixture version without a financial mandate")
	}
	book, e := wenScenario(in.ScenarioID)
	if e != nil {
		return out, e
	}
	if book.Digest != in.ScenarioDigest || doc.Schedule.StartsAt != book.StartsAt || doc.Schedule.ExpiresAt != book.ExpiresAt {
		return out, errors.New("source window and compiled scenario must match")
	}
	raw, _ := json.Marshal(d)
	var trial agentDocument
	if e = json.Unmarshal(raw, &trial); e != nil {
		return out, e
	}
	w := wenSessionData(&trial)
	pi := wenPoolIndex(w, in.FixtureWalletID)
	pool := wenPool(in.FixtureWalletID)
	if pi >= 0 {
		pool = w.Pools[pi]
	} else {
		w.Pools = append(w.Pools, pool)
	}
	pin := []string{}
	if doc.Schedule.RandomSelection != nil {
		pin, e = wenstrategy.PinRandom(doc.Schedule, book.Slots)
		if e != nil {
			return out, e
		}
	}
	out = WENFixtureRunReview{Schema: "wen-fixture-run-review/1", Input: in, Source: source, Scenario: book, InventoryDigest: wenSessionHash(book.Slots), PinnedIDs: pin, Pool: pool, Capability: h.store.fixtureRunCapability(), InstanceID: h.store.instanceID, BootID: h.store.bootID, PreparedAt: at, ExpiresAt: at + 120, MaxWallSeconds: 300}
	var run WENFixtureRun
	index := -1
	if in.RunID != nil {
		index = wenRunIndex(w, *in.RunID, in.Profile)
		if index < 0 {
			return out, errors.New("run not found")
		}
		run = w.Runs[index]
		if run.Source.Digest != in.StrategyDigest || run.Source.Raw != source.Raw || run.Name != in.Name || run.ScenarioID != in.ScenarioID || run.Record.WalletID != in.FixtureWalletID || run.InventoryDigest != out.InventoryDigest || !reflect.DeepEqual(run.PinnedIDs, pin) {
			return out, errors.New("original fixture run inputs cannot change")
		}
		if run.StopRequested || (run.Record.Status != "held" && run.Record.Status != "paused") || wenRunOutstanding(run) || at >= run.Deadline || at < run.LastWallAt || len(run.ConsentHistory) >= 8 {
			return out, errors.New("original run cannot resume; inspect deadline, consent capacity and unresolved holds")
		}
		copy := run
		copy.Record.Budget = wenBudgetAt(pool, run.ClockAt)
		out.Run = &copy
		deadline := run.Deadline
		out.RunDeadline = &deadline
		run.Record.Status = "waiting"
		run.BootID = h.store.bootID
		run.LastWallAt = at
		run.Reason = "awaiting-fixture-wake"
		wenRevokeRun(&run, at)
	} else {
		if len(w.Runs)+len(w.Sessions) >= 8 || len(trial.Notices) >= 200 {
			return out, errors.New("combined rehearsal/inbox capacity reached; preserve history")
		}
		run = wenNewRun(out, at, rev+1)
	}
	run.ConsentHistory = append(run.ConsentHistory, wenConsent(run, out, at))
	if index < 0 {
		w.Runs = append(w.Runs, run)
	} else {
		w.Runs[index] = run
	}
	projected := wenRunSession(run)
	wenSessionNotice(&trial, &projected, at)
	if e = agentCapacity(trial); e != nil {
		return out, e
	}
	out.ReservedBytes, e = wenSessionStorage(trial)
	return out, e
}
func (h *HTTPHandler) ReviewWENFixtureRun(ctx context.Context, in WENFixtureRunPrepare) (WENFixtureRunReview, error) {
	tx, e := h.store.db.BeginTx(ctx, nil)
	if e != nil {
		return WENFixtureRunReview{}, e
	}
	defer tx.Rollback()
	phase, _, e := readPause(ctx, tx)
	if e != nil {
		return WENFixtureRunReview{}, e
	}
	if phase == "paused" {
		return WENFixtureRunReview{}, ErrCorePaused
	}
	v, e := h.prepareWENFixtureRun(ctx, tx, in, h.store.now().Unix())
	if e == nil {
		v.Witness, e = h.wenRunWitness(ctx, tx, v)
	}
	return v, e
}
func (s *Store) wenRunView(ctx context.Context, tx snapshotReader, profile string) (WENFixtureRunView, error) {
	v := WENFixtureRunView{Schema: "wen-fixture-run-view/1", Profile: profile, Capability: s.fixtureRunCapability(), EvidenceClass: "fixture", Runs: []WENFixtureRun{}, Pools: []WENSessionPool{}}
	d, rev, e := readAgents(ctx, tx)
	if e != nil {
		return v, e
	}
	if agentProfile(d, profile) < 0 {
		return v, errors.New("profile not found")
	}
	_, libRev, e := readWENLibraries(ctx, tx)
	if e != nil {
		return v, e
	}
	v.Revision = rev
	v.LibraryRevision = libRev
	w := wenSessionData(&d)
	v.Pools = append(v.Pools, w.Pools...)
	for _, r := range w.Runs {
		if r.Profile != profile {
			continue
		}
		if pi := wenPoolIndex(w, r.Record.WalletID); pi >= 0 {
			r.Record.Budget = wenBudgetAt(w.Pools[pi], r.ClockAt)
		}
		if r.Record.Status == "waiting" || r.Record.Status == "active" {
			if e = s.checkWENFixtureRun(ctx, tx, r); e != nil {
				r.Record.Status = "held"
				r.Reason = e.Error()
			}
		}
		v.Runs = append(v.Runs, r)
	}
	return v, nil
}
func (s *Store) WENFixtureRuns(ctx context.Context, profile string) (WENFixtureRunView, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return WENFixtureRunView{}, e
	}
	defer tx.Rollback()
	return s.wenRunView(ctx, tx, profile)
}
func (h *HTTPHandler) StartWENFixtureRun(ctx context.Context, v WENFixtureRunReview) (WENFixtureRunView, error) {
	tx, e := h.store.db.BeginTx(ctx, nil)
	if e != nil {
		return WENFixtureRunView{}, e
	}
	defer tx.Rollback()
	d, rev, e := readAgents(ctx, tx)
	if e != nil {
		return WENFixtureRunView{}, e
	}
	if agentProfile(d, v.Input.Profile) < 0 {
		return WENFixtureRunView{}, errors.New("profile not found")
	}
	w := wenSessionData(&d)
	hash := wenSessionHash(v)
	// An exact prior Start is inspection only, including after pause/restart.
	for _, r := range w.Runs {
		if r.Profile == v.Input.Profile {
			for _, old := range r.ConsentHistory {
				if old.ReviewDigest == hash {
					view, e := h.store.wenRunView(ctx, tx, v.Input.Profile)
					receipt := WENSessionReceipt{ID: old.ID, CommandHash: hash, Action: "start", PoolRevision: old.PoolRevision}
					view.Receipt = &receipt
					return view, e
				}
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
	now := h.store.now().Unix()
	if v.Schema != "wen-fixture-run-review/1" || now < v.PreparedAt || now >= v.ExpiresAt || v.BootID != h.store.bootID || v.InstanceID != h.store.instanceID {
		return WENFixtureRunView{}, errors.New("fixture-run review expired or belongs to another boot")
	}
	fresh, e := h.prepareWENFixtureRun(ctx, tx, v.Input, v.PreparedAt)
	if e != nil {
		return WENFixtureRunView{}, e
	}
	fresh.Witness, e = h.wenRunWitness(ctx, tx, fresh)
	if e != nil {
		return WENFixtureRunView{}, e
	}
	if !reflect.DeepEqual(fresh, v) || !hmac.Equal([]byte(fresh.Witness), []byte(v.Witness)) {
		return WENFixtureRunView{}, errors.New("complete fixture-run review changed")
	}
	var run WENFixtureRun
	index := -1
	if v.Input.RunID == nil {
		run = wenNewRun(v, now, rev+1)
	} else {
		index = wenRunIndex(w, *v.Input.RunID, v.Input.Profile)
		if index < 0 {
			return WENFixtureRunView{}, errors.New("run missing")
		}
		run = w.Runs[index]
		wenRevokeRun(&run, now)
	}
	if now >= run.Deadline || now < run.LastWallAt || h.store.now().Unix() >= run.Deadline {
		return WENFixtureRunView{}, errors.New("original fixture deadline reached or clock reversed")
	}
	if wenPoolIndex(w, v.Input.FixtureWalletID) < 0 {
		w.Pools = append(w.Pools, v.Pool)
	}
	run.BootID = h.store.bootID
	run.LastWallAt = now
	run.Record.Status = "waiting"
	run.Reason = "awaiting-fixture-wake"
	consent := wenConsent(run, v, now)
	run.ConsentHistory = append(run.ConsentHistory, consent)
	if index < 0 {
		w.Runs = append(w.Runs, run)
	} else {
		w.Runs[index] = run
	}
	temp := wenRunSession(run)
	wenSessionNotice(&d, &temp, now)
	receipt := WENSessionReceipt{ID: consent.ID, CommandHash: hash, Action: "start", PoolRevision: v.Pool.Revision}
	if finalNow := h.store.now().Unix(); finalNow < now || finalNow >= v.ExpiresAt || finalNow >= run.Deadline {
		return WENFixtureRunView{}, errors.New("fixture review/deadline changed before consent commit")
	}
	return h.store.finishWENFixtureRun(ctx, tx, d, rev, v.Input.Profile, &receipt)
}
func (s *Store) finishWENFixtureRun(ctx context.Context, tx *sql.Tx, d agentDocument, rev int64, profile string, receipt *WENSessionReceipt) (WENFixtureRunView, error) {
	if e := saveAgents(ctx, tx, d, rev); e != nil {
		return WENFixtureRunView{}, e
	}
	v, e := s.wenRunView(ctx, tx, profile)
	if e != nil {
		return v, e
	}
	if e = tx.Commit(); e != nil {
		return v, e
	}
	v.Receipt = receipt
	return v, nil
}
