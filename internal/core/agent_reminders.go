package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// ReminderReview is a transient response, never persisted or scheduled. The
// existing opaque task ID records single-use source identity without new fields.
type ReminderReview struct {
	Version         string `json:"version"`
	ProfileID       string `json:"profileId"`
	CardID          string `json:"cardId"`
	RequestID       string `json:"requestId"`
	SessionID       string `json:"sessionId"`
	TaskID          string `json:"taskId"`
	SourceTitle     string `json:"sourceTitle"`
	SourceText      string `json:"sourceText"`
	Name            string `json:"name"`
	Text            string `json:"text"`
	IntervalSeconds int64  `json:"intervalSeconds"`
	MaxRuns         int    `json:"maxRuns"`
	Revision        int64  `json:"revision"`
	ExpiresAt       int64  `json:"expiresAt"`
	Digest          string `json:"digest"`
}

func reviewChatReminder(d *agentDocument, c AgentCommand, profile string, revision, now int64) (*ReminderReview, *AIActionCard, error) {
	// Only a plain reminder can be proposed. Execution bindings are explicitly rejected.
	if err := plainReminderBindings(c); err != nil {
		return nil, nil, err
	}
	var source *AIActionCard
	var request *AgentAIRequest
	for i := range d.AIRequests {
		q := &d.AIRequests[i]
		if q.ProfileID != profile || q.State != "completed" || q.TaskID != "" {
			continue
		}
		for j := range q.Cards {
			card := &q.Cards[j]
			if card.ID != c.ID {
				continue
			}
			if source != nil || card.Kind != "reminder-draft" || (card.Review != "unreviewed" && card.Review != "kept") {
				return nil, nil, errors.New("reminder draft unavailable or ambiguous")
			}
			source, request = card, q
		}
	}
	if source == nil {
		return nil, nil, errors.New("same-profile completed chat reminder draft required")
	}
	found := false
	for _, session := range d.Sessions {
		if session.ID == request.SessionID && session.ProfileID == profile {
			found = true
		}
	}
	if !found {
		return nil, nil, errors.New("source conversation unavailable")
	}
	taskID := "chat-reminder:" + source.ID
	for _, task := range d.Tasks {
		if task.ID == taskID {
			return nil, nil, errors.New("this draft already created a reminder; use Tasks to manage it")
		}
	}
	plain := AgentCommand{Name: c.Name, Text: c.Text, IntervalSeconds: c.IntervalSeconds, MaxRuns: c.MaxRuns}
	if _, err := validateAgentTask(*d, plain, profile); err != nil {
		return nil, nil, err
	}
	expires := now + 600
	if c.Action == "ai-reminder-create" {
		expires = c.ExpiresAt
		if expires <= now || expires > now+600 {
			return nil, nil, errors.New("reminder review expired; review again")
		}
	}
	review := &ReminderReview{Version: "chat-reminder-v1", ProfileID: profile, CardID: source.ID, RequestID: request.ID, SessionID: request.SessionID, TaskID: taskID, SourceTitle: source.Title, SourceText: source.Text, Name: c.Name, Text: c.Text, IntervalSeconds: c.IntervalSeconds, MaxRuns: c.MaxRuns, Revision: revision, ExpiresAt: expires}
	bytes, err := json.Marshal(review)
	if err != nil {
		return nil, nil, err
	}
	digest := sha256.Sum256(bytes)
	review.Digest = hex.EncodeToString(digest[:])
	return review, source, nil
}

func validateAgentTask(d agentDocument, c AgentCommand, profile string) (*TaskNewsInput, error) {
	if !validAgentText(c.Name, 80, true) || len(d.Tasks) >= 100 || c.IntervalSeconds < 60 || c.IntervalSeconds > 604800 || c.MaxRuns < 1 || c.MaxRuns > 20 {
		return nil, errors.New("task requires name, interval 60–604800 seconds and 1–20 runs")
	}
	if c.BotID != "" {
		found := false
		for _, b := range d.Bots {
			if b.ID == c.BotID && b.ProfileID == profile {
				found = true
			}
		}
		if !found {
			return nil, errors.New("profile paper bot not found")
		}
	} else if !validAgentText(c.Text, 4000, true) {
		return nil, errors.New("reminder text required")
	}
	reserved := c.MaxRuns
	for _, t := range d.Tasks {
		reserved += t.MaxRuns
	}
	if reserved > 200 {
		return nil, errors.New("local prototype allows 200 total scheduled evaluations")
	}
	news, e := taskNewsPolicy(d, c, profile)
	if e != nil {
		return nil, e
	}
	return news, nil
}

func appendAgentTask(d *agentDocument, c AgentCommand, profile, id string, now int64) error {
	news, err := validateAgentTask(*d, c, profile)
	if err != nil {
		return err
	}
	d.Tasks = append(d.Tasks, AgentTask{News: news, ID: id, ProfileID: profile, Name: c.Name, Text: c.Text, BotID: c.BotID, IntervalSeconds: c.IntervalSeconds, MaxRuns: c.MaxRuns, NextAt: now + c.IntervalSeconds, State: "scheduled"})
	return nil
}
