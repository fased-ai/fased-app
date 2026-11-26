package trade

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/fased-ai/fased-app/internal/marketdata"
)

var (
	ErrBotNotFound = errors.New("trade bot not found")
)

// LiveExecReceipt records a verified live execution result returned by Core's Stage 4 router.
type LiveExecReceipt struct {
	VenueOrderID string
	TxSignature  string
	FilledPrice  float64
	Immediate    bool
}

// TradeBot persists an automated FasedScript or AI Agent trading worker.
type TradeBot struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Kind           string  `json:"kind"` // fasedscript | ai_agent
	AccountID      string  `json:"accountId"`
	ExecutionMode  string  `json:"executionMode"` // paper | live
	Venue          string  `json:"venue"`
	Symbol         string  `json:"symbol"`
	Interval       string  `json:"interval"`
	ScriptID       string  `json:"scriptId,omitempty"`
	AgentID        string  `json:"agentId,omitempty"`
	MaxNotionalUSD float64 `json:"maxNotionalUsd"`
	Status         string  `json:"status"` // running | paused | stopped
	LastSignal     string  `json:"lastSignal,omitempty"`
	LastError      string  `json:"lastError,omitempty"`
	TotalOrders    int     `json:"totalOrders"`
	LastTickAt     int64   `json:"lastTickAt,omitempty"`
	LastBarTime    int64   `json:"lastBarTime,omitempty"`
	CreatedAt      int64   `json:"createdAt"`
	UpdatedAt      int64   `json:"updatedAt,omitempty"`
}

// CreateBotRequest validates creation of a persistent bot in trade.sqlite.
type CreateBotRequest struct {
	Name           string  `json:"name"`
	Kind           string  `json:"kind"`
	AccountID      string  `json:"accountId"`
	ExecutionMode  string  `json:"executionMode,omitempty"`
	Venue          string  `json:"venue"`
	Symbol         string  `json:"symbol"`
	Interval       string  `json:"interval"`
	ScriptID       string  `json:"scriptId,omitempty"`
	AgentID        string  `json:"agentId,omitempty"`
	MaxNotionalUSD float64 `json:"maxNotionalUsd"`
}

// BotEvaluateSummary reports outcomes of an EvaluateBots pass.
type BotEvaluateSummary struct {
	Evaluated int  `json:"evaluated"`
	Submitted int  `json:"submitted"`
	Paused    int  `json:"paused"`
	Errors    int  `json:"errors"`
	Halted    bool `json:"halted,omitempty"`
}

func newBotID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "bot-" + hex.EncodeToString(b[:]), nil
}

func (s *store) saveBot(ctx context.Context, bot TradeBot) error {
	raw, err := json.Marshal(bot)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return errors.New("trade store closed")
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO trade_bots (id, kind, status, payload_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	kind = excluded.kind,
	status = excluded.status,
	payload_json = excluded.payload_json,
	updated_at = excluded.updated_at
`, bot.ID, bot.Kind, bot.Status, string(raw), bot.CreatedAt, bot.UpdatedAt)
	return err
}

func (s *store) getBot(ctx context.Context, id string) (TradeBot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return TradeBot{}, errors.New("trade store closed")
	}
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM trade_bots WHERE id = ?`, strings.TrimSpace(id)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return TradeBot{}, ErrBotNotFound
	}
	if err != nil {
		return TradeBot{}, err
	}
	var b TradeBot
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		return TradeBot{}, err
	}
	return b, nil
}

