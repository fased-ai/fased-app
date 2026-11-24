package telegram

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fased-ai/fased-app/internal/automation"
)

// Service manages native Go Telegram Bot API polling, encrypted token storage,
// owner-allowlisted command execution, and outbound exception notifications.
type Service struct {
	secrets       automation.SecretStore
	hooks         ControllerHooks
	client        *botClient
	fixtureMode   bool
	now           func() time.Time
	mu            sync.Mutex
	configMu      sync.Mutex
	deliveries    []DeliveryReceipt
	pollingActive atomic.Bool
	lastOffset    int64
}

// Config parameterizes the Telegram Bot Service.
type Config struct {
	Secrets       automation.SecretStore
	Hooks         ControllerHooks
	FixtureMode   bool
	Now           func() time.Time
	HTTPTransport http.RoundTripper // Tests only.
}

// NewService constructs a Telegram Bot Service backed by encrypted SecretStore persistence.
func NewService(cfg Config) (*Service, error) {
	if cfg.Secrets == nil {
		return nil, errors.New("telegram secret store required")
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	svc := &Service{
		secrets:     cfg.Secrets,
		hooks:       cfg.Hooks,
		client:      newBotClient(cfg.HTTPTransport, nowFn),
		fixtureMode: cfg.FixtureMode,
		now:         nowFn,
	}
	return svc, nil
}

// SetHooks injects runtime ControllerHooks once initialized.
func (s *Service) SetHooks(hooks ControllerHooks) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks = hooks
}

func (s *Service) getHooks() ControllerHooks {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hooks
}

// ValidateBotToken validates Telegram @BotFather token shape cleanly without logging secrets.
func ValidateBotToken(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if !botTokenPattern.MatchString(trimmed) {
		return "", errors.New("invalid Telegram bot token format")
	}
	return trimmed, nil
}

