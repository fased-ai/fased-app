package core

// Research preparation is inert. Only an exact-consented fixture child can run;
// its durable record is a bounded input/obligation ledger, not a task or grant.
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

const researchContextVersion = "manual-ai-research-v1"
const researchFutureChildBytes = 96*1024 + agentAIResultReserve

type WENResearchBounds struct {
	MaxModelAttempts int   `json:"maxModelAttempts"`
	MaxToolAttempts  int   `json:"maxToolAttempts"`
	WallSeconds      int64 `json:"wallSeconds"`
	BudgetMicrousd   int64 `json:"budgetMicrousd"`
}
type WENResearchAIPrepareInput struct {
	Schema          string               `json:"schema"`
	Selection       WENResearchReadInput `json:"selection"`
	Prompt          string               `json:"prompt"`
	Model           string               `json:"model"`
	ConnectionRef   string               `json:"connectionRef,omitempty"`
	MemorySelection AIMemorySelection    `json:"memorySelection"`
	Limits          AILimits             `json:"limits"`
	Bounds          WENResearchBounds    `json:"bounds"`
}
type WENResearchAISelection struct {
	Schema   string `json:"schema"`
	Profile  string `json:"profile"`
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}
type researchFactResult struct {
	ID         string              `json:"id"`
	Tool       string              `json:"tool"`
	SourceRefs []string            `json:"sourceRefs"`
	Sources    []WENReadProvenance `json:"sources"`
	Status     string              `json:"status"`
	Data       json.RawMessage     `json:"data,omitempty"`
	Error      string              `json:"error,omitempty"`
}
type WENResearchFacts struct {
	Schema    string               `json:"schema"`
	Profile   string               `json:"profile"`
	SessionID string               `json:"sessionId"`
	Sources   []WENReadProvenance  `json:"sources"`
	Results   []researchFactResult `json:"results"`
}
type researchBaseSource struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}
type researchRoot struct {
	Schema          string               `json:"schema"`
	Profile         string               `json:"profile"`
	SessionID       string               `json:"sessionId"`
	MemorySelection AIMemorySelection    `json:"memorySelection"`
	Context         json.RawMessage      `json:"context"`
	BaseSources     []researchBaseSource `json:"baseSources"`
	FactsSHA256     string               `json:"factsSHA256"`
}
type researchModelRecord struct {
	ID        string `json:"id"`
	Step      int    `json:"step"`
	Digest    string `json:"digest"`
	Attempted bool   `json:"attempted"`
}
type researchToolRecord struct {
	ID        string          `json:"id"`
	Call      WENReadCall     `json:"call"`
	StartedAt int64           `json:"startedAt"`
	EndedAt   int64           `json:"endedAt"`
	Result    json.RawMessage `json:"result"`
	SHA256    string          `json:"sha256"`
}
type WENResearchTurn struct {
	Schema          string                `json:"schema"`
	ID              string                `json:"id"`
	Profile         string                `json:"profile"`
	SessionID       string                `json:"sessionId"`
	CreatedAt       int64                 `json:"createdAt"`
	ReviewExpiresAt int64                 `json:"reviewExpiresAt"`
	RootSHA256      string                `json:"rootSHA256"`
	Root            researchRoot          `json:"root"`
	Facts           WENResearchFacts      `json:"facts"`
	Selection       WENResearchReadInput  `json:"selection"`
	Bounds          WENResearchBounds     `json:"bounds"`
	State           string                `json:"state"`
	StartedAt       int64                 `json:"startedAt"`
	DeadlineAt      int64                 `json:"deadlineAt"`
	LastObservedAt  int64                 `json:"lastObservedAt"`
	ConsentBoot     string                `json:"consentBoot,omitempty"`
	CurrentChildID  string                `json:"currentChildId"`
	CurrentStep     int                   `json:"currentStep"`
	StopReason      string                `json:"stopReason"`
	Models          []researchModelRecord `json:"models"`
	Tools           []researchToolRecord  `json:"tools"`
}
type WENResearchAIReview struct {
	Schema          string          `json:"schema"`
	Revision        int64           `json:"revision"`
	Turn            WENResearchTurn `json:"turn"`
	Request         AgentAIRequest  `json:"request"`
	SourceState     string          `json:"sourceState"`
	DispatchEnabled bool            `json:"dispatchEnabled"`
}

