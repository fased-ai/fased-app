package core

// A pilot narrows existing account, Chat and task authority. It never replaces
// those permissions. Ordinary constructors leave target nil; only controlled
// tests configure it. Live activation still needs a separately reviewed patch.
// All mutable pilot state uses provider.mu. No permit survives a core restart;
// attempted requests and uncertain costs remain in the existing durable journal.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type OpenAIPilotAllowance struct {
	ID                 string   `json:"id"`
	InstanceID         string   `json:"instanceId"`
	BootID             string   `json:"bootId"`
	ConnectionRef      string   `json:"connectionRef"`
	ConnectionEpoch    uint64   `json:"connectionEpoch"`
	AccessEpoch        uint64   `json:"accessEpoch"`
	ProfileID          string   `json:"profileId"`
	Model              string   `json:"model"`
	PolicyID           string   `json:"policyId"`
	Revision           int64    `json:"revision"`
	CreatedAt          int64    `json:"createdAt"`
	ExpiresAt          int64    `json:"expiresAt"`
	MaxRequests        int      `json:"maxRequests"`
	BudgetMicrousd     int64    `json:"budgetMicrousd"`
	LaneBudgetMicrousd int64    `json:"laneBudgetMicrousd"`
	Limits             AILimits `json:"limits"`
	UsedRequests       int      `json:"usedRequests"`
	AllocatedMicrousd  int64    `json:"allocatedMicrousd"`
}

type openAIPilotTarget struct{ instance, ref, profile, model string }
type openAIPilotEntry struct {
	digest string
	amount int64
	task   bool
	stage  int
}
type openAIPilotState struct {
	binding   *pilotActivationBinding
	bootstrap bool
	transport *openAITransport
	target    *openAIPilotTarget
	permit    *OpenAIPilotAllowance
	entries   map[string]openAIPilotEntry
	last      int64
	used      bool
	revoked   bool
	listed    bool
}

