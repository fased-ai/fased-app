package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/automation"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

type mockHooks struct {
	mu           sync.Mutex
	state        automation.ControlState
	alerts       []automation.Notification
	approvals    []PendingApprovalBrief
	secretID     string
	resolvedLog  []string
	stateActions []string
}

func (m *mockHooks) StatusSummary(_ context.Context) (StatusSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return StatusSnapshot{
		InstanceID:       "inst-test",
		AutomationState:  m.state,
		EnabledTriggers:  3,
		UnreadAlerts:     len(m.alerts),
		PendingApprovals: len(m.approvals),
		FinancialLive:    false,
	}, nil
}

func (m *mockHooks) ListRecentAlerts(_ context.Context, limit int) ([]automation.Notification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit > 0 && limit < len(m.alerts) {
		return m.alerts[:limit], nil
	}
	return m.alerts, nil
}

func (m *mockHooks) ListPendingApprovals(_ context.Context) ([]PendingApprovalBrief, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PendingApprovalBrief, len(m.approvals))
	copy(out, m.approvals)
	return out, nil
}

func (m *mockHooks) ResolveApproval(_ context.Context, id, decision, note string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.KillSwitchActive && decision == "approve" {
		return "", errors.New("kill switch is active; approvals locked")
	}
	if id == m.secretID {
		return "", errors.New("request_secret approvals are disabled over Telegram")
	}
	m.resolvedLog = append(m.resolvedLog, id+":"+decision+":"+note)
	return decision + "d " + id, nil
}

func (m *mockHooks) SetAutomationState(_ context.Context, action, reason, actor string) (automation.ControlState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stateActions = append(m.stateActions, action+":"+actor)
	switch action {
	case "pause":
		m.state.Paused = true
	case "resume":
		if m.state.KillSwitchActive {
			return automation.ControlState{}, errors.New("kill switch active; unkill first")
		}
		m.state.Paused = false
	case "kill":
		m.state.KillSwitchActive = true
		m.state.Paused = true
		m.state.KillSwitchReason = reason
	case "unkill":
		m.state.KillSwitchActive = false
		m.state.Paused = false
		m.state.KillSwitchReason = ""
	}
	return m.state, nil
}

