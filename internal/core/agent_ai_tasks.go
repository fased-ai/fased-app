package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

const taskAICompletionReserve = 8192

type TaskAIStep struct {
	SessionID      string    `json:"sessionId"`
	Context        string    `json:"context"`
	Disclosure     string    `json:"disclosure"`
	Limits         AILimits  `json:"limits"`
	BudgetMicrousd int64     `json:"budgetMicrousd"`
	CreatedAt      int64     `json:"createdAt"`
	ExpiresAt      int64     `json:"expiresAt"`
	Digest         string    `json:"digest"`
	Authorized     bool      `json:"authorized"`
	PendingID      string    `json:"pendingId"`
	Bot            *PaperBot `json:"bot,omitempty"`
}

func migrateTaskAI(ctx context.Context, db *sql.DB) error {
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var phase string
	if e = tx.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase); e != nil {
		return e
	}
	if phase == "paused" {
		return errors.New("core metadata paused; schema 5 must finish recovery before AI task migration")
	}
	if _, e = tx.ExecContext(ctx, "PRAGMA user_version=6"); e != nil {
		return e
	}
	return tx.Commit()
}
func taskAIActive(t AgentTask) bool {
	return t.AI != nil && t.State != "held" && t.State != "completed" && t.State != "cancelled"
}
func taskAIFuture(t AgentTask) int {
	if !taskAIActive(t) {
		return 0
	}
	n := t.MaxRuns - t.Runs
	if t.AI.PendingID != "" {
		n--
	}
	if n < 0 {
		return 0
	}
	return n
}
func taskAIRequestSlots(d agentDocument) int {
	n := len(d.AIRequests)
	for _, t := range d.Tasks {
		n += taskAIFuture(t)
	}
	return n
}
func taskAIRequest(t AgentTask, now int64) AgentAIRequest {
	a := t.AI
	q := AgentAIRequest{ID: fmt.Sprintf("%s:%d-ai", t.ID, t.Runs+1), TaskID: t.ID, TaskDigest: a.Digest, ProfileID: t.ProfileID, SessionID: a.SessionID, Prompt: t.Text, Model: fixtureAIModel, Context: a.Context, Disclosure: a.Disclosure, Limits: a.Limits, CreatedAt: now, State: "queued", HeldMicrousd: aiReservation(a.Limits), Cards: []AIActionCard{}}
	q.Digest = aiDigest(q)
	return q
}
func taskAIFutureStorage(d agentDocument) int {
	reserved := 0
	for _, t := range d.Tasks {
		if n := taskAIFuture(t); n > 0 {
			q := taskAIRequest(t, t.AI.CreatedAt)
			b, _ := json.Marshal(q)
			reserved += n * (len(b) + agentAIResultReserve + taskAICompletionReserve + 1024)
		}
	}
	return reserved
}
func taskAIDigest(t AgentTask) string {
	a := *t.AI
	a.Authorized = false
	a.PendingID = ""
	a.Digest = ""
	b, _ := json.Marshal(struct {
		ID, Profile, Name, Text, BotID string
		Interval                       int64
		MaxRuns                        int
		AI                             TaskAIStep
	}{t.ID, t.ProfileID, t.Name, t.Text, t.BotID, t.IntervalSeconds, t.MaxRuns, a})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func validateTaskAI(d agentDocument, t AgentTask, now int64) error {
	if t.AI == nil || !t.AI.Authorized || t.AI.Digest != taskAIDigest(t) || now < t.AI.CreatedAt || now >= t.AI.ExpiresAt {
		return errors.New("task consent missing, changed or expired")
	}
	wire, e := aiWire(taskAIRequest(t, now))
	if e != nil || string(wire) != t.AI.Disclosure {
		return errors.New("AI wire contract changed; review a fresh plan")
	}
	context, e := aiContext(d, t.ProfileID, t.AI.SessionID, t.Text, now)
	if e != nil || context != t.AI.Context {
		return errors.New("task context changed; review a fresh plan")
	}
	if t.AI.Bot != nil {
		found := false
		for _, bot := range d.Bots {
			if bot.ID == t.BotID && bot.ProfileID == t.ProfileID {
				bot.SpentCents = 0
				if bot != *t.AI.Bot {
					return errors.New("paper inputs changed")
				}
				found = true
			}
		}
		if !found {
			return errors.New("paper bot missing")
		}
	}
	return nil
}
func taskAIRequestValid(d agentDocument, q AgentAIRequest, now int64) error {
	if q.TaskID == "" {
		return nil
	}
	for _, t := range d.Tasks {
		if t.ID == q.TaskID && t.ProfileID == q.ProfileID && t.AI != nil && t.State == "waiting-ai" && t.AI.PendingID == q.ID && t.AI.Digest == q.TaskDigest {
			return validateTaskAI(d, t, now)
		}
	}
	return errors.New("task no longer authorizes this request")
}
func changeTaskAI(d *agentDocument, c AgentCommand, profile, id string, now int64, fixture bool) error {
	if c.Action == "task-ai-prepare" {
		if !validAgentText(c.Name, 80, true) || !validAgentText(c.Text, 1000, true) || c.IntervalSeconds < 60 || c.IntervalSeconds > 604800 || c.MaxRuns < 1 || c.MaxRuns > 10 || c.AuthorizationSeconds < 60 || c.AuthorizationSeconds > 604800 || c.TotalBudgetMicrousd < 1 || c.TotalBudgetMicrousd > 1000000 || len(d.Tasks) >= 100 {
			return errors.New("bounded AI task name/prompt/schedule/authorization and total budget required")
		}
		// Reuse the exact request validator and complete wire disclosure, then reserve
		// all future request slots rather than publishing a disposable preview request.
		copy := *d
		copy.AIRequests = append([]AgentAIRequest{}, d.AIRequests...)
		if e := changeAgentAI(&copy, AgentCommand{Action: "ai-prepare", ID: c.ID, Text: c.Text, Limits: c.Limits}, profile, id, now, false); e != nil {
			return e
		}
		q := copy.AIRequests[len(copy.AIRequests)-1]
		if aiReservation(q.Limits)*int64(c.MaxRuns) > c.TotalBudgetMicrousd || taskAIRequestSlots(*d)+c.MaxRuns > 10 {
			return errors.New("total AI budget or reserved request capacity insufficient")
		}
		runs := c.MaxRuns
		for _, t := range d.Tasks {
			runs += t.MaxRuns
		}
		if runs > 200 {
			return errors.New("local scheduled evaluation capacity reached")
		}
		a := &TaskAIStep{SessionID: c.ID, Context: q.Context, Disclosure: q.Disclosure, Limits: q.Limits, BudgetMicrousd: c.TotalBudgetMicrousd, CreatedAt: now, ExpiresAt: now + c.AuthorizationSeconds}
		if c.BotID != "" {
			for _, bot := range d.Bots {
				if bot.ID == c.BotID && bot.ProfileID == profile {
					bot.SpentCents = 0
					a.Bot = &bot
				}
			}
			if a.Bot == nil {
				return errors.New("profile paper bot not found")
			}
		}
		t := AgentTask{ID: id, ProfileID: profile, Name: c.Name, Text: c.Text, BotID: c.BotID, IntervalSeconds: c.IntervalSeconds, MaxRuns: c.MaxRuns, State: "review", AI: a}
		a.Digest = taskAIDigest(t)
		d.Tasks = append(d.Tasks, t)
		return nil
	}
	for i, t := range d.Tasks {
		if t.ID != c.ID || t.ProfileID != profile || t.AI == nil {
			continue
		}
		switch c.Action {
		case "task-ai-authorize":
			if !fixture || t.State != "review" || c.ConsentDigest != t.AI.Digest {
				return errors.New("active development fixture and exact task consent required")
			}
			copy := t
			a := *t.AI
			copy.AI = &a
			a.Authorized = true
			if e := validateTaskAI(*d, copy, now); e != nil {
				return e
			}
			d.Tasks[i].AI.Authorized = true
			d.Tasks[i].State = "scheduled"
			d.Tasks[i].NextAt = now + t.IntervalSeconds
			return nil
		case "task-ai-cancel":
			return cancelTaskAI(d, i, now)
		}
	}
	return errors.New("profile AI task not found")
}
func taskAIHeld(d *agentDocument, i int, requestID, outcome string, now int64) error {
	if len(d.Runs) >= 200 || len(d.Notices) >= 200 {
		return errors.New("task journal/inbox capacity reached")
	}
	t := &d.Tasks[i]
	id := fmt.Sprintf("%s:%d", t.ID, t.Runs+1)
	d.Runs = append(d.Runs, AgentRun{ID: id, ProfileID: t.ProfileID, TaskID: t.ID, BotID: t.BotID, Strategy: "ai-step-v1", At: now, Outcome: outcome, AIRequestID: requestID, AIDigest: t.AI.Digest})
	d.Notices = append(d.Notices, AgentNotice{ID: id, ProfileID: t.ProfileID, Text: t.Name + ": " + outcome + "; dependent step stopped. Review a new task plan.", RunID: id, At: now})
	t.Runs++
	t.State = "held"
	t.NextAt = 0
	t.AI.PendingID = ""
	return nil
}
func evaluateAgentTask(d *agentDocument, i int, now int64, fixture bool) error {
	t := d.Tasks[i]
	if t.AI == nil {
		return runAgentTask(d, i, now)
	}
	if !fixture {
		return taskAIHeld(d, i, "", "ai-disabled", now)
	}
	if validateTaskAI(*d, t, now) != nil {
		return taskAIHeld(d, i, "", "ai-consent-invalid", now)
	}
	used := int64(0)
	for _, q := range d.AIRequests {
		if q.TaskID == t.ID {
			used += q.SpentMicrousd + q.HeldMicrousd
		}
		if q.ProfileID == t.ProfileID && q.HeldMicrousd > 0 {
			if q.State == "queued" || q.State == "running" {
				d.Tasks[i].NextAt = now + 1
				return nil
			}
			return taskAIHeld(d, i, "", "ai-profile-held", now)
		}
	}
	if used+aiReservation(t.AI.Limits) > t.AI.BudgetMicrousd || len(d.AIRequests) >= 10 {
		return taskAIHeld(d, i, "", "ai-budget-blocked", now)
	}
	q := taskAIRequest(t, now)
	for _, old := range d.AIRequests {
		if old.ID == q.ID {
			return errors.New("task request identity already exists; preserve history")
		}
	}
	d.AIRequests = append(d.AIRequests, q)
	d.Tasks[i].State = "waiting-ai"
	d.Tasks[i].AI.PendingID = q.ID
	return nil
}
func settleTaskAI(d *agentDocument, taskID string, now int64) error {
	if taskID == "" {
		return nil
	}
	for i, t := range d.Tasks {
		if t.ID != taskID || t.AI == nil || t.State != "waiting-ai" {
			continue
		}
		for _, q := range d.AIRequests {
			if q.ID != t.AI.PendingID {
				continue
			}
			if q.State == "queued" || q.State == "running" {
				return nil
			}
			if q.State != "completed" {
				return taskAIHeld(d, i, q.ID, "ai-"+q.State, now)
			}
			if validateTaskAI(*d, t, now) != nil {
				return taskAIHeld(d, i, q.ID, "ai-consent-invalid", now)
			}
			if e := runAgentTask(d, i, now); e != nil {
				return e
			}
			n := len(d.Runs) - 1
			d.Runs[n].AIRequestID = q.ID
			d.Runs[n].AIDigest = t.AI.Digest
			d.Runs[n].Strategy = "ai-step-v1/" + d.Runs[n].Strategy
			d.Tasks[i].AI.PendingID = ""
			if d.Tasks[i].Runs < d.Tasks[i].MaxRuns {
				d.Tasks[i].State = "scheduled"
			}
			return nil
		}
		return errors.New("pending task request missing")
	}
	return nil
}
func cancelTaskAI(d *agentDocument, i int, now int64) error {
	t := d.Tasks[i]
	if !taskAIActive(t) {
		return errors.New("AI task is terminal")
	}
	if t.AI.PendingID == "" {
		d.Tasks[i].State = "cancelled"
		d.Tasks[i].NextAt = 0
		return nil
	}
	for qi := range d.AIRequests {
		q := &d.AIRequests[qi]
		if q.ID == t.AI.PendingID {
			if q.State == "queued" {
				q.HeldMicrousd = 0
			}
			if q.State == "queued" || q.State == "running" {
				q.State = "cancelled"
				q.Failure = "Task stopped locally; a dispatched reservation remains held."
			}
			return settleTaskAI(d, t.ID, now)
		}
	}
	return errors.New("pending task request missing")
}