func pilotDigest(p OpenAIPilotAllowance) string {
	p.ID, p.UsedRequests, p.AllocatedMicrousd = "", 0, 0
	b, _ := json.Marshal(p)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func pilotAction(action string) bool { return strings.HasPrefix(action, "pilot-") }
func (s *Store) pilotTargetLocked(ref string) bool {
	t := s.pilot.target
	return t != nil && t.instance == s.instanceID && t.ref == ref && validConnectionRef(ref) && t.profile != "" && t.model == admittedOpenAIModel
}
func (s *Store) pilotCurrentLocked() bool {
	p, now := s.pilot.permit, s.now().Unix()
	if p == nil || !s.pilotTargetLocked(p.ConnectionRef) || p.InstanceID != s.instanceID || p.BootID != s.bootID || p.ProfileID != s.pilot.target.profile || p.Model != s.pilot.target.model || p.ID != pilotDigest(*p) || p.AccessEpoch != s.chatEpoch.Load() || s.pilot.revoked {
		return false
	}
	if now < s.pilot.last || now < p.CreatedAt || now >= p.ExpiresAt {
		s.pilot.revoked = true // Clock rollback/expiry cannot be undone by waiting.
		return false
	}
	s.pilot.last = now
	state := s.credentialLocked(p.ConnectionRef)
	return state.ref == p.ConnectionRef && state.epoch == p.ConnectionEpoch && len(state.key) > 0
}
func (s *Store) revokePilotLocked(ref string) {
	if s.pilot.permit != nil && (ref == "" || ref == s.pilot.permit.ConnectionRef) {
		s.pilot.revoked = true
	}
}
func (s *Store) pilotViewLocked(v *AIConnectionView) {
	v.PilotAvailable = s.pilotTargetLocked(v.Ref)
	if !v.PilotAvailable || s.pilot.permit == nil {
		return
	}
	copy := *s.pilot.permit
	v.Pilot = &copy
	v.PilotState = "active"
	if !s.pilotCurrentLocked() {
		v.PilotState = "revoked"
	} else if copy.UsedRequests >= copy.MaxRequests {
		v.PilotState = "exhausted"
	}
}
func pilotLimitsFit(l, cap AILimits) bool {
	return validAILimits(l) && l.MaxInputTokens <= cap.MaxInputTokens && l.MaxOutputTokens <= cap.MaxOutputTokens && l.DeadlineSeconds <= cap.DeadlineSeconds && l.BudgetMicrousd <= cap.BudgetMicrousd
}
func (s *Store) reviewPilotLocked(d agentDocument, ref, profile string, revision int64) (OpenAIPilotAllowance, error) {
	p := OpenAIPilotAllowance{InstanceID: s.instanceID, BootID: s.bootID, ConnectionRef: ref, ProfileID: profile, Model: admittedOpenAIModel, PolicyID: openAIModelPolicy().ID, Revision: revision, CreatedAt: s.now().Unix(), ExpiresAt: s.now().Unix() + 900, MaxRequests: 4, BudgetMicrousd: 500000, LaneBudgetMicrousd: 250000, Limits: AILimits{2000, 256, 5, 100000}}
	if !s.pilotTargetLocked(ref) || profile != s.pilot.target.profile || agentProfile(d, profile) < 0 || s.pilot.used {
		return p, errors.New("pilot unavailable; no live activation")
	}
	v, e := s.aiConnectionLocked(ref)
	if e != nil || v.Ref != ref || v.Provider != "openai" || v.CredentialMode != "openai" || v.Locked || validateAIModelPolicy(AgentAIRequest{ConnectionRef: ref, Model: p.Model, Policy: chatModelPolicy(p.Model), Limits: p.Limits}, p.CreatedAt) != nil || openAIModelPolicy().ExpiresAt < p.ExpiresAt {
		return p, errors.New("pilot account or current model unavailable")
	}
	for _, q := range d.AIRequests {
		if q.ProfileID == profile && (q.HeldMicrousd > 0 || q.DispatchBoot != "") {
			return p, errors.New("pilot requires an unused profile without pending usage")
		}
	}
	p.ConnectionEpoch = s.credentialLocked(ref).epoch
	p.AccessEpoch = s.chatEpoch.Load()
	p.ID = pilotDigest(p)
	return p, nil
}
func (s *Store) changeOpenAIPilot(ctx context.Context, c AIConnectionCommand) (AIConnectionView, error) {
	if c.Key != "" || c.Provider != "" || c.CredentialMode != "" || c.Name != "" || c.Allowance != nil || len(c.TaskIDs) > 0 || c.AuthorizationSeconds != 0 {
		return AIConnectionView{}, errors.New("pilot accepts one exact reviewed scope only")
	}
	// Preserve the established SQLite -> credential-mutex order.
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return AIConnectionView{}, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "UPDATE instance SET revision=revision WHERE id=1"); e != nil {
		return AIConnectionView{}, metadataWriteError(e)
	}
	d, revision, e := readAgents(ctx, tx)
	if e != nil {
		return AIConnectionView{}, e
	}
	if c.Action == "pilot-review" || c.Action == "pilot-allow" {
		profile := c.ProfileID
		if c.Action == "pilot-allow" && c.Pilot != nil {
			profile = c.Pilot.ProfileID
		}
		archived, err := pilotProfileHasHistory(ctx, tx, profile)
		if err != nil {
			return AIConnectionView{}, err
		}
		if archived {
			return AIConnectionView{}, errors.New("pilot requires an unused profile without immutable conversation history")
		}
	}
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	v, e := s.aiConnectionLocked(c.Ref)
	if e != nil {
		return v, e
	}
	if !s.pilotTargetLocked(c.Ref) {
		return v, errors.New("pilot unavailable; no live activation")
	}
	if c.Action == "pilot-revoke" {
		if c.Pilot != nil || c.Passphrase != "" || c.ProfileID != "" || c.Revision != 0 || c.ConsentDigest != "" {
			return v, errors.New("revoke accepts an account only")
		}
		s.revokePilotLocked(c.Ref)
		return s.aiConnectionLocked(c.Ref)
	}
	if c.Action == "pilot-review" {
		if c.Pilot != nil || c.Passphrase != "" || c.ConsentDigest != "" || c.Revision != revision {
			return v, ErrConflict
		}
		p, err := s.reviewPilotLocked(d, c.Ref, c.ProfileID, revision)
		if err != nil {
			return v, err
		}
		v.PilotReview = &p
		return v, nil
	}
	if c.Action != "pilot-allow" || c.Pilot == nil || c.ProfileID != "" || c.Revision != 0 || !validPassword(c.Passphrase) {
		return v, errors.New("reviewed pilot and vault passphrase required")
	}
	p := *c.Pilot
	expected, e := s.reviewPilotLocked(d, c.Ref, p.ProfileID, revision)
	if e != nil {
		return v, e
	}
	expected.CreatedAt, expected.ExpiresAt = p.CreatedAt, p.CreatedAt+900
	expected.ID = pilotDigest(expected)
	if p != expected || p.ID != c.ConsentDigest || p.CreatedAt > s.now().Unix() || s.now().Unix()-p.CreatedAt > 300 || p.ExpiresAt <= s.now().Unix() {
		return v, errors.New("pilot scope changed; review again")
	}
	env, e := s.readAIVault(c.Ref)
	if e != nil {
		return v, e
	}
	plain, e := s.openAIVault(env, c.Passphrase)
	defer clear(plain)
	if e != nil {
		return v, errors.New("vault unlock failed")
	}
	s.pilot.permit, s.pilot.used, s.pilot.last = &p, true, s.now().Unix()
	s.pilot.entries = make(map[string]openAIPilotEntry)
	return s.aiConnectionLocked(c.Ref)
}

