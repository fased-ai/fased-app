package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// TaskReview is an inert, expiring projection. Only the existing atomic task
// creation writes state; revision and digest bind the reviewed policy.
type TaskReview struct {
	Version         string         `json:"version"`
	Revision        int64          `json:"revision"`
	ProfileID       string         `json:"profileId"`
	Name            string         `json:"name"`
	Text            string         `json:"text"`
	Bot             *PaperBot      `json:"bot,omitempty"`
	News            *TaskNewsInput `json:"news,omitempty"`
	IntervalSeconds int64          `json:"intervalSeconds"`
	MaxRuns         int            `json:"maxRuns"`
	ExpiresAt       int64          `json:"expiresAt"`
	Digest          string         `json:"digest"`
}

type TaskCapacity struct {
	Tasks       int `json:"tasks"`
	Evaluations int `json:"evaluations"`
	Runs        int `json:"runs"`
	Notices     int `json:"notices"`
	Bytes       int `json:"bytes"`
}

func agentTaskCapacity(d agentDocument) TaskCapacity {
	b, _ := json.Marshal(d)
	c := TaskCapacity{Tasks: len(d.Tasks), Runs: len(d.Runs), Notices: len(d.Notices), Bytes: len(b)}
	for _, t := range d.Tasks {
		c.Evaluations += t.MaxRuns
	}
	return c
}

func reviewAgentTask(d agentDocument, c AgentCommand, profile string, revision, now int64) (*TaskReview, error) {
	unexpected := c
	unexpected.Action, unexpected.ProfileID, unexpected.Name, unexpected.Text, unexpected.BotID, unexpected.NewsSourceID, unexpected.ConsentDigest = "", "", "", "", "", "", ""
	unexpected.Revision, unexpected.IntervalSeconds, unexpected.NewsMaxAgeSeconds, unexpected.ExpiresAt, unexpected.MaxRuns = 0, 0, 0, 0, 0
	if unexpected != (AgentCommand{}) {
		return nil, errors.New("task review accepts only reminder, paper and selected news inputs")
	}
	news, err := validateAgentTask(d, c, profile)
	if err != nil {
		return nil, err
	}
	if len(d.Runs) >= 200 || len(d.Notices) >= 200 {
		return nil, errors.New("run/inbox capacity reached; preserve history before creating more tasks")
	}
	expires := now + 600
	if c.Action == "task-reviewed-create" {
		expires = c.ExpiresAt
		if expires <= now || expires > now+600 {
			return nil, errors.New("task review expired; review again")
		}
	}
	r := &TaskReview{Version: "task-review-v1", Revision: revision, ProfileID: profile, Name: c.Name, Text: c.Text, News: news, IntervalSeconds: c.IntervalSeconds, MaxRuns: c.MaxRuns, ExpiresAt: expires}
	if c.BotID != "" {
		for _, b := range d.Bots {
			if b.ID == c.BotID && b.ProfileID == profile {
				copy := b
				r.Bot = &copy
				break
			}
		}
	}
	b, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(b)
	r.Digest = hex.EncodeToString(h[:])
	return r, nil
}
