package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// IsAuthorized checks fail-closed owner chat and sender identity allowlists.
func IsAuthorized(allowed []int64, chatID, fromID int64) bool {
	if len(allowed) == 0 || chatID == 0 {
		return false
	}
	inAllowed := func(id int64) bool {
		for _, okID := range allowed {
			if okID != 0 && okID == id {
				return true
			}
		}
		return false
	}
	if !inAllowed(chatID) {
		return false
	}
	if fromID != 0 && !inAllowed(fromID) {
		return false
	}
	return true
}

// ParseCommand splits a Telegram slash command (stripping optional @bot suffix) and argument string.
func ParseCommand(raw string) (cmd string, args string) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "/") {
		return "", ""
	}
	spaceIdx := strings.IndexAny(trimmed, " \t\n\r")
	head := trimmed
	if spaceIdx >= 0 {
		head = trimmed[:spaceIdx]
		args = strings.TrimSpace(trimmed[spaceIdx+1:])
	}
	head = strings.TrimPrefix(head, "/")
	if atIdx := strings.Index(head, "@"); atIdx >= 0 {
		head = head[:atIdx]
	}
	return strings.ToLower(strings.TrimSpace(head)), args
}

// ParseCallbackData parses bounded inline button callbacks into command invocations.
func ParseCallbackData(data string) (cmd string, args string) {
	trimmed := strings.TrimSpace(data)
	switch {
	case strings.HasPrefix(trimmed, "approve:"):
		return "approve", strings.TrimSpace(strings.TrimPrefix(trimmed, "approve:"))
	case strings.HasPrefix(trimmed, "reject:"):
		return "reject", strings.TrimSpace(strings.TrimPrefix(trimmed, "reject:"))
	case trimmed == "pause":
		return "pause", "Telegram inline action"
	case trimmed == "kill":
		return "kill", "Telegram inline emergency stop"
	case trimmed == "status":
		return "status", ""
	default:
		return "", ""
	}
}

