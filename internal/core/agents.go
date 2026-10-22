package core

// Local agent records have no wallet reference, tool execution, provider secret
// or financial authority. One bounded document and SQL transaction make a task
// result, paper ledger entry, inbox notification and next schedule atomic.
import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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
	WEN             *WENPaperPlan  `json:"wen,omitempty"`
	News            *TaskNewsInput `json:"news,omitempty"`
	AI              *TaskAIStep    `json:"ai,omitempty"`
	ID              string         `json:"id"`
	ProfileID       string         `json:"profileId"`
	Name            string         `json:"name"`
	Text            string         `json:"text"`
	BotID           string         `json:"botId"`
	IntervalSeconds int64          `json:"intervalSeconds"`
	MaxRuns         int            `json:"maxRuns"`
	Runs            int            `json:"runs"`
	NextAt          int64          `json:"nextAt"`
	State           string         `json:"state"`
}
type AgentRun struct {
	WEN          *WENPaperResult `json:"wen,omitempty"`
	News         *NewsEvidence   `json:"news,omitempty"`
	AIRequestID  string          `json:"aiRequestId,omitempty"`
	AIDigest     string          `json:"aiDigest,omitempty"`
	ID           string          `json:"id"`
	ProfileID    string          `json:"profileId"`
	TaskID       string          `json:"taskId"`
	BotID        string          `json:"botId"`
	Strategy     string          `json:"strategy"`
	At           int64           `json:"at"`
	Outcome      string          `json:"outcome"`
	Quantity     int64           `json:"quantity"`
	PriceCents   int64           `json:"priceCents"`
	CostCents    int64           `json:"costCents"`
	BudgetBefore int64           `json:"budgetBefore"`
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
	NewsSources []NewsSource     `json:"newsSources"`
	NewsFetches []NewsFetch      `json:"newsFetches"`
	Profiles    []AgentProfile   `json:"profiles"`
	Sessions    []AgentSession   `json:"sessions"`
	Messages    []AgentMessage   `json:"messages"`
	Memories    []AgentMemory    `json:"memories"`
	Bots        []PaperBot       `json:"bots"`
	Tasks       []AgentTask      `json:"tasks"`
	Runs        []AgentRun       `json:"runs"`
	Notices     []AgentNotice    `json:"notices"`
	AIRequests  []AgentAIRequest `json:"aiRequests"`
}
type AgentView struct {
	ReminderReview       *ReminderReview  `json:"reminderReview,omitempty"`
	NewsSources          []NewsSource     `json:"newsSources"`
	NewsFetches          []NewsFetch      `json:"newsFetches"`
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
	WEN                  *WENPaperInput `json:"wen,omitempty"`
	NewsSourceID         string         `json:"newsSourceId,omitempty"`
	NewsMaxAgeSeconds    int64          `json:"newsMaxAgeSeconds,omitempty"`
	ConnectionRef        string         `json:"connectionRef,omitempty"`
	Revision             int64          `json:"revision"`
	Action               string         `json:"action"`
	ProfileID            string         `json:"profileId,omitempty"`
	ID                   string         `json:"id,omitempty"`
	Name                 string         `json:"name,omitempty"`
	Text                 string         `json:"text,omitempty"`
	Source               string         `json:"source,omitempty"`
	ExpiresAt            int64          `json:"expiresAt,omitempty"`
	Provider             string         `json:"provider,omitempty"`
	Model                string         `json:"model,omitempty"`
	BotID                string         `json:"botId,omitempty"`
	IntervalSeconds      int64          `json:"intervalSeconds,omitempty"`
	MaxRuns              int            `json:"maxRuns,omitempty"`
	Quantity             int64          `json:"quantity,omitempty"`
	PriceCents           int64          `json:"priceCents,omitempty"`
	BudgetCents          int64          `json:"budgetCents,omitempty"`
	Limits               *AILimits      `json:"limits,omitempty"`
	ConsentDigest        string         `json:"consentDigest,omitempty"`
	TotalBudgetMicrousd  int64          `json:"totalBudgetMicrousd,omitempty"`
	AuthorizationSeconds int64          `json:"authorizationSeconds,omitempty"`
}