func TestAuthorizationCommandsOffsetAndRedaction(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 24, 13, 0, 0, 0, time.UTC)

	autoSvc, err := automation.NewService(automation.Config{
		DataDir: dir,
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("automation NewService: %v", err)
	}
	defer autoSvc.Close()

	hooks := &mockHooks{
		alerts: []automation.Notification{
			{ID: "n1", Severity: "warn", Title: "Brent shock", Urgency: 0.9, Untrusted: true},
		},
		approvals: []PendingApprovalBrief{
			{ID: "ap-draft-1", ToolName: "draft_swap", Title: "Draft swap preview"},
			{ID: "ap-secret-1", ToolName: "request_secret", Title: "Enter secret"},
		},
		secretID: "ap-secret-1",
	}

	var (
		mu          sync.Mutex
		sentTexts   []string
		pollCount   int
		fakeOffsets []int64
	)
	botToken := "123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef123"

	svc, err := NewService(Config{
		Secrets: autoSvc,
		Hooks:   hooks,
		Now:     func() time.Time { return now },
		HTTPTransport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			raw, _ := io.ReadAll(r.Body)
			if strings.HasSuffix(r.URL.Path, "/getUpdates") {
				var reqBody struct {
					Offset int64 `json:"offset"`
				}
				_ = json.Unmarshal(raw, &reqBody)
				mu.Lock()
				fakeOffsets = append(fakeOffsets, reqBody.Offset)
				pollCount++
				p := pollCount
				mu.Unlock()
				if p == 1 {
					payload := `{"ok":true,"result":[
						{"update_id":10,"message":{"message_id":1,"chat":{"id":999999},"from":{"id":999999},"text":"/status"}},
						{"update_id":11,"message":{"message_id":2,"chat":{"id":777001},"from":{"id":888888},"text":"/status"}},
						{"update_id":12,"message":{"message_id":3,"chat":{"id":777001},"from":{"id":777001},"text":"/status@fased_bot"}}
					]}`
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(payload)), Header: make(http.Header)}, nil
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":[]}`)), Header: make(http.Header)}, nil
			}
			if strings.HasSuffix(r.URL.Path, "/sendMessage") {
				var msgBody struct {
					ChatID int64  `json:"chat_id"`
					Text   string `json:"text"`
				}
				_ = json.Unmarshal(raw, &msgBody)
				mu.Lock()
				sentTexts = append(sentTexts, msgBody.Text)
				mu.Unlock()
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":99}}`)), Header: make(http.Header)}, nil
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
		}),
	})
	if err != nil {
		t.Fatalf("telegram NewService: %v", err)
	}

	// Fail closed when enabling with empty allowlist.
	if _, err := svc.SaveConfig(t.Context(), ConfigInput{
		Enabled:        true,
		BotToken:       botToken,
		AllowedChatIDs: nil,
		MinUrgency:     0.7,
	}); err == nil {
		t.Fatal("expected error enabling telegram without allowedChatIds")
	}

	view, err := svc.SaveConfig(t.Context(), ConfigInput{
		Enabled:            true,
		BotToken:           botToken,
		AllowedChatIDs:     []int64{777001},
		MinUrgency:         0.70,
		NotifyOnApproval:   true,
		NotifyOnKillSwitch: true,
	})
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if !view.HasBotToken || strings.Contains(view.BotTokenMasked, botToken[:12]) {
		t.Fatalf("unexpected bot token masking: %+v", view)
	}

	// PollOnce processes update 10 (unauthorized), 11 (spoofed fromID), 12 (authorized).
	if err := svc.PollOnce(t.Context()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	mu.Lock()
	if len(sentTexts) != 1 || !strings.Contains(sentTexts[0], "Fased Status") {
		t.Fatalf("expected only authorized update_id=12 reply, got %+v", sentTexts)
	}
	mu.Unlock()

	// Reopening service preserves monotonic offset >= 13 so updates are never replayed.
	svcReopened, err := NewService(Config{
		Secrets: autoSvc,
		Hooks:   hooks,
		Now:     func() time.Time { return now },
		HTTPTransport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			raw, _ := io.ReadAll(r.Body)
			if strings.HasSuffix(r.URL.Path, "/getUpdates") {
				var reqBody struct {
					Offset int64 `json:"offset"`
				}
				_ = json.Unmarshal(raw, &reqBody)
				mu.Lock()
				fakeOffsets = append(fakeOffsets, reqBody.Offset)
				mu.Unlock()
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":[]}`)), Header: make(http.Header)}, nil
		}),
	})
	if err != nil {
		t.Fatalf("reopen telegram NewService: %v", err)
	}
	if err := svcReopened.PollOnce(t.Context()); err != nil {
		t.Fatalf("PollOnce #2: %v", err)
	}
	mu.Lock()
	lastOffset := fakeOffsets[len(fakeOffsets)-1]
	mu.Unlock()
	if lastOffset != 13 {
		t.Fatalf("expected persisted monotonic offset 13, got %d", lastOffset)
	}

	// Test bare /alerts (zero-arg), bounded /alerts N, and status inline callback routing.
	if bareAlerts, err := svc.SimulateCommand(t.Context(), 777001, "/alerts"); err != nil || !bareAlerts.Authorized || !strings.Contains(bareAlerts.Text, "Recent Alerts") {
		t.Fatalf("bare /alerts regression failed: %+v err=%v", bareAlerts, err)
	}
	if twoAlerts, err := svc.SimulateCommand(t.Context(), 777001, "/alerts 2"); err != nil || !twoAlerts.Authorized || !strings.Contains(twoAlerts.Text, "Recent Alerts") {
		t.Fatalf("/alerts 2 regression failed: %+v err=%v", twoAlerts, err)
	}
	if cbCmd, cbArgs := ParseCallbackData("status"); cbCmd != "status" || cbArgs != "" {
		t.Fatalf("ParseCallbackData(status) = (%q, %q), want (\"status\", \"\")", cbCmd, cbArgs)
	}

	// Test request_secret refusal and valid draft approval via SimulateCommand.
	secReply, err := svc.SimulateCommand(t.Context(), 777001, "/approve ap-secret-1")
	if err != nil || !strings.Contains(secReply.Text, "disabled over Telegram") {
		t.Fatalf("expected request_secret refusal, got %+v err=%v", secReply, err)
	}

	okReply, err := svc.SimulateCommand(t.Context(), 777001, "/approve ap-draft-1 looks solid")
	if err != nil || !strings.Contains(okReply.Text, "approved ap-draft-1") {
		t.Fatalf("expected draft approval success, got %+v err=%v", okReply, err)
	}

	// Kill switch blocks /approve and /resume, and /unkill requires exact CONFIRM token.
	killReply, _ := svc.SimulateCommand(t.Context(), 777001, "/kill drawdown")
	if !strings.Contains(killReply.Text, "KILL SWITCH ACTIVE") {
		t.Fatalf("expected kill activation, got %+v", killReply)
	}
	blockedApprove, _ := svc.SimulateCommand(t.Context(), 777001, "/approve ap-draft-1")
	if !strings.Contains(blockedApprove.Text, "failed:") {
		t.Fatalf("expected approve blocked under kill switch, got %+v", blockedApprove)
	}
	badUnkill, _ := svc.SimulateCommand(t.Context(), 777001, "/unkill")
	if !strings.Contains(badUnkill.Text, "CONFIRM") {
		t.Fatalf("expected /unkill CONFIRM guard, got %+v", badUnkill)
	}
	goodUnkill, _ := svc.SimulateCommand(t.Context(), 777001, "/unkill CONFIRM")
	if !strings.Contains(goodUnkill.Text, "Kill switch cleared") {
		t.Fatalf("expected /unkill CONFIRM success, got %+v", goodUnkill)
	}

	// Verify MinUrgency filtering suppresses low-urgency noise but passes high-urgency alerts.
	lowStatus := svc.SendNotification(t.Context(), automation.Notification{
		Kind:     automation.KindPrice,
		Severity: automation.SeverityInfo,
		Title:    "Routine tick",
		Urgency:  0.40,
	})
	if lowStatus != "below-urgency-threshold" {
		t.Fatalf("expected below-urgency-threshold, got %q", lowStatus)
	}
	highStatus := svc.SendNotification(t.Context(), automation.Notification{
		Kind:      automation.KindNews,
		Severity:  automation.SeverityCritical,
		Title:     "Urgent macro shock",
		Body:      "Major event",
		Urgency:   0.92,
		Untrusted: true,
	})
	if highStatus != "sent" {
		t.Fatalf("expected sent for urgent notification, got %q", highStatus)
	}

	// Verify redactToken scrubs token from HTTP client errors cleanly.
	errSvc, err := NewService(Config{
		Secrets: autoSvc,
		Now:     func() time.Time { return now },
		HTTPTransport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("Post \"" + r.URL.String() + "\": connection reset")
		}),
	})
	if err != nil {
		t.Fatalf("errSvc: %v", err)
	}
	_, sendErr := errSvc.SendTest(t.Context())
	if sendErr == nil || strings.Contains(sendErr.Error(), botToken) || !strings.Contains(sendErr.Error(), "[REDACTED]") {
		t.Fatalf("expected bot token redacted from error, got %v", sendErr)
	}
}

