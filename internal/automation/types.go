package automation

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"

	"github.com/fased-ai/fased-app/internal/marketdata"
	"github.com/fased-ai/fased-app/internal/scheduler"
	"github.com/fased-ai/fased-app/internal/worldintel"
)

const (
	KindPrice       = "price"
	KindIndicator   = "indicator"
	KindNews        = "news"
	KindPrediction  = "prediction"
	KindCron        = "cron"
	KindFasedScript = "fasedscript"

	CompAbove        = "above"
	CompBelow        = "below"
	CompCrossAbove   = "cross_above"
	CompCrossBelow   = "cross_below"
	CompPctChangeGTE = "pct_change_gte"

	IndRSI14       = "rsi_14"
	IndEMACross    = "ema_cross"
	IndVWAPCross   = "vwap_cross"
	IndVolumeSpike = "volume_spike"
	IndCVDDelta    = "cvd_delta"

	ActionNotify      = "notify"
	ActionJevTriage   = "jev_triage"
	ActionAgentPrompt = "agent_prompt"
	ActionPaperAlert  = "paper_alert"
	ActionDeskCycle   = "desk_cycle"

	SeverityInfo     = "info"
	SeverityWarn     = "warn"
	SeverityCritical = "critical"

	StateActionPause  = "pause"
	StateActionResume = "resume"
	StateActionKill   = "kill"
	StateActionUnkill = "unkill"

	defaultCooldownSeconds   = 300
	minCooldownSeconds       = 10
	maxCooldownSeconds       = 86400
	maxTriggers              = 64
	maxNotificationsRetained = 1000
	maxWebhookResponseBytes  = 64 << 10
)

var (
	safeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

	validKinds = map[string]bool{
		KindPrice:       true,
		KindIndicator:   true,
		KindNews:        true,
		KindPrediction:  true,
		KindCron:        true,
		KindFasedScript: true,
	}

	validComparators = map[string]bool{
		CompAbove:        true,
		CompBelow:        true,
		CompCrossAbove:   true,
		CompCrossBelow:   true,
		CompPctChangeGTE: true,
	}

	validIndicators = map[string]bool{
		IndRSI14:       true,
		IndEMACross:    true,
		IndVWAPCross:   true,
		IndVolumeSpike: true,
		IndCVDDelta:    true,
	}

	validActions = map[string]bool{
		ActionNotify:      true,
		ActionJevTriage:   true,
		ActionAgentPrompt: true,
		ActionPaperAlert:  true,
		ActionDeskCycle:   true,
	}

	validSeverities = map[string]bool{
		SeverityInfo:     true,
		SeverityWarn:     true,
		SeverityCritical: true,
	}
)

// SecretStore exposes isolated AES-256-GCM encrypted key-value persistence shared
// with Stage 2 notification subsystems such as internal/telegram.
type SecretStore interface {
	GetSealedValue(ctx context.Context, key string) (string, bool, error)
	SetSealedValue(ctx context.Context, key, plaintext string) error
	DeleteSealedValue(ctx context.Context, key string) error
	GetJSONValue(ctx context.Context, key string, dst any) (bool, error)
	SetJSONValue(ctx context.Context, key string, src any) error
}

// TriggerCondition parameterizes when a rule evaluates to matched.
type TriggerCondition struct {
	Venue        string  `json:"venue,omitempty"`
	Symbol       string  `json:"symbol,omitempty"`
	Interval     string  `json:"interval,omitempty"`
	Indicator    string  `json:"indicator,omitempty"`
	ScriptID     string  `json:"scriptId,omitempty"`
	Comparator   string  `json:"comparator,omitempty"`
	Threshold    float64 `json:"threshold,omitempty"`
	Category     string  `json:"category,omitempty"`
	Region       string  `json:"region,omitempty"`
	Ticker       string  `json:"ticker,omitempty"`
	Query        string  `json:"query,omitempty"`
	MinUrgency   float64 `json:"minUrgency,omitempty"`
	Sentiment    string  `json:"sentiment,omitempty"`
	MinSpreadPct float64 `json:"minSpreadPct,omitempty"`
	CronExpr     string  `json:"cronExpr,omitempty"`
}

