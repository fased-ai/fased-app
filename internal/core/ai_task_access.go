package core

// Ephemeral credential access for already-reviewed tasks. This never changes a
// task's disclosure/schedule/budget or unlocks interactive Chat after 15 minutes.
import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

type TaskAccessBinding struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}
type TaskAccessAllowance struct {
	ID                string              `json:"id"`
	ConnectionRef     string              `json:"connectionRef"`
	ProfileID         string              `json:"profileId"`
	Model             string              `json:"model"`
	Tasks             []TaskAccessBinding `json:"tasks"`
	Limits            AILimits            `json:"limits"`
	BootID            string              `json:"bootId"`
	Revision          int64               `json:"revision"`
	CreatedAt         int64               `json:"createdAt"`
	ExpiresAt         int64               `json:"expiresAt"`
	MaxRequests       int                 `json:"maxRequests"`
	BudgetMicrousd    int64               `json:"budgetMicrousd"`
	UsedRequests      int                 `json:"usedRequests"`
	AllocatedMicrousd int64               `json:"allocatedMicrousd"`
}

func taskAccessDigest(a TaskAccessAllowance) string {
	a.ID = ""
	a.UsedRequests = 0
	a.AllocatedMicrousd = 0
	b, _ := json.Marshal(a)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s *Store) taskAccessCurrent(a *TaskAccessAllowance, state *providerCredential) bool {
	now := s.now().Unix()
	if a == nil || a.ID != taskAccessDigest(*a) || a.BootID != s.bootID || now < a.CreatedAt || now >= a.ExpiresAt || now < state.taskAccessLast || a.ConnectionRef != state.ref || len(state.key) == 0 {
		return false
	}
	state.taskAccessLast = now
	return true
}
func (s *Store) reviewTaskAccess(d agentDocument, c AIConnectionCommand, revision int64) (TaskAccessAllowance, error) {
	now := s.now().Unix()
	a := TaskAccessAllowance{ConnectionRef: c.Ref, ProfileID: c.ProfileID, Revision: revision, BootID: s.bootID, CreatedAt: now, ExpiresAt: now + c.AuthorizationSeconds, Tasks: []TaskAccessBinding{}}
	if c.Revision != revision || agentProfile(d, c.ProfileID) < 0 || len(c.TaskIDs) < 1 || len(c.TaskIDs) > 10 || c.AuthorizationSeconds < 60 || c.AuthorizationSeconds > 86400 {
		return a, errors.New("select current-profile reviewed tasks and 1 minute–24 hours of credential access")
	}
	selected := map[string]bool{}
	for _, id := range c.TaskIDs {
		if selected[id] {
			return a, errors.New("duplicate task selection")
		}
		selected[id] = true
	}
	for _, q := range d.AIRequests {
		if q.ProfileID == c.ProfileID && q.HeldMicrousd > 0 {
			return a, errors.New("pending or uncertain usage keeps new credential access locked")
		}
	}
	for _, t := range d.Tasks {
		if !selected[t.ID] {
			continue
		}
		if t.ProfileID != c.ProfileID || t.AI == nil || t.AI.ConnectionRef != c.Ref || validateTaskAI(d, t, now) != nil || t.Runs >= t.MaxRuns || t.State == "waiting-ai" || t.State == "held" || t.State == "review" || t.State == "cancelled" || t.State == "completed" {
			return a, errors.New("selected task is not ready with this connection")
		}
		q := taskAIRequest(t, now)
		if a.Model != "" && a.Model != q.Model {
			return a, errors.New("one model per credential allowance")
		}
		a.Model = q.Model
		a.Tasks = append(a.Tasks, TaskAccessBinding{ID: t.ID, Digest: t.AI.Digest})
		a.MaxRequests += t.MaxRuns - t.Runs
		a.BudgetMicrousd += t.AI.BudgetMicrousd
		if t.AI.ExpiresAt < a.ExpiresAt {
			a.ExpiresAt = t.AI.ExpiresAt
		}
		l := q.Limits
		if l.MaxInputTokens > a.Limits.MaxInputTokens {
			a.Limits.MaxInputTokens = l.MaxInputTokens
		}
		if l.MaxOutputTokens > a.Limits.MaxOutputTokens {
			a.Limits.MaxOutputTokens = l.MaxOutputTokens
		}
		if l.DeadlineSeconds > a.Limits.DeadlineSeconds {
			a.Limits.DeadlineSeconds = l.DeadlineSeconds
		}
		if l.BudgetMicrousd > a.Limits.BudgetMicrousd {
			a.Limits.BudgetMicrousd = l.BudgetMicrousd
		}
	}
	if len(a.Tasks) != len(c.TaskIDs) || a.MaxRequests < 1 || a.MaxRequests > 10 || a.BudgetMicrousd < 1 || a.BudgetMicrousd > 10000000 || a.ExpiresAt <= now {
		return a, errors.New("selected tasks exceed finite credential allowance")
	}
	a.ID = taskAccessDigest(a)
	return a, nil
}
func (s *Store) changeTaskAccess(ctx context.Context, tx *sql.Tx, c AIConnectionCommand, v AIConnectionView) (AIConnectionView, error) {
	state := s.credentialLocked(c.Ref)
	if c.Action == "task-access-revoke" {
		if c.Passphrase != "" || c.Key != "" || c.Allowance != nil {
			return v, errors.New("revoke accepts no secrets or new allowance")
		}
		state.taskAccess = nil
		if state.expires <= s.now().Unix() {
			s.clearAIConnectionLocked(c.Ref)
		}
		return s.aiConnectionLocked(c.Ref)
	}
	d, revision, e := readAgents(ctx, tx)
	if e != nil {
		return v, e
	}
	if c.Action == "task-access-review" {
		if c.Passphrase != "" || c.Key != "" || c.Allowance != nil {
			return v, errors.New("review accepts task selectors only")
		}
		a, e := s.reviewTaskAccess(d, c, revision)
		if e != nil {
			return v, e
		}
		v.AccessReview = &a
		return v, nil
	}
	if c.Action != "task-access-allow" || c.Allowance == nil || c.Key != "" || !validPassword(c.Passphrase) {
		return v, errors.New("exact reviewed allowance and vault passphrase required")
	}
	p := *c.Allowance
	ids := []string{}
	for _, task := range p.Tasks {
		ids = append(ids, task.ID)
	}
	expected, e := s.reviewTaskAccess(d, AIConnectionCommand{Ref: c.Ref, ProfileID: p.ProfileID, Revision: p.Revision, TaskIDs: ids, AuthorizationSeconds: p.ExpiresAt - s.now().Unix()}, revision)
	if e != nil {
		return v, e
	}
	expected.CreatedAt = p.CreatedAt
	expected.ExpiresAt = p.ExpiresAt
	expected.ID = taskAccessDigest(expected)
	if p.CreatedAt > s.now().Unix() || s.now().Unix()-p.CreatedAt > 300 || p.UsedRequests != 0 || p.AllocatedMicrousd != 0 || p.ID != expected.ID || p.ID != c.ConsentDigest || p.BootID != s.bootID {
		return v, errors.New("task access scope changed; review it again")
	}
	env, e := s.readAIVault(c.Ref)
	if e != nil {
		return v, errors.New("credential vault unavailable")
	}
	plain, e := s.openAIVault(env, c.Passphrase)
	defer clear(plain)
	if e != nil {
		return v, errors.New("vault unlock failed")
	}
	// Preserve the original interactive expiry; this access is task-scoped only.
	clear(state.key)
	state.key = append([]byte(nil), plain...)
	state.ref = env.Ref
	encoded, _ := json.Marshal(env)
	state.digest = sha256.Sum256(encoded)
	state.taskAccess = &p
	state.taskAccessLast = s.now().Unix()
	return s.aiConnectionLocked(c.Ref)
}
func (s *Store) taskAccessMatches(q AgentAIRequest, a *TaskAccessAllowance, allocated bool, state *providerCredential) bool {
	if !s.taskAccessCurrent(a, state) || q.TaskID == "" || q.ProfileID != a.ProfileID || q.Model != a.Model || q.ConnectionRef != a.ConnectionRef || q.Limits.MaxInputTokens > a.Limits.MaxInputTokens || q.Limits.MaxOutputTokens > a.Limits.MaxOutputTokens || q.Limits.DeadlineSeconds > a.Limits.DeadlineSeconds || q.Limits.BudgetMicrousd > a.Limits.BudgetMicrousd {
		return false
	}
	if allocated {
		if q.taskAccessID != a.ID {
			return false
		}
	} else if a.UsedRequests >= a.MaxRequests || a.AllocatedMicrousd+aiRequestReservation(q) > a.BudgetMicrousd {
		return false
	}
	for _, t := range a.Tasks {
		if t.ID == q.TaskID && t.Digest == q.TaskDigest {
			return true
		}
	}
	return false
}
func (s *Store) aiRequestConnectionValid(q AgentAIRequest) bool {
	if q.ConnectionRef == "" {
		return true
	}
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	state := s.credentialLocked(q.ConnectionRef)
	v, e := s.aiConnectionLocked(q.ConnectionRef)
	if e != nil || v.Ref != q.ConnectionRef {
		return false
	}
	if q.TaskID != "" {
		return s.taskAccessMatches(q, state.taskAccess, q.taskAccessID != "", state)
	}
	return !v.Locked
}
func (s *Store) reserveTaskAccess(q *AgentAIRequest) bool {
	if q.ConnectionRef == "" {
		return true
	}
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	state := s.credentialLocked(q.ConnectionRef)
	v, e := s.aiConnectionLocked(q.ConnectionRef)
	if e != nil || v.Ref != q.ConnectionRef {
		return false
	}
	if q.TaskID == "" {
		return !v.Locked
	}
	if !s.taskAccessMatches(*q, state.taskAccess, false, state) {
		return false
	}
	a := state.taskAccess
	a.UsedRequests++
	a.AllocatedMicrousd += aiRequestReservation(*q)
	q.taskAccessID = a.ID
	return true
}
func taskAccessAction(action string) bool { return strings.HasPrefix(action, "task-access-") }
