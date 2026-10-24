package core

// A single consented fixture child uses the ordinary serialized AI worker. This
// is not a tool loop, continuation driver, provider grant or financial mandate.
import (
	"context"
	"errors"
)

type WENResearchAIStartInput struct {
	Schema        string `json:"schema"`
	Profile       string `json:"profile"`
	ID            string `json:"id"`
	Revision      int64  `json:"revision"`
	RequestID     string `json:"requestId"`
	ConsentDigest string `json:"consentDigest"`
	Step          int    `json:"step,omitempty"`
}

func researchSourceState(ctx context.Context, r snapshotReader, d agentDocument, rev int64, t WENResearchTurn, q AgentAIRequest, now int64) string {
	if now < t.LastObservedAt {
		return "clock_ambiguous"
	}
	if t.StartedAt > 0 && now >= t.DeadlineAt {
		return "deadline_expired"
	}
	if t.StartedAt == 0 && now > t.ReviewExpiresAt {
		return "expired"
	}
	if validateAIModelPolicy(q, now) != nil {
		return "policy_expired"
	}
	_, libRev, err := readWENLibraries(ctx, r)
	sel := t.Selection
	sel.Revision, sel.LibraryRevision = rev, libRev
	facts, e2 := researchFacts(ctx, r, sel)
	root, e3 := researchBase(d, t.Profile, t.SessionID, q.Prompt, t.Root.MemorySelection, now)
	if err == nil && e2 == nil && e3 == nil {
		root.FactsSHA256 = researchHash(facts)
		if t.Schema == researchLoopTurn {
			root.Schema = "wen-research-root/2"
			root.CallsSHA256 = researchHash(t.Selection.Calls)
		}
	}
	if err != nil || e2 != nil || e3 != nil || researchHash(root) != t.RootSHA256 || q.Context != researchRequestContext(t, q) {
		return "source_changed"
	}
	return "unchanged"
}

// The caller supplies its existing transaction: source and agent revisions must
// be one snapshot, and SQLite's single connection must never be nested here.
func reviewedAIContext(ctx context.Context, r snapshotReader, d agentDocument, rev int64, q AgentAIRequest, now int64) (string, error) {
	if !isResearchAI(q) {
		return aiRequestContext(d, q, now)
	}
	i := researchTurnIndex(d, q.ProfileID, q.ResearchTurnID)
	if i < 0 || d.ResearchTurns[i].CurrentChildID != q.ID {
		return "", errors.New("research child unavailable")
	}
	if state := researchSourceState(ctx, r, d, rev, d.ResearchTurns[i], q, now); state != "unchanged" {
		return "", errors.New("research " + state)
	}
	return q.Context, nil
}

