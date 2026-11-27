package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	agentV2MaxDelegationRounds = 3
	agentV2DelegationTimeout   = 45 * time.Second
)

type delegateDepthContextKey struct{}

func delegationDepth(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	if depth, ok := ctx.Value(delegateDepthContextKey{}).(int); ok {
		return depth
	}
	return 0
}

func agentV2DelegatedToolSpecs() []AgentToolSpec {
	entries := agentV2ToolRegistry()
	out := make([]AgentToolSpec, 0, len(entries))
	for _, e := range entries {
		if e.spec.Tier == toolTierRead && e.spec.Name != "delegate_to_agent" {
			out = append(out, e.spec)
		}
	}
	return out
}

func toolDelegateToAgent(ctx context.Context, s *Store, callerProfileID string, rawArgs json.RawMessage) (any, error) {
	if s == nil {
		return nil, errors.New("store unavailable")
	}
	var args struct {
		Agent   string `json:"agent"`
		Task    string `json:"task"`
		Context string `json:"context"`
	}
	if err := decodeToolArgs(rawArgs, &args); err != nil {
		return nil, err
	}

	agentQuery := strings.TrimSpace(args.Agent)
	task := strings.TrimSpace(args.Task)
	extraContext := strings.TrimSpace(args.Context)
	if agentQuery == "" || utf8.RuneCountInString(agentQuery) > 80 {
		return nil, errors.New("delegate_to_agent requires a target agent id or name")
	}
	if task == "" || utf8.RuneCountInString(task) > 2000 {
		return nil, errors.New("delegate_to_agent task must be 1-2000 characters")
	}
	if utf8.RuneCountInString(extraContext) > 2000 {
		return nil, errors.New("delegate_to_agent context exceeds 2000 characters")
	}

	depth := delegationDepth(ctx)
	if depth >= 1 {
		return nil, errors.New("nested agent delegation is not allowed")
	}

	d, _, err := readAgents(ctx, s.db)
	if err != nil {
		return nil, err
	}
	var targetProfile *AgentProfile
	avail := make([]string, 0, len(d.Profiles))
	for i := range d.Profiles {
		p := d.Profiles[i]
		avail = append(avail, p.ID)
		if strings.EqualFold(p.ID, agentQuery) || strings.EqualFold(strings.TrimSpace(p.Name), agentQuery) {
			copyP := p
			targetProfile = &copyP
			break
		}
	}
	if targetProfile == nil {
		return nil, fmt.Errorf("unknown target agent %q; available: %s", agentQuery, strings.Join(avail, ", "))
	}
	if targetProfile.ID == callerProfileID {
		return nil, errors.New("cannot delegate to the same agent profile; select another agent")
	}

	targetProvider, err := resolveAgentV2Provider(ctx, s, "", "", "", "", targetProfile.ID)
	if err != nil {
		return nil, err
	}

	childCtx, cancel := context.WithTimeout(ctx, agentV2DelegationTimeout)
	defer cancel()
	childCtx = context.WithValue(childCtx, delegateDepthContextKey{}, depth+1)

	instructions := buildAgentV2Instructions(childCtx, s, targetProfile.ID, "", task)
	instructions += "\n\nDelegation mode: you are a read-only specialist answering a delegated brief from another workspace agent. Return a concise, factual synthesis using only read tools."

	userPrompt := task
	if extraContext != "" {
		userPrompt = "Task: " + task + "\n\nContext: " + extraContext
	}

	msgs := []AgentV2Message{
		{
			Role:    "user",
			Content: userPrompt,
			Status:  "completed",
		},
	}
	delegatedSpecs := agentV2DelegatedToolSpecs()

	var (
		finalText   string
		toolsUsed   []string
		seenTools   = map[string]bool{}
		subTainted  = IsContextTainted(ctx)
		totalIn     int64
		totalOut    int64
		totalCached int64
	)

	for round := 0; round < agentV2MaxDelegationRounds; round++ {
		if childCtx.Err() != nil {
			return nil, childCtx.Err()
		}
		turn, streamErr := streamProviderV2(childCtx, s, targetProvider, providerV2Prompt{
			Instructions: instructions,
			Messages:     msgs,
			Tools:        delegatedSpecs,
		}, providerStreamCallbacks{})
		if streamErr != nil {
			return nil, streamErr
		}
		totalIn += turn.InputTokens
		totalOut += turn.OutputTokens
		totalCached += turn.CachedTokens

		assistantMsg := AgentV2Message{
			Role:      "assistant",
			Content:   turn.Text,
			Reasoning: turn.Reasoning,
			Status:    "completed",
			Model:     targetProvider.Model,
			Provider:  targetProvider.Provider,
		}
		if len(turn.ToolCalls) == 0 {
			finalText = turn.Text
			break
		}

		toolMsgs := make([]AgentV2Message, 0, len(turn.ToolCalls))
		for idx, call := range turn.ToolCalls {
			if idx >= agentV2MaxToolCallsPerRound {
				break
			}
			callID := strings.TrimSpace(call.CallID)
			if callID == "" {
				callID = fmt.Sprintf("delegate_%d_%d", round+1, idx+1)
			}
			argsJSON := strings.TrimSpace(call.ArgsJSON)
			if argsJSON == "" {
				argsJSON = "{}"
			}
			entry, known := lookupAgentV2Tool(call.Name)
			tc := AgentToolCall{
				CallID:   callID,
				ToolName: call.Name,
				ArgsJSON: argsJSON,
			}
			if !known || entry.spec.Tier != toolTierRead || call.Name == "delegate_to_agent" {
				tc.Tier = toolTierRead
				tc.Status = "error"
				tc.ErrorText = fmt.Sprintf("tool %q is not permitted in read-only delegation mode", call.Name)
				tc.ResultJSON = encodeToolResultJSON(map[string]any{"status": "error", "error": tc.ErrorText})
			} else if subTainted && isEgressToolBlockedWhenTainted(call.Name) {
				tc.Tier = entry.spec.Tier
				tc.Status = "error"
				tc.ErrorText = fmt.Sprintf("tool %q blocked in tainted delegation context", call.Name)
				tc.ResultJSON = encodeToolResultJSON(map[string]any{"status": "blocked", "error": tc.ErrorText})
			} else {
				tc.Tier = entry.spec.Tier
				if !seenTools[call.Name] {
					seenTools[call.Name] = true
					toolsUsed = append(toolsUsed, call.Name)
				}
				toolCtx, toolCancel := context.WithTimeout(WithTaintState(childCtx, subTainted), agentV2PerToolTimeout)
				res, execErr := entry.execute(toolCtx, s, targetProfile.ID, json.RawMessage(argsJSON))
				toolCancel()
				if isUntrustedExternalTool(call.Name) {
					subTainted = true
				}
				if execErr != nil {
					tc.Status = "error"
					tc.ErrorText = execErr.Error()
					tc.ResultJSON = encodeToolResultJSON(map[string]any{"status": "error", "error": execErr.Error()})
				} else {
					tc.Status = "completed"
					tc.ResultJSON = encodeToolResultJSON(res)
				}
			}
			assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, tc)
			toolContent := tc.ResultJSON
			if isUntrustedExternalTool(call.Name) && tc.Status == "completed" {
				toolContent = wrapUntrustedContextBlock("untrusted_tool_output", tc.ResultJSON)
			}
			toolMsgs = append(toolMsgs, AgentV2Message{
				Role:     "tool",
				Content:  toolContent,
				CallID:   tc.CallID,
				ToolName: tc.ToolName,
				Status:   tc.Status,
			})
		}
		msgs = append(msgs, assistantMsg)
		msgs = append(msgs, toolMsgs...)
	}

	if strings.TrimSpace(finalText) == "" {
		finalText = "Completed read-only delegation tools without a final summary."
	}
	if toolsUsed == nil {
		toolsUsed = []string{}
	}

	return map[string]any{
		"agentId":      targetProfile.ID,
		"agentName":    targetProfile.Name,
		"provider":     targetProvider.Provider,
		"model":        targetProvider.Model,
		"summary":      strings.TrimSpace(finalText),
		"toolsUsed":    toolsUsed,
		"inputTokens":  totalIn,
		"outputTokens": totalOut,
		"cachedTokens": totalCached,
	}, nil
}
