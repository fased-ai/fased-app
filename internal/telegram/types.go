package telegram

import (
	"context"
	"regexp"

	"github.com/fased-ai/fased-app/internal/automation"
)

const (
	TelegramAPIBase = "https://api.telegram.org"

	maxAllowedChats     = 8
	maxDeliveryReceipts = 25
	maxReplyBytes       = 3500
	defaultMinUrgency   = 0.70

	telegramConfigKey = "telegram_config"
	telegramTokenKey  = "telegram_token"
	telegramOffsetKey = "telegram_offset"
)

var botTokenPattern = regexp.MustCompile(`^[0-9]{5,16}:[A-Za-z0-9_-]{20,96}$`)

// ConfigRecord stores persistent non-secret Telegram controller settings.
type ConfigRecord struct {
	Enabled            bool    `json:"enabled"`
	AllowedChatIDs     []int64 `json:"allowedChatIds"`
	MinUrgency         float64 `json:"minUrgency"`
	NotifyOnApproval   bool    `json:"notifyOnApproval"`
	NotifyOnKillSwitch bool    `json:"notifyOnKillSwitch"`
	LastPollAt         int64   `json:"lastPollAt,omitempty"`
	LastError          string  `json:"lastError,omitempty"`
}

// ConfigInput updates Telegram bot token and controller settings safely.
type ConfigInput struct {
	Enabled            bool    `json:"enabled"`
	BotToken           string  `json:"botToken,omitempty"`
	ClearToken         bool    `json:"clearToken,omitempty"`
	AllowedChatIDs     []int64 `json:"allowedChatIds"`
	MinUrgency         float64 `json:"minUrgency"`
	NotifyOnApproval   bool    `json:"notifyOnApproval"`
	NotifyOnKillSwitch bool    `json:"notifyOnKillSwitch"`
}

// DeliveryReceipt captures a recent outbound or simulated Telegram message attempt.
type DeliveryReceipt struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	ChatID    int64  `json:"chatId"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"createdAt"`
}

// ConfigView exposes masked Telegram configuration and recent delivery diagnostics.
type ConfigView struct {
	Enabled            bool              `json:"enabled"`
	HasBotToken        bool              `json:"hasBotToken"`
	BotTokenMasked     string            `json:"botTokenMasked,omitempty"`
	AllowedChatIDs     []int64           `json:"allowedChatIds"`
	MinUrgency         float64           `json:"minUrgency"`
	NotifyOnApproval   bool              `json:"notifyOnApproval"`
	NotifyOnKillSwitch bool              `json:"notifyOnKillSwitch"`
	PollingActive      bool              `json:"pollingActive"`
	LastPollAt         int64             `json:"lastPollAt,omitempty"`
	LastError          string            `json:"lastError,omitempty"`
	RecentDeliveries   []DeliveryReceipt `json:"recentDeliveries"`
}

// InlineKeyboardButton represents a single Telegram inline button callback.
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
}

// InlineKeyboardMarkup defines Telegram inline keyboard rows.
type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard,omitempty"`
}

// Chat represents a Telegram conversation header.
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type,omitempty"`
}

// User represents a Telegram sender.
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username,omitempty"`
}

// Message represents an incoming Telegram message update.
type Message struct {
	MessageID int64  `json:"message_id"`
	Chat      Chat   `json:"chat"`
	From      *User  `json:"from,omitempty"`
	Text      string `json:"text,omitempty"`
}

// CallbackQuery represents a Telegram inline button press.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

// Update mirrors a Telegram Bot API update payload.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

// PendingApprovalBrief summarizes a queued Agent V2 approval without sensitive payload fields.
type PendingApprovalBrief struct {
	ID       string `json:"id"`
	ToolName string `json:"toolName"`
	Title    string `json:"title"`
	Summary  string `json:"summary"`
}

// StatusSnapshot reports live automation and workspace status for /status.
type StatusSnapshot struct {
	InstanceID       string                  `json:"instanceId"`
	AutomationState  automation.ControlState `json:"automationState"`
	EnabledTriggers  int                     `json:"enabledTriggers"`
	UnreadAlerts     int                     `json:"unreadAlerts"`
	PendingApprovals int                     `json:"pendingApprovals"`
	FinancialLive    bool                    `json:"financialLive"`
}

// CommandReply describes the authorization outcome and formatted reply for a Telegram command.
type CommandReply struct {
	Authorized bool                     `json:"authorized"`
	Command    string                   `json:"command"`
	Text       string                   `json:"text"`
	Buttons    [][]InlineKeyboardButton `json:"buttons,omitempty"`
}

// ControllerHooks decouples Telegram command routing from Core storage and approval orchestration.
type ControllerHooks interface {
	StatusSummary(ctx context.Context) (StatusSnapshot, error)
	ListRecentAlerts(ctx context.Context, limit int) ([]automation.Notification, error)
	ListPendingApprovals(ctx context.Context) ([]PendingApprovalBrief, error)
	ResolveApproval(ctx context.Context, id, decision, note string) (string, error)
	SetAutomationState(ctx context.Context, action, reason, actor string) (automation.ControlState, error)
}

// BriefHooks is optionally implemented by ControllerHooks providers to support /brief and /positions.
type BriefHooks interface {
	BriefSummary(ctx context.Context) (string, error)
	PositionsSummary(ctx context.Context) (string, error)
}