// TriggerAction defines the read-only/paper response when a trigger fires.
type TriggerAction struct {
	Type           string `json:"type"`
	PromptTemplate string `json:"promptTemplate,omitempty"`
	DeskMode       string `json:"deskMode,omitempty"`
	ScriptID       string `json:"scriptId,omitempty"`
	SendTelegram   bool   `json:"sendTelegram,omitempty"`
	SendWebhook    bool   `json:"sendWebhook,omitempty"`
	Severity       string `json:"severity,omitempty"`
}

// Trigger describes a persistent event-driven automation rule.
type Trigger struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Kind            string           `json:"kind"`
	Enabled         bool             `json:"enabled"`
	CooldownSeconds int              `json:"cooldownSeconds"`
	Condition       TriggerCondition `json:"condition"`
	Action          TriggerAction    `json:"action"`
	LastEvaluatedAt int64            `json:"lastEvaluatedAt,omitempty"`
	LastFiredAt     int64            `json:"lastFiredAt,omitempty"`
	LastValue       float64          `json:"lastValue,omitempty"`
	LastFingerprint string           `json:"lastFingerprint,omitempty"`
	FireCount       int              `json:"fireCount"`
	CreatedAt       int64            `json:"createdAt"`
	UpdatedAt       int64            `json:"updatedAt"`
}

// TriggerInput creates or updates a Trigger cleanly.
type TriggerInput struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Kind            string           `json:"kind"`
	Enabled         *bool            `json:"enabled,omitempty"`
	CooldownSeconds int              `json:"cooldownSeconds,omitempty"`
	Condition       TriggerCondition `json:"condition"`
	Action          TriggerAction    `json:"action"`
}

// Notification captures a persisted automation alert or system notice.
type Notification struct {
	ID             string  `json:"id"`
	TriggerID      string  `json:"triggerId,omitempty"`
	TriggerName    string  `json:"triggerName,omitempty"`
	Kind           string  `json:"kind"`
	Severity       string  `json:"severity"`
	Title          string  `json:"title"`
	Body           string  `json:"body"`
	Ticker         string  `json:"ticker,omitempty"`
	Venue          string  `json:"venue,omitempty"`
	Urgency        float64 `json:"urgency"`
	Read           bool    `json:"read"`
	Untrusted      bool    `json:"untrusted"`
	MetadataJSON   string  `json:"metadataJson,omitempty"`
	DeliveryStatus string  `json:"deliveryStatus,omitempty"`
	CreatedAt      int64   `json:"createdAt"`
}

// NotificationFilter narrows notification queries.
type NotificationFilter struct {
	UnreadOnly bool
	Severity   string
	Limit      int
}

// NotificationList bundles queried notifications and live unread totals.
type NotificationList struct {
	Notifications []Notification `json:"notifications"`
	UnreadCount   int            `json:"unreadCount"`
	Total         int            `json:"total"`
}

// ControlState tracks persistent safety pause & kill-switch state.
type ControlState struct {
	Paused           bool   `json:"paused"`
	KillSwitchActive bool   `json:"killSwitchActive"`
	KillSwitchReason string `json:"killSwitchReason,omitempty"`
	UpdatedAt        int64  `json:"updatedAt"`
	UpdatedBy        string `json:"updatedBy,omitempty"`
}

// WebhookConfig stores non-secret outbound HTTPS webhook configuration.
type WebhookConfig struct {
	Enabled      bool   `json:"enabled"`
	Endpoint     string `json:"endpoint"`
	HasSecret    bool   `json:"hasSecret"`
	SecretMasked string `json:"secretMasked,omitempty"`
	LastStatus   string `json:"lastStatus,omitempty"`
	LastError    string `json:"lastError,omitempty"`
	LastSentAt   int64  `json:"lastSentAt,omitempty"`
}

// WebhookInput updates webhook delivery settings without exposing existing secrets.
type WebhookInput struct {
	Enabled     bool   `json:"enabled"`
	Endpoint    string `json:"endpoint"`
	Secret      string `json:"secret,omitempty"`
	ClearSecret bool   `json:"clearSecret,omitempty"`
}

// EvalResult reports the outcome of evaluating one trigger rule.
type EvalResult struct {
	TriggerID    string        `json:"triggerId"`
	Matched      bool          `json:"matched"`
	Fired        bool          `json:"fired"`
	Suppressed   string        `json:"suppressed,omitempty"`
	MetricValue  float64       `json:"metricValue,omitempty"`
	Summary      string        `json:"summary"`
	Notification *Notification `json:"notification,omitempty"`
}

