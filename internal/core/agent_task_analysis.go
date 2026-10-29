package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const taskAnalysisVersion = "task-analysis-v1"

type TaskAnalysisPolicy struct {
	Version string `json:"version"`
	Failure string `json:"failure"`
}
type TaskAnalysisResult struct {
	Version string `json:"version"`
	State   string `json:"state"`
	Reason  string `json:"reason,omitempty"`
}

func typedAnalysis(t AgentTask) bool { return t.AI != nil && t.AI.Analysis != nil }
func validAnalysisPolicy(p *TaskAnalysisPolicy, watch bool) bool {
	return p != nil && p.Version == taskAnalysisVersion && (p.Failure == "required" || watch && p.Failure == "optional")
}
func watchRule(t AgentTask) *NewsWatchRule {
	if t.Watch == nil {
		return nil
	}
	return &t.Watch.Rule
}

// Review uses a private document copy. Its deterministic identities and original
// time keep review/create identical without persisting an abandoned draft.
func prepareTaskAnalysis(d agentDocument, c AgentCommand, profile string, revision, expires int64) (agentDocument, AgentTask, error) {
	var zero AgentTask
	if !validAnalysisPolicy(c.Analysis, c.Watch != nil) || c.BotID != "" || c.WEN != nil || c.MaxRuns > 10 {
		return d, zero, errors.New("choose bounded analysis or a news watch with required/optional analysis")
	}
	if c.Watch != nil && !validWatchRule(*c.Watch) {
		return d, zero, errors.New("invalid headline rule")
	}
	if c.Calendar != nil {
		if err := validateCalendarCommand(c, expires-600); err != nil {
			return d, zero, err
		}
	}
	c.Action = "task-ai-prepare"
	c.Revision = 0
	c.ExpiresAt = 0
	c.ConsentDigest = ""
	c.Calendar = normalizedCalendar(c.Calendar, expires-600)
	c.UseAgentDefaults = false
	seed, _ := json.Marshal([]any{profile, revision, expires, c})
	id := "analysis-task:" + newsHash(seed)
	copy := d
	copy.Tasks = append([]AgentTask{}, d.Tasks...)
	copy.Sessions = append([]AgentSession{}, d.Sessions...)
	if c.SessionID == "" {
		if len(copy.Sessions) >= 100 {
			return d, zero, errors.New("conversation capacity reached")
		}
		c.SessionID = "context:" + newsHash(seed)
		copy.Sessions = append(copy.Sessions, AgentSession{ID: c.SessionID, ProfileID: profile, Name: c.Name, CreatedAt: expires - 600})
	}
	c.ID = c.SessionID
	if c.MemorySelection == nil {
		c.MemorySelection = &AIMemorySelection{Mode: "relevant"}
	}
	if err := changeTaskAI(&copy, c, profile, id, expires-600, false); err != nil {
		return d, zero, err
	}
	task := copy.Tasks[len(copy.Tasks)-1]
	if c.Watch != nil && task.News == nil {
		return d, zero, errors.New("news watch requires a selected source")
	}
	if task.Calendar != nil && task.Calendar.At >= task.AI.ExpiresAt {
		return d, zero, errors.New("first run must be inside the reviewed AI permission")
	}
	if err := agentCapacity(copy); err != nil {
		return d, zero, err
	}
	return copy, task, nil
}
func createReviewedTaskAnalysis(d *agentDocument, c AgentCommand, profile string, revision, now int64, fixture bool) error {
	copy, t, err := prepareTaskAnalysis(*d, c, profile, revision, c.ExpiresAt)
	if err != nil {
		return err
	}
	if err = changeTaskAI(&copy, AgentCommand{Action: "task-ai-authorize", ID: t.ID, ConsentDigest: t.AI.Digest}, profile, "", now, fixture); err != nil {
		return err
	}
	*d = copy
	return nil
}
func finishTaskAnalysis(d *agentDocument, i int, now int64, requestID, state, reason string) error {
	t := d.Tasks[i]
	d.Tasks[i].State = "scheduled"
	before := len(d.Runs)
	if err := runAgentTask(d, i, now); err != nil {
		return err
	}
	if len(d.Runs) == before {
		return nil
	} // Expired cron consumes no evaluation.
	run := &d.Runs[len(d.Runs)-1]
	if run.Strategy == "news-gate-v1" {
		return nil
	}
	run.Analysis = &TaskAnalysisResult{Version: taskAnalysisVersion, State: state, Reason: reason}
	run.AIRequestID, run.AIDigest = requestID, t.AI.Digest
	d.Tasks[i].AI.PendingID = ""
	return nil
}
func holdOrSkipTaskAI(d *agentDocument, i int, requestID, outcome string, now int64) error {
	if typedAnalysis(d.Tasks[i]) && d.Tasks[i].Watch != nil && d.Tasks[i].AI.Analysis.Failure == "optional" {
		return finishTaskAnalysis(d, i, now, requestID, "skipped", outcome)
	}
	return taskAIHeld(d, i, requestID, outcome, now)
}
func noNewAnalysisMatches(d agentDocument, t AgentTask, now int64) bool {
	if t.Watch == nil {
		return false
	}
	f, err := freshNews(d, t.ProfileID, t.News, now)
	if err != nil {
		return false
	}
	copy := t
	watch := *t.Watch
	watch.Seen = append([]string{}, watch.Seen...)
	copy.Watch = &watch
	return len(applyNewsWatch(&copy, f).NewIDs) == 0
}
func validAnalysisCalendar(t AgentTask) bool {
	if !validCalendar(t.Calendar) || t.IntervalSeconds != 0 || t.MaxRuns < 1 || t.MaxRuns > 10 || t.Runs < 0 || t.Runs > t.MaxRuns {
		return false
	}
	if t.State == "review" {
		return t.NextAt == 0 && t.Runs == 0 && !t.AI.Authorized
	}
	if t.State == "held" || t.State == "cancelled" {
		return t.NextAt == 0
	}
	if cronTask(t) {
		if t.State == "waiting-news" || t.State == "waiting-ai" {
			t.State = "scheduled"
		}
		return validCronTask(t)
	}
	return t.MaxRuns == 1 && t.NextAt == t.Calendar.At && ((t.Runs == 0 && (t.State == "scheduled" || t.State == "paused" || t.State == "waiting-news" || t.State == "waiting-ai")) || (t.Runs == 1 && t.State == "completed"))
}

