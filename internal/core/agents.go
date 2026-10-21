package core

// Local agent records have no wallet reference, tool execution, provider secret
// or financial authority. One bounded document and SQL transaction make a task
// result, paper ledger entry, inbox notification and next schedule atomic.
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const agentDocumentLimit = 512 * 1024
const agentMigration = `CREATE TABLE agent_workspace(id INTEGER PRIMARY KEY CHECK(id=1), document TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 0);
CREATE TRIGGER update_pause_agents BEFORE UPDATE ON agent_workspace
WHEN (SELECT phase FROM update_pause WHERE id=1)='paused' BEGIN SELECT RAISE(ABORT,'core metadata paused for update'); END;
CREATE TRIGGER update_pause_agents_delete BEFORE DELETE ON agent_workspace
WHEN (SELECT phase FROM update_pause WHERE id=1)='paused' BEGIN SELECT RAISE(ABORT,'core metadata paused for update'); END;
PRAGMA user_version=4;`

type AgentProfile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Persona  string `json:"persona"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}
type AgentSession struct {
	ID        string `json:"id"`
	ProfileID string `json:"profileId"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
}
type AgentMessage struct {
	ID        string `json:"id"`
	ProfileID string `json:"profileId"`
	SessionID string `json:"sessionId"`
	Role      string `json:"role"`
	Text      string `json:"text"`
	CreatedAt int64  `json:"createdAt"`
}
type MemoryRevision struct {
	Text      string `json:"text"`
	Source    string `json:"source"`
	At        int64  `json:"at"`
	ExpiresAt int64  `json:"expiresAt"`
}
type AgentMemory struct {
	ID        string           `json:"id"`
	ProfileID string           `json:"profileId"`
	Text      string           `json:"text"`
	Source    string           `json:"source"`
	UpdatedAt int64            `json:"updatedAt"`
	ExpiresAt int64            `json:"expiresAt"`
	Expired   bool             `json:"expired"`
	History   []MemoryRevision `json:"history"`
}
type PaperBot struct {
	ID          string `json:"id"`
	ProfileID   string `json:"profileId"`
	Name        string `json:"name"`
	Strategy    string `json:"strategy"`
	Quantity    int64  `json:"quantity"`
	PriceCents  int64  `json:"priceCents"`
	BudgetCents int64  `json:"budgetCents"`
	SpentCents  int64  `json:"spentCents"`
}
type AgentTask struct {
	ID              string `json:"id"`
	ProfileID       string `json:"profileId"`
	Name            string `json:"name"`
	Text            string `json:"text"`
	BotID           string `json:"botId"`
	IntervalSeconds int64  `json:"intervalSeconds"`
	MaxRuns         int    `json:"maxRuns"`
	Runs            int    `json:"runs"`
	NextAt          int64  `json:"nextAt"`
	State           string `json:"state"`
}
type AgentRun struct {
	ID           string `json:"id"`
	ProfileID    string `json:"profileId"`
	TaskID       string `json:"taskId"`
	BotID        string `json:"botId"`
	Strategy     string `json:"strategy"`
	At           int64  `json:"at"`
	Outcome      string `json:"outcome"`
	Quantity     int64  `json:"quantity"`
	PriceCents   int64  `json:"priceCents"`
	CostCents    int64  `json:"costCents"`
	BudgetBefore int64  `json:"budgetBefore"`
}
type AgentNotice struct {
	ID        string `json:"id"`
	ProfileID string `json:"profileId"`
	Text      string `json:"text"`
	RunID     string `json:"runId"`
	At        int64  `json:"at"`
	Read      bool   `json:"read"`
}
type agentDocument struct {
	Profiles   []AgentProfile   `json:"profiles"`
	Sessions   []AgentSession   `json:"sessions"`
	Messages   []AgentMessage   `json:"messages"`
	Memories   []AgentMemory    `json:"memories"`
	Bots       []PaperBot       `json:"bots"`
	Tasks      []AgentTask      `json:"tasks"`
	Runs       []AgentRun       `json:"runs"`
	Notices    []AgentNotice    `json:"notices"`
	AIRequests []AgentAIRequest `json:"aiRequests"`
}
type AgentView struct {
	AIRequests           []AgentAIRequest `json:"aiRequests"`
	AIFixtureAvailable   bool             `json:"aiFixtureAvailable"`
	Revision             int64            `json:"revision"`
	ProfileID            string           `json:"profileId"`
	Profiles             []AgentProfile   `json:"profiles"`
	Sessions             []AgentSession   `json:"sessions"`
	Messages             []AgentMessage   `json:"messages"`
	Memories             []AgentMemory    `json:"memories"`
	Bots                 []PaperBot       `json:"bots"`
	Tasks                []AgentTask      `json:"tasks"`
	Runs                 []AgentRun       `json:"runs"`
	Notices              []AgentNotice    `json:"notices"`
	ProviderCallsEnabled bool             `json:"providerCallsEnabled"`
	FinancialExecution   bool             `json:"financialExecution"`
}