// Conservative allocation never refunds an attempted reservation. This keeps
// stop, unknown usage and failed commits from replenishing the shared ceiling.
func (s *Store) reservePilot(q *AgentAIRequest) bool {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	if s.pilot.target == nil {
		return true
	}
	if !s.pilotRequestLocked(*q, false) {
		return false
	}
	p := s.pilot.permit
	amount := aiRequestReservation(*q)
	if _, exists := s.pilot.entries[q.ID]; exists || amount <= 0 || p.UsedRequests >= p.MaxRequests || p.AllocatedMicrousd+amount > p.BudgetMicrousd {
		return false
	}
	count, allocated := 0, int64(0)
	for _, entry := range s.pilot.entries {
		if entry.task == (q.TaskID != "") {
			count++
			allocated += entry.amount
		}
	}
	max := 3
	if q.TaskID != "" {
		max = 1
	}
	if count >= max || allocated+amount > p.LaneBudgetMicrousd {
		return false
	}
	s.pilot.entries[q.ID] = openAIPilotEntry{digest: aiDigest(*q), amount: amount, task: q.TaskID != ""}
	p.UsedRequests++
	p.AllocatedMicrousd += amount
	q.pilotID = p.ID
	return true
}
func (s *Store) pilotRequestLocked(q AgentAIRequest, reserved bool) bool {
	if s.pilot.target == nil {
		return true
	}
	if !s.pilotCurrentLocked() {
		return false
	}
	p := s.pilot.permit
	if q.ConnectionRef != p.ConnectionRef || q.ProfileID != p.ProfileID || q.Model != p.Model || q.Policy == nil || q.Policy.ID != p.PolicyID || q.News != nil || isResearchAI(q) || q.CreatedAt < p.CreatedAt || !pilotLimitsFit(q.Limits, p.Limits) || (q.TaskID == "" && q.ChatPermissionID == "") {
		return false
	}
	if reserved {
		entry, ok := s.pilot.entries[q.ID]
		return ok && q.pilotID == p.ID && entry.digest == aiDigest(q)
	}
	return true
}
func (s *Store) pilotGrantAllowed(ref, profile, model string, expiry int64, max int, budget int64, limits AILimits, task bool) bool {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	return s.pilotGrantAllowedLocked(ref, profile, model, expiry, max, budget, limits, task)
}
func (s *Store) pilotGrantAllowedLocked(ref, profile, model string, expiry int64, max int, budget int64, limits AILimits, task bool) bool {
	if s.pilot.target == nil {
		return true
	}
	if !s.pilotCurrentLocked() {
		return false
	}
	p := s.pilot.permit
	cap := 3
	if task {
		cap = 1
	}
	return ref == p.ConnectionRef && profile == p.ProfileID && model == p.Model && expiry <= p.ExpiresAt && max <= cap && budget <= p.LaneBudgetMicrousd && pilotLimitsFit(limits, p.Limits)
}
func (s *Store) pilotExchangeLocked(q *AgentAIRequest, ref string, r *http.Request, listing bool) bool {
	if s.pilot.target == nil {
		return true
	}
	if !s.pilotCurrentLocked() || ref != s.pilot.permit.ConnectionRef {
		return false
	}
	if listing {
		if s.pilot.listed || r.Method != "GET" || r.URL.Path != "/v1/models" {
			return false
		}
		s.pilot.listed = true
		return true
	}
	if q == nil || !s.pilotRequestLocked(*q, true) {
		return false
	}
	entry := s.pilot.entries[q.ID]
	paths := []string{"/v1/models/" + q.Model, "/v1/responses/input_tokens", "/v1/responses"}
	methods := []string{"GET", "POST", "POST"}
	if entry.stage >= len(paths) || r.Method != methods[entry.stage] || r.URL.Path != paths[entry.stage] {
		return false
	}
	entry.stage++ // Consumed before I/O: no replay after a failed exchange.
	s.pilot.entries[q.ID] = entry
	return true
}