// SweepResult summarizes one evaluation pass across enabled triggers.
type SweepResult struct {
	Evaluated int          `json:"evaluated"`
	Fired     int          `json:"fired"`
	Skipped   string       `json:"skipped,omitempty"`
	Results   []EvalResult `json:"results"`
}

// StreamEvent broadcasts automation state and notification updates over SSE.
type StreamEvent struct {
	Type         string        `json:"type"`
	Timestamp    int64         `json:"timestamp"`
	Trigger      *Trigger      `json:"trigger,omitempty"`
	Notification *Notification `json:"notification,omitempty"`
	State        *ControlState `json:"state,omitempty"`
	Command      string        `json:"command,omitempty"`
}

// ValidateSafeID validates a bounded identifier slug.
func ValidateSafeID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if !safeIDPattern.MatchString(id) {
		return "", fmt.Errorf("invalid identifier %q", raw)
	}
	return id, nil
}

// NormalizeTriggerInput validates and normalizes user-supplied trigger settings.
func NormalizeTriggerInput(in TriggerInput) (Trigger, error) {
	id, err := ValidateSafeID(in.ID)
	if err != nil {
		return Trigger{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 120 {
		return Trigger{}, errors.New("trigger name must be 1-120 characters")
	}
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	if !validKinds[kind] {
		return Trigger{}, fmt.Errorf("unsupported trigger kind %q", in.Kind)
	}

	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}

	cooldown := in.CooldownSeconds
	if cooldown == 0 {
		cooldown = defaultCooldownSeconds
	}
	if cooldown < minCooldownSeconds || cooldown > maxCooldownSeconds {
		return Trigger{}, fmt.Errorf("cooldownSeconds must be between %d and %d", minCooldownSeconds, maxCooldownSeconds)
	}

	cond := in.Condition
	if math.IsNaN(cond.Threshold) || math.IsInf(cond.Threshold, 0) {
		return Trigger{}, errors.New("threshold must be finite")
	}
	if math.IsNaN(cond.MinUrgency) || math.IsInf(cond.MinUrgency, 0) {
		return Trigger{}, errors.New("minUrgency must be finite")
	}
	if math.IsNaN(cond.MinSpreadPct) || math.IsInf(cond.MinSpreadPct, 0) {
		return Trigger{}, errors.New("minSpreadPct must be finite")
	}
	if len(cond.Query) > 240 {
		return Trigger{}, errors.New("query exceeds 240 characters")
	}
	cond.Query = strings.TrimSpace(cond.Query)

	switch kind {
	case KindPrice, KindIndicator:
		v, vErr := marketdata.NormalizeVenue(cond.Venue)
		if vErr != nil {
			return Trigger{}, vErr
		}
		sym, sErr := marketdata.NormalizeSymbol(cond.Symbol)
		if sErr != nil {
			return Trigger{}, sErr
		}
		iv := strings.TrimSpace(cond.Interval)
		if iv == "" {
			iv = "1h"
		}
		normIV, ivErr := marketdata.NormalizeInterval(iv)
		if ivErr != nil {
			return Trigger{}, ivErr
		}
		comp := strings.ToLower(strings.TrimSpace(cond.Comparator))
		if comp == "" {
			comp = CompAbove
		}
		if !validComparators[comp] {
			return Trigger{}, fmt.Errorf("unsupported comparator %q", cond.Comparator)
		}
		if kind == KindPrice && cond.Threshold <= 0 {
			return Trigger{}, errors.New("price threshold must be positive")
		}
		if kind == KindIndicator {
			ind := strings.ToLower(strings.TrimSpace(cond.Indicator))
			if !validIndicators[ind] {
				return Trigger{}, fmt.Errorf("unsupported indicator %q", cond.Indicator)
			}
			cond.Indicator = ind
		} else {
			cond.Indicator = ""
		}
		cond.Venue = v
		cond.Symbol = sym
		cond.Interval = normIV
		cond.Comparator = comp

	case KindNews:
		cond.Category = worldintel.NormalizeCategory(cond.Category)
		cond.Region = worldintel.NormalizeRegion(cond.Region)
		cond.Ticker = worldintel.NormalizeTicker(cond.Ticker)
		if cond.MinUrgency < 0 || cond.MinUrgency > 1 {
			return Trigger{}, errors.New("minUrgency must be between 0 and 1")
		}
		if cond.Sentiment != "" {
			sent := strings.ToLower(strings.TrimSpace(cond.Sentiment))
			if sent != "bullish" && sent != "bearish" && sent != "neutral" {
				return Trigger{}, fmt.Errorf("unsupported sentiment %q", cond.Sentiment)
			}
			cond.Sentiment = sent
		}

	case KindPrediction:
		cond.Category = worldintel.NormalizeCategory(cond.Category)
		if cond.MinSpreadPct < 0 || cond.MinSpreadPct > 100 {
			return Trigger{}, errors.New("minSpreadPct must be between 0 and 100")
		}
		if cond.MinSpreadPct == 0 && cond.Query == "" && cond.Category == "" {
			cond.MinSpreadPct = 5.0
		}

	case KindCron:
		expr := strings.TrimSpace(cond.CronExpr)
		if _, cErr := scheduler.ParseCron(expr); cErr != nil {
			return Trigger{}, cErr
		}
		cond.CronExpr = expr

	case KindFasedScript:
		venueRaw := strings.TrimSpace(cond.Venue)
		if venueRaw == "" {
			venueRaw = marketdata.VenueHyperliquid
		}
		v, vErr := marketdata.NormalizeVenue(venueRaw)
		if vErr != nil {
			return Trigger{}, vErr
		}
		symRaw := strings.TrimSpace(cond.Symbol)
		if symRaw == "" {
			symRaw = "BTC"
		}
		sym, sErr := marketdata.NormalizeSymbol(symRaw)
		if sErr != nil {
			return Trigger{}, sErr
		}
		ivRaw := strings.TrimSpace(cond.Interval)
		if ivRaw == "" {
			ivRaw = "1h"
		}
		normIV, ivErr := marketdata.NormalizeInterval(ivRaw)
		if ivErr != nil {
			return Trigger{}, ivErr
		}
		sid, sidErr := ValidateSafeID(cond.ScriptID)
		if sidErr != nil {
			return Trigger{}, sidErr
		}
		cond.Venue = v
		cond.Symbol = sym
		cond.Interval = normIV
		cond.ScriptID = sid
	}

	act := in.Action
	actType := strings.ToLower(strings.TrimSpace(act.Type))
	if actType == "" {
		actType = ActionNotify
	}
	if !validActions[actType] {
		return Trigger{}, fmt.Errorf("unsupported action type %q", act.Type)
	}
	if len(act.PromptTemplate) > 600 {
		return Trigger{}, errors.New("promptTemplate exceeds 600 characters")
	}
	act.Type = actType
	act.PromptTemplate = strings.TrimSpace(act.PromptTemplate)
	if actType == ActionDeskCycle {
		dm := strings.ToLower(strings.TrimSpace(act.DeskMode))
		if dm == "" {
			dm = "shadow"
		}
		if dm != "shadow" && dm != "paper" {
			return Trigger{}, fmt.Errorf("unsupported deskMode %q (shadow or paper only)", act.DeskMode)
		}
		act.DeskMode = dm
		if strings.TrimSpace(act.ScriptID) != "" {
			sid, sErr := ValidateSafeID(act.ScriptID)
			if sErr != nil {
				return Trigger{}, sErr
			}
			act.ScriptID = sid
		} else {
			act.ScriptID = ""
		}
	} else {
		act.DeskMode = ""
		act.ScriptID = ""
	}
	sev := strings.ToLower(strings.TrimSpace(act.Severity))
	if sev == "" {
		if kind == KindNews && cond.MinUrgency >= 0.75 {
			sev = SeverityWarn
		} else {
			sev = SeverityInfo
		}
	}
	if !validSeverities[sev] {
		return Trigger{}, fmt.Errorf("unsupported severity %q", act.Severity)
	}
	act.Severity = sev

	return Trigger{
		ID:              id,
		Name:            name,
		Kind:            kind,
		Enabled:         enabled,
		CooldownSeconds: cooldown,
		Condition:       cond,
		Action:          act,
	}, nil
}

// ValidateWebhookEndpoint validates a strict https:// webhook endpoint URL.
func ValidateWebhookEndpoint(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("webhook endpoint URL required")
	}
	if len(trimmed) > 512 {
		return "", errors.New("webhook endpoint exceeds 512 characters")
	}
	u, err := url.Parse(trimmed)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return "", errors.New("invalid webhook URL")
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return "", errors.New("webhook endpoint must use https://")
	}
	if u.User != nil {
		return "", errors.New("webhook URL must not embed credentials")
	}
	return u.String(), nil
}