func researchHash(v any) string { b, _ := json.Marshal(v); return wenHash(b) }
func validResearchBounds(b WENResearchBounds) bool {
	return b.MaxModelAttempts >= 1 && b.MaxModelAttempts <= 3 && b.MaxToolAttempts >= 1 && b.MaxToolAttempts <= 8 && b.WallSeconds >= 1 && b.WallSeconds <= 90 && b.BudgetMicrousd >= 1 && b.BudgetMicrousd <= 3000000
}
func isResearchAI(q AgentAIRequest) bool {
	var v struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal([]byte(q.Context), &v)
	return q.ResearchTurnID != "" || q.ResearchStep != 0 || strings.HasPrefix(v.Version, "manual-ai-research")
}
func researchFutureSlots(t WENResearchTurn) int {
	if t.State == "prepared" || t.State == "queued" || t.State == "running" {
		return t.Bounds.MaxModelAttempts - len(t.Models)
	}
	return 0
}
func researchStorage(d agentDocument) int {
	n := 0
	for _, t := range d.ResearchTurns {
		if t.State == "prepared" || t.State == "queued" || t.State == "running" {
			b, _ := json.Marshal(t)
			// Reserve the whole bounded turn (including future tool records) and
			// worst-case encoded context/wire/metadata plus result for future children.
			n += wenReadLimit - len(b) + researchFutureSlots(t)*researchFutureChildBytes
		}
	}
	return n
}
func researchContext(t WENResearchTurn) string {
	research := struct {
		Schema     string            `json:"schema"`
		TurnID     string            `json:"turnId"`
		RootSHA256 string            `json:"rootSHA256"`
		Facts      WENResearchFacts  `json:"facts"`
		Step       int               `json:"step"`
		PriorSteps []json.RawMessage `json:"priorSteps"`
		Remaining  WENResearchBounds `json:"remaining"`
	}{"wen-research-turn/1", t.ID, t.RootSHA256, t.Facts, 1, []json.RawMessage{}, t.Bounds}
	b, _ := json.Marshal(struct {
		Version         string            `json:"version"`
		MemorySelection AIMemorySelection `json:"memorySelection"`
		Context         json.RawMessage   `json:"context"`
		Research        any               `json:"research"`
	}{researchContextVersion, t.Root.MemorySelection, t.Root.Context, research})
	return string(b)
}

func researchFacts(ctx context.Context, r snapshotReader, c WENResearchReadInput) (WENResearchFacts, error) {
	p, sources, err := resolveWENRead(ctx, r, c)
	if err != nil {
		return WENResearchFacts{}, err
	}
	seed := WENResearchReadResult{Schema: "wen-research-result/1", Profile: c.Profile, SessionID: c.SessionID, Sources: p, Results: []WENReadResult{}}
	result := runWENRead(ctx, c, seed, sources, time.Now(), time.Now, projectWENRead)
	if result.Status != "complete" {
		return WENResearchFacts{}, errors.New("complete successful selected research is required; narrow the selection explicitly")
	}
	f := WENResearchFacts{"wen-research-facts/1", c.Profile, c.SessionID, p, []researchFactResult{}}
	for _, v := range result.Results {
		b, e := json.Marshal(v.Data)
		if e != nil {
			return f, e
		}
		f.Results = append(f.Results, researchFactResult{v.ID, v.Tool, v.SourceRefs, v.Sources, v.Status, b, v.Error})
	}
	return f, ctx.Err()
}

func researchBase(d agentDocument, profile, session, prompt string, choice AIMemorySelection, now int64) (researchRoot, error) {
	base, err := aiContextWithMemory(d, profile, session, prompt, now, &choice)
	if err != nil {
		return researchRoot{}, err
	}
	var env struct {
		Version   string            `json:"version"`
		Selection AIMemorySelection `json:"memorySelection"`
		Context   json.RawMessage   `json:"context"`
	}
	if err = update.StrictJSON([]byte(base), &env); err != nil {
		return researchRoot{}, err
	}
	r := researchRoot{Schema: "wen-research-root/1", Profile: profile, SessionID: session, MemorySelection: env.Selection, Context: env.Context, BaseSources: []researchBaseSource{}}
	add := func(kind, id string, v any) {
		r.BaseSources = append(r.BaseSources, researchBaseSource{kind, id, researchHash(v)})
	}
	pi := agentProfile(d, profile)
	add("persona", profile, d.Profiles[pi].Persona)
	seen := map[string]bool{}
	for _, m := range d.Messages {
		if m.ProfileID == profile && m.SessionID == session && m.Role == "user" {
			if seen["message:"+m.ID] {
				return r, errors.New("ambiguous base message")
			}
			seen["message:"+m.ID] = true
			for _, other := range d.Messages {
				if other.ID == m.ID && (other.ProfileID != profile || other.SessionID != session) {
					return r, errors.New("ambiguous base message")
				}
			}
			add("message", m.ID, m)
		}
	}
	for _, q := range d.AIRequests {
		if q.ProfileID == profile && q.SessionID == session && q.State == "completed" && q.TaskID == "" && !isResearchAI(q) {
			if seen["request:"+q.ID] {
				return r, errors.New("ambiguous base request")
			}
			seen["request:"+q.ID] = true
			for _, other := range d.AIRequests {
				if other.ID == q.ID && (other.ProfileID != profile || other.SessionID != session) {
					return r, errors.New("ambiguous base request")
				}
			}
			add("manual-request", q.ID, struct {
				Prompt, Answer string
				CreatedAt      int64
			}{q.Prompt, q.Answer, q.CreatedAt})
		}
	}
	_, notes, err := selectedAIMemories(d, profile, choice, now)
	if err != nil {
		return r, err
	}
	for _, m := range notes {
		add("memory", m.ID, m)
	}
	return r, nil
}

