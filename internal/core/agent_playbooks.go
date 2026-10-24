package core

// Playbooks are bounded settings, not credentials, consent or executable code.
import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"unicode/utf8"
)

const playbookKind = "fixture-news-digest-v1"

type PlaybookVersion struct {
	Version           int      `json:"version"`
	Name              string   `json:"name"`
	Text              string   `json:"text"`
	NewsSourceID      string   `json:"newsSourceId"`
	NewsMaxAgeSeconds int64    `json:"newsMaxAgeSeconds"`
	Limits            AILimits `json:"limits"`
	CreatedAt         int64    `json:"createdAt"`
}
type AgentPlaybook struct {
	ID        string            `json:"id"`
	ProfileID string            `json:"profileId"`
	Kind      string            `json:"kind"`
	Versions  []PlaybookVersion `json:"versions"`
}
type PlaybookOrigin struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	SHA256  string `json:"sha256"`
}
type AgentPlaybookDraft struct {
	ProfileID string          `json:"profileId"`
	Revision  int64           `json:"revision"`
	Selected  PlaybookVersion `json:"selected"`
	Source    NewsSource      `json:"source"`
	Command   AgentCommand    `json:"command"`
}
type PlaybookReview struct {
	Version   string        `json:"version"`
	ProfileID string        `json:"profileId"`
	Revision  int64         `json:"revision"`
	BootID    string        `json:"bootId"`
	ExpiresAt int64         `json:"expiresAt"`
	Task      AgentTask     `json:"task"`
	News      NewsFetch     `json:"news"`
	Digest    string        `json:"digest"`
	Command   *AgentCommand `json:"command,omitempty"`
}

