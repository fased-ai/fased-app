package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/fased-ai/fased-app/internal/update"
)

func onchainRowFields(row map[string]json.RawMessage, key string, unprotected bool) error {
	for field, body := range row {
		if unprotected && (strings.EqualFold(field, "id") || strings.EqualFold(field, "taskId")) {
			var id string
			if json.Unmarshal(body, &id) == nil && strings.HasPrefix(id, swapPrefix) {
				return errors.New("unprotected swap identity")
			}
		}
		if !strings.EqualFold(field, "swap") {
			continue
		}
		if unprotected || field != "swap" || string(body) == "null" {
			return errors.New("unprotected or ambiguous swap fields")
		}
		if key == "tasks" {
			var policy SwapTaskPolicy
			if update.StrictJSONBounded(body, &policy, agentDocumentLimit) != nil {
				return errors.New("invalid typed swap policy")
			}
		} else {
			var execution SwapExecution
			if update.StrictJSONBounded(body, &execution, agentDocumentLimit) != nil {
				return errors.New("invalid typed swap execution")
			}
		}
	}
	return nil
}

func onchainFields(raw []byte, unprotected bool) error {
	root, e := strategyWorkspaceFields(raw)
	if e != nil {
		return e
	}
	for key, value := range root {
		if !strings.EqualFold(key, "tasks") && !strings.EqualFold(key, "runs") {
			continue
		}
		if key != "tasks" && key != "runs" {
			return errors.New("ambiguous swap container")
		}
		var rows []map[string]json.RawMessage
		if json.Unmarshal(value, &rows) != nil {
			return errors.New("invalid swap container")
		}
		for _, row := range rows {
			if e := onchainRowFields(row, key, unprotected); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *Store) controlSwapTask(ctx context.Context, c AgentCommand) (AgentView, error) {
	unexpected := c
	unexpected.Action, unexpected.ProfileID, unexpected.ID = "", "", ""
	unexpected.Revision = 0
	if unexpected != (AgentCommand{}) || s.onchain == nil {
		return AgentView{}, errors.New("one available swap task required")
	}
	s.onchain.mu.Lock()
	defer s.onchain.mu.Unlock()
	_, revision, e := s.swapSnapshot(ctx, c.ProfileID)
	if e != nil {
		return AgentView{}, e
	}
	if revision != c.Revision {
		return AgentView{}, ErrConflict
	}
	e = s.writeSwap(ctx, c.ProfileID, c.ID, func(t *AgentTask, r *AgentRun) error {
		switch c.Action {
		case "task-pause":
			if r.Swap.State != "queued" {
				return errors.New("signed or uncertain order must be reconciled")
			}
			t.State = "paused"
			swapEvent(r.Swap, "paused", s.now().Unix())
		case "task-resume", "task-run":
			if r.Swap.State != "paused" || r.Swap.BootID != s.bootID || s.now().Unix() >= t.Swap.Proposal.Quote.ExpiresAt {
				return errors.New("review a fresh order; never replay an old swap")
			}
			t.State = "held"
			swapEvent(r.Swap, "queued", s.now().Unix())
		}
		return nil
	})
	if e != nil {
		return AgentView{}, e
	}
	return s.Agents(ctx, c.ProfileID, "")
}