func validateResearchStorage(raw []byte, d agentDocument) error {
	bad := errors.New("unsupported or inconsistent research state; preserve and recover")
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return bad
	}
	if v, ok := fields["researchTurns"]; ok {
		var strict []WENResearchTurn
		if update.StrictJSON(v, &strict) != nil {
			return bad
		}
	}
	if len(d.ResearchTurns) > 10 {
		return bad
	}
	seen := map[string]bool{}
	for _, t := range d.ResearchTurns {
		b, _ := json.Marshal(t)
		if t.Schema != "wen-research-turn/1" || !readRefValid(t.ID) || seen[t.ID] || !validResearchBounds(t.Bounds) || len(b) > wenReadLimit || t.RootSHA256 != researchHash(t.Root) || t.Root.FactsSHA256 != researchHash(t.Facts) || t.Root.Schema != "wen-research-root/1" || t.Facts.Schema != "wen-research-facts/1" || t.Profile != t.Root.Profile || t.SessionID != t.Root.SessionID || t.Profile != t.Facts.Profile || t.SessionID != t.Facts.SessionID || len(t.Models) != 1 || len(t.Tools) != 0 || t.CurrentStep != 1 || t.CreatedAt <= 0 || t.ReviewExpiresAt < t.CreatedAt || t.LastObservedAt < t.CreatedAt {
			return bad
		}
		if t.State != "prepared" && t.State != "queued" && t.State != "running" && t.State != "completed" && t.State != "failed" && t.State != "cancelled" && t.State != "held" {
			return bad
		}
		seen[t.ID] = true
		m := t.Models[0]
		if m.ID != t.CurrentChildID || m.Step != 1 {
			return bad
		}
		found := 0
		for _, q := range d.AIRequests {
			if q.ID == m.ID {
				found++
				wire, e := aiWire(q)
				if e != nil || q.ProfileID != t.Profile || q.SessionID != t.SessionID || q.ResearchTurnID != t.ID || q.ResearchStep != 1 || q.TaskID != "" || q.TaskDigest != "" || q.News != nil || q.Context != researchContext(t) || len(q.Context) > 8000 || q.Disclosure != string(wire) || q.Digest != m.Digest || q.Digest != aiDigest(q) || !aiWireWithinBound(q, wire) || q.HeldMicrousd < 0 || q.SpentMicrousd < 0 || q.HeldMicrousd+q.SpentMicrousd > t.Bounds.BudgetMicrousd || !validResearchLifecycle(t, q) {
					return bad
				}
			}
		}
		if found != 1 {
			return bad
		}
	}
	for _, q := range d.AIRequests {
		if isResearchAI(q) && (!seen[q.ResearchTurnID] || q.ResearchStep != 1) {
			return bad
		}
	}
	if taskAIRequestSlots(d) > 10 {
		return bad
	}
	return nil
}