func (s *Store) StartWENResearchAI(ctx context.Context, c WENResearchAIStartInput, fixture bool) (WENResearchAIReview, error) {
	var out WENResearchAIReview
	if !fixture {
		return out, errors.New("research execution disabled; an explicitly started development fixture is required")
	}
	if (c.Schema != "wen-research-ai-start/1" && c.Schema != "wen-research-ai-start/2") || !readRefValid(c.Profile) || !readRefValid(c.ID) || !readRefValid(c.RequestID) || c.Revision < 0 {
		return out, errors.New("exact research turn, request and consent required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	d, rev, err := readAgents(ctx, tx)
	if err != nil {
		return out, err
	}
	if rev != c.Revision {
		return out, ErrConflict
	}
	ti := researchTurnIndex(d, c.Profile, c.ID)
	if ti < 0 {
		return out, errors.New("research turn unavailable")
	}
	t := &d.ResearchTurns[ti]
	if (t.Schema == researchLoopTurn) != (c.Schema == "wen-research-ai-start/2") || (t.Schema == researchLoopTurn && c.Step != t.CurrentStep) || (t.Schema != researchLoopTurn && c.Step != 0) {
		return out, errors.New("exact research version and step required")
	}
	if t.State != "prepared" || t.CurrentChildID != c.RequestID || t.Models[t.CurrentStep-1].Attempted {
		return out, errors.New("research turn already started or stopped; no replay")
	}

	for _, q := range d.AIRequests {
		if q.ProfileID == c.Profile && q.HeldMicrousd > 0 {
			return out, errors.New("profile has a pending or uncertain request; no additional call allowed")
		}
	}
	now := s.now().Unix()
	for i := range d.AIRequests {
		q := &d.AIRequests[i]
		if q.ID != c.RequestID {
			continue
		}
		if q.State != "prepared" || c.ConsentDigest != q.Digest || aiDigest(*q) != q.Digest || q.DispatchBoot != "" {
			return out, errors.New("unexecuted preview and exact disclosure digest required")
		}
		if _, err = reviewedAIContext(ctx, tx, d, rev, *q, now); err != nil {
			return out, err
		}
		if !s.aiConnectionValid(q.ConnectionRef) {
			return out, errors.New("selected fixture connection unavailable")
		}
		t.State, t.LastObservedAt = "queued", now
		t.ConsentBoot = s.bootID
		q.State, q.HeldMicrousd = "queued", aiRequestReservation(*q)
		if err = saveAgents(ctx, tx, d, rev); err != nil {
			return out, err
		}
		if err = tx.Commit(); err != nil {
			return out, err
		}
		return WENResearchAIReview{researchReviewSchema(*t), rev + 1, *t, *q, "unchanged", true}, nil
	}
	return out, errors.New("research child unavailable")
}

func settleResearchAI(d *agentDocument, q AgentAIRequest, now int64) {
	i := researchTurnIndex(*d, q.ProfileID, q.ResearchTurnID)
	if i < 0 {
		return
	}
	t := &d.ResearchTurns[i]
	if t.Schema == researchLoopTurn {
		settleLoopAI(t, q, now)
		return
	}
	t.State, t.StopReason = q.State, q.Failure
	if q.State == "unknown" {
		t.State = "held"
	}
	if now > t.LastObservedAt {
		t.LastObservedAt = now
	}
}

// Revalidate a consistent source snapshot immediately before EACH provider
// access/count/create operation, including when an adapter was test-injected.
func (s *Store) reviewAIRequest(ctx context.Context, q AgentAIRequest) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	d, rev, err := readAgents(ctx, tx)
	if err != nil {
		return err
	}
	if isResearchAI(q) {
		active := false
		for _, current := range d.AIRequests {
			if current.ID == q.ID && current.State == "running" && current.DispatchBoot == q.DispatchBoot && current.Digest == q.Digest {
				active = true
			}
		}
		var phase string
		if !active || tx.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase) != nil || phase == "paused" || !s.aiConnectionValid(q.ConnectionRef) {
			return errors.New("research stopped before provider operation")
		}
	}
	current, err := reviewedAIContext(ctx, tx, d, rev, q, s.now().Unix())
	if err != nil || current != q.Context {
		return errors.New("reviewed context changed before provider operation")
	}
	return nil
}

func validResearchLifecycle(t WENResearchTurn, q AgentAIRequest) bool {
	if t.StartedAt == 0 {
		// Preserve inert/old interrupted liabilities; the worker holds rather
		// than dispatching those records without the explicit start contract.
		if t.DeadlineAt != 0 || t.Models[0].Attempted {
			return false
		}
		if t.State == "queued" {
			return q.State == "queued" && q.DispatchBoot == "" && q.HeldMicrousd == aiRequestReservation(q) && q.SpentMicrousd == 0
		}
		return t.State == "prepared" || t.State == "held" || t.State == "cancelled"
	}
	if t.StartedAt < t.CreatedAt || t.StartedAt > t.ReviewExpiresAt || t.DeadlineAt != t.StartedAt+t.Bounds.WallSeconds || t.LastObservedAt < t.StartedAt || t.State == "prepared" {
		return false
	}
	if t.Models[0].Attempted != (q.DispatchBoot != "") {
		return false
	}
	switch t.State {
	case "queued":
		return q.State == "queued" && !t.Models[0].Attempted && q.HeldMicrousd == aiRequestReservation(q) && q.SpentMicrousd == 0
	case "running":
		return q.State == "running" && t.Models[0].Attempted && q.HeldMicrousd == aiRequestReservation(q) && q.SpentMicrousd == 0
	case "completed", "failed":
		return q.State == t.State && t.Models[0].Attempted && q.HeldMicrousd == 0
	case "held":
		return q.State == "unknown" && q.HeldMicrousd == aiRequestReservation(q)
	case "cancelled":
		return q.State == "cancelled" && (t.Models[0].Attempted || q.HeldMicrousd == 0)
	}
	return false
}