func migratePlaybooks(ctx context.Context, db *sql.DB) error {
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
	var root map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &root) != nil {
		return errors.New("invalid original workspace; preserve state")
	}
	if _, ok := root["playbooks"]; ok {
		return errors.New("playbooks without schema protection; preserve state")
	}
	var tasks []map[string]json.RawMessage
	if json.Unmarshal(root["tasks"], &tasks) != nil {
		return errors.New("invalid original tasks; preserve state")
	}
	for _, task := range tasks {
		if _, ok := task["playbook"]; ok {
			return errors.New("playbook tasks without schema protection; preserve state")
		}
	}
	_, err = tx.ExecContext(ctx, "PRAGMA user_version=16")
	if err != nil {
		return err
	}
	return tx.Commit()
}
func playbookLimits(l AILimits) bool {
	return l.MaxInputTokens >= 1000 && l.MaxInputTokens <= 32000 && l.MaxOutputTokens >= 128 && l.MaxOutputTokens <= 1024 && l.DeadlineSeconds >= 1 && l.DeadlineSeconds <= 5 && l.BudgetMicrousd >= aiReservation(l) && l.BudgetMicrousd <= 1000000
}
func playbookHash(v PlaybookVersion) string { b, _ := json.Marshal(v); return newsHash(b) }
func validatePlaybookStorage(raw []byte, d agentDocument) error {
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return errors.New("invalid playbook document; preserve state")
	}
	if data, ok := root["playbooks"]; ok {
		var records []AgentPlaybook
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&records) != nil {
			return errors.New("unsupported playbook fields; preserve state")
		}
	}
	return validatePlaybooks(d)
}
func validatePlaybooks(d agentDocument) error {
	if len(d.Playbooks) > 20 {
		return errors.New("playbook capacity exceeded; preserve state")
	}
	ids := map[string]bool{}
	for _, p := range d.Playbooks {
		if !validAgentText(p.ID, 80, true) || ids[p.ID] || agentProfile(d, p.ProfileID) < 0 || p.Kind != playbookKind || len(p.Versions) < 1 || len(p.Versions) > 8 {
			return errors.New("invalid playbook history; preserve state")
		}
		ids[p.ID] = true
		for i, v := range p.Versions {
			if v.Version != i+1 || !validAgentText(v.Name, 80, true) || !validAgentText(v.Text, 1000, true) || !utf8.ValidString(v.Name+v.Text) || !validAgentText(v.NewsSourceID, 80, true) || v.NewsMaxAgeSeconds < 60 || v.NewsMaxAgeSeconds > 86400 || !playbookLimits(v.Limits) || v.CreatedAt < 0 {
				return errors.New("invalid playbook version; preserve state")
			}
		}
	}
	for _, t := range d.Tasks {
		if t.Playbook == nil {
			continue
		}
		v, err := savedPlaybook(d, t.ProfileID, t.Playbook.ID, t.Playbook.Version)
		if err != nil || t.Playbook.SHA256 != playbookHash(v) || t.News == nil || t.News.SourceID != v.NewsSourceID || t.News.URL != fixtureNewsURL || t.News.MaxAgeSeconds != v.NewsMaxAgeSeconds || t.AI == nil || t.AI.Model != fixtureAIModel || t.AI.ConnectionRef != "" || t.AI.Policy != nil || t.AI.Limits != v.Limits || t.Text != v.Text || t.Name != v.Name || t.BotID != "" || t.WEN != nil || t.MaxRuns != 1 {
			return errors.New("invalid playbook task binding; preserve state")
		}
	}
	return nil
}
func savedPlaybook(d agentDocument, profile, id string, version int) (PlaybookVersion, error) {
	var selected PlaybookVersion
	n := 0
	for _, p := range d.Playbooks {
		if p.ID == id {
			if p.ProfileID != profile || version < 1 || version > len(p.Versions) || p.Kind != playbookKind {
				return selected, errors.New("same-profile playbook version required")
			}
			n++
			selected = p.Versions[version-1]
		}
	}
	if n != 1 {
		return selected, errors.New("one saved same-profile playbook required")
	}
	return selected, nil
}
func playbookCommand(c AgentCommand) error {
	x := c
	x.Action = ""
	x.ProfileID = ""
	x.Revision = 0
	x.ID = ""
	if c.Action == "playbook-save" {
		x.Name = ""
		x.Text = ""
		x.NewsSourceID = ""
		x.NewsMaxAgeSeconds = 0
		x.Limits = nil
	} else {
		x.PlaybookVersion = 0
		x.SessionID = ""
		x.ExpiresAt = 0
		if c.Action == "playbook-run-create" {
			x.ConsentDigest = ""
		}
	}
	if !reflect.DeepEqual(x, AgentCommand{}) {
		return errors.New("playbooks accept only bounded fixture settings; credentials, authority and extra bindings are forbidden")
	}
	return nil
}
func savePlaybook(d *agentDocument, c AgentCommand, profile, id string, now int64) error {
	if err := playbookCommand(c); err != nil {
		return err
	}
	source, err := newsSource(*d, profile, c.NewsSourceID)
	if err != nil || source.URL != fixtureNewsURL || c.NewsMaxAgeSeconds < 60 || c.NewsMaxAgeSeconds > source.MaxAgeSeconds || c.Limits == nil || !playbookLimits(*c.Limits) || !validAgentText(c.Name, 80, true) || !validAgentText(c.Text, 1000, true) || !utf8.ValidString(c.Name+c.Text) {
		return errors.New("select the compiled fixture feed and bounded digest settings")
	}
	i := -1
	for n, p := range d.Playbooks {
		if p.ID == c.ID {
			if p.ProfileID != profile || i != -1 {
				return errors.New("same-profile playbook required")
			}
			i = n
		}
	}
	if c.ID != "" && i < 0 {
		return errors.New("saved playbook unavailable")
	}
	if i < 0 {
		if len(d.Playbooks) >= 20 {
			return errors.New("twenty-playbook capacity reached; preserve history")
		}
		d.Playbooks = append(d.Playbooks, AgentPlaybook{ID: id, ProfileID: profile, Kind: playbookKind, Versions: []PlaybookVersion{}})
		i = len(d.Playbooks) - 1
	}
	p := &d.Playbooks[i]
	if len(p.Versions) >= 8 {
		return errors.New("eight-version capacity reached; preserve history")
	}
	p.Versions = append(p.Versions, PlaybookVersion{Version: len(p.Versions) + 1, Name: c.Name, Text: c.Text, NewsSourceID: source.ID, NewsMaxAgeSeconds: c.NewsMaxAgeSeconds, Limits: *c.Limits, CreatedAt: now})
	return nil
}
func (s *Store) AgentPlaybookDraft(ctx context.Context, profile, id string, version int) (AgentPlaybookDraft, error) {
	d, revision, err := readAgents(ctx, s.db)
	if err != nil {
		return AgentPlaybookDraft{}, err
	}
	v, err := savedPlaybook(d, profile, id, version)
	if err != nil {
		return AgentPlaybookDraft{}, err
	}
	source, err := newsSource(d, profile, v.NewsSourceID)
	if err != nil || source.URL != fixtureNewsURL {
		return AgentPlaybookDraft{}, errors.New("playbook news source unavailable; edit a new version")
	}
	return AgentPlaybookDraft{ProfileID: profile, Revision: revision, Selected: v, Source: source, Command: AgentCommand{Action: "playbook-run-review", Revision: revision, ProfileID: profile, ID: id, PlaybookVersion: version}}, nil
}
func (s *Store) reviewPlaybook(d agentDocument, c AgentCommand, profile string, revision, now int64) (PlaybookReview, error) {
	if err := playbookCommand(c); err != nil {
		return PlaybookReview{}, err
	}
	v, err := savedPlaybook(d, profile, c.ID, c.PlaybookVersion)
	if err != nil {
		return PlaybookReview{}, err
	}
	expires := c.ExpiresAt
	if c.Action == "playbook-run-review" {
		expires = now + 600
	}
	created := expires - 600
	if created > now || expires <= now {
		return PlaybookReview{}, errors.New("playbook review expired; review again")
	}
	news, err := taskNewsPolicy(d, AgentCommand{NewsSourceID: v.NewsSourceID, NewsMaxAgeSeconds: v.NewsMaxAgeSeconds}, profile)
	if err != nil || news.URL != fixtureNewsURL {
		return PlaybookReview{}, errors.New("compiled fixture source required")
	}
	// The latest retained snapshot must be fresh; never fall back after failure.
	for i := len(d.NewsFetches) - 1; i >= 0; i-- {
		if d.NewsFetches[i].ProfileID == profile && d.NewsFetches[i].SourceID == v.NewsSourceID {
			news.FetchID = d.NewsFetches[i].ID
			break
		}
	}
	f, err := freshNews(d, profile, news, now)
	if err != nil {
		return PlaybookReview{}, errors.New("fetch fresh fixture headlines before reviewing this playbook")
	}
	seed, _ := json.Marshal([]any{profile, c.ID, c.PlaybookVersion, revision, c.SessionID, expires, s.bootID})
	id := "playbook-run:" + newsHash(seed)
	copy := d
	copy.Tasks = append([]AgentTask{}, d.Tasks...)
	err = changeTaskAI(&copy, AgentCommand{Action: "task-ai-prepare", ID: c.SessionID, Name: v.Name, Text: v.Text, IntervalSeconds: 60, MaxRuns: 1, AuthorizationSeconds: 600, TotalBudgetMicrousd: v.Limits.BudgetMicrousd, Limits: &v.Limits, Model: fixtureAIModel, NewsSourceID: v.NewsSourceID, NewsMaxAgeSeconds: v.NewsMaxAgeSeconds}, profile, id, created, false)
	if err != nil {
		return PlaybookReview{}, err
	}
	task := copy.Tasks[len(copy.Tasks)-1]
	task.Playbook = &PlaybookOrigin{ID: c.ID, Version: c.PlaybookVersion, SHA256: playbookHash(v)}
	task.AI.Digest = taskAIDigest(task)
	r := PlaybookReview{Version: "playbook-review-v1", ProfileID: profile, Revision: revision, BootID: s.bootID, ExpiresAt: expires, Task: task, News: f}
	b, _ := json.Marshal(r)
	r.Digest = newsHash(b)
	command := c
	command.Action = "playbook-run-create"
	command.Revision = revision
	command.ProfileID = profile
	command.ExpiresAt = expires
	command.ConsentDigest = r.Digest
	r.Command = &command
	return r, nil
}
func (s *Store) createPlaybookRun(d *agentDocument, c AgentCommand, profile string, revision, now int64, fixture bool) error {
	if !fixture {
		return errors.New("playbook runs require the explicit development AI fixture")
	}
	r, err := s.reviewPlaybook(*d, c, profile, revision, now)
	if err != nil {
		return err
	}
	if c.ConsentDigest == "" || c.ConsentDigest != r.Digest {
		return errors.New("unchanged playbook review and exact consent required")
	}
	for _, t := range d.Tasks {
		if t.ID == r.Task.ID {
			return fmt.Errorf("this playbook review already created a run")
		}
	}
	t := r.Task
	t.AI.Authorized = true
	t.State = "scheduled"
	t.NextAt = now
	if err = validateTaskAI(*d, t, now); err != nil {
		return err
	}
	d.Tasks = append(d.Tasks, t)
	return evaluateAgentTask(d, len(d.Tasks)-1, now, true)
}