func initialAgents() agentDocument {
	return agentDocument{NewsSources: []NewsSource{}, NewsFetches: []NewsFetch{}, Profiles: []AgentProfile{
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
	v := AgentView{NewsSources: []NewsSource{}, NewsFetches: []NewsFetch{}, Revision: revision, ProfileID: profile, Profiles: d.Profiles, Sessions: []AgentSession{}, Messages: []AgentMessage{}, Memories: []AgentMemory{}, Bots: []PaperBot{}, Tasks: []AgentTask{}, Runs: []AgentRun{}, Notices: []AgentNotice{}, AIRequests: []AgentAIRequest{}}
	for _, s := range d.NewsSources {
		if s.ProfileID == profile {
			v.NewsSources = append(v.NewsSources, s)
		}
	}
	for _, f := range d.NewsFetches {
		if f.ProfileID == profile {
			_, e := freshNews(d, profile, &TaskNewsInput{SourceID: f.SourceID, URL: f.URL, MaxAgeSeconds: f.MaxAgeSeconds, FetchID: f.ID}, now)
			f.Fresh = e == nil
			v.NewsFetches = append(v.NewsFetches, f)
		}
	}
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
			if !match {
				for _, q := range d.AIRequests {
					if q.ProfileID == profile && q.SessionID == s.ID && q.State == "completed" && q.TaskID == "" && (strings.Contains(strings.ToLower(q.Prompt), query) || strings.Contains(strings.ToLower(q.Answer), query)) {
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
	wenReserved, err := wenPaperStorage(d)
	if err != nil {
		return err
	}
	reserved := taskAIFutureStorage(d) + newsStorage(d) + wenReserved
	for _, q := range d.AIRequests {
		if q.State == "prepared" || q.State == "queued" || q.State == "running" {
			reserved += agentAIResultReserve
			if q.TaskID != "" {
				reserved += taskAICompletionReserve
			}
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
	if c.Action == "ai-prepare" || c.Action == "task-ai-prepare" {
		if c.ConnectionRef != "" && !s.aiConnectionValid(c.ConnectionRef) {
			return AgentView{}, errors.New("unlock the selected connection first")
		}
	}
	if c.Action == "ai-start" || c.Action == "task-ai-authorize" {
		for _, q := range d.AIRequests {
			if q.ID == c.ID && q.ProfileID == profile && !s.aiConnectionValid(q.ConnectionRef) {
				return AgentView{}, errors.New("connection changed or locked; review a fresh request")
			}
		}
		for _, t := range d.Tasks {
			if t.ID == c.ID && t.ProfileID == profile && t.AI != nil && !s.aiConnectionValid(t.AI.ConnectionRef) {
				return AgentView{}, errors.New("connection changed or locked; review a fresh task")
			}
		}
	}
	switch c.Action {
	case "task-run-explain":
		if e = captureAgentRunContext(&d, profile, c.ID, id, revision, now); e != nil {
			return AgentView{}, e
		}
	case "wen-paper-explain":
		if e = captureWENPaperContext(&d, profile, c.ID, id, revision, now); e != nil {
			return AgentView{}, e
		}
	case "wen-paper-prepare", "wen-paper-approve", "wen-paper-cancel":
		if e = changeWEN(&d, c, profile, id, now); e != nil {
			return AgentView{}, e
		}
	case "news-source-add", "news-source-disable", "news-fetch", "news-cancel":
		if e = changeNews(&d, c, profile, id, now); e != nil {
			return AgentView{}, e
		}
	case "ai-prepare", "ai-start", "ai-cancel", "ai-card-keep", "ai-card-dismiss":
		if e = changeAgentAI(&d, c, profile, id, now, fixture); e != nil {
			return AgentView{}, e
		}
		if c.Action == "ai-prepare" {
			n, err := taskNewsPolicy(d, c, profile)
			if err != nil {
				return AgentView{}, err
			}
			if n != nil {
				if err = attachAINews(d, &d.AIRequests[len(d.AIRequests)-1], n, now); err != nil {
					return AgentView{}, err
				}
			}
		}
	case "task-ai-prepare", "task-ai-authorize", "task-ai-cancel":
		if e = changeTaskAI(&d, c, profile, id, now, fixture); e != nil {
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
		if e = putAgentMemory(&d, c, profile, id, now); e != nil {
			return AgentView{}, e
		}
	case "memory-chat-save":
		if e = saveChatMemory(&d, c, profile, id, now); e != nil {
			return AgentView{}, e
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
	case "ai-reminder-review", "ai-reminder-create":
		review, card, err := reviewChatReminder(&d, c, profile, revision, now)
		if err != nil {
			return AgentView{}, err
		}
		if c.Action == "ai-reminder-review" {
			v, err := agentView(d, revision, profile, "", now)
			v.ReminderReview = review
			return v, err
		}
		if c.ConsentDigest != review.Digest {
			return AgentView{}, errors.New("reminder review changed; review again")
		}
		plain := AgentCommand{Name: review.Name, Text: review.Text, IntervalSeconds: review.IntervalSeconds, MaxRuns: review.MaxRuns}
		if err = appendAgentTask(&d, plain, profile, review.TaskID, now); err != nil {
			return AgentView{}, err
		}
		card.Review = "kept"
	case "task-create":
		if e = appendAgentTask(&d, c, profile, id, now); e != nil {
			return AgentView{}, e
		}
	case "task-pause", "task-resume", "task-run":
		found := false
		for i, t := range d.Tasks {
			if t.ID != c.ID || t.ProfileID != profile {
				continue
			}
			found = true
			if t.WEN != nil {
				if (t.State != "scheduled" && t.State != "paused") || (c.Action == "task-resume" && (t.State != "paused" || validateWEN(t, now, true) != nil)) {
					return AgentView{}, errors.New("paper task needs an unchanged unexpired review")
				}
			}
			if t.AI != nil && (t.State == "review" || t.State == "held" || t.State == "cancelled") {
				return AgentView{}, errors.New("AI task needs a fresh reviewed plan")
			}
			if t.State == "completed" || t.Runs >= t.MaxRuns {
				return AgentView{}, errors.New("task evaluation limit reached; create a new bounded task")
			}
			switch c.Action {
			case "task-pause":
				if t.News != nil && t.News.PendingID != "" {
					if e = cancelNewsTask(&d, i, now); e != nil {
						return AgentView{}, e
					}
					break
				}
				if t.AI != nil && t.AI.PendingID != "" {
					if e = cancelTaskAI(&d, i, now); e != nil {
						return AgentView{}, e
					}
				} else {
					d.Tasks[i].State = "paused"
				}
			case "task-resume":
				if t.AI != nil && (t.State != "paused" || !fixture || validateTaskAI(d, t, now) != nil) {
					return AgentView{}, errors.New("AI task requires active fixture and unchanged unexpired consent")
				}
				d.Tasks[i].State = "scheduled"
				d.Tasks[i].NextAt = now + t.IntervalSeconds
			case "task-run":
				if t.State != "scheduled" {
					return AgentView{}, errors.New("resume the paused task first")
				}
				if e = evaluateAgentTask(&d, i, now, fixture); e != nil {
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
	if newsSlots(d) > newsFetchLimit {
		return AgentView{}, errors.New("news fetch capacity must cover the complete schedule; retained records are preserved")
	}
	if e = saveAgents(ctx, tx, d, revision); e != nil {
		return AgentView{}, e
	}
	if e = tx.Commit(); e != nil {
		return AgentView{}, e
	}
	return agentView(d, revision+1, profile, "", now)
}

// Capture retained facts only. None of the source task's execution/consent paths
// are entered, even if its schedule or AI authorization has since expired.
func captureAgentRunContext(d *agentDocument, profile, runID, id string, revision, now int64) error {
	refused := errors.New("saved reminder run references missing, ambiguous, changed or unsupported")
	if len(d.Sessions) >= 100 || len(d.Messages) >= 200 {
		return errors.New("conversation/message capacity reached; no context saved")
	}
	var r *AgentRun
	for i := range d.Runs {
		if d.Runs[i].ID == runID {
			if r != nil {
				return refused
			}
			r = &d.Runs[i]
		}
	}
	if r == nil || r.ProfileID != profile || r.WEN != nil || r.BotID != "" || r.Quantity != 0 || r.PriceCents != 0 || r.CostCents != 0 || r.BudgetBefore != 0 || r.At <= 0 || r.At > now {
		return refused
	}
	var task *AgentTask
	for i := range d.Tasks {
		if d.Tasks[i].ID == r.TaskID {
			if task != nil {
				return refused
			}
			task = &d.Tasks[i]
		}
	}
	if task == nil || task.ProfileID != profile || task.WEN != nil || task.BotID != "" {
		return refused
	}
	n, err := strconv.Atoi(strings.TrimPrefix(r.ID, task.ID+":"))
	if err != nil || n < 1 || n > task.Runs || r.ID != fmt.Sprintf("%s:%d", task.ID, n) {
		return refused
	}
	plain := r.Strategy == "reminder-v1"
	success := r.Strategy == "ai-step-v1/reminder-v1"
	held := r.Strategy == "ai-step-v1" && strings.HasPrefix(r.Outcome, "ai-")
	if (!plain && !success && !held) || ((plain || success) && r.Outcome != "reminded") {
		return refused
	}
	expectedNotice := task.Name + ": " + task.Text
	if held {
		expectedNotice = task.Name + ": " + r.Outcome + "; dependent step stopped. Review a new task plan."
	}
	var notice *AgentNotice
	for i := range d.Notices {
		if d.Notices[i].RunID == runID || d.Notices[i].ID == runID {
			if notice != nil {
				return refused
			}
			notice = &d.Notices[i]
		}
	}
	if notice == nil || notice.ID != runID || notice.RunID != runID || notice.ProfileID != profile || notice.At != r.At || notice.Text != expectedNotice {
		return refused
	}
	// Whitelist public historical fields; never serialize TaskAIStep or the
	// request itself (both contain private connection/context/disclosure data).
	type recordedAI struct {
		ID, SessionID, Digest, State, Model, Answer                                          string
		Limits                                                                               AILimits
		CreatedAt, InputTokens, OutputTokens, CachedInputTokens, SpentMicrousd, HeldMicrousd int64
	}
	var ai *recordedAI
	if plain {
		if task.AI != nil || r.AIRequestID != "" || r.AIDigest != "" {
			return refused
		}
	} else {
		if task.AI == nil || r.AIDigest == "" || r.AIDigest != task.AI.Digest || r.AIDigest != taskAIDigest(*task) {
			return refused
		}
		if r.AIRequestID == "" {
			if !held {
				return refused
			}
		} else {
			var q *AgentAIRequest
			for i := range d.AIRequests {
				if d.AIRequests[i].ID == r.AIRequestID {
					if q != nil {
						return refused
					}
					q = &d.AIRequests[i]
				}
			}
			if q == nil || q.ProfileID != profile || q.TaskID != task.ID || q.TaskDigest != r.AIDigest || q.Prompt != task.Text || q.Digest != aiDigest(*q) || q.CreatedAt > r.At {
				return refused
			}
			switch q.State {
			case "completed", "failed", "cancelled", "unknown":
			default:
				return refused
			}
			if success && q.State != "completed" {
				return refused
			}
			sessions := 0
			for _, s := range d.Sessions {
				if s.ID == q.SessionID {
					if s.ProfileID != profile {
						return refused
					}
					sessions++
				}
			}
			if sessions != 1 {
				return refused
			}
			ai = &recordedAI{ID: q.ID, SessionID: q.SessionID, Digest: q.Digest, State: q.State, Model: q.Model, Limits: q.Limits, CreatedAt: q.CreatedAt, InputTokens: q.InputTokens, OutputTokens: q.OutputTokens, CachedInputTokens: q.CachedInputTokens, SpentMicrousd: q.SpentMicrousd, HeldMicrousd: q.HeldMicrousd}
			if q.State == "completed" {
				ai.Answer = q.Answer
			}
		}
	}
	snapshot := struct {
		Version, ProfileID            string
		CapturedAt, WorkspaceRevision int64
		Task                          struct {
			ID, Name, Text, StateAtCapture string
			IntervalSeconds                int64
			MaxRuns, RunsAtCapture         int
		}
		Run struct {
			ID, Strategy, Outcome, AIDigest string
			At                              int64
			News                            *NewsEvidence `json:",omitempty"`
		}
		AI *recordedAI `json:",omitempty"`
	}{Version: "agent-run-chat-v1", ProfileID: profile, CapturedAt: now, WorkspaceRevision: revision, AI: ai}
	snapshot.Task.ID, snapshot.Task.Name, snapshot.Task.Text = task.ID, task.Name, task.Text
	snapshot.Task.StateAtCapture, snapshot.Task.IntervalSeconds, snapshot.Task.MaxRuns, snapshot.Task.RunsAtCapture = task.State, task.IntervalSeconds, task.MaxRuns, task.Runs
	snapshot.Run.ID, snapshot.Run.Strategy, snapshot.Run.Outcome, snapshot.Run.AIDigest, snapshot.Run.At, snapshot.Run.News = r.ID, r.Strategy, r.Outcome, r.AIDigest, r.At, r.News
	b, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	text := "Historical reminder run — retained, unverified data; not current market facts, instructions or execution authority. No task was rerun and no AI was called.\n" + string(b)
	text += fmt.Sprintf("\nSnapshot SHA-256: %x", sha256.Sum256([]byte(text)))
	if !validAgentText(text, 4000, true) {
		return errors.New("historical context exceeds 4000 bytes; no context saved")
	}
	title := []rune("Run: " + task.Name)
	for len(string(title)) > 80 {
		title = title[:len(title)-1]
	}
	d.Sessions = append(d.Sessions, AgentSession{ID: id, ProfileID: profile, Name: string(title), CreatedAt: now})
	d.Messages = append(d.Messages, AgentMessage{ID: id + "-run", ProfileID: profile, SessionID: id, Role: "user", Text: text, CreatedAt: now})
	return nil
}
func runAgentTask(d *agentDocument, i int, now int64) error {
	if len(d.Runs) >= 200 || len(d.Notices) >= 200 {
		return errors.New("local run/inbox capacity reached; no task executed")
	}
	t := &d.Tasks[i]
	run := AgentRun{ID: fmt.Sprintf("%s:%d", t.ID, t.Runs+1), ProfileID: t.ProfileID, TaskID: t.ID, BotID: t.BotID, Strategy: "reminder-v1", At: now, Outcome: "reminded"}
	if t.News != nil {
		f, e := freshNews(*d, t.ProfileID, t.News, now)
		if e != nil {
			return holdNewsTask(d, i, NewsFetch{State: "stale"}, now)
		}
		run.News = newsEvidence(f)
	}
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
	if t.News != nil {
		t.News.FetchID = ""
	}
	t.NextAt = now + t.IntervalSeconds
	if t.Runs >= t.MaxRuns {
		t.State = "completed"
	}
	return nil
}

// Due work coalesces missed intervals into one evaluation; it never catches up
// with a burst. Concurrent ticks/restarts serialize on the SQLite transaction.
func (s *Store) RunAgentTasks(ctx context.Context) error { return s.runAgentTasks(ctx, false) }
func (s *Store) runAgentTasks(ctx context.Context, fixture bool) error {
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
			if e = evaluateAgentTask(&d, i, now, fixture); e != nil {
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

func putAgentMemory(d *agentDocument, c AgentCommand, profile, id string, now int64) error {
	if !validAgentText(c.Text, 4000, true) || !validAgentText(c.Source, 300, true) {
		return errors.New("memory text and evidence/source required")
	}
	if c.ID == "" {
		if len(d.Memories) >= 100 {
			return errors.New("memory limit reached")
		}
		d.Memories = append(d.Memories, AgentMemory{ID: id, ProfileID: profile, Text: c.Text, Source: c.Source, UpdatedAt: now, ExpiresAt: c.ExpiresAt, History: []MemoryRevision{}})
	} else {
		found := false
		for i, m := range d.Memories {
			if m.ID == c.ID && m.ProfileID == profile {
				found = true
				if len(m.History) >= 8 {
					return errors.New("memory correction history limit reached")
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
			return errors.New("profile memory not found")
		}
	}

	return nil
}

// Source text is retained in chat; only the owner-edited note and a bounded,
// server-resolved reference are stored in the existing memory fields.
func saveChatMemory(d *agentDocument, c AgentCommand, profile, id string, now int64) error {
	if c.ExpiresAt != 0 && c.ExpiresAt <= now {
		return errors.New("choose a future memory expiry or no expiry")
	}
	var session, text string
	matches := 0
	switch c.Source {
	case "message":
		for _, m := range d.Messages {
			if m.ID == c.ID && m.ProfileID == profile {
				matches++
				if m.Role != "user" {
					return errors.New("only retained user messages can become memory")
				}
				session, text = m.SessionID, m.Text
			}
		}
	case "answer":
		for _, q := range d.AIRequests {
			if q.ID == c.ID && q.ProfileID == profile {
				matches++
				if q.State != "completed" || q.TaskID != "" {
					return errors.New("only completed manual chat answers can become memory")
				}
				session, text = q.SessionID, q.Answer
			}
		}
	default:
		return errors.New("select a chat message or answer; source evidence is server-resolved")
	}
	if matches != 1 || !validAgentText(text, 4000, true) {
		return errors.New("same-profile retained chat source unavailable or ambiguous")
	}
	sessions := 0
	for _, s := range d.Sessions {
		if s.ID == session && s.ProfileID == profile {
			sessions++
		}
	}
	if sessions != 1 {
		return errors.New("source conversation unavailable or ambiguous")
	}
	digest := sha256.Sum256([]byte(text))
	source := fmt.Sprintf("chat-memory-v1 kind=%s profile=%s session=%s id=%s sha256=%x", c.Source, profile, session, c.ID, digest)
	return putAgentMemory(d, AgentCommand{Text: c.Text, Source: source, ExpiresAt: c.ExpiresAt}, profile, id, now)
}