func strictAnalysisObject(raw json.RawMessage, result bool) error {
	var f map[string]json.RawMessage
	if json.Unmarshal(raw, &f) != nil || f == nil {
		return errors.New("invalid analysis record; preserve state")
	}
	allowed := map[string]bool{"version": true, "failure": !result, "state": result, "reason": result}
	if f["version"] == nil || (!result && (len(f) != 2 || f["failure"] == nil)) || (result && (len(f) < 2 || len(f) > 3 || f["state"] == nil)) {
		return errors.New("unsupported analysis record; preserve state")
	}
	for k := range f {
		if !allowed[k] {
			return errors.New("unsupported analysis fields; preserve state")
		}
	}
	return nil
}
func analysisFields(raw []byte, unprotected bool) error {
	var root struct {
		Tasks []map[string]json.RawMessage
		Runs  []map[string]json.RawMessage
	}
	if json.Unmarshal(raw, &root) != nil {
		return errors.New("invalid original analysis state")
	}
	for _, row := range root.Tasks {
		for k, b := range row {
			if strings.EqualFold(k, "ai") {
				var fields map[string]json.RawMessage
				if json.Unmarshal(b, &fields) != nil {
					return errors.New("invalid analysis step")
				}
				for key, v := range fields {
					if strings.EqualFold(key, "analysis") {
						if unprotected || k != "ai" || key != "analysis" {
							return errors.New("analysis fields lack canonical schema protection; preserve state")
						}
						if err := strictAnalysisObject(v, false); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	for _, row := range root.Runs {
		for k, b := range row {
			if strings.EqualFold(k, "analysis") {
				if unprotected || k != "analysis" {
					return errors.New("analysis results lack canonical schema protection; preserve state")
				}
				if err := strictAnalysisObject(b, true); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func validateTaskAnalysisStorage(raw []byte, d agentDocument) error {
	if err := analysisFields(raw, false); err != nil {
		return err
	}
	for _, t := range d.Tasks {
		if strings.HasPrefix(t.ID, "analysis-task:") && !typedAnalysis(t) {
			return errors.New("analysis policy erased; preserve records")
		}
		if !typedAnalysis(t) {
			continue
		}
		if !validAnalysisPolicy(t.AI.Analysis, t.Watch != nil) || !validAgentText(t.Text, 1000, true) || t.BotID != "" || t.AI.Bot != nil || t.WEN != nil || t.Playbook != nil || t.MaxRuns < 1 || t.MaxRuns > 10 || t.Runs < 0 || t.Runs > t.MaxRuns || t.AI.CreatedAt <= 0 || t.AI.ExpiresAt <= t.AI.CreatedAt || t.AI.Digest != taskAIDigest(t) {
			return errors.New("unsupported analysis task; preserve records")
		}
		found := 0
		for _, s := range d.Sessions {
			if s.ID == t.AI.SessionID {
				if s.ProfileID != t.ProfileID {
					return errors.New("foreign analysis context")
				}
				found++
			}
		}
		if found != 1 {
			return errors.New("missing analysis context")
		}
		if t.Calendar != nil && !validAnalysisCalendar(t) {
			return errors.New("invalid analysis calendar")
		}
		count := 0
		for _, r := range d.Runs {
			if r.TaskID != t.ID {
				continue
			}
			count++
			if r.ProfileID != t.ProfileID || r.ID != fmt.Sprintf("%s:%d", t.ID, count) || (r.Analysis == nil && r.Strategy != "ai-step-v1" && r.Strategy != "news-gate-v1") {
				return errors.New("analysis result missing or replayed; preserve records")
			}
		}
		if count != t.Runs {
			return errors.New("analysis run counter changed; preserve records")
		}
	}
	for _, run := range d.Runs {
		if run.Analysis == nil {
			continue
		}
		var t *AgentTask
		for i := range d.Tasks {
			if d.Tasks[i].ID == run.TaskID {
				if t != nil {
					return errors.New("ambiguous analysis task")
				}
				t = &d.Tasks[i]
			}
		}
		if t == nil || !typedAnalysis(*t) || t.ProfileID != run.ProfileID || run.AIDigest != t.AI.Digest || run.Analysis.Version != taskAnalysisVersion || run.BotID != "" || run.WEN != nil || run.Quantity != 0 || run.PriceCents != 0 || run.CostCents != 0 || run.BudgetBefore != 0 || (run.AIRequestID != "" && run.AIRequestID != run.ID+"-ai") {
			return errors.New("invalid analysis result; preserve records")
		}
		if (t.Watch == nil && (run.Strategy != "analysis-v1" || run.Outcome != "analyzed")) || (t.Watch != nil && run.Strategy != newsWatchVersion) {
			return errors.New("analysis result changed")
		}
		var q *AgentAIRequest
		for i := range d.AIRequests {
			if d.AIRequests[i].ID == run.AIRequestID && run.AIRequestID != "" {
				if q != nil {
					return errors.New("ambiguous analysis request")
				}
				q = &d.AIRequests[i]
			}
		}
		if q != nil && (q.TaskID != t.ID || q.ProfileID != t.ProfileID || q.TaskDigest != t.AI.Digest || q.Digest != aiDigest(*q) || q.Prompt != t.Text || q.Model != t.AI.Model && !(q.Model == fixtureAIModel && t.AI.Model == "") || q.CreatedAt > run.At) {
			return errors.New("analysis request changed")
		}
		switch run.Analysis.State {
		case "completed":
			if q == nil || q.State != "completed" || run.Analysis.Reason != "" {
				return errors.New("unfinished analysis result")
			}
		case "skipped":
			if t.Watch == nil || (!validAgentText(run.Analysis.Reason, 80, true)) || (t.AI.Analysis.Failure != "optional" && run.Analysis.Reason != "no-new-matches") {
				return errors.New("unreviewed analysis omission")
			}
			if run.Analysis.Reason == "no-new-matches" {
				if run.Watch == nil || len(run.Watch.NewIDs) != 0 || run.AIRequestID != "" {
					return errors.New("analysis matches omitted")
				}
			} else if q != nil {
				knownFailure := (q.State == "failed" || q.State == "unknown" || q.State == "cancelled") && run.Analysis.Reason == "ai-"+q.State
				// The provider may finish with verified usage after task consent
				// expires. Keep that receipt, but the result never admits output.
				withheldCompletion := q.State == "completed" && run.Analysis.Reason == "ai-consent-invalid"
				if !knownFailure && !withheldCompletion {
					return errors.New("analysis failure changed")
				}
			} else if run.AIRequestID != "" || !allowedAnalysisSkip(run.Analysis.Reason) {
				return errors.New("analysis omission reason invalid")
			}
		default:
			return errors.New("unsupported analysis result state")
		}
		if q != nil && run.News != nil && (q.News == nil || *q.News != *run.News) {
			return errors.New("analysis source changed")
		}
	}
	return nil
}
func allowedAnalysisSkip(reason string) bool {
	switch reason {
	case "ai-disabled", "ai-consent-invalid", "ai-profile-held", "ai-budget-blocked":
		return true
	}
	return false
}
func migrateTaskAnalysis(ctx context.Context, db *sql.DB) error {
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
	if err = analysisFields([]byte(raw), true); err != nil {
		return err
	}
	if _, _, err = readAgents(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA user_version=29"); err != nil {
		return err
	}
	return tx.Commit()
}

// Plan consent never lends interactive key access to unattended work. Check the
// existing finite grant before queueing; reserveTaskAccess still allocates once
// immediately before dispatch.
func (s *Store) analysisTaskCredentials(t AgentTask) bool {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	state := s.credentialLocked(t.AI.ConnectionRef)
	return s.taskAccessMatches(taskAIRequest(t, s.now().Unix()), state.taskAccess, false, state)
}
func (s *Store) newsTaskAIAllowed(d agentDocument, f NewsFetch, fixture bool) bool {
	for _, t := range d.Tasks {
		if t.ID == f.TaskID && t.ProfileID == f.ProfileID {
			return s.providerTaskAllowed(t, fixture)
		}
	}
	return fixture
}