func TestTelegramSaveConfigValidationPreservesTokenAndPollDoesNotRevertConfig(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC)
	botToken := "987654321:XYZ_valid_secret_token_987654321"

	autoSvc, err := automation.NewService(automation.Config{
		DataDir: dir,
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("automation NewService: %v", err)
	}
	defer func() { _ = autoSvc.Close() }()

	pollStarted := make(chan struct{})
	releasePoll := make(chan struct{})
	svc, err := NewService(Config{
		Secrets: autoSvc,
		Hooks:   &mockHooks{},
		Now:     func() time.Time { return now },
		HTTPTransport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/getUpdates") {
				close(pollStarted)
				<-releasePoll
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":[]}`)), Header: make(http.Header)}, nil
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	if _, err := svc.SaveConfig(t.Context(), ConfigInput{
		Enabled:        true,
		BotToken:       botToken,
		AllowedChatIDs: []int64{777001},
		MinUrgency:     0.7,
	}); err != nil {
		t.Fatalf("SaveConfig initial: %v", err)
	}

	// Failing SaveConfig (Enabled=true without chat IDs + ClearToken=true) must NOT wipe existing token.
	if _, err := svc.SaveConfig(t.Context(), ConfigInput{
		Enabled:        true,
		ClearToken:     true,
		AllowedChatIDs: nil,
		MinUrgency:     0.7,
	}); err == nil {
		t.Fatal("expected SaveConfig error when enabling with empty AllowedChatIDs")
	}
	cfgAfterErr, err := svc.GetConfig(t.Context())
	if err != nil || !cfgAfterErr.HasBotToken {
		t.Fatalf("expected existing bot token to remain intact after validation error, got %+v err=%v", cfgAfterErr, err)
	}

	// Concurrent SaveConfig disabling bot during PollOnce must NOT be reverted when PollOnce finishes.
	pollDone := make(chan error, 1)
	go func() {
		pollDone <- svc.PollOnce(t.Context())
	}()
	<-pollStarted
	if _, err := svc.SaveConfig(t.Context(), ConfigInput{
		Enabled:        false,
		AllowedChatIDs: []int64{555002},
		MinUrgency:     0.8,
	}); err != nil {
		t.Fatalf("concurrent SaveConfig disable: %v", err)
	}
	close(releasePoll)
	if err := <-pollDone; err != nil {
		t.Fatalf("PollOnce returned unexpected error: %v", err)
	}
	finalCfg, err := svc.GetConfig(t.Context())
	if err != nil || finalCfg.Enabled || len(finalCfg.AllowedChatIDs) != 1 || finalCfg.AllowedChatIDs[0] != 555002 {
		t.Fatalf("PollOnce overwrote concurrent SaveConfig: %+v err=%v", finalCfg, err)
	}
}