func normalizeChatIDs(raw []int64) ([]int64, error) {
	if len(raw) > maxAllowedChats {
		return nil, fmt.Errorf("allowedChatIds exceeds maximum of %d", maxAllowedChats)
	}
	seen := make(map[int64]bool, len(raw))
	out := make([]int64, 0, len(raw))
	for _, id := range raw {
		if id == 0 {
			return nil, errors.New("chat ID cannot be 0")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func maskToken(tok string) string {
	trimmed := strings.TrimSpace(tok)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) <= 4 {
		return "****"
	}
	return "****" + trimmed[len(trimmed)-4:]
}

func (s *Service) loadRecord(ctx context.Context) ConfigRecord {
	var rec ConfigRecord
	found, err := s.secrets.GetJSONValue(ctx, telegramConfigKey, &rec)
	if err != nil || !found {
		return ConfigRecord{
			MinUrgency:         defaultMinUrgency,
			NotifyOnApproval:   true,
			NotifyOnKillSwitch: true,
		}
	}
	if rec.AllowedChatIDs == nil {
		rec.AllowedChatIDs = []int64{}
	}
	return rec
}

func (s *Service) saveRecord(ctx context.Context, rec ConfigRecord) error {
	return s.secrets.SetJSONValue(ctx, telegramConfigKey, rec)
}

func (s *Service) appendReceipt(r DeliveryReceipt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.ID == "" {
		r.ID = fmt.Sprintf("tg-%d-%d", r.CreatedAt, len(s.deliveries)+1)
	}
	s.deliveries = append([]DeliveryReceipt{r}, s.deliveries...)
	if len(s.deliveries) > maxDeliveryReceipts {
		s.deliveries = s.deliveries[:maxDeliveryReceipts]
	}
}

func (s *Service) listReceipts() []DeliveryReceipt {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DeliveryReceipt, len(s.deliveries))
	copy(out, s.deliveries)
	return out
}

// GetConfig returns non-secret Telegram configuration, token presence mask, and recent deliveries.
func (s *Service) GetConfig(ctx context.Context) (ConfigView, error) {
	rec := s.loadRecord(ctx)
	tok, hasTok, err := s.secrets.GetSealedValue(ctx, telegramTokenKey)
	if err != nil {
		return ConfigView{}, err
	}
	return ConfigView{
		Enabled:            rec.Enabled,
		HasBotToken:        hasTok && tok != "",
		BotTokenMasked:     maskToken(tok),
		AllowedChatIDs:     rec.AllowedChatIDs,
		MinUrgency:         rec.MinUrgency,
		NotifyOnApproval:   rec.NotifyOnApproval,
		NotifyOnKillSwitch: rec.NotifyOnKillSwitch,
		PollingActive:      s.pollingActive.Load(),
		LastPollAt:         rec.LastPollAt,
		LastError:          rec.LastError,
		RecentDeliveries:   s.listReceipts(),
	}, nil
}

// SaveConfig validates and persists Telegram settings and encrypted bot token cleanly.
func (s *Service) SaveConfig(ctx context.Context, in ConfigInput) (ConfigView, error) {
	if math.IsNaN(in.MinUrgency) || math.IsInf(in.MinUrgency, 0) || in.MinUrgency < 0 || in.MinUrgency > 1 {
		return ConfigView{}, errors.New("minUrgency must be between 0 and 1")
	}
	chats, err := normalizeChatIDs(in.AllowedChatIDs)
	if err != nil {
		return ConfigView{}, err
	}
	if in.Enabled && len(chats) == 0 {
		return ConfigView{}, errors.New("at least one allowedChatId is required before enabling Telegram bot")
	}
	trimmedToken := strings.TrimSpace(in.BotToken)
	var validatedToken string
	if !in.ClearToken && trimmedToken != "" {
		var vErr error
		validatedToken, vErr = ValidateBotToken(trimmedToken)
		if vErr != nil {
			return ConfigView{}, vErr
		}
	}

	if in.ClearToken {
		if err := s.secrets.DeleteSealedValue(ctx, telegramTokenKey); err != nil {
			return ConfigView{}, err
		}
	} else if validatedToken != "" {
		if err := s.secrets.SetSealedValue(ctx, telegramTokenKey, validatedToken); err != nil {
			return ConfigView{}, err
		}
	}

	s.configMu.Lock()
	rec := s.loadRecord(ctx)
	rec.Enabled = in.Enabled
	rec.AllowedChatIDs = chats
	rec.MinUrgency = in.MinUrgency
	rec.NotifyOnApproval = in.NotifyOnApproval
	rec.NotifyOnKillSwitch = in.NotifyOnKillSwitch
	err = s.saveRecord(ctx, rec)
	s.configMu.Unlock()
	if err != nil {
		return ConfigView{}, err
	}
	return s.GetConfig(ctx)
}

func (s *Service) recordPollOutcome(ctx context.Context, pollAt int64, lastErr string) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	rec := s.loadRecord(ctx)
	if pollAt > 0 {
		rec.LastPollAt = pollAt
	}
	rec.LastError = lastErr
	_ = s.saveRecord(ctx, rec)
}

func (s *Service) recordSendError(ctx context.Context, lastErr string) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	rec := s.loadRecord(ctx)
	rec.LastError = lastErr
	_ = s.saveRecord(ctx, rec)
}