func migrateWENResearchAI(ctx context.Context, db *sql.DB) error {
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
	var d agentDocument
	if err = update.StrictJSON([]byte(raw), &d); err != nil {
		return err
	}
	if len(d.ResearchTurns) != 0 {
		return errors.New("unexpected research state before schema 12; preserve and recover")
	}
	for _, q := range d.AIRequests {
		if isResearchAI(q) {
			return errors.New("unexpected research request before schema 12")
		}
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal([]byte(raw), &fields); err != nil {
		return err
	}
	fields["researchTurns"] = json.RawMessage("[]")
	b, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	if len(b) > agentDocumentLimit {
		return errors.New("research migration exceeds local capacity")
	}
	if _, err = tx.ExecContext(ctx, "UPDATE agent_workspace SET document=? WHERE id=1", string(b)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA user_version=12"); err != nil {
		return err
	}
	return tx.Commit()
}

func DecodeWENResearchAIPrepare(raw []byte) (WENResearchAIPrepareInput, error) {
	var c WENResearchAIPrepareInput
	if len(raw) > wenReadLimit || update.StrictJSON(raw, &c) != nil || c.Schema != "wen-research-ai-prepare/1" || !validWENRead(c.Selection) || !validAgentText(c.Prompt, 2000, true) || !validAgentText(c.Model, 120, true) || !validAgentText(c.ConnectionRef, 200, false) || !validResearchBounds(c.Bounds) || c.MemorySelection.Mode == "" {
		return c, errors.New("invalid explicit research preparation")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return c, errors.New("invalid research envelope")
	}
	for _, key := range []string{"schema", "selection", "prompt", "model", "memorySelection", "limits", "bounds"} {
		if len(fields[key]) == 0 || string(fields[key]) == "null" {
			return c, errors.New("explicit research fields required")
		}
	}
	if _, err := DecodeWENResearchRead(fields["selection"]); err != nil {
		return c, err
	}
	return c, nil
}
func (s *Store) PrepareWENResearchAI(ctx context.Context, c WENResearchAIPrepareInput) (WENResearchAIReview, error) {
	var out WENResearchAIReview
	encoded, _ := json.Marshal(c)
	if _, err := DecodeWENResearchAIPrepare(encoded); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	d, rev, err := readAgents(ctx, tx)
	if err != nil {
		return out, err
	}
	if len(d.ResearchTurns) >= 10 || taskAIRequestSlots(d)+c.Bounds.MaxModelAttempts > 10 {
		return out, errors.New("research/request capacity reached; preserve existing records")
	}
	facts, err := researchFacts(ctx, tx, c.Selection)
	if err != nil {
		return out, err
	}
	now := s.now().Unix()
	root, err := researchBase(d, c.Selection.Profile, c.Selection.SessionID, c.Prompt, c.MemorySelection, now)
	if err != nil {
		return out, err
	}
	root.FactsSHA256 = researchHash(facts)
	id, err := randomToken()
	if err != nil {
		return out, err
	}
	child, err := randomToken()
	if err != nil {
		return out, err
	}
	t := WENResearchTurn{Schema: "wen-research-turn/1", ID: id, Profile: c.Selection.Profile, SessionID: c.Selection.SessionID, CreatedAt: now, ReviewExpiresAt: now + 600, LastObservedAt: now, RootSHA256: researchHash(root), Root: root, Facts: facts, Selection: c.Selection, Bounds: c.Bounds, State: "prepared", CurrentChildID: child, CurrentStep: 1, Models: []researchModelRecord{}, Tools: []researchToolRecord{}}
	l := c.Limits
	if l.MaxInputTokens < 1000 || l.MaxInputTokens > 32000 || l.MaxOutputTokens < 128 || l.MaxOutputTokens > 1024 || l.DeadlineSeconds < 1 || l.DeadlineSeconds > 5 || l.BudgetMicrousd < 1 || l.BudgetMicrousd > 1000000 {
		return out, errors.New("invalid explicit request limits")
	}
	q := AgentAIRequest{ID: child, ResearchTurnID: id, ResearchStep: 1, ProfileID: t.Profile, SessionID: t.SessionID, Prompt: c.Prompt, Model: c.Model, ConnectionRef: c.ConnectionRef, Context: researchContext(t), Limits: l, State: "prepared", CreatedAt: now, Cards: []AIActionCard{}}
	if c.Model != fixtureAIModel {
		p := openAIModelPolicy()
		q.Policy = &p
	}
	if err = validateAIModelPolicy(q, now); err != nil {
		return out, err
	}
	if len(q.Context) > 8000 {
		return out, errors.New("complete research context exceeds 8000 bytes; narrow and review explicitly")
	}
	if aiRequestReservation(q) > l.BudgetMicrousd || int64(c.Bounds.MaxModelAttempts)*aiRequestReservation(q) > c.Bounds.BudgetMicrousd {
		return out, errors.New("insufficient aggregate research budget for reserved requests")
	}
	wire, err := aiWire(q)
	if err != nil || !aiWireWithinBound(q, wire) {
		return out, errors.New("complete research wire exceeds input-token bound")
	}
	q.Disclosure = string(wire)
	q.Digest = aiDigest(q)
	t.Models = append(t.Models, researchModelRecord{ID: child, Step: 1, Digest: q.Digest})
	d.ResearchTurns = append(d.ResearchTurns, t)
	d.AIRequests = append(d.AIRequests, q)
	if err = saveAgents(ctx, tx, d, rev); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	return WENResearchAIReview{"wen-research-ai-review/1", rev + 1, t, q, "unchanged", false}, nil
}

func researchTurnIndex(d agentDocument, profile, id string) int {
	for i, t := range d.ResearchTurns {
		if t.Profile == profile && t.ID == id {
			return i
		}
	}
	return -1
}
func cancelResearch(d *agentDocument, profile, id string, now int64) error {
	i := researchTurnIndex(*d, profile, id)
	if i < 0 {
		return errors.New("research turn unavailable")
	}
	t := &d.ResearchTurns[i]
	if t.State == "cancelled" {
		return errors.New("research turn already cancelled")
	}
	for i := range d.AIRequests {
		q := &d.AIRequests[i]
		if q.ID == t.CurrentChildID {
			if q.State != "prepared" && q.State != "queued" && q.State != "running" && q.State != "unknown" {
				return errors.New("research request already terminal")
			}
			if (q.State == "prepared" || q.State == "queued") && q.DispatchBoot == "" {
				q.HeldMicrousd = 0
			}
			q.State = "cancelled"
			q.Failure = "Research cancelled; uncertain usage remains reserved."
		}
	}
	t.State = "cancelled"
	t.StopReason = "cancelled by owner"
	if now > t.LastObservedAt {
		t.LastObservedAt = now
	}
	return nil
}
func (s *Store) WENResearchAI(ctx context.Context, c WENResearchAISelection, cancel bool) (WENResearchAIReview, error) {
	var out WENResearchAIReview
	if c.Schema != "wen-research-ai-selection/1" || !readRefValid(c.Profile) || !readRefValid(c.ID) || c.Revision < 0 {
		return out, errors.New("select one research turn")
	}
	ctx, stop := context.WithTimeout(ctx, 90*time.Second)
	defer stop()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	d, rev, err := readAgents(ctx, tx)
	if err != nil {
		return out, err
	}
	i := researchTurnIndex(d, c.Profile, c.ID)
	if i < 0 {
		return out, errors.New("research turn unavailable")
	}
	if cancel {
		if c.Revision != rev {
			return out, ErrConflict
		}
		if err = cancelResearch(&d, c.Profile, c.ID, s.now().Unix()); err != nil {
			return out, err
		}
		if err = saveAgents(ctx, tx, d, rev); err != nil {
			return out, err
		}
		rev++
	}
	t := d.ResearchTurns[i]
	var q AgentAIRequest
	for _, v := range d.AIRequests {
		if v.ID == t.CurrentChildID {
			q = v
		}
	}
	state := researchSourceState(ctx, tx, d, rev, t, q, s.now().Unix())
	if cancel {
		if err = tx.Commit(); err != nil {
			return out, err
		}
	}
	return WENResearchAIReview{"wen-research-ai-review/1", rev, t, q, state, false}, nil
}

func (h *HTTPHandler) wenResearchAIAPI(w http.ResponseWriter, r *http.Request, token string, action string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if len(r.URL.Query()) != 0 {
		writeError(w, 400, "unexpected query")
		return
	}
	if !h.mutation(w, r) || !h.csrf(w, r, token) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, wenReadLimit)
	defer r.Body.Close()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 413, "complete research request exceeds 64 KiB")
		return
	}
	var result WENResearchAIReview
	if action == "start" {
		var c WENResearchAIStartInput
		err = update.StrictJSON(raw, &c)
		if err == nil {
			result, err = h.store.StartWENResearchAI(r.Context(), c, h.agentFixture)
		}
	} else if action == "prepare" {
		var c WENResearchAIPrepareInput
		c, err = DecodeWENResearchAIPrepare(raw)
		if err == nil {
			result, err = h.store.PrepareWENResearchAI(r.Context(), c)
		}
	} else {
		var c WENResearchAISelection
		err = update.StrictJSON(raw, &c)
		if err == nil {
			result, err = h.store.WENResearchAI(r.Context(), c, action == "cancel")
		}
	}
	if err != nil {
		status := 400
		var selected wenReadError
		if errors.As(err, &selected) {
			status = selected.status
		} else if errors.Is(err, ErrConflict) {
			status = 409
		} else if errors.Is(err, ErrCorePaused) {
			status = 423
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, 200, result)
}
