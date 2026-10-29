package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

// TaskReminderReview is transient. Reusing a task copies its plain settings,
// never its run counters, schedule position or execution authority.
type TaskReminderReview struct {
	Version         string    `json:"version"`
	ProfileID       string    `json:"profileId"`
	Source          AgentTask `json:"source"`
	SourceSHA256    string    `json:"sourceSha256"`
	Name            string    `json:"name"`
	Text            string    `json:"text"`
	IntervalSeconds int64     `json:"intervalSeconds"`
	MaxRuns         int       `json:"maxRuns"`
	Revision        int64     `json:"revision"`
	ExpiresAt       int64     `json:"expiresAt"`
	Digest          string    `json:"digest"`
}

func plainReminderBindings(c AgentCommand) error {
	if c.Watch != nil {
		return errors.New("news watch cannot become a plain reminder")
	}
	if c.BotID != "" || c.WEN != nil || c.NewsSourceID != "" || c.NewsMaxAgeSeconds != 0 || c.ConnectionRef != "" || c.Limits != nil || c.TotalBudgetMicrousd != 0 || c.AuthorizationSeconds != 0 || c.Provider != "" || c.Model != "" || c.Quantity != 0 || c.PriceCents != 0 || c.BudgetCents != 0 || c.Source != "" {
		return errors.New("plain reminders cannot attach AI, news, bots or financial operations")
	}
	return nil
}

func savedPlainReminder(d agentDocument, profile, id string) (AgentTask, error) {
	var source AgentTask
	profiles, tasks := 0, 0
	for _, p := range d.Profiles {
		if p.ID == profile {
			profiles++
		}
	}
	for _, t := range d.Tasks {
		if t.ID == id {
			source = t
			tasks++
		}
	}
	if !validAgentText(id, 80, true) || !validAgentText(profile, 80, true) || !utf8.ValidString(id+profile) || profiles != 1 || tasks != 1 || source.ProfileID != profile || source.AI != nil || source.News != nil || source.WEN != nil || source.BotID != "" {
		return AgentTask{}, errors.New("one same-profile saved plain reminder required")
	}
	if !validAgentText(source.Name, 80, true) || !validAgentText(source.Text, 4000, true) || !utf8.ValidString(source.Name+source.Text) || (source.Calendar == nil && (source.IntervalSeconds < 60 || source.IntervalSeconds > 604800)) || (source.Calendar != nil && !validCalendarTask(source)) || source.MaxRuns < 1 || source.MaxRuns > 20 || source.Runs < 0 || source.Runs > source.MaxRuns || source.NextAt < 0 ||
		(source.State != "scheduled" && source.State != "paused" && source.State != "completed") || (source.State == "completed") != (source.Runs == source.MaxRuns) {
		return AgentTask{}, errors.New("saved reminder integrity invalid; preserve its records")
	}
	return source, nil
}

func reviewTaskReminder(d agentDocument, c AgentCommand, profile string, revision, now int64) (*TaskReminderReview, error) {
	if err := plainReminderBindings(c); err != nil {
		return nil, err
	}
	source, err := savedPlainReminder(d, profile, c.ID)
	if err != nil {
		return nil, err
	}
	if source.Calendar != nil {
		return nil, errors.New("choose New task to review a new calendar date")
	}
	plain := AgentCommand{Name: c.Name, Text: c.Text, IntervalSeconds: c.IntervalSeconds, MaxRuns: c.MaxRuns}
	if _, err = validateAgentTask(d, plain, profile); err != nil {
		return nil, err
	}
	if !validAgentText(c.Text, 4000, true) || !utf8.ValidString(c.Name+c.Text) {
		return nil, errors.New("invalid reminder draft text")
	}
	expires := now + 600
	if c.Action == "task-reminder-create" {
		expires = c.ExpiresAt
		if expires <= now || expires > now+600 {
			return nil, errors.New("reminder review expired; review again")
		}
	}
	b, _ := json.Marshal(source)
	sourceHash := sha256.Sum256(b)
	v := &TaskReminderReview{Version: "saved-reminder-v1", ProfileID: profile, Source: source, SourceSHA256: hex.EncodeToString(sourceHash[:]), Name: c.Name, Text: c.Text, IntervalSeconds: c.IntervalSeconds, MaxRuns: c.MaxRuns, Revision: revision, ExpiresAt: expires}
	b, err = json.Marshal(v)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(b)
	v.Digest = hex.EncodeToString(digest[:])
	return v, nil
}

func (s *Store) AgentReminderDraft(ctx context.Context, profile, id string) (TaskReminderReview, error) {
	d, revision, err := readAgents(ctx, s.db)
	if err != nil {
		return TaskReminderReview{}, err
	}
	source, err := savedPlainReminder(d, profile, id)
	if err != nil {
		return TaskReminderReview{}, err
	}
	v, err := reviewTaskReminder(d, AgentCommand{ID: id, Name: source.Name, Text: source.Text, IntervalSeconds: source.IntervalSeconds, MaxRuns: source.MaxRuns}, profile, revision, s.now().Unix())
	if err != nil {
		return TaskReminderReview{}, err
	}
	return *v, nil
}