// SendNotification dispatches an exception or high-urgency automation alert to allowed chats.
func (s *Service) SendNotification(ctx context.Context, n automation.Notification) string {
	rec := s.loadRecord(ctx)
	if !rec.Enabled || len(rec.AllowedChatIDs) == 0 {
		return ""
	}
	isKill := n.Kind == "kill_switch"
	isApproval := n.Kind == "approval"
	if isKill && !rec.NotifyOnKillSwitch {
		return "suppressed-kill-pref"
	}
	if isApproval && !rec.NotifyOnApproval {
		return "suppressed-approval-pref"
	}
	if !isKill && !isApproval && n.Urgency+1e-9 < rec.MinUrgency {
		return "below-urgency-threshold"
	}

	tok, hasTok, err := s.secrets.GetSealedValue(ctx, telegramTokenKey)
	if err != nil || !hasTok || tok == "" {
		return "no-token"
	}

	prefix := fmt.Sprintf("[%s]", strings.ToUpper(n.Severity))
	if n.Untrusted {
		prefix += " [untrusted]"
	}
	text := fmt.Sprintf("%s %s\n%s", prefix, n.Title, n.Body)

	var buttons [][]InlineKeyboardButton
	if isKill {
		buttons = [][]InlineKeyboardButton{{{Text: "Status", CallbackData: "status"}}}
	}

	nowSec := s.now().UTC().Unix()
	status := "sent"
	for _, chatID := range rec.AllowedChatIDs {
		if s.fixtureMode {
			s.appendReceipt(DeliveryReceipt{
				Kind:      n.Kind,
				ChatID:    chatID,
				Title:     n.Title,
				Status:    "fixture-sent",
				CreatedAt: nowSec,
			})
			status = "fixture-sent"
			continue
		}
		sendErr := s.client.sendMessage(ctx, tok, chatID, text, buttons)
		st := "sent"
		if sendErr != nil {
			st = "error"
			status = "error"
			s.recordSendError(ctx, redactToken(sendErr.Error(), tok))
		}
		s.appendReceipt(DeliveryReceipt{
			Kind:      n.Kind,
			ChatID:    chatID,
			Title:     n.Title,
			Status:    st,
			CreatedAt: nowSec,
		})
	}
	return status
}

// SendTest sends or simulates a Telegram test message across all configured allowedChatIds.
func (s *Service) SendTest(ctx context.Context) (ConfigView, error) {
	rec := s.loadRecord(ctx)
	if len(rec.AllowedChatIDs) == 0 {
		return ConfigView{}, errors.New("configure at least one allowed chat ID first")
	}
	tok, hasTok, err := s.secrets.GetSealedValue(ctx, telegramTokenKey)
	if err != nil || !hasTok || tok == "" {
		return ConfigView{}, errors.New("save a Telegram bot token first")
	}

	nowSec := s.now().UTC().Unix()
	for _, chatID := range rec.AllowedChatIDs {
		if s.fixtureMode {
			s.appendReceipt(DeliveryReceipt{
				Kind:      "test",
				ChatID:    chatID,
				Title:     "Fased Telegram test ping",
				Status:    "fixture-sent",
				CreatedAt: nowSec,
			})
			continue
		}
		if sendErr := s.client.sendMessage(ctx, tok, chatID, "Fased Telegram controller connected (read-only/paper mode).", nil); sendErr != nil {
			redacted := redactToken(sendErr.Error(), tok)
			s.recordSendError(ctx, redacted)
			return ConfigView{}, errors.New(redacted)
		}
		s.appendReceipt(DeliveryReceipt{
			Kind:      "test",
			ChatID:    chatID,
			Title:     "Fased Telegram test ping",
			Status:    "sent",
			CreatedAt: nowSec,
		})
	}
	return s.GetConfig(ctx)
}

// SimulateCommand executes a command against live owner allowlist and ControllerHooks without network access.
func (s *Service) SimulateCommand(ctx context.Context, chatID int64, text string) (CommandReply, error) {
	rec := s.loadRecord(ctx)
	reply := ExecuteCommand(ctx, s.getHooks(), rec.AllowedChatIDs, chatID, chatID, text, "")
	s.appendReceipt(DeliveryReceipt{
		Kind:      "command",
		ChatID:    chatID,
		Title:     "/" + reply.Command,
		Status:    boolStatus(reply.Authorized),
		CreatedAt: s.now().UTC().Unix(),
	})
	return reply, nil
}

func boolStatus(authorized bool) string {
	if authorized {
		return "authorized"
	}
	return "rejected"
}