// ExecuteCommand authenticates the sender and routes supported owner control commands.
func ExecuteCommand(ctx context.Context, hooks ControllerHooks, allowed []int64, chatID, fromID int64, rawText, callbackData string) CommandReply {
	var cmd, args string
	if strings.TrimSpace(callbackData) != "" {
		cmd, args = ParseCallbackData(callbackData)
	} else {
		cmd, args = ParseCommand(rawText)
	}

	if !IsAuthorized(allowed, chatID, fromID) {
		return CommandReply{
			Authorized: false,
			Command:    cmd,
			Text:       fmt.Sprintf("Unauthorized chat %d rejected.", chatID),
		}
	}
	if cmd == "" {
		return CommandReply{
			Authorized: true,
			Command:    "",
			Text:       "Send /help to list available commands.",
		}
	}
	if hooks == nil {
		return CommandReply{
			Authorized: true,
			Command:    cmd,
			Text:       "Controller unavailable.",
		}
	}

	actor := fmt.Sprintf("telegram:%d", chatID)

	switch cmd {
	case "start", "help":
		return CommandReply{
			Authorized: true,
			Command:    cmd,
			Text: strings.Join([]string{
				"Fased Telegram Controller (read-only / paper mode):",
				"/status - Automation & workspace summary",
				"/brief - Trading desk & automation brief",
				"/positions - Open paper/shadow desk positions",
				"/alerts [n] - Recent alerts",
				"/approvals - Pending Agent & Desk approvals",
				"/approve <id> [note] - Approve pending non-secret action",
				"/reject <id> [note] - Reject pending action",
				"/pause [reason] - Pause event triggers",
				"/resume - Resume event triggers",
				"/kill [reason] - Activate emergency kill switch",
				"/unkill CONFIRM - Clear kill switch",
			}, "\n"),
		}

	case "status":
		snap, err := hooks.StatusSummary(ctx)
		if err != nil {
			return CommandReply{Authorized: true, Command: cmd, Text: "Status error: " + err.Error()}
		}
		stateLabel := "ACTIVE"
		if snap.AutomationState.KillSwitchActive {
			stateLabel = "KILL_SWITCH"
		} else if snap.AutomationState.Paused {
			stateLabel = "PAUSED"
		}
		msg := fmt.Sprintf(
			"Fased Status\nState: %s\nTriggers: %d enabled\nUnread alerts: %d\nPending approvals: %d\nLive custody: %v",
			stateLabel, snap.EnabledTriggers, snap.UnreadAlerts, snap.PendingApprovals, snap.FinancialLive,
		)
		if snap.AutomationState.KillSwitchReason != "" {
			msg += "\nKill reason: " + snap.AutomationState.KillSwitchReason
		}
		return CommandReply{
			Authorized: true,
			Command:    cmd,
			Text:       msg,
			Buttons: [][]InlineKeyboardButton{{
				{Text: "Pause", CallbackData: "pause"},
				{Text: "Kill Switch", CallbackData: "kill"},
			}},
		}

	case "alerts":
		limit := 5
		if fields := strings.Fields(args); len(fields) > 0 {
			if n, err := strconv.Atoi(fields[0]); err == nil && n > 0 && n <= 15 {
				limit = n
			}
		}
		alerts, err := hooks.ListRecentAlerts(ctx, limit)
		if err != nil {
			return CommandReply{Authorized: true, Command: cmd, Text: "Alerts error: " + err.Error()}
		}
		if len(alerts) == 0 {
			return CommandReply{Authorized: true, Command: cmd, Text: "No alerts recorded."}
		}
		lines := make([]string, 0, len(alerts)+1)
		lines = append(lines, fmt.Sprintf("Recent Alerts (%d):", len(alerts)))
		for _, a := range alerts {
			tag := ""
			if a.Untrusted {
				tag = " [untrusted]"
			}
			lines = append(lines, fmt.Sprintf("- [%s] %s%s", strings.ToUpper(a.Severity), a.Title, tag))
		}
		return CommandReply{Authorized: true, Command: cmd, Text: strings.Join(lines, "\n")}

	case "approvals":
		items, err := hooks.ListPendingApprovals(ctx)
		if err != nil {
			return CommandReply{Authorized: true, Command: cmd, Text: "Approvals error: " + err.Error()}
		}
		if len(items) == 0 {
			return CommandReply{Authorized: true, Command: cmd, Text: "No pending approvals."}
		}
		lines := make([]string, 0, len(items)+1)
		lines = append(lines, fmt.Sprintf("Pending Approvals (%d):", len(items)))
		var rows [][]InlineKeyboardButton
		for i, ap := range items {
			if i >= 5 {
				break
			}
			lines = append(lines, fmt.Sprintf("- %s (%s): %s", ap.ID, ap.ToolName, ap.Title))
			if ap.ToolName != "request_secret" {
				rows = append(rows, []InlineKeyboardButton{
					{Text: "Approve " + shortID(ap.ID), CallbackData: "approve:" + ap.ID},
					{Text: "Reject " + shortID(ap.ID), CallbackData: "reject:" + ap.ID},
				})
			}
		}
		return CommandReply{Authorized: true, Command: cmd, Text: strings.Join(lines, "\n"), Buttons: rows}

	case "approve", "reject":
		parts := strings.Fields(args)
		if len(parts) == 0 {
			return CommandReply{Authorized: true, Command: cmd, Text: fmt.Sprintf("Usage: /%s <approval-id> [note]", cmd)}
		}
		id := parts[0]
		note := ""
		if len(parts) > 1 {
			note = strings.TrimSpace(strings.TrimPrefix(args, id))
		}
		msg, err := hooks.ResolveApproval(ctx, id, cmd, note)
		if err != nil {
			return CommandReply{Authorized: true, Command: cmd, Text: fmt.Sprintf("%s failed: %s", cmd, err.Error())}
		}
		return CommandReply{Authorized: true, Command: cmd, Text: msg}

	case "pause":
		st, err := hooks.SetAutomationState(ctx, "pause", args, actor)
		if err != nil {
			return CommandReply{Authorized: true, Command: cmd, Text: "Pause failed: " + err.Error()}
		}
		return CommandReply{Authorized: true, Command: cmd, Text: fmt.Sprintf("Automation paused (killSwitch=%v).", st.KillSwitchActive)}

	case "resume":
		st, err := hooks.SetAutomationState(ctx, "resume", args, actor)
		if err != nil {
			return CommandReply{Authorized: true, Command: cmd, Text: "Resume refused: " + err.Error()}
		}
		return CommandReply{Authorized: true, Command: cmd, Text: fmt.Sprintf("Automation resumed (paused=%v).", st.Paused)}

	case "kill":
		reason := args
		if reason == "" {
			reason = "Telegram emergency stop"
		}
		st, err := hooks.SetAutomationState(ctx, "kill", reason, actor)
		if err != nil {
			return CommandReply{Authorized: true, Command: cmd, Text: "Kill switch failed: " + err.Error()}
		}
		return CommandReply{Authorized: true, Command: cmd, Text: fmt.Sprintf("KILL SWITCH ACTIVE: %s", st.KillSwitchReason)}

	case "unkill":
		if strings.TrimSpace(args) != "CONFIRM" {
			return CommandReply{Authorized: true, Command: cmd, Text: "Refused: pass exact confirmation token `/unkill CONFIRM` to clear the kill switch."}
		}
		_, err := hooks.SetAutomationState(ctx, "unkill", "Telegram confirmed unkill", actor)
		if err != nil {
			return CommandReply{Authorized: true, Command: cmd, Text: "Unkill failed: " + err.Error()}
		}
		return CommandReply{Authorized: true, Command: cmd, Text: "Kill switch cleared. Automation active."}

	case "brief":
		bh, ok := hooks.(BriefHooks)
		if !ok {
			return CommandReply{Authorized: true, Command: cmd, Text: "Brief unavailable."}
		}
		text, err := bh.BriefSummary(ctx)
		if err != nil {
			return CommandReply{Authorized: true, Command: cmd, Text: "Brief error: " + err.Error()}
		}
		return CommandReply{Authorized: true, Command: cmd, Text: text}

	case "positions":
		bh, ok := hooks.(BriefHooks)
		if !ok {
			return CommandReply{Authorized: true, Command: cmd, Text: "No open desk positions."}
		}
		text, err := bh.PositionsSummary(ctx)
		if err != nil {
			return CommandReply{Authorized: true, Command: cmd, Text: "Positions error: " + err.Error()}
		}
		return CommandReply{Authorized: true, Command: cmd, Text: text}

	default:
		return CommandReply{
			Authorized: true,
			Command:    cmd,
			Text:       fmt.Sprintf("Unknown command /%s. Send /help for valid commands.", cmd),
		}
	}
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