// Optional fields are inert data for a fixed action; unknown fields/actions are
// rejected. No arbitrary callback, URL, model tool or shell can be submitted.
type AgentCommand struct {
	Revision        int64     `json:"revision"`
	Action          string    `json:"action"`
	ProfileID       string    `json:"profileId,omitempty"`
	ID              string    `json:"id,omitempty"`
	Name            string    `json:"name,omitempty"`
	Text            string    `json:"text,omitempty"`
	Source          string    `json:"source,omitempty"`
	ExpiresAt       int64     `json:"expiresAt,omitempty"`
	Provider        string    `json:"provider,omitempty"`
	Model           string    `json:"model,omitempty"`
	BotID           string    `json:"botId,omitempty"`
	IntervalSeconds int64     `json:"intervalSeconds,omitempty"`
	MaxRuns         int       `json:"maxRuns,omitempty"`
	Quantity        int64     `json:"quantity,omitempty"`
	PriceCents      int64     `json:"priceCents,omitempty"`
	BudgetCents     int64     `json:"budgetCents,omitempty"`
	Limits          *AILimits `json:"limits,omitempty"`
	ConsentDigest   string    `json:"consentDigest,omitempty"`
}

func initialAgents() agentDocument {
	return agentDocument{Profiles: []AgentProfile{
		{ID: "wen-operator", Name: "WEN Operator", Persona: "Explain WEN prototypes. Financial execution is unavailable.", Provider: "none"},
		{ID: "trading-analyst", Name: "Trading Analyst", Persona: "Research and compare bounded paper strategies.", Provider: "none"},
		{ID: "news-researcher", Name: "News Researcher", Persona: "Keep sourced research separate from instructions and financial facts.", Provider: "none"},
	}, Sessions: []AgentSession{}, Messages: []AgentMessage{}, Memories: []AgentMemory{}, Bots: []PaperBot{}, Tasks: []AgentTask{}, Runs: []AgentRun{}, Notices: []AgentNotice{}, AIRequests: []AgentAIRequest{}}
}
func migrateAgents(ctx context.Context, db *sql.DB) error {
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
		return errors.New("core metadata paused; schema 3 must finish its existing recovery before P5 migration")
	}
	if _, e = tx.ExecContext(ctx, agentMigration); e != nil {
		return e
	}
	b, e := json.Marshal(initialAgents())
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO agent_workspace(id,document) VALUES(1,?)", string(b)); e != nil {
		return e
	}
	return tx.Commit()
}
func readAgents(ctx context.Context, r snapshotReader) (agentDocument, int64, error) {
	var d agentDocument
	var revision int64
	var b string
	e := r.QueryRowContext(ctx, "SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&b, &revision)
	if e == nil {
		if len(b) > agentDocumentLimit {
			e = errors.New("agent storage exceeds local prototype capacity")
		} else {
			e = json.Unmarshal([]byte(b), &d)
			if e == nil && len(d.Profiles) == 0 {
				e = errors.New("missing agent profiles; preserve state")
			}
		}
	}
	return d, revision, e
}
func agentProfile(d agentDocument, id string) int {
	for i, p := range d.Profiles {
		if p.ID == id {
			return i
		}
	}
	return -1
}
func agentView(d agentDocument, revision int64, profile, query string, now int64) (AgentView, error) {
	if profile == "" {
		profile = d.Profiles[0].ID
	}
	if agentProfile(d, profile) < 0 {
		return AgentView{}, errors.New("profile not found")
	}
	v := AgentView{Revision: revision, ProfileID: profile, Profiles: d.Profiles, Sessions: []AgentSession{}, Messages: []AgentMessage{}, Memories: []AgentMemory{}, Bots: []PaperBot{}, Tasks: []AgentTask{}, Runs: []AgentRun{}, Notices: []AgentNotice{}, AIRequests: []AgentAIRequest{}}
	for _, q := range d.AIRequests {
		if q.ProfileID == profile {
			v.AIRequests = append(v.AIRequests, q)
		}
	}
	query = strings.ToLower(query)
	sessions := map[string]bool{}
	for _, s := range d.Sessions {
		if s.ProfileID == profile {
			match := query == "" || strings.Contains(strings.ToLower(s.Name), query)
			if !match {
				for _, m := range d.Messages {
					if m.SessionID == s.ID && m.ProfileID == profile && strings.Contains(strings.ToLower(m.Text), query) {
						match = true
						break
					}
				}
			}
			if match {
				v.Sessions = append(v.Sessions, s)
				sessions[s.ID] = true
			}
		}
	}
	for _, m := range d.Messages {
		if m.ProfileID == profile && sessions[m.SessionID] {
			v.Messages = append(v.Messages, m)
		}
	}
	for _, m := range d.Memories {
		if m.ProfileID == profile {
			m.Expired = m.ExpiresAt > 0 && m.ExpiresAt <= now
			v.Memories = append(v.Memories, m)
		}
	}
	for _, b := range d.Bots {
		if b.ProfileID == profile {
			v.Bots = append(v.Bots, b)
		}
	}
	for _, t := range d.Tasks {
		if t.ProfileID == profile {
			v.Tasks = append(v.Tasks, t)
		}
	}
	for _, r := range d.Runs {
		if r.ProfileID == profile {
			v.Runs = append(v.Runs, r)
		}
	}
	for _, n := range d.Notices {
		if n.ProfileID == profile {
			v.Notices = append(v.Notices, n)
		}
	}
	return v, nil
}
func (s *Store) Agents(ctx context.Context, profile, query string) (AgentView, error) {
	d, r, e := readAgents(ctx, s.db)
	if e != nil {
		return AgentView{}, e
	}
	if len(query) > 200 {
		return AgentView{}, errors.New("search is too long")
	}
	return agentView(d, r, profile, query, s.now().Unix())
}
func validAgentText(v string, max int, required bool) bool {
	return (!required || strings.TrimSpace(v) != "") && len(v) <= max && !strings.ContainsRune(v, 0)
}
func saveAgents(ctx context.Context, tx *sql.Tx, d agentDocument, revision int64) error {
	b, e := json.Marshal(d)
	if e != nil {
		return e
	}
	reserved := 0
	for _, q := range d.AIRequests {
		if q.State == "prepared" || q.State == "queued" || q.State == "running" {
			reserved += agentAIResultReserve
		}
	}
	if len(b)+reserved > agentDocumentLimit {
		return errors.New("local prototype capacity reached; preserve records before further work")
	}
	result, e := tx.ExecContext(ctx, "UPDATE agent_workspace SET document=?,revision=revision+1 WHERE id=1 AND revision=?", string(b), revision)
	if e != nil {
		return metadataWriteError(e)
	}
	n, e := result.RowsAffected()
	if e == nil && n != 1 {
		return ErrConflict
	}
	return e
}
func (s *Store) ChangeAgents(ctx context.Context, c AgentCommand) (AgentView, error) {
	return s.changeAgents(ctx, c, false)
}
func (s *Store) changeAgents(ctx context.Context, c AgentCommand, fixture bool) (AgentView, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return AgentView{}, e
	}
	defer tx.Rollback()
	d, revision, e := readAgents(ctx, tx)
	if e != nil {
		return AgentView{}, e
	}
	if c.Revision != revision {
		return AgentView{}, ErrConflict
	}
	if !validAgentText(c.Name, 80, false) || !validAgentText(c.Text, 4000, false) || !validAgentText(c.Source, 300, false) || !validAgentText(c.Model, 120, false) || c.ExpiresAt < 0 {
		return AgentView{}, errors.New("invalid or oversized agent input")
	}
	profile := c.ProfileID
	if profile == "" {
		profile = d.Profiles[0].ID
	}
	pi := agentProfile(d, profile)
	if pi < 0 {
		return AgentView{}, errors.New("profile not found")
	}
	id, e := randomToken()
	if e != nil {
		return AgentView{}, e
	}
	now := s.now().Unix()
	switch c.Action {
	case "ai-prepare", "ai-start", "ai-cancel", "ai-card-keep", "ai-card-dismiss":
		if e = changeAgentAI(&d, c, profile, id, now, fixture); e != nil {
			return AgentView{}, e
		}
	case "profile-create":
		if !validAgentText(c.Name, 80, true) || len(d.Profiles) >= 30 {
			return AgentView{}, errors.New("profile name required or profile limit reached")
		}
		profile = id
		d.Profiles = append(d.Profiles, AgentProfile{ID: id, Name: strings.TrimSpace(c.Name), Persona: c.Text, Provider: "none"})
	case "profile-update":
		if !validAgentText(c.Name, 80, true) || (c.Provider != "none" && c.Provider != "openai" && c.Provider != "anthropic" && c.Provider != "xai") {
			return AgentView{}, errors.New("name and supported provider preference required")
		}
		d.Profiles[pi] = AgentProfile{ID: profile, Name: strings.TrimSpace(c.Name), Persona: c.Text, Provider: c.Provider, Model: c.Model}
	case "session-create":
		if !validAgentText(c.Name, 80, true) || len(d.Sessions) >= 100 {
			return AgentView{}, errors.New("session name required or session limit reached")
		}
		d.Sessions = append(d.Sessions, AgentSession{ID: id, ProfileID: profile, Name: c.Name, CreatedAt: now})
	case "message-add":
		found := false
		for _, v := range d.Sessions {
			if v.ID == c.ID && v.ProfileID == profile {
				found = true
			}
		}
		if !found || !validAgentText(c.Text, 4000, true) || len(d.Messages) >= 198 {
			return AgentView{}, errors.New("profile session and message required; maximum 200 messages")
		}
		d.Messages = append(d.Messages, AgentMessage{ID: id, ProfileID: profile, SessionID: c.ID, Role: "user", Text: c.Text, CreatedAt: now}, AgentMessage{ID: id + "-local", ProfileID: profile, SessionID: c.ID, Role: "system", Text: "Saved locally. No AI provider was called. Use Tasks for bounded reminders or paper examples.", CreatedAt: now})
	case "memory-put":
		if !validAgentText(c.Text, 4000, true) || !validAgentText(c.Source, 300, true) {
			return AgentView{}, errors.New("memory text and evidence/source required")
		}
		if c.ID == "" {
			if len(d.Memories) >= 100 {
				return AgentView{}, errors.New("memory limit reached")
			}
			d.Memories = append(d.Memories, AgentMemory{ID: id, ProfileID: profile, Text: c.Text, Source: c.Source, UpdatedAt: now, ExpiresAt: c.ExpiresAt, History: []MemoryRevision{}})
		} else {
			found := false
			for i, m := range d.Memories {
				if m.ID == c.ID && m.ProfileID == profile {
					found = true
					if len(m.History) >= 8 {
						return AgentView{}, errors.New("memory correction history limit reached")
					}
					m.History = append(m.History, MemoryRevision{Text: m.Text, Source: m.Source, At: m.UpdatedAt, ExpiresAt: m.ExpiresAt})
					m.Text = c.Text
					m.Source = c.Source
					m.UpdatedAt = now
					m.ExpiresAt = c.ExpiresAt
					d.Memories[i] = m
				}
			}
			if !found {
				return AgentView{}, errors.New("profile memory not found")
			}
		}
	case "memory-delete":
		found := false
		for i, m := range d.Memories {
			if m.ID == c.ID && m.ProfileID == profile {
				d.Memories = append(d.Memories[:i], d.Memories[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			return AgentView{}, errors.New("profile memory not found")
		}
	case "bot-create":
		if !validAgentText(c.Name, 80, true) || len(d.Bots) >= 30 || c.Quantity < 1 || c.Quantity > 1000000 || c.PriceCents < 1 || c.PriceCents > 100000000 || c.BudgetCents < 1 || c.BudgetCents > 1000000000000 {
			return AgentView{}, errors.New("bounded paper inputs and bot name required")
		}
		d.Bots = append(d.Bots, PaperBot{ID: id, ProfileID: profile, Name: c.Name, Strategy: "fixed-paper-buy-v1", Quantity: c.Quantity, PriceCents: c.PriceCents, BudgetCents: c.BudgetCents})
	case "task-create":
		if !validAgentText(c.Name, 80, true) || len(d.Tasks) >= 100 || c.IntervalSeconds < 60 || c.IntervalSeconds > 604800 || c.MaxRuns < 1 || c.MaxRuns > 20 {
			return AgentView{}, errors.New("task requires name, interval 60–604800 seconds and 1–20 runs")
		}
		if c.BotID != "" {
			found := false
			for _, b := range d.Bots {
				if b.ID == c.BotID && b.ProfileID == profile {
					found = true
				}
			}
			if !found {
				return AgentView{}, errors.New("profile paper bot not found")
			}
		} else if !validAgentText(c.Text, 4000, true) {
			return AgentView{}, errors.New("reminder text required")
		}
		reserved := c.MaxRuns
		for _, t := range d.Tasks {
			reserved += t.MaxRuns
		}
		if reserved > 200 {
			return AgentView{}, errors.New("local prototype allows 200 total scheduled evaluations")
		}
		d.Tasks = append(d.Tasks, AgentTask{ID: id, ProfileID: profile, Name: c.Name, Text: c.Text, BotID: c.BotID, IntervalSeconds: c.IntervalSeconds, MaxRuns: c.MaxRuns, NextAt: now + c.IntervalSeconds, State: "scheduled"})
	case "task-pause", "task-resume", "task-run":
		found := false
		for i, t := range d.Tasks {
			if t.ID != c.ID || t.ProfileID != profile {
				continue
			}
			found = true
			if t.State == "completed" {
				return AgentView{}, errors.New("task is completed")
			}
			switch c.Action {
			case "task-pause":
				d.Tasks[i].State = "paused"
			case "task-resume":
				d.Tasks[i].State = "scheduled"
				d.Tasks[i].NextAt = now + t.IntervalSeconds
			case "task-run":
				if t.State != "scheduled" {
					return AgentView{}, errors.New("resume the paused task first")
				}
				if e = runAgentTask(&d, i, now); e != nil {
					return AgentView{}, e
				}
			}
			break
		}
		if !found {
			return AgentView{}, errors.New("profile task not found")
		}
	case "notice-read":
		found := false
		for i, n := range d.Notices {
			if n.ID == c.ID && n.ProfileID == profile {
				d.Notices[i].Read = true
				found = true
			}
		}
		if !found {
			return AgentView{}, errors.New("profile notification not found")
		}
	default:
		return AgentView{}, errors.New("unsupported local agent action")
	}
	if e = saveAgents(ctx, tx, d, revision); e != nil {
		return AgentView{}, e
	}
	if e = tx.Commit(); e != nil {
		return AgentView{}, e
	}
	return agentView(d, revision+1, profile, "", now)
}
func runAgentTask(d *agentDocument, i int, now int64) error {
	if len(d.Runs) >= 200 || len(d.Notices) >= 200 {
		return errors.New("local run/inbox capacity reached; no task executed")
	}
	t := &d.Tasks[i]
	run := AgentRun{ID: fmt.Sprintf("%s:%d", t.ID, t.Runs+1), ProfileID: t.ProfileID, TaskID: t.ID, BotID: t.BotID, Strategy: "reminder-v1", At: now, Outcome: "reminded"}
	text := t.Name + ": " + t.Text
	if t.BotID != "" {
		found := false
		for i, b := range d.Bots {
			if b.ID != t.BotID || b.ProfileID != t.ProfileID {
				continue
			}
			found = true
			run.Strategy = b.Strategy
			run.Quantity = b.Quantity
			run.PriceCents = b.PriceCents
			run.CostCents = b.Quantity * b.PriceCents
			run.BudgetBefore = b.BudgetCents - b.SpentCents
			run.Outcome = "paper-filled"
			if run.CostCents > run.BudgetBefore {
				run.Outcome = "budget-blocked"
			} else {
				d.Bots[i].SpentCents += run.CostCents
			}
			text = t.Name + ": " + run.Outcome + " (fixed fixture price; no market data or order)"
			break
		}
		if !found {
			return errors.New("paper bot missing; preserve task")
		}
	}
	d.Runs = append(d.Runs, run)
	d.Notices = append(d.Notices, AgentNotice{ID: run.ID, ProfileID: t.ProfileID, Text: text, RunID: run.ID, At: now})
	t.Runs++
	t.NextAt = now + t.IntervalSeconds
	if t.Runs >= t.MaxRuns {
		t.State = "completed"
	}
	return nil
}

// Due work coalesces missed intervals into one evaluation; it never catches up
// with a burst. Concurrent ticks/restarts serialize on the SQLite transaction.
func (s *Store) RunAgentTasks(ctx context.Context) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var phase string
	if e = tx.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase); e != nil {
		return e
	}
	if phase == "paused" {
		return ErrCorePaused
	}
	d, r, e := readAgents(ctx, tx)
	if e != nil {
		return e
	}
	changed := false
	now := s.now().Unix()
	for i, t := range d.Tasks {
		if t.State == "scheduled" && t.Runs < t.MaxRuns && t.NextAt <= now {
			if e = runAgentTask(&d, i, now); e != nil {
				return e
			}
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if e = saveAgents(ctx, tx, d, r); e != nil {
		return e
	}
	return tx.Commit()
}