func (s *Service) loadOffset(ctx context.Context) int64 {
	s.mu.Lock()
	mem := s.lastOffset
	s.mu.Unlock()
	if mem > 0 {
		return mem
	}
	raw, ok, err := s.secrets.GetSealedValue(ctx, telegramOffsetKey)
	if err != nil || !ok {
		return 0
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || parsed < 0 {
		return 0
	}
	s.mu.Lock()
	if parsed > s.lastOffset {
		s.lastOffset = parsed
	}
	s.mu.Unlock()
	return parsed
}

func (s *Service) saveOffset(ctx context.Context, nextOffset int64) {
	s.mu.Lock()
	if nextOffset <= s.lastOffset {
		s.mu.Unlock()
		return
	}
	s.lastOffset = nextOffset
	s.mu.Unlock()
	_ = s.secrets.SetSealedValue(ctx, telegramOffsetKey, strconv.FormatInt(nextOffset, 10))
}

// PollOnce polls pending Telegram Bot API updates and routes authorized commands/callbacks.
func (s *Service) PollOnce(ctx context.Context) error {
	s.configMu.Lock()
	rec := s.loadRecord(ctx)
	s.configMu.Unlock()
	if !rec.Enabled || len(rec.AllowedChatIDs) == 0 {
		return nil
	}
	tok, hasTok, err := s.secrets.GetSealedValue(ctx, telegramTokenKey)
	if err != nil || !hasTok || tok == "" {
		return nil
	}

	offset := s.loadOffset(ctx)
	pollTimeout := 10
	if s.fixtureMode {
		pollTimeout = 0
	}
	updates, err := s.client.getUpdates(ctx, tok, offset, pollTimeout)
	nowSec := s.now().UTC().Unix()
	if err != nil {
		redacted := redactToken(err.Error(), tok)
		s.recordPollOutcome(ctx, nowSec, redacted)
		return errors.New(redacted)
	}

	s.recordPollOutcome(ctx, nowSec, "")

	hooks := s.getHooks()
	maxNextOffset := offset
	for _, upd := range updates {
		if upd.UpdateID+1 > maxNextOffset {
			maxNextOffset = upd.UpdateID + 1
		}
		// Advance monotonic offset before side effects so crash/restart never replays.
		s.saveOffset(ctx, maxNextOffset)

		var chatID, fromID int64
		var rawText, cbData, cbID string
		if upd.CallbackQuery != nil {
			cb := upd.CallbackQuery
			cbID = cb.ID
			cbData = cb.Data
			fromID = cb.From.ID
			if cb.Message != nil {
				chatID = cb.Message.Chat.ID
			} else {
				chatID = fromID
			}
		} else if upd.Message != nil {
			chatID = upd.Message.Chat.ID
			if upd.Message.From != nil {
				fromID = upd.Message.From.ID
			}
			rawText = upd.Message.Text
		} else {
			continue
		}

		reply := ExecuteCommand(ctx, hooks, rec.AllowedChatIDs, chatID, fromID, rawText, cbData)
		s.appendReceipt(DeliveryReceipt{
			Kind:      "poll-command",
			ChatID:    chatID,
			Title:     "/" + reply.Command,
			Status:    boolStatus(reply.Authorized),
			CreatedAt: s.now().UTC().Unix(),
		})
		if cbID != "" {
			ack := "Unauthorized"
			if reply.Authorized {
				ack = "OK"
			}
			_ = s.client.answerCallbackQuery(ctx, tok, cbID, ack)
		}
		if reply.Authorized && reply.Text != "" {
			_ = s.client.sendMessage(ctx, tok, chatID, reply.Text, reply.Buttons)
		}
	}
	return nil
}

// RunPollingLoop polls Telegram updates until ctx cancels. Exits immediately in FixtureMode.
func (s *Service) RunPollingLoop(ctx context.Context) {
	if s.fixtureMode {
		return
	}
	s.pollingActive.Store(true)
	defer s.pollingActive.Store(false)

	backoff := 2 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		rec := s.loadRecord(ctx)
		if !rec.Enabled || len(rec.AllowedChatIDs) == 0 {
			timer := time.NewTimer(3 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		err := s.PollOnce(ctx)
		wait := 1 * time.Second
		if err != nil {
			wait = backoff
			if backoff < 30*time.Second {
				backoff *= 2
			}
		} else {
			backoff = 2 * time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