func (s *store) listBots(ctx context.Context, status string, limit int) ([]TradeBot, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return nil, errors.New("trade store closed")
	}
	var (
		rows *sql.Rows
		err  error
	)
	if strings.TrimSpace(status) != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT payload_json FROM trade_bots WHERE status = ? ORDER BY updated_at DESC, id ASC LIMIT ?`,
			strings.TrimSpace(status), limit,
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT payload_json FROM trade_bots ORDER BY updated_at DESC, id ASC LIMIT ?`,
			limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TradeBot, 0)
	for rows.Next() {
		var raw string
		if scanErr := rows.Scan(&raw); scanErr != nil {
			return nil, scanErr
		}
		var b TradeBot
		if uErr := json.Unmarshal([]byte(raw), &b); uErr != nil {
			continue
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *store) deleteBot(ctx context.Context, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return false, errors.New("trade store closed")
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM trade_bots WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// CreateBot registers a persistent automated FasedScript or AI Agent trading bot.
func (s *Service) CreateBot(ctx context.Context, raw CreateBotRequest) (TradeBot, error) {
	name := strings.TrimSpace(raw.Name)
	if name == "" {
		return TradeBot{}, errors.New("bot name required")
	}
	if len(name) > 80 {
		name = name[:80]
	}
	kind := strings.ToLower(strings.TrimSpace(raw.Kind))
	if kind != BotKindFasedScript && kind != BotKindAIAgent {
		return TradeBot{}, fmt.Errorf("unsupported bot kind %q", raw.Kind)
	}
	venue, err := marketdata.NormalizeVenue(raw.Venue)
	if err != nil {
		return TradeBot{}, err
	}
	symbol, err := marketdata.NormalizeSymbol(raw.Symbol)
	if err != nil {
		return TradeBot{}, err
	}
	ivRaw := strings.TrimSpace(raw.Interval)
	if ivRaw == "" {
		ivRaw = "1h"
	}
	iv, err := marketdata.NormalizeInterval(ivRaw)
	if err != nil {
		return TradeBot{}, err
	}
	accountID := strings.TrimSpace(raw.AccountID)
	if accountID == "" {
		accountID = ExecutionModePaper
	}
	execMode := strings.ToLower(strings.TrimSpace(raw.ExecutionMode))
	if execMode == "" {
		if accountID == ExecutionModePaper {
			execMode = ExecutionModePaper
		} else {
			execMode = ExecutionModeLive
		}
	}
	if execMode != ExecutionModePaper && execMode != ExecutionModeLive {
		return TradeBot{}, fmt.Errorf("unsupported executionMode %q", raw.ExecutionMode)
	}
	if !isFinitePositive(raw.MaxNotionalUSD) || raw.MaxNotionalUSD > 1_000_000 {
		return TradeBot{}, errors.New("maxNotionalUsd must be between 0 and 1,000,000")
	}
	scriptID := strings.TrimSpace(raw.ScriptID)
	agentID := strings.TrimSpace(raw.AgentID)
	if kind == BotKindFasedScript && scriptID == "" {
		return TradeBot{}, errors.New("scriptId required for fasedscript bot")
	}
	if kind == BotKindAIAgent && agentID == "" {
		return TradeBot{}, errors.New("agentId required for ai_agent bot")
	}
	id, err := newBotID()
	if err != nil {
		return TradeBot{}, err
	}
	nowSec := s.now().UTC().Unix()
	bot := TradeBot{
		ID:             id,
		Name:           name,
		Kind:           kind,
		AccountID:      accountID,
		ExecutionMode:  execMode,
		Venue:          venue,
		Symbol:         symbol,
		Interval:       iv,
		ScriptID:       scriptID,
		AgentID:        agentID,
		MaxNotionalUSD: roundUSD(raw.MaxNotionalUSD),
		Status:         BotStatusPaused,
		CreatedAt:      nowSec,
		UpdatedAt:      nowSec,
	}
	if err := s.store.saveBot(ctx, bot); err != nil {
		return TradeBot{}, err
	}
	return bot, nil
}

// ListBots returns persisted trading bots ordered by latest update.
func (s *Service) ListBots(ctx context.Context) ([]TradeBot, error) {
	return s.store.listBots(ctx, "", 100)
}

// SetBotStatus transitions a bot to running ("start"), paused ("pause"), or stopped ("stop").
func (s *Service) SetBotStatus(ctx context.Context, id, action string) (TradeBot, error) {
	cleanAction := strings.ToLower(strings.TrimSpace(action))
	var nextStatus string
	switch cleanAction {
	case "start", BotStatusRunning:
		halted, ks, err := s.isKillHalted(ctx)
		if err != nil {
			return TradeBot{}, err
		}
		if halted {
			return TradeBot{}, fmt.Errorf("%w: %s", ErrKillSwitchActive, ks.Reason)
		}
		nextStatus = BotStatusRunning
	case "pause", BotStatusPaused:
		nextStatus = BotStatusPaused
	case "stop", BotStatusStopped:
		nextStatus = BotStatusStopped
	default:
		return TradeBot{}, fmt.Errorf("unsupported bot action %q", action)
	}
	bot, err := s.store.getBot(ctx, id)
	if err != nil {
		return TradeBot{}, err
	}
	bot.Status = nextStatus
	if nextStatus == BotStatusRunning {
		bot.LastError = ""
	}
	bot.UpdatedAt = s.now().UTC().Unix()
	if err := s.store.saveBot(ctx, bot); err != nil {
		return TradeBot{}, err
	}
	return bot, nil
}

// DeleteBot removes a persisted bot definition.
func (s *Service) DeleteBot(ctx context.Context, id string) (bool, error) {
	return s.store.deleteBot(ctx, id)
}

// RecordExternalPlan records an already-executed plan (for example, an approved Desk paper fill)
// into trade.sqlite so #trade displays a single unified book of orders & positions.
func (s *Service) RecordExternalPlan(ctx context.Context, plan OrderPlan) (OrderPlan, error) {
	if strings.TrimSpace(plan.ID) == "" {
		id, err := newPlanID()
		if err != nil {
			return OrderPlan{}, err
		}
		plan.ID = id
	}
	nowSec := s.now().UTC().Unix()
	if plan.CreatedAt == 0 {
		plan.CreatedAt = nowSec
	}
	if plan.UpdatedAt == 0 {
		plan.UpdatedAt = nowSec
	}
	if plan.AccountID == "" {
		plan.AccountID = ExecutionModePaper
	}
	if plan.ExecutionMode == "" {
		plan.ExecutionMode = ExecutionModePaper
	}
	if plan.Source == "" {
		plan.Source = SourceDesk
	}
	if err := s.store.savePlan(ctx, plan); err != nil {
		return OrderPlan{}, err
	}
	return plan, nil
}

// EvaluateBots ticks all running bots against latest market candles and submits qualifying
// FasedScript or AI Agent orders through the unified risk/kill-switch SubmitOrder gate.
func (s *Service) EvaluateBots(ctx context.Context) (BotEvaluateSummary, error) {
	var sum BotEvaluateSummary
	bots, err := s.store.listBots(ctx, BotStatusRunning, 100)
	if err != nil {
		return sum, err
	}
	if len(bots) == 0 {
		return sum, nil
	}

	halted, ks, err := s.isKillHalted(ctx)
	if err != nil {
		return sum, err
	}
	nowSec := s.now().UTC().Unix()
	if halted {
		sum.Halted = true
		for _, bot := range bots {
			bot.Status = BotStatusPaused
			bot.LastError = firstNonEmpty(ks.Reason, "Paused by global kill switch")
			bot.LastTickAt = nowSec
			bot.UpdatedAt = nowSec
			if s.store.saveBot(ctx, bot) == nil {
				sum.Paused++
			}
		}
		return sum, nil
	}

	for _, bot := range bots {
		sum.Evaluated++
		bot.LastTickAt = nowSec
		bot.UpdatedAt = nowSec

		var candles []marketdata.Candle
		if s.cfg.MarketReader != nil {
			c, cErr := s.readCandles(ctx, bot.Venue, bot.Symbol, bot.Interval, 120)
			if cErr != nil {
				bot.LastError = cErr.Error()
				sum.Errors++
				_ = s.store.saveBot(ctx, bot)
				continue
			}
			candles = c
		}
		if len(candles) == 0 {
			bot.LastSignal = "no_candles"
			_ = s.store.saveBot(ctx, bot)
			continue
		}
		lastCandle := candles[len(candles)-1]
		if bot.LastBarTime == lastCandle.Time && lastCandle.Time > 0 {
			_ = s.store.saveBot(ctx, bot)
			continue
		}

		var (
			sig  string
			side string
			note string
			eErr error
		)
		switch bot.Kind {
		case BotKindFasedScript:
			if s.cfg.ScriptEvaluator == nil {
				eErr = errors.New("script evaluator unavailable")
			} else {
				sig, side, note, eErr = s.cfg.ScriptEvaluator(ctx, bot, candles)
			}
		case BotKindAIAgent:
			if s.cfg.AgentEvaluator == nil {
				eErr = errors.New("ai agent evaluator unavailable")
			} else {
				sig, side, note, eErr = s.cfg.AgentEvaluator(ctx, bot, candles)
			}
		}
		if eErr != nil {
			bot.LastError = eErr.Error()
			sum.Errors++
			_ = s.store.saveBot(ctx, bot)
			continue
		}
		bot.LastSignal = firstNonEmpty(sig, "hold")
		bot.LastError = ""

		if (side == SideBuy || side == SideSell) && isFinitePositive(lastCandle.Close) {
			qty := math.Round((bot.MaxNotionalUSD/lastCandle.Close)*1e6) / 1e6
			if qty > 0 {
				mode := ModeBot
				src := SourceBot
				if bot.Kind == BotKindAIAgent {
					mode = ModeAIAgent
					src = SourceAIAgent
				}
				_, oErr := s.SubmitOrder(ctx, OrderRequest{
					Venue:         bot.Venue,
					Symbol:        bot.Symbol,
					Interval:      bot.Interval,
					Mode:          mode,
					Strategy:      StrategyMarket,
					Side:          side,
					Quantity:      qty,
					EntryPrice:    lastCandle.Close,
					AccountID:     bot.AccountID,
					ExecutionMode: bot.ExecutionMode,
					Source:        src,
					BotID:         bot.ID,
					AgentID:       bot.AgentID,
					AgentNote:     note,
				})
				if oErr != nil {
					bot.LastError = oErr.Error()
					sum.Errors++
				} else {
					bot.TotalOrders++
					bot.LastBarTime = lastCandle.Time
					sum.Submitted++
				}
			}
		} else {
			bot.LastBarTime = lastCandle.Time
		}
		_ = s.store.saveBot(ctx, bot)
	}
	return sum, nil
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
