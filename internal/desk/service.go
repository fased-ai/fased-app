package desk

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/decision"
	"github.com/fased-ai/fased-app/internal/fasedscript"
	"github.com/fased-ai/fased-app/internal/marketdata"
	"github.com/fased-ai/fased-app/internal/quantlab"
	"github.com/fased-ai/fased-app/internal/worldintel"
)

// Config wires injected read-only dependencies and storage location for Desk Service.
type Config struct {
	DataDir            string
	FixtureMode        bool
	Now                func() time.Time
	MarketReader       func(ctx context.Context, venue, symbol, interval string, limit int) ([]marketdata.Candle, error)
	OrderBookReader    func(ctx context.Context, venue, symbol string) (marketdata.OrderBookSnapshot, error)
	IntelEvents        func(ctx context.Context, filter worldintel.EventFilter) ([]worldintel.IntelEvent, error)
	PredictionReader   func(ctx context.Context, filter worldintel.PredictionFilter) (worldintel.PredictionList, error)
	LabRunsReader      func(ctx context.Context, limit int) ([]quantlab.ValidationRunSummary, error)
	ScriptReader       func(ctx context.Context, scriptID string) (quantlab.ScriptRecord, bool, error)
	Scorer             func(ctx context.Context, req decision.EvaluateRequest) (decision.EvaluateResponse, error)
	ExternalKillActive func(ctx context.Context) bool
	OnAlert            func(ctx context.Context, severity, title, body string)
	OnApprovalTicket   func(ctx context.Context, ticket OrderTicket)
}

// Service coordinates the Multi-Seat Trading Desk, isolated Paper/Shadow engine,
// Trade Journal, Lessons memory, and unoverridable Go Kill Switch.
type Service struct {
	cfg   Config
	store *store
	bus   *Bus
	now   func() time.Time
	opMu  sync.Mutex
}

// NewService opens the isolated desk SQLite store and initializes the event bus.
func NewService(cfg Config) (*Service, error) {
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	st, err := openStore(cfg.DataDir, nowFn)
	if err != nil {
		return nil, err
	}
	return &Service{
		cfg:   cfg,
		store: st,
		bus:   NewBus(),
		now:   nowFn,
	}, nil
}

// Close closes the event bus and underlying SQLite connection.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	if s.bus != nil {
		s.bus.Close()
	}
	if s.store != nil {
		return s.store.close()
	}
	return nil
}

// Subscribe returns a live stream channel of DeskEvents and its cancel func.
func (s *Service) Subscribe() (<-chan DeskEvent, func()) {
	return s.bus.Subscribe()
}

func (s *Service) controlWithExternal(ctx context.Context) (ControlState, error) {
	st, err := s.store.getControl(ctx)
	if err != nil {
		return ControlState{}, err
	}
	if s.cfg.ExternalKillActive != nil && s.cfg.ExternalKillActive(ctx) {
		st.ExternalKillActive = true
	}
	st.EffectiveHalted = st.Paused || st.KillSwitchActive || st.ExternalKillActive
	return st, nil
}

// GetSnapshot computes a full point-in-time Multi-Seat Desk view.
func (s *Service) GetSnapshot(ctx context.Context) (DeskSnapshot, error) {
	now := s.now().UnixMilli()
	ctrl, err := s.controlWithExternal(ctx)
	if err != nil {
		return DeskSnapshot{}, err
	}
	limits, err := s.store.getLimits(ctx)
	if err != nil {
		return DeskSnapshot{}, err
	}
	acctRow, err := s.store.getAccountRow(ctx)
	if err != nil {
		return DeskSnapshot{}, err
	}
	positions, err := s.store.listPositions(ctx, "", 100)
	if err != nil {
		return DeskSnapshot{}, err
	}
	tickets, err := s.store.listTickets(ctx, "", 100)
	if err != nil {
		return DeskSnapshot{}, err
	}
	bench, err := s.store.listActiveBench(ctx, now)
	if err != nil {
		return DeskSnapshot{}, err
	}
	lessons, err := s.store.listLessons(ctx, maxLessonsRetained)
	if err != nil {
		return DeskSnapshot{}, err
	}
	decay, err := s.GetDecayReport(ctx)
	if err != nil {
		return DeskSnapshot{}, err
	}

	var openCount int
	var unrealized float64
	for _, p := range positions {
		if p.Status == "OPEN" {
			openCount++
			unrealized += p.UnrealizedPnL
		}
	}
	var pendingCount int
	for _, t := range tickets {
		if t.Status == "PENDING_PM" {
			pendingCount++
		}
	}
	dailyLossPct := 0.0
	if acctRow.DailyPnL < 0 && acctRow.InitialCapital > 0 {
		dailyLossPct = math.Round((-acctRow.DailyPnL/acctRow.InitialCapital)*10000) / 100
	}

	account := PaperAccount{
		InitialCapitalUSD: math.Round(acctRow.InitialCapital*100) / 100,
		CashUSD:           math.Round(acctRow.CashUSD*100) / 100,
		EquityUSD:         math.Round((acctRow.CashUSD+unrealized)*100) / 100,
		RealizedPnLUSD:    math.Round(acctRow.RealizedPnL*100) / 100,
		UnrealizedPnLUSD:  math.Round(unrealized*100) / 100,
		DailyPnLUSD:       math.Round(acctRow.DailyPnL*100) / 100,
		DailyLossPct:      finiteFloat(dailyLossPct),
		TotalFeesUSD:      math.Round(acctRow.TotalFees*100) / 100,
		ConsecutiveLosses: acctRow.ConsecutiveLosses,
		OpenPositions:     openCount,
		PendingTickets:    pendingCount,
		UpdatedAt:         acctRow.UpdatedAt,
	}

	return DeskSnapshot{
		Control:      ctrl,
		Limits:       limits,
		Seats:        DefaultSeatPolicies(),
		Account:      account,
		Positions:    positions,
		Tickets:      tickets,
		Bench:        bench,
		Decay:        decay,
		LessonsCount: len(lessons),
		UpdatedAt:    now,
	}, nil
}

// UpdateLimits validates and persists updated owner risk limits.
// Any attempt to switch Mode out of shadow/paper fails closed with ErrLiveExecutionDisabled.
func (s *Service) UpdateLimits(ctx context.Context, limits RiskLimits) (RiskLimits, error) {
	if err := limits.Validate(); err != nil {
		return RiskLimits{}, err
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	openPos, err := s.store.listPositions(ctx, "OPEN", 10)
	if err != nil {
		return RiskLimits{}, err
	}
	acct, err := s.store.getAccountRow(ctx)
	if err != nil {
		return RiskLimits{}, err
	}
	adjustIdle := len(openPos) == 0 && acct.RealizedPnL == 0 && acct.TotalFees == 0
	now := s.now().UnixMilli()
	if err := s.store.saveLimits(ctx, limits, adjustIdle, now); err != nil {
		return RiskLimits{}, err
	}
	s.publish("limits_updated", fmt.Sprintf("Desk mode=%s risk/trade=%.2f%% dailyMax=%.2f%%", limits.Mode, limits.RiskPerTradePct, limits.MaxDailyLossPct), limits)
	return limits, nil
}

// SetControl handles pause, resume, kill, unkill (requiring "CONFIRM"), and reset_paper.
func (s *Service) SetControl(ctx context.Context, action, confirm, reason, actor string) (ControlState, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	act := strings.ToLower(strings.TrimSpace(action))
	if actor = strings.TrimSpace(actor); actor == "" {
		actor = "owner"
	}
	now := s.now().UnixMilli()
	curr, err := s.controlWithExternal(ctx)
	if err != nil {
		return ControlState{}, err
	}

	switch act {
	case "pause":
		st, err := s.store.saveControl(ctx, true, curr.KillSwitchActive, firstNonEmpty(reason, "Paused by "+actor), actor, now)
		if err != nil {
			return ControlState{}, err
		}
		st.ExternalKillActive = curr.ExternalKillActive
		st.EffectiveHalted = true
		s.publish("control", "Desk paused by "+actor, st)
		return st, nil

	case "resume":
		if curr.KillSwitchActive || curr.ExternalKillActive {
			return ControlState{}, ErrKillSwitchActive
		}
		st, err := s.store.saveControl(ctx, false, false, firstNonEmpty(reason, "Resumed by "+actor), actor, now)
		if err != nil {
			return ControlState{}, err
		}
		s.publish("control", "Desk resumed by "+actor, st)
		return st, nil

	case "kill":
		msg := firstNonEmpty(reason, "Kill switch activated by "+actor)
		if err := s.haltPendingAndFlatten(ctx, msg, "EXIT_KILL_SWITCH", now); err != nil {
			return ControlState{}, err
		}
		st, err := s.store.saveControl(ctx, true, true, msg, actor, now)
		if err != nil {
			return ControlState{}, err
		}
		st.ExternalKillActive = curr.ExternalKillActive
		st.EffectiveHalted = true
		s.publish("kill_switch", msg, st)
		s.emitAlert(ctx, "critical", "Desk Kill Switch Activated", msg)
		return st, nil

	case "unkill":
		if strings.TrimSpace(confirm) != "CONFIRM" {
			return ControlState{}, errors.New("clearing desk kill switch requires exact confirmation string CONFIRM")
		}
		if curr.ExternalKillActive {
			return ControlState{}, errors.New("external Automation/Telegram kill switch is still active; clear it first")
		}
		st, err := s.store.saveControl(ctx, false, false, firstNonEmpty(reason, "Kill switch cleared by "+actor), actor, now)
		if err != nil {
			return ControlState{}, err
		}
		s.publish("control", "Desk kill switch cleared by "+actor, st)
		return st, nil

	case "reset_paper":
		if err := s.resetPaperAccount(ctx, now); err != nil {
			return ControlState{}, err
		}
		st, err := s.controlWithExternal(ctx)
		if err != nil {
			return ControlState{}, err
		}
		s.publish("paper_reset", "Paper trading account reset by "+actor, st)
		return st, nil

	default:
		return ControlState{}, fmt.Errorf("unsupported control action %q", action)
	}
}

// SyncExternalKill cancels pending paper tickets and flattens open paper positions when an
// external kill switch (Telegram /kill or Automation) activates.
func (s *Service) SyncExternalKill(ctx context.Context, reason, actor string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	now := s.now().UnixMilli()
	msg := firstNonEmpty(reason, "External kill switch activated by "+firstNonEmpty(actor, "system"))
	if err := s.haltPendingAndFlatten(ctx, msg, "EXIT_EXTERNAL_KILL", now); err != nil {
		return err
	}
	curr, err := s.store.getControl(ctx)
	if err == nil {
		s.publish("kill_switch", msg, ControlState{
			Paused:             curr.Paused,
			KillSwitchActive:   curr.KillSwitchActive,
			ExternalKillActive: true,
			EffectiveHalted:    true,
			Reason:             msg,
			UpdatedBy:          actor,
			UpdatedAt:          now,
		})
	}
	return nil
}

func (s *Service) haltPendingAndFlatten(ctx context.Context, reason, exitReason string, now int64) error {
	if _, err := s.store.cancelPendingTickets(ctx, reason, now); err != nil {
		return err
	}
	openPos, err := s.store.listPositions(ctx, "OPEN", 100)
	if err != nil {
		return err
	}
	for _, p := range openPos {
		if _, err := s.closePositionInternal(ctx, p, p.MarkPrice, exitReason, true, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) resetPaperAccount(ctx context.Context, now int64) error {
	limits, err := s.store.getLimits(ctx)
	if err != nil {
		return err
	}
	if err := s.haltPendingAndFlatten(ctx, "Paper account reset", "RESET_PAPER", now); err != nil {
		return err
	}
	if err := s.store.clearBench(ctx); err != nil {
		return err
	}
	return s.store.updateAccountRow(ctx, accountRow{
		InitialCapital:    limits.InitialCapitalUSD,
		CashUSD:           limits.InitialCapitalUSD,
		RealizedPnL:       0,
		DailyPnL:          0,
		DailyDay:          s.store.dayString(now),
		TotalFees:         0,
		ConsecutiveLosses: 0,
		ResetAt:           now,
		UpdatedAt:         now,
	})
}

// RunCycle orchestrates all 8 specialist seats cleanly with Go-enforced tool locks
// and strictly Paper/Shadow execution.
func (s *Service) RunCycle(ctx context.Context, req CycleRequest) (CycleResult, error) {
	limits, err := s.store.getLimits(ctx)
	if err != nil {
		return CycleResult{}, err
	}
	modeRaw := strings.TrimSpace(req.Mode)
	if modeRaw == "" {
		modeRaw = limits.Mode
	}
	mode, err := NormalizeMode(modeRaw)
	if err != nil {
		return CycleResult{}, err
	}
	venue, err := marketdata.NormalizeVenue(firstNonEmpty(req.Venue, marketdata.VenueHyperliquid))
	if err != nil {
		return CycleResult{}, err
	}
	symbol, err := marketdata.NormalizeSymbol(firstNonEmpty(req.Symbol, "BTC"))
	if err != nil {
		return CycleResult{}, err
	}
	interval, err := marketdata.NormalizeInterval(firstNonEmpty(req.Interval, "1h"))
	if err != nil {
		return CycleResult{}, err
	}
	scriptID := strings.TrimSpace(req.ScriptID)
	if scriptID == "" {
		scriptID = "ema-rsi-trend"
	} else if !safeSlugPattern.MatchString(scriptID) {
		return CycleResult{}, fmt.Errorf("invalid scriptId %q", req.ScriptID)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 120
	}
	if limit < 30 {
		limit = 30
	}
	if limit > 1000 {
		limit = 1000
	}

	nowTime := s.now()
	now := nowTime.UnixMilli()
	cycleID := newID("cyc")
	result := CycleResult{
		ID:        cycleID,
		Venue:     venue,
		Symbol:    symbol,
		Interval:  interval,
		ScriptID:  scriptID,
		Mode:      mode,
		CreatedAt: now,
	}

	ctrl, err := s.controlWithExternal(ctx)
	if err != nil {
		return CycleResult{}, err
	}
	if ctrl.KillSwitchActive || ctrl.ExternalKillActive || ctrl.Paused {
		reason := firstNonEmpty(ctrl.Reason, "Kill switch or pause active")
		status := "PAUSED"
		retErr := ErrDeskPaused
		if ctrl.KillSwitchActive || ctrl.ExternalKillActive {
			status = "KILLED"
			retErr = ErrKillSwitchActive
		}
		result.FinalStatus = status
		result.Artifacts = []SeatArtifact{
			{Seat: SeatChief, Verdict: "HALTED", Summary: reason, Timestamp: now},
			{Seat: SeatJournal, Verdict: "LOGGED", Summary: "Cycle blocked before evaluation: " + reason, Timestamp: now},
		}
		result.RiskCheck = RiskEvaluation{Approved: false, ViolatedRules: []string{status}, Summary: reason}
		_ = s.store.saveCycle(ctx, result)
		_ = s.store.appendJournal(ctx, JournalEntry{
			ID:        newID("jrn"),
			CycleID:   cycleID,
			Seat:      SeatChief,
			Venue:     venue,
			Symbol:    symbol,
			ScriptID:  scriptID,
			Mode:      mode,
			Outcome:   status,
			RiskCheck: reason,
			Reason:    reason,
			CreatedAt: now,
			UpdatedAt: now,
		})
		return result, retErr
	}

	// Seat 1: Chief of Staff
	if err := AssertSeatToolAllowed(SeatChief, "get_desk_state"); err != nil {
		return CycleResult{}, err
	}
	lessons, _ := s.store.listLessons(ctx, maxLessonsRetained)
	activeLessons := 0
	var matchedLessons []string
	for _, l := range lessons {
		if !l.Active {
			continue
		}
		activeLessons++
		if len(matchedLessons) < 3 && (l.Symbol == "" || strings.EqualFold(l.Symbol, "ALL") || l.Symbol == "*" || strings.EqualFold(l.Symbol, symbol)) {
			matchedLessons = append(matchedLessons, fmt.Sprintf("[%s] %s", l.Category, l.Rule))
		}
	}
	result.Artifacts = append(result.Artifacts, SeatArtifact{
		Seat:      SeatChief,
		Verdict:   "PASS",
		Summary:   fmt.Sprintf("Routed %s %s (%s) in %s mode with %d active desk lessons loaded.", venue, symbol, interval, mode, activeLessons),
		Evidence:  matchedLessons,
		Timestamp: now,
	})

	// Seat 2: Scout (WorldIntel catalyst, predictions & primary-source check)
	if err := AssertSeatToolAllowed(SeatScout, "get_world_intel"); err != nil {
		return CycleResult{}, err
	}
	var intelEvents []worldintel.IntelEvent
	if s.cfg.IntelEvents != nil {
		evts, evErr := s.cfg.IntelEvents(ctx, worldintel.EventFilter{
			Ticker: symbol,
			Limit:  10,
		})
		if evErr == nil {
			intelEvents = evts
		}
	}
	toxicCatalyst := ""
	unsourcedHighUrgency := false
	var scoutEvidence []string
	for _, ev := range intelEvents {
		if ev.URL != "" && len(scoutEvidence) < 3 {
			scoutEvidence = append(scoutEvidence, fmt.Sprintf("[untrusted] %s (%s, %.2f)", ev.Title, ev.URL, ev.Urgency))
		}
		if ev.Urgency >= 0.85 && (strings.EqualFold(ev.Sentiment, "bearish") || strings.EqualFold(ev.Sentiment, "negative")) {
			toxicCatalyst = fmt.Sprintf("High-urgency bearish catalyst (%s): %s", ev.SourceName, ev.Title)
		}
		if ev.Urgency >= 0.80 && (strings.TrimSpace(ev.URL) == "" || ev.PublishedAt <= 0) {
			unsourcedHighUrgency = true
		}
	}
	predMatches := 0
	if s.cfg.PredictionReader != nil {
		if preds, pErr := s.cfg.PredictionReader(ctx, worldintel.PredictionFilter{Query: symbol, Limit: 6}); pErr == nil {
			for i, mkt := range preds.Markets {
				if i >= 2 {
					break
				}
				predMatches++
				scoutEvidence = append(scoutEvidence, fmt.Sprintf("Prediction [untrusted]: %s (%.0f%% yes)", mkt.Question, mkt.YesProb*100))
			}
			for i, arb := range preds.Arbitrage {
				if i >= 2 {
					break
				}
				predMatches++
				scoutEvidence = append(scoutEvidence, fmt.Sprintf("Pred Arb [untrusted]: %s (+%.1f%% spread)", arb.Topic, arb.SpreadPct))
			}
		}
	}
	scoutSummary := "NO_SIGNAL: no recent WorldIntel events matching " + symbol + "; technical rules apply."
	scoutVerdict := "NO_SIGNAL"
	if len(intelEvents) > 0 || predMatches > 0 {
		scoutVerdict = "PASS"
		scoutSummary = fmt.Sprintf("Scanned %d untrusted catalysts & %d predictions for %s.", len(intelEvents), predMatches, symbol)
		if toxicCatalyst != "" {
			scoutSummary += " WARNING: " + toxicCatalyst
		}
	}
	result.Artifacts = append(result.Artifacts, SeatArtifact{
		Seat:      SeatScout,
		Verdict:   scoutVerdict,
		Summary:   scoutSummary,
		Evidence:  scoutEvidence,
		Timestamp: now,
	})

	// Seat 3: Chartist (Strictly causal trend/RSI/ATR structure & R:R)
	if err := AssertSeatToolAllowed(SeatChartist, "get_market_candles"); err != nil {
		return CycleResult{}, err
	}
	if s.cfg.MarketReader == nil {
		return CycleResult{}, errors.New("market data reader unavailable")
	}
	candles, err := s.cfg.MarketReader(ctx, venue, symbol, interval, limit)
	if err != nil {
		return CycleResult{}, fmt.Errorf("chartist candle fetch failed: %w", err)
	}
	if len(candles) < 25 {
		return CycleResult{}, fmt.Errorf("insufficient market candles (%d < 25) for %s %s", len(candles), venue, symbol)
	}
	last := candles[len(candles)-1]
	lastSec := normalizeCandleSeconds(last.Time)
	staleData := !s.cfg.FixtureMode && (nowTime.Unix()-lastSec) > limits.StaleDataMaxAgeSec

	setup := analyzeCandles(candles)
	chartVerdict := "PASS"
	if staleData || setup.Score < limits.MinChartScore {
		chartVerdict = "KILL"
	}
	var chartEvidence []string
	var spreadBps float64
	if s.cfg.OrderBookReader != nil {
		if ob, obErr := s.cfg.OrderBookReader(ctx, venue, symbol); obErr == nil && ob.MidPrice > 0 {
			spreadBps = (ob.Spread / ob.MidPrice) * 10000.0
			var bidTop5, askTop5 float64
			for i, lv := range ob.Bids {
				if i >= 5 {
					break
				}
				bidTop5 += lv.Size
			}
			for i, lv := range ob.Asks {
				if i >= 5 {
					break
				}
				askTop5 += lv.Size
			}
			ratio := 1.0
			if askTop5 > 0 {
				ratio = bidTop5 / askTop5
			}
			chartEvidence = append(chartEvidence, fmt.Sprintf("Orderbook: spread=%.2fbps, top5 bid/ask ratio=%.2f", spreadBps, ratio))
		}
	}

	// Seat 4: Quant (FasedScript signal evaluation + QuantLab 4-Gate linkage)
	if err := AssertSeatToolAllowed(SeatQuant, "list_lab_scripts"); err != nil {
		return CycleResult{}, err
	}
	scriptNoSignal := false
	effectiveLimits := limits
	quantSignalNote := ""
	if s.cfg.ScriptReader != nil && scriptID != "" {
		rec, found, sErr := s.cfg.ScriptReader(ctx, scriptID)
		if sErr != nil {
			return CycleResult{}, fmt.Errorf("script lookup failed: %w", sErr)
		}
		if !found {
			if strings.TrimSpace(req.ScriptID) != "" {
				return CycleResult{}, fmt.Errorf("%w: script %q", ErrNotFound, scriptID)
			}
		} else {
			prog, cErr := fasedscript.Compile(rec.Source)
			if cErr != nil {
				return CycleResult{}, fmt.Errorf("compile script %q: %w", scriptID, cErr)
			}
			ev, eErr := prog.Eval(candles, nil)
			if eErr != nil {
				return CycleResult{}, fmt.Errorf("evaluate script %q: %w", scriptID, eErr)
			}
			sigSide, _, hasRules := evaluateStrategySignal(ev.Strategy, len(candles))
			if !hasRules || sigSide == "" {
				scriptNoSignal = true
				quantSignalNote = fmt.Sprintf("Script %s emitted no active signal at latest bar.", scriptID)
			} else {
				setup = deriveScriptSetup(setup, sigSide, ev.Strategy, limits.MinRiskReward)
				quantSignalNote = fmt.Sprintf("Script %s active %s signal.", scriptID, strings.ToUpper(sigSide))
				if ev.Strategy.SizePct > 0 && ev.Strategy.SizePct < effectiveLimits.MaxPositionPct {
					effectiveLimits.MaxPositionPct = ev.Strategy.SizePct
				}
			}
		}
	}

	result.Artifacts = append(result.Artifacts, SeatArtifact{
		Seat:      SeatChartist,
		Verdict:   chartVerdict,
		Score:     setup.Score,
		Summary:   fmt.Sprintf("%s setup score=%.1f entry=%s stop=%s target=%s R:R=%.2f (ATR=%s, RSI=%.1f, EMA20/50=%+.2f%%)", strings.ToUpper(setup.Side), setup.Score, formatPriceStr(setup.EntryPrice), formatPriceStr(setup.StopPrice), formatPriceStr(setup.TargetPrice), setup.RiskReward, formatPriceStr(setup.ATR14), setup.RSI14, setup.EMASpreadPct),
		Evidence:  chartEvidence,
		Timestamp: now,
	})

	fourGatePassed := false
	expectedAvgR := 0.50
	quantSummary := fmt.Sprintf("Script %s evaluated; no prior 4-Gate PASS run recorded for %s.", scriptID, symbol)
	if quantSignalNote != "" {
		quantSummary = quantSignalNote + " " + quantSummary
	}
	if s.cfg.LabRunsReader != nil {
		runs, rErr := s.cfg.LabRunsReader(ctx, 50)
		if rErr == nil {
			for _, run := range runs {
				if run.ScriptID == scriptID && run.PromotionReady {
					if run.Symbol == "" || strings.EqualFold(run.Symbol, symbol) {
						fourGatePassed = true
						expectedAvgR = math.Max(0.25, math.Min(1.50, math.Round(finiteFloat(run.Sharpe)*0.25*100)/100))
						quantSummary = fmt.Sprintf("Script %s passed 4-Gate validation (%d/4, grade=%s, Sharpe=%.2f, expAvgR=%.2fR).", scriptID, run.GatesPassed, run.Grade, run.Sharpe, expectedAvgR)
						if quantSignalNote != "" {
							quantSummary = quantSignalNote + " " + quantSummary
						}
						break
					}
				}
			}
		}
	}
	quantVerdict := "PASS"
	if scriptNoSignal {
		quantVerdict = "NO_SIGNAL"
	} else if limits.RequireFourGatePass && !fourGatePassed {
		quantVerdict = "REJECTED"
	}
	result.Artifacts = append(result.Artifacts, SeatArtifact{
		Seat:      SeatQuant,
		Verdict:   quantVerdict,
		Score:     expectedAvgR,
		Summary:   quantSummary,
		Timestamp: now,
	})

	if scriptNoSignal {
		reason := fmt.Sprintf("No active FasedScript strategy signal for %s on %s %s.", scriptID, venue, symbol)
		result.FinalStatus = "NO_SIGNAL"
		result.RiskCheck = RiskEvaluation{Approved: false, Summary: reason}
		result.Artifacts = append(result.Artifacts,
			SeatArtifact{Seat: SeatSkeptic, Verdict: "SKIPPED", Summary: reason, Timestamp: now},
			SeatArtifact{Seat: SeatRisk, Verdict: "NO_SIGNAL", Summary: reason, Timestamp: now},
			SeatArtifact{Seat: SeatExecutor, Verdict: "NO_SIGNAL", Summary: reason, Timestamp: now},
			SeatArtifact{Seat: SeatJournal, Verdict: "NO_SIGNAL", Summary: reason, Timestamp: now},
		)
		if err := s.store.saveCycle(ctx, result); err != nil {
			return CycleResult{}, err
		}
		_ = s.store.appendJournal(ctx, JournalEntry{
			ID:        newID("jrn"),
			CycleID:   cycleID,
			Seat:      SeatQuant,
			Venue:     venue,
			Symbol:    symbol,
			ScriptID:  scriptID,
			Mode:      mode,
			Outcome:   "NO_SIGNAL",
			Score:     setup.Score,
			Reason:    reason,
			CreatedAt: now,
			UpdatedAt: now,
		})
		s.publish("cycle_completed", fmt.Sprintf("%s %s %s -> NO_SIGNAL", venue, symbol, mode), result)
		return result, nil
	}

	// Seat 5: Skeptic (Adversarial attack; CanProposeTrades is strictly false in Go)
	if err := AssertSeatToolAllowed(SeatSkeptic, "list_desk_journal"); err != nil {
		return CycleResult{}, err
	}
	benched, benchRec, err := s.store.isBenched(ctx, symbol, scriptID, now)
	if err != nil {
		return CycleResult{}, err
	}
	skepticVerdict, skepticReason := s.runSkepticReview(ctx, skepticInput{
		Symbol:               symbol,
		ScriptID:             scriptID,
		StaleData:            staleData,
		ToxicCatalyst:        toxicCatalyst,
		UnsourcedHighUrgency: unsourcedHighUrgency,
		Benched:              benched,
		BenchReason:          benchRec.Reason,
		ChartScore:           setup.Score,
		MinChartScore:        limits.MinChartScore,
		RiskReward:           setup.RiskReward,
		MinRiskReward:        limits.MinRiskReward,
		RequireFourGate:      limits.RequireFourGatePass,
		FourGatePassed:       fourGatePassed,
	})
	skepticEvidence := append([]string(nil), matchedLessons...)
	if limits.MaxSlippageBps > 0 && spreadBps > limits.MaxSlippageBps*2 {
		skepticEvidence = append(skepticEvidence, fmt.Sprintf("Wide spread warning: %.2fbps > 2x MaxSlippageBps (%.0fbps)", spreadBps, limits.MaxSlippageBps))
	}
	result.Artifacts = append(result.Artifacts, SeatArtifact{
		Seat:      SeatSkeptic,
		Verdict:   skepticVerdict,
		Summary:   skepticReason,
		Evidence:  skepticEvidence,
		Timestamp: now,
	})

	// Seat 6: Risk Officer (Immutable Go evaluation, no negotiation) & Seat 7: Executor (atomic under opMu)
	if err := AssertSeatToolAllowed(SeatRisk, "get_desk_state"); err != nil {
		return CycleResult{}, err
	}
	s.opMu.Lock()
	ctrlNow, cErr := s.controlWithExternal(ctx)
	if cErr != nil {
		s.opMu.Unlock()
		return CycleResult{}, cErr
	}
	snap, err := s.GetSnapshot(ctx)
	if err != nil {
		s.opMu.Unlock()
		return CycleResult{}, err
	}
	benchedNow, benchRecNow, bErr := s.store.isBenched(ctx, symbol, scriptID, now)
	if bErr == nil && benchedNow && !benched {
		benched = true
		skepticVerdict = "KILL"
		skepticReason = firstNonEmpty(benchRecNow.Reason, "Cooldown bench is active on "+symbol+"/"+scriptID)
	}
	riskEval := EvaluateRisk(RiskCheckInput{
		Limits:             effectiveLimits,
		Mode:               mode,
		KillSwitchActive:   ctrlNow.KillSwitchActive,
		ExternalKillActive: ctrlNow.ExternalKillActive,
		Paused:             ctrlNow.Paused,
		SkepticVerdict:     skepticVerdict,
		Benched:            benched,
		StaleData:          staleData,
		FourGatePassed:     fourGatePassed,
		EquityUSD:          snap.Account.EquityUSD,
		OpenPositions:      snap.Account.OpenPositions,
		DailyLossPct:       snap.Account.DailyLossPct,
		ConsecutiveLosses:  snap.Account.ConsecutiveLosses,
		ChartScore:         setup.Score,
		Side:               setup.Side,
		EntryPrice:         setup.EntryPrice,
		StopPrice:          setup.StopPrice,
		TargetPrice:        setup.TargetPrice,
	})
	result.RiskCheck = riskEval
	riskVerdict := "APPROVED"
	if !riskEval.Approved {
		riskVerdict = "REJECTED"
	}
	result.Artifacts = append(result.Artifacts, SeatArtifact{
		Seat:      SeatRisk,
		Verdict:   riskVerdict,
		Summary:   riskEval.Summary,
		Timestamp: now,
	})

	ticketStatus := ""
	finalReason := ""
	switch {
	case skepticVerdict != "PASS":
		ticketStatus = "KILLED_BY_SKEPTIC"
		finalReason = skepticReason
	case !riskEval.Approved:
		ticketStatus = "FAILED_RISK_CHECK"
		finalReason = riskEval.Summary
	case mode == ModeShadow:
		ticketStatus = "SHADOW"
		finalReason = "Shadow mode: ticket logged, paper account untouched"
	case limits.RequirePMTap:
		ticketStatus = "PENDING_PM"
		finalReason = "Awaiting PM approval tap to execute paper trade"
	default:
		ticketStatus = "FILLED"
		finalReason = "Auto-filled in paper mode (RequirePMTap=false)"
	}

	ticket := OrderTicket{
		ID:           newID("tkt"),
		CycleID:      cycleID,
		Venue:        venue,
		Symbol:       symbol,
		Interval:     interval,
		ScriptID:     scriptID,
		Side:         setup.Side,
		Mode:         mode,
		EntryPrice:   setup.EntryPrice,
		StopPrice:    setup.StopPrice,
		TargetPrice:  setup.TargetPrice,
		Quantity:     riskEval.ComputedQty,
		NotionalUSD:  riskEval.NotionalUSD,
		RiskUSD:      riskEval.RiskUSD,
		RiskReward:   setup.RiskReward,
		ExpectedAvgR: expectedAvgR,
		ChartScore:   setup.Score,
		Status:       ticketStatus,
		Reason:       finalReason,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// Seat 7: Executor (Sole seat permitted to open paper/shadow positions)
	if skepticVerdict == "PASS" && riskEval.Approved {
		if err := AssertSeatToolAllowed(SeatExecutor, "place_paper_order"); err != nil {
			s.opMu.Unlock()
			return CycleResult{}, err
		}
		if err := s.store.saveTicket(ctx, ticket); err != nil {
			s.opMu.Unlock()
			return CycleResult{}, err
		}
		result.Ticket = &ticket
		switch ticketStatus {
		case "SHADOW":
			result.Artifacts = append(result.Artifacts, SeatArtifact{
				Seat:      SeatExecutor,
				Verdict:   "SHADOWED",
				Summary:   fmt.Sprintf("Shadow ticket %s recorded (%s %.4f %s @ %.4f); paper balance untouched.", ticket.ID, ticket.Side, ticket.Quantity, ticket.Symbol, ticket.EntryPrice),
				Timestamp: now,
			})
		case "PENDING_PM":
			result.Artifacts = append(result.Artifacts, SeatArtifact{
				Seat:      SeatExecutor,
				Verdict:   "PENDING_PM",
				Summary:   fmt.Sprintf("Paper ticket %s queued for PM approval (%s %.4f %s @ %.4f, risk=$%.2f).", ticket.ID, ticket.Side, ticket.Quantity, ticket.Symbol, ticket.EntryPrice, ticket.RiskUSD),
				Timestamp: now,
			})
		case "FILLED":
			pos, pErr := s.openPaperPositionLocked(ctx, ticket, effectiveLimits, now)
			if pErr != nil {
				s.opMu.Unlock()
				return CycleResult{}, pErr
			}
			result.Position = &pos
			result.Artifacts = append(result.Artifacts, SeatArtifact{
				Seat:      SeatExecutor,
				Verdict:   "EXECUTED",
				Summary:   fmt.Sprintf("Paper position %s opened for %s (qty=%.4f entry=%.4f fee=$%.2f).", pos.ID, pos.Symbol, pos.Quantity, pos.EntryPrice, pos.EntryFeeUSD),
				Timestamp: now,
			})
		}
	} else {
		if err := s.store.saveTicket(ctx, ticket); err != nil {
			s.opMu.Unlock()
			return CycleResult{}, err
		}
		result.Ticket = &ticket
		result.Artifacts = append(result.Artifacts, SeatArtifact{
			Seat:      SeatExecutor,
			Verdict:   "REJECTED",
			Summary:   "Executor blocked from touching orders: " + finalReason,
			Timestamp: now,
		})
	}
	s.opMu.Unlock()
	if ticketStatus == "PENDING_PM" && s.cfg.OnApprovalTicket != nil {
		s.cfg.OnApprovalTicket(ctx, ticket)
	}

	// Seat 8: Journal (Log every outcome, including kills & shadows)
	if err := AssertSeatToolAllowed(SeatJournal, "record_journal_entry"); err != nil {
		return CycleResult{}, err
	}
	journalEntry := JournalEntry{
		ID:             newID("jrn"),
		CycleID:        cycleID,
		TicketID:       ticket.ID,
		Seat:           SeatJournal,
		Venue:          venue,
		Symbol:         symbol,
		ScriptID:       scriptID,
		Side:           setup.Side,
		Mode:           mode,
		Outcome:        ticketStatus,
		Score:          setup.Score,
		EntryPrice:     setup.EntryPrice,
		StopPrice:      setup.StopPrice,
		TargetPrice:    setup.TargetPrice,
		Quantity:       riskEval.ComputedQty,
		RiskUSD:        riskEval.RiskUSD,
		ExpectedAvgR:   expectedAvgR,
		RiskCheck:      riskEval.Summary,
		SkepticVerdict: skepticVerdict,
		Reason:         finalReason,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.store.appendJournal(ctx, journalEntry); err != nil {
		return CycleResult{}, err
	}
	result.Artifacts = append(result.Artifacts, SeatArtifact{
		Seat:      SeatJournal,
		Verdict:   "LOGGED",
		Summary:   fmt.Sprintf("Recorded Trade Journal entry %s (%s).", journalEntry.ID, journalEntry.Outcome),
		Timestamp: now,
	})

	result.FinalStatus = ticketStatus
	if err := s.store.saveCycle(ctx, result); err != nil {
		return CycleResult{}, err
	}
	s.publish("cycle_completed", fmt.Sprintf("%s %s %s -> %s", venue, symbol, mode, ticketStatus), result)
	return result, nil
}

// ApproveTicket converts a PENDING_PM paper ticket into an open PaperPosition after re-checking kill switches, slippage & risk.
func (s *Service) ApproveTicket(ctx context.Context, ticketID, actor string) (PaperPosition, error) {
	if err := AssertSeatToolAllowed(SeatExecutor, "place_paper_order"); err != nil {
		return PaperPosition{}, err
	}
	cleanID := strings.TrimSpace(ticketID)
	ticketPre, foundPre, err := s.store.getTicket(ctx, cleanID)
	if err != nil {
		return PaperPosition{}, err
	}
	if !foundPre {
		return PaperPosition{}, ErrNotFound
	}
	if ticketPre.Status != "PENDING_PM" {
		return PaperPosition{}, fmt.Errorf("ticket %s has status %s, expected PENDING_PM", ticketPre.ID, ticketPre.Status)
	}

	var latestCandles []marketdata.Candle
	if s.cfg.MarketReader != nil {
		if c, cErr := s.cfg.MarketReader(ctx, ticketPre.Venue, ticketPre.Symbol, firstNonEmpty(ticketPre.Interval, "1h"), 5); cErr == nil && len(c) > 0 {
			latestCandles = c
		}
	}
	var labRuns []quantlab.ValidationRunSummary
	if s.cfg.LabRunsReader != nil {
		if r, rErr := s.cfg.LabRunsReader(ctx, 50); rErr == nil {
			labRuns = r
		}
	}

	s.opMu.Lock()
	defer s.opMu.Unlock()

	ctrl, err := s.controlWithExternal(ctx)
	if err != nil {
		return PaperPosition{}, err
	}
	if ctrl.KillSwitchActive || ctrl.ExternalKillActive {
		return PaperPosition{}, ErrKillSwitchActive
	}
	if ctrl.Paused {
		return PaperPosition{}, ErrDeskPaused
	}

	ticket, found, err := s.store.getTicket(ctx, cleanID)
	if err != nil {
		return PaperPosition{}, err
	}
	if !found {
		return PaperPosition{}, ErrNotFound
	}
	if ticket.Status != "PENDING_PM" {
		return PaperPosition{}, fmt.Errorf("ticket %s has status %s, expected PENDING_PM", ticket.ID, ticket.Status)
	}
	mode, err := NormalizeMode(ticket.Mode)
	if err != nil {
		return PaperPosition{}, err
	}
	if mode != ModePaper {
		return PaperPosition{}, errors.New("only paper tickets can be opened into positions")
	}

	limits, err := s.store.getLimits(ctx)
	if err != nil {
		return PaperPosition{}, err
	}
	snap, err := s.GetSnapshot(ctx)
	if err != nil {
		return PaperPosition{}, err
	}
	nowTime := s.now()
	now := nowTime.UnixMilli()
	benched, _, err := s.store.isBenched(ctx, ticket.Symbol, ticket.ScriptID, now)
	if err != nil {
		return PaperPosition{}, err
	}

	fourGatePassed := false
	for _, run := range labRuns {
		if run.ScriptID == ticket.ScriptID && run.PromotionReady {
			if run.Symbol == "" || strings.EqualFold(run.Symbol, ticket.Symbol) {
				fourGatePassed = true
				break
			}
		}
	}

	fillPrice := ticket.EntryPrice
	staleData := false
	if len(latestCandles) > 0 {
		last := latestCandles[len(latestCandles)-1]
		lastSec := normalizeCandleSeconds(last.Time)
		staleData = !s.cfg.FixtureMode && (nowTime.Unix()-lastSec) > limits.StaleDataMaxAgeSec
		if ticket.EntryPrice > 0 && last.Close > 0 {
			driftBps := math.Abs(last.Close-ticket.EntryPrice) / ticket.EntryPrice * 10000.0
			if limits.MaxSlippageBps > 0 && driftBps > limits.MaxSlippageBps {
				slipSummary := fmt.Sprintf("REJECTED (MAX_SLIPPAGE_EXCEEDED: %.1f > %.1f bps)", driftBps, limits.MaxSlippageBps)
				ticket.Status = "FAILED_RISK_CHECK"
				ticket.Reason = slipSummary
				ticket.UpdatedAt = now
				_ = s.store.saveTicket(ctx, ticket)
				_ = s.store.updateJournalOutcomeByTicket(ctx, ticket.ID, "FAILED_RISK_CHECK", 0, 0, 0, slipSummary, "", now)
				return PaperPosition{}, fmt.Errorf("%w: %s", ErrRiskRejected, slipSummary)
			}
			slipRate := math.Min(limits.MaxSlippageBps, 5.0) / 10000.0
			if strings.EqualFold(ticket.Side, "short") {
				fillPrice = roundPrice(last.Close * (1.0 - slipRate))
			} else {
				fillPrice = roundPrice(last.Close * (1.0 + slipRate))
			}
		}
	}

	riskEval := EvaluateRisk(RiskCheckInput{
		Limits:             limits,
		Mode:               ModePaper,
		KillSwitchActive:   ctrl.KillSwitchActive,
		ExternalKillActive: ctrl.ExternalKillActive,
		Paused:             ctrl.Paused,
		SkepticVerdict:     "PASS",
		Benched:            benched,
		StaleData:          staleData,
		FourGatePassed:     fourGatePassed,
		EquityUSD:          snap.Account.EquityUSD,
		OpenPositions:      snap.Account.OpenPositions,
		DailyLossPct:       snap.Account.DailyLossPct,
		ConsecutiveLosses:  snap.Account.ConsecutiveLosses,
		ChartScore:         ticket.ChartScore,
		Side:               ticket.Side,
		EntryPrice:         fillPrice,
		StopPrice:          ticket.StopPrice,
		TargetPrice:        ticket.TargetPrice,
	})
	if !riskEval.Approved {
		ticket.Status = "FAILED_RISK_CHECK"
		ticket.Reason = riskEval.Summary
		ticket.UpdatedAt = now
		_ = s.store.saveTicket(ctx, ticket)
		_ = s.store.updateJournalOutcomeByTicket(ctx, ticket.ID, "FAILED_RISK_CHECK", 0, 0, 0, riskEval.Summary, "", now)
		return PaperPosition{}, fmt.Errorf("%w: %s", ErrRiskRejected, riskEval.Summary)
	}

	ticket.EntryPrice = fillPrice
	ticket.Quantity = riskEval.ComputedQty
	ticket.NotionalUSD = riskEval.NotionalUSD
	ticket.RiskUSD = riskEval.RiskUSD
	ticket.RiskReward = riskEval.RiskReward
	ticket.Status = "FILLED"
	ticket.Reason = "Approved by PM (" + firstNonEmpty(actor, "owner") + ")"
	ticket.UpdatedAt = now

	notional := ticket.Quantity * ticket.EntryPrice
	entryFee := math.Round(notional*(limits.FeeBps/10000.0)*100) / 100
	riskUSD := math.Abs(ticket.EntryPrice-ticket.StopPrice) * ticket.Quantity
	if riskUSD <= 0 {
		riskUSD = ticket.RiskUSD
	}
	pos := PaperPosition{
		ID:             newID("pos"),
		TicketID:       ticket.ID,
		Venue:          ticket.Venue,
		Symbol:         ticket.Symbol,
		ScriptID:       ticket.ScriptID,
		Side:           ticket.Side,
		Quantity:       ticket.Quantity,
		EntryPrice:     ticket.EntryPrice,
		MarkPrice:      ticket.EntryPrice,
		StopPrice:      ticket.StopPrice,
		TargetPrice:    ticket.TargetPrice,
		InitialRiskUSD: math.Round(riskUSD*100) / 100,
		EntryFeeUSD:    entryFee,
		Status:         "OPEN",
		OpenedAt:       now,
	}
	transitioned, err := s.store.approveTicketAndOpenPositionTx(ctx, ticket, pos, entryFee, now)
	if err != nil {
		return PaperPosition{}, err
	}
	if !transitioned {
		return PaperPosition{}, fmt.Errorf("ticket %s already processed", ticket.ID)
	}
	_ = s.store.updateJournalOutcomeByTicket(ctx, ticket.ID, "FILLED", 0, 0, 0, ticket.Reason, "", now)
	s.publish("ticket_approved", fmt.Sprintf("Opened paper position %s (%s %s @ %s)", pos.Symbol, pos.Side, strconv.FormatFloat(pos.Quantity, 'f', -1, 64), formatPriceStr(pos.EntryPrice)), pos)
	return pos, nil
}

// RejectTicket declines a PENDING_PM ticket and logs REJECTED_BY_PM in Trade Journal.
func (s *Service) RejectTicket(ctx context.Context, ticketID, reason, actor string) (OrderTicket, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	ticket, found, err := s.store.getTicket(ctx, strings.TrimSpace(ticketID))
	if err != nil {
		return OrderTicket{}, err
	}
	if !found {
		return OrderTicket{}, ErrNotFound
	}
	if ticket.Status != "PENDING_PM" {
		return OrderTicket{}, fmt.Errorf("ticket %s has status %s, expected PENDING_PM", ticket.ID, ticket.Status)
	}
	now := s.now().UnixMilli()
	msg := firstNonEmpty(reason, "Rejected by PM ("+firstNonEmpty(actor, "owner")+")")
	ticket.Status = "REJECTED_BY_PM"
	ticket.Reason = msg
	ticket.UpdatedAt = now
	ok, err := s.store.rejectTicketIfPending(ctx, ticket)
	if err != nil {
		return OrderTicket{}, err
	}
	if !ok {
		return OrderTicket{}, fmt.Errorf("ticket %s already processed", ticket.ID)
	}
	_ = s.store.updateJournalOutcomeByTicket(ctx, ticket.ID, "REJECTED_BY_PM", 0, 0, 0, msg, "", now)
	s.publish("ticket_rejected", fmt.Sprintf("PM rejected ticket %s (%s)", ticket.ID, ticket.Symbol), ticket)
	return ticket, nil
}

// MarkPositions refreshes unrealized PnL for open positions using latest market candles
// and conservatively closes stops before targets when reached.
func (s *Service) MarkPositions(ctx context.Context) ([]PaperPosition, error) {
	openPos, err := s.store.listPositions(ctx, "OPEN", 100)
	if err != nil {
		return nil, err
	}
	if len(openPos) == 0 || s.cfg.MarketReader == nil {
		return openPos, nil
	}

	type candleFetch struct {
		candles []marketdata.Candle
	}
	fetched := make(map[string]candleFetch, len(openPos))
	for _, p := range openPos {
		candles, cErr := s.cfg.MarketReader(ctx, p.Venue, p.Symbol, "1m", 60)
		if cErr != nil || len(candles) == 0 {
			candles, cErr = s.cfg.MarketReader(ctx, p.Venue, p.Symbol, "1h", 24)
		}
		if cErr == nil && len(candles) > 0 {
			fetched[p.ID] = candleFetch{candles: candles}
		}
	}

	updated := make([]PaperPosition, 0, len(openPos))
	type closeNotice struct {
		pos PaperPosition
	}
	var closedNotices []closeNotice

	s.opMu.Lock()
	now := s.now().UnixMilli()
	for _, item := range openPos {
		cur, found, gErr := s.store.getPosition(ctx, item.ID)
		if gErr != nil || !found || cur.Status != "OPEN" {
			continue
		}
		fc, ok := fetched[cur.ID]
		if !ok || len(fc.candles) == 0 {
			updated = append(updated, cur)
			continue
		}
		candles := fc.candles
		last := candles[len(candles)-1]
		openedSec := cur.OpenedAt / 1000

		triggerReason := ""
		triggerPx := 0.0
		sawPostEntryBar := false
		for _, c := range candles {
			if normalizeCandleSeconds(c.Time) <= openedSec {
				continue
			}
			sawPostEntryBar = true
			if cur.Side == "long" {
				if cur.StopPrice > 0 && c.Low <= cur.StopPrice {
					triggerReason = "STOP_LOSS"
					triggerPx = cur.StopPrice
					break
				}
				if cur.TargetPrice > 0 && c.High >= cur.TargetPrice {
					triggerReason = "TAKE_PROFIT"
					triggerPx = cur.TargetPrice
					break
				}
			} else {
				if cur.StopPrice > 0 && c.High >= cur.StopPrice {
					triggerReason = "STOP_LOSS"
					triggerPx = cur.StopPrice
					break
				}
				if cur.TargetPrice > 0 && c.Low <= cur.TargetPrice {
					triggerReason = "TAKE_PROFIT"
					triggerPx = cur.TargetPrice
					break
				}
			}
		}
		if triggerReason == "" && !sawPostEntryBar {
			// Avoid triggering on a pre-entry bar wick; only check live close breach conservatively.
			if cur.Side == "long" {
				if cur.StopPrice > 0 && last.Close <= cur.StopPrice {
					triggerReason = "STOP_LOSS"
					triggerPx = cur.StopPrice
				} else if cur.TargetPrice > 0 && last.Close >= cur.TargetPrice {
					triggerReason = "TAKE_PROFIT"
					triggerPx = cur.TargetPrice
				}
			} else {
				if cur.StopPrice > 0 && last.Close >= cur.StopPrice {
					triggerReason = "STOP_LOSS"
					triggerPx = cur.StopPrice
				} else if cur.TargetPrice > 0 && last.Close <= cur.TargetPrice {
					triggerReason = "TAKE_PROFIT"
					triggerPx = cur.TargetPrice
				}
			}
		}

		if triggerReason != "" {
			closed, cErr := s.closePositionInternal(ctx, cur, triggerPx, triggerReason, false, now)
			if cErr == nil {
				updated = append(updated, closed)
				closedNotices = append(closedNotices, closeNotice{pos: closed})
				continue
			}
		}

		if cur.Side == "long" {
			cur.UnrealizedPnL = math.Round((last.Close-cur.EntryPrice)*cur.Quantity*100) / 100
		} else {
			cur.UnrealizedPnL = math.Round((cur.EntryPrice-last.Close)*cur.Quantity*100) / 100
		}
		cur.MarkPrice = roundPrice(last.Close)
		_ = s.store.savePosition(ctx, cur)
		updated = append(updated, cur)
	}
	s.opMu.Unlock()

	for _, cn := range closedNotices {
		s.publish("position_closed", fmt.Sprintf("Auto-closed paper %s (%s) PnL=$%.2f (%.2fR)", cn.pos.Symbol, cn.pos.ExitReason, cn.pos.RealizedPnL, cn.pos.PnLR), cn.pos)
	}
	return updated, nil
}

// ClosePosition manually closes an OPEN paper position via SeatExecutor.
func (s *Service) ClosePosition(ctx context.Context, posID string, overridePrice float64, reason string) (PaperPosition, error) {
	if err := AssertSeatToolAllowed(SeatExecutor, "close_paper_position"); err != nil {
		return PaperPosition{}, err
	}
	cleanID := strings.TrimSpace(posID)
	prePos, foundPre, err := s.store.getPosition(ctx, cleanID)
	if err != nil {
		return PaperPosition{}, err
	}
	if !foundPre {
		return PaperPosition{}, ErrNotFound
	}
	if prePos.Status != "OPEN" {
		return PaperPosition{}, fmt.Errorf("position %s already closed (%s)", prePos.ID, prePos.Status)
	}

	fetchedClose := 0.0
	hasOverride := overridePrice > 0 && !math.IsNaN(overridePrice) && !math.IsInf(overridePrice, 0)
	if !hasOverride && s.cfg.MarketReader != nil {
		if candles, cErr := s.cfg.MarketReader(ctx, prePos.Venue, prePos.Symbol, "1h", 3); cErr == nil && len(candles) > 0 {
			fetchedClose = candles[len(candles)-1].Close
		}
	}

	s.opMu.Lock()
	p, found, err := s.store.getPosition(ctx, cleanID)
	if err != nil {
		s.opMu.Unlock()
		return PaperPosition{}, err
	}
	if !found {
		s.opMu.Unlock()
		return PaperPosition{}, ErrNotFound
	}
	if p.Status != "OPEN" {
		s.opMu.Unlock()
		return PaperPosition{}, fmt.Errorf("position %s already closed (%s)", p.ID, p.Status)
	}
	exitPx := p.MarkPrice
	if hasOverride {
		exitPx = overridePrice
	} else if fetchedClose > 0 {
		exitPx = fetchedClose
	}
	if exitPx <= 0 {
		exitPx = p.EntryPrice
	}
	closed, err := s.closePositionInternal(ctx, p, exitPx, firstNonEmpty(reason, "PM_MANUAL_CLOSE"), false, s.now().UnixMilli())
	s.opMu.Unlock()
	if err != nil {
		return PaperPosition{}, err
	}
	s.publish("position_closed", fmt.Sprintf("Closed paper %s (%s) PnL=$%.2f (%.2fR)", closed.Symbol, closed.ExitReason, closed.RealizedPnL, closed.PnLR), closed)
	return closed, nil
}

func (s *Service) openPaperPositionLocked(ctx context.Context, ticket OrderTicket, limits RiskLimits, now int64) (PaperPosition, error) {
	notional := ticket.Quantity * ticket.EntryPrice
	entryFee := math.Round(notional*(limits.FeeBps/10000.0)*100) / 100
	riskUSD := math.Abs(ticket.EntryPrice-ticket.StopPrice) * ticket.Quantity
	if riskUSD <= 0 {
		riskUSD = ticket.RiskUSD
	}
	pos := PaperPosition{
		ID:             newID("pos"),
		TicketID:       ticket.ID,
		Venue:          ticket.Venue,
		Symbol:         ticket.Symbol,
		ScriptID:       ticket.ScriptID,
		Side:           ticket.Side,
		Quantity:       ticket.Quantity,
		EntryPrice:     ticket.EntryPrice,
		MarkPrice:      ticket.EntryPrice,
		StopPrice:      ticket.StopPrice,
		TargetPrice:    ticket.TargetPrice,
		InitialRiskUSD: math.Round(riskUSD*100) / 100,
		EntryFeeUSD:    entryFee,
		Status:         "OPEN",
		OpenedAt:       now,
	}
	if err := s.store.savePosition(ctx, pos); err != nil {
		return PaperPosition{}, err
	}
	acct, err := s.store.getAccountRow(ctx)
	if err != nil {
		return PaperPosition{}, err
	}
	acct.CashUSD = math.Round((acct.CashUSD-entryFee)*100) / 100
	acct.TotalFees = math.Round((acct.TotalFees+entryFee)*100) / 100
	acct.UpdatedAt = now
	if err := s.store.updateAccountRow(ctx, acct); err != nil {
		return PaperPosition{}, err
	}
	return pos, nil
}

func (s *Service) closePositionInternal(ctx context.Context, p PaperPosition, exitPrice float64, exitReason string, duringKill bool, now int64) (PaperPosition, error) {
	limits, err := s.store.getLimits(ctx)
	if err != nil {
		return PaperPosition{}, err
	}
	if exitPrice <= 0 {
		exitPrice = p.EntryPrice
	}
	exitFee := math.Round((exitPrice*p.Quantity)*(limits.FeeBps/10000.0)*100) / 100
	var grossPnL float64
	if p.Side == "long" {
		grossPnL = (exitPrice - p.EntryPrice) * p.Quantity
	} else {
		grossPnL = (p.EntryPrice - exitPrice) * p.Quantity
	}
	netPnL := math.Round((grossPnL-p.EntryFeeUSD-exitFee)*100) / 100
	var pnlR float64
	if p.InitialRiskUSD > 0 {
		pnlR = math.Round((netPnL/p.InitialRiskUSD)*100) / 100
	}

	p.ExitPrice = exitPrice
	p.MarkPrice = exitPrice
	p.ExitFeeUSD = exitFee
	p.UnrealizedPnL = 0
	p.RealizedPnL = finiteFloat(netPnL)
	p.PnLR = finiteFloat(pnlR)
	p.Status = "CLOSED"
	p.ExitReason = exitReason
	p.ClosedAt = now
	closedOK, acct, err := s.store.closePositionAndUpdateAccountTx(ctx, p, grossPnL, netPnL, exitFee, now)
	if err != nil {
		return PaperPosition{}, err
	}
	if !closedOK {
		return PaperPosition{}, fmt.Errorf("position %s already closed", p.ID)
	}

	outcome := "CLOSED_LOSS"
	if netPnL >= 0 {
		outcome = "CLOSED_WIN"
	}
	_ = s.store.updateJournalOutcomeByTicket(ctx, p.TicketID, outcome, exitPrice, p.RealizedPnL, p.PnLR, exitReason, "", now)

	// Cooldown bench after MaxConsecutiveLosses; reset streak once bench takes over so desk does not deadlock after cooldown expires.
	if acct.ConsecutiveLosses >= limits.MaxConsecutiveLosses && exitReason != "RESET_PAPER" {
		benchedUntil := now + int64(limits.CooldownHours)*int64(time.Hour/time.Millisecond)
		reason := fmt.Sprintf("Cooldown %dh triggered after %d consecutive paper losses", limits.CooldownHours, acct.ConsecutiveLosses)
		_, _ = s.store.setBench(ctx, "symbol:"+strings.ToUpper(p.Symbol), reason, acct.ConsecutiveLosses, benchedUntil, now)
		if p.ScriptID != "" {
			_, _ = s.store.setBench(ctx, "script:"+p.ScriptID, reason, acct.ConsecutiveLosses, benchedUntil, now)
		}
		_ = s.store.resetConsecutiveLosses(ctx, now)
		s.emitAlert(ctx, "warn", "Desk Cooldown Bench Triggered", reason)
	}

	// Auto-trip Kill Switch if DailyLossPct >= MaxDailyLossPct and flatten remaining open positions.
	if !duringKill && acct.DailyPnL < 0 && acct.InitialCapital > 0 {
		lossPct := (-acct.DailyPnL / acct.InitialCapital) * 100.0
		if lossPct >= limits.MaxDailyLossPct {
			reason := fmt.Sprintf("Daily loss limit breached (%.2f%% >= %.2f%%)", lossPct, limits.MaxDailyLossPct)
			_, _ = s.store.saveControl(ctx, true, true, reason, "risk-engine", now)
			_ = s.haltPendingAndFlatten(ctx, reason, "EXIT_DAILY_LOSS_KILL", now)
			s.publish("kill_switch", reason, p)
			s.emitAlert(ctx, "critical", "Desk Daily Loss Kill Switch Triggered", reason)
		}
	}
	return p, nil
}

// GetDecayReport computes paper vs backtest R-multiple health over closed paper trades.
func (s *Service) GetDecayReport(ctx context.Context) (DecayReport, error) {
	acct, err := s.store.getAccountRow(ctx)
	if err != nil {
		return DecayReport{}, err
	}
	positions, err := s.store.listPositions(ctx, "CLOSED", 200)
	if err != nil {
		return DecayReport{}, err
	}
	journal, err := s.store.listJournal(ctx, "", "", 250)
	if err != nil {
		return DecayReport{}, err
	}

	var kills, riskRejects, shadows int
	var sumExpectedR float64
	var expectedCount int
	for _, j := range journal {
		if acct.ResetAt > 0 && j.CreatedAt <= acct.ResetAt {
			continue
		}
		switch j.Outcome {
		case "KILLED_BY_SKEPTIC":
			kills++
		case "FAILED_RISK_CHECK":
			riskRejects++
		case "SHADOW":
			shadows++
		}
		if j.Outcome == "FILLED" || j.Outcome == "CLOSED_WIN" || j.Outcome == "CLOSED_LOSS" {
			if j.ExpectedAvgR > 0 {
				sumExpectedR += j.ExpectedAvgR
				expectedCount++
			}
		}
	}

	var validClosed int
	var wins int
	var sumR float64
	for _, p := range positions {
		if p.ExitReason == "RESET_PAPER" {
			continue
		}
		if acct.ResetAt > 0 && p.ClosedAt <= acct.ResetAt {
			continue
		}
		validClosed++
		if p.RealizedPnL >= 0 {
			wins++
		}
		sumR += p.PnLR
	}

	avgR := 0.0
	winRate := 0.0
	if validClosed > 0 {
		avgR = math.Round((sumR/float64(validClosed))*100) / 100
		winRate = math.Round((float64(wins)/float64(validClosed))*1000) / 10
	}
	backtestAvgR := 0.50
	if expectedCount > 0 {
		backtestAvgR = math.Round((sumExpectedR/float64(expectedCount))*100) / 100
	}

	status := "WARMING_UP"
	decaying := false
	summary := fmt.Sprintf("Warming up (%d/20 closed paper trades logged; avgR=%.2fR vs exp=%.2fR).", validClosed, avgR, backtestAvgR)
	if validClosed >= 20 {
		if avgR < backtestAvgR*0.5 {
			status = "DECAYING"
			decaying = true
			summary = fmt.Sprintf("DECAY ALERT: %d paper trades avg %.2fR (< 50%% of %.2fR backtest expectancy).", validClosed, avgR, backtestAvgR)
		} else {
			status = "HEALTHY"
			summary = fmt.Sprintf("Healthy expectancy: %d paper trades avg %.2fR vs %.2fR backtest target (%.1f%% win).", validClosed, avgR, backtestAvgR, winRate)
		}
	}

	return DecayReport{
		ClosedTrades:   validClosed,
		PaperAvgR:      finiteFloat(avgR),
		BacktestAvgR:   finiteFloat(backtestAvgR),
		WinRatePct:     finiteFloat(winRate),
		Decaying:       decaying,
		Status:         status,
		SkepticKills:   kills,
		RiskRejections: riskRejects,
		ShadowCount:    shadows,
		Summary:        summary,
	}, nil
}

// ListCycles returns recent multi-seat desk cycle records.
func (s *Service) ListCycles(ctx context.Context, limit int) ([]CycleResult, error) {
	return s.store.listCycles(ctx, limit)
}

// ListTickets returns recent desk tickets optionally filtered by status.
func (s *Service) ListTickets(ctx context.Context, status string, limit int) ([]OrderTicket, error) {
	return s.store.listTickets(ctx, status, limit)
}

// ListPositions returns recent paper positions optionally filtered by status.
func (s *Service) ListPositions(ctx context.Context, status string, limit int) ([]PaperPosition, error) {
	return s.store.listPositions(ctx, status, limit)
}

// ListJournal returns Trade Journal records filtered by optional symbol & outcome.
func (s *Service) ListJournal(ctx context.Context, symbol, outcome string, limit int) ([]JournalEntry, error) {
	return s.store.listJournal(ctx, symbol, outcome, limit)
}

// ListLessons returns durable desk rules/lessons read before each cycle.
func (s *Service) ListLessons(ctx context.Context, limit int) ([]LessonRecord, error) {
	return s.store.listLessons(ctx, limit)
}

// SaveLesson creates or updates a durable desk lesson.
func (s *Service) SaveLesson(ctx context.Context, in LessonInput) (LessonRecord, error) {
	rec, err := s.store.saveLesson(ctx, in, s.now().UnixMilli())
	if err != nil {
		return LessonRecord{}, err
	}
	s.publish("lesson_saved", "Saved desk lesson: "+rec.Rule, rec)
	return rec, nil
}

// DeleteLesson removes a desk lesson by ID.
func (s *Service) DeleteLesson(ctx context.Context, id string) (bool, error) {
	return s.store.deleteLesson(ctx, id)
}

func (s *Service) publish(evtType, summary string, payload any) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(DeskEvent{
		Type:      evtType,
		Timestamp: s.now().UnixMilli(),
		Summary:   summary,
		Payload:   payload,
	})
}

func (s *Service) emitAlert(ctx context.Context, severity, title, body string) {
	if s.cfg.OnAlert != nil {
		s.cfg.OnAlert(ctx, severity, title, body)
	}
}

type skepticInput struct {
	Symbol               string
	ScriptID             string
	StaleData            bool
	ToxicCatalyst        string
	UnsourcedHighUrgency bool
	Benched              bool
	BenchReason          string
	ChartScore           float64
	MinChartScore        float64
	RiskReward           float64
	MinRiskReward        float64
	RequireFourGate      bool
	FourGatePassed       bool
}

func (s *Service) runSkepticReview(ctx context.Context, in skepticInput) (string, string) {
	if in.StaleData {
		return "KILL", "Market candles are stale beyond StaleDataMaxAgeSec; refusing blind execution."
	}
	if in.UnsourcedHighUrgency {
		return "KILL", "High-urgency catalyst lacks primary source link or timestamp (UNVERIFIED)."
	}
	if in.ToxicCatalyst != "" {
		return "KILL", "Adversarial catalyst check triggered: " + in.ToxicCatalyst
	}
	if in.Benched {
		return "KILL", firstNonEmpty(in.BenchReason, "Cooldown bench is active on "+in.Symbol+"/"+in.ScriptID)
	}
	if in.ChartScore < in.MinChartScore {
		return "KILL", fmt.Sprintf("Chart score %.1f is below minimum threshold %.1f.", in.ChartScore, in.MinChartScore)
	}
	if in.RiskReward < in.MinRiskReward {
		return "KILL", fmt.Sprintf("Reward-to-risk %.2f is below minimum threshold %.2f.", in.RiskReward, in.MinRiskReward)
	}
	if in.RequireFourGate && !in.FourGatePassed {
		return "KILL", fmt.Sprintf("Script %s has not passed 4-Gate validation (RequireFourGatePass=true).", in.ScriptID)
	}

	if s.cfg.Scorer != nil {
		state := fmt.Sprintf("symbol=%s script=%s score=%.1f rr=%.2f fourGate=%t stale=false toxic=false",
			in.Symbol, in.ScriptID, in.ChartScore, in.RiskReward, in.FourGatePassed)
		resp, err := s.cfg.Scorer(ctx, decision.EvaluateRequest{
			State: state,
			Questions: map[string]decision.QuestionSpec{
				"allow": {
					Type:         decision.TypeNoul,
					Instructions: "Setup has sourced catalysts, valid ATR stop/target, and passes risk thresholds.",
				},
			},
		})
		if err == nil {
			if ans, ok := resp.Answers["allow"]; ok && ans.Noul != nil && *ans.Noul < 0.25 && ans.Confidence >= 0.85 {
				return "KILL", fmt.Sprintf("TypeSafe Jev Skeptic gate rejected setup (p=%.2f, conf=%.2f).", *ans.Noul, ans.Confidence)
			}
		}
	}
	return "PASS", fmt.Sprintf("Passed Skeptic attack (score=%.1f, R:R=%.2f, sourced & within rules).", in.ChartScore, in.RiskReward)
}

type chartSetup struct {
	Side         string
	Score        float64
	EntryPrice   float64
	StopPrice    float64
	TargetPrice  float64
	RiskReward   float64
	ATR14        float64
	RSI14        float64
	EMASpreadPct float64
}

func analyzeCandles(candles []marketdata.Candle) chartSetup {
	n := len(candles)
	closes := make([]float64, n)
	highs := make([]float64, n)
	lows := make([]float64, n)
	for i, c := range candles {
		closes[i] = c.Close
		highs[i] = c.High
		lows[i] = c.Low
	}
	ema20 := causalEMA(closes, 20)
	ema50 := causalEMA(closes, 50)
	rsi14 := causalRSI(closes, 14)
	atr14 := causalATR(highs, lows, closes, 14)

	lastClose := closes[n-1]
	lastATR := atr14
	if lastATR <= 0 || math.IsNaN(lastATR) {
		lastATR = math.Max(lastClose*0.01, 1e-6)
	}
	side := "long"
	spreadPct := 0.0
	if ema50 > 0 {
		spreadPct = ((ema20 - ema50) / ema50) * 100.0
	}
	if ema20 < ema50 {
		side = "short"
	}

	score := 7.2
	if math.Abs(spreadPct) >= 0.15 {
		score += 0.8
	}
	if side == "long" && rsi14 >= 42 && rsi14 <= 74 {
		score += 0.6
	} else if side == "short" && rsi14 >= 26 && rsi14 <= 58 {
		score += 0.6
	} else if rsi14 > 82 || rsi14 < 18 {
		score -= 1.2
	}
	if score > 9.8 {
		score = 9.8
	}
	if score < 3.0 {
		score = 3.0
	}
	score = math.Round(score*10) / 10

	var stop, target float64
	if side == "long" {
		stop = lastClose - 1.5*lastATR
		if stop <= 0 {
			stop = lastClose * 0.95
		}
		target = lastClose + 2.5*lastATR
	} else {
		stop = lastClose + 1.5*lastATR
		target = lastClose - 2.5*lastATR
		if target <= 0 {
			target = lastClose * 0.90
		}
	}
	stopDist := math.Abs(lastClose - stop)
	rewardDist := math.Abs(target - lastClose)
	rr := 0.0
	if stopDist > 0 {
		rr = math.Round((rewardDist/stopDist)*100) / 100
	}

	return chartSetup{
		Side:         side,
		Score:        score,
		EntryPrice:   roundPrice(lastClose),
		StopPrice:    roundPrice(stop),
		TargetPrice:  roundPrice(target),
		RiskReward:   rr,
		ATR14:        roundPrice(lastATR),
		RSI14:        math.Round(rsi14*10) / 10,
		EMASpreadPct: math.Round(spreadPct*100) / 100,
	}
}

func evaluateStrategySignal(strat fasedscript.StrategySpec, barCount int) (side string, signalBar int, hasRules bool) {
	hasRules = len(strat.Long) > 0 || len(strat.Short) > 0
	if !hasRules || barCount <= 0 {
		return "", -1, hasRules
	}
	flagAt := func(arr []bool, i int) bool {
		return i >= 0 && i < len(arr) && arr[i]
	}
	activeSide := ""
	lastEntryBar := -1
	for i := 0; i < barCount; i++ {
		wantLong := flagAt(strat.Long, i)
		wantExitLong := flagAt(strat.ExitLong, i)
		wantShort := flagAt(strat.Short, i)
		wantExitShort := flagAt(strat.ExitShort, i)

		if activeSide == "long" && (wantExitLong || (wantShort && !wantLong)) {
			activeSide = ""
		} else if activeSide == "short" && (wantExitShort || (wantLong && !wantShort)) {
			activeSide = ""
		}
		if activeSide == "" {
			if wantLong && !wantShort {
				activeSide = "long"
				lastEntryBar = i
			} else if wantShort && !wantLong {
				activeSide = "short"
				lastEntryBar = i
			}
		}
	}
	return activeSide, lastEntryBar, true
}

func deriveScriptSetup(base chartSetup, side string, strat fasedscript.StrategySpec, minRR float64) chartSetup {
	entry := base.EntryPrice
	if entry <= 0 {
		return base
	}
	atr := base.ATR14
	if atr <= 0 {
		atr = entry * 0.01
	}

	var stopDist float64
	switch {
	case strat.StopLossPct > 0:
		stopDist = entry * (strat.StopLossPct / 100.0)
	case strat.TrailingAtrMult > 0:
		stopDist = strat.TrailingAtrMult * atr
	default:
		stopDist = 1.5 * atr
	}
	if stopDist <= 0 {
		stopDist = entry * 0.01
	}

	var rewardDist float64
	if strat.TakeProfitPct > 0 {
		rewardDist = entry * (strat.TakeProfitPct / 100.0)
	} else {
		rewardDist = math.Max(2.5*atr, stopDist*math.Max(minRR, 1.8))
	}

	var stop, target float64
	if side == "short" {
		stop = entry + stopDist
		target = entry - rewardDist
		if target <= 0 {
			target = entry * 0.85
		}
	} else {
		side = "long"
		stop = entry - stopDist
		if stop <= 0 {
			stop = entry * 0.90
		}
		target = entry + rewardDist
	}

	effStopDist := math.Abs(entry - stop)
	effRewardDist := math.Abs(target - entry)
	rr := 0.0
	if effStopDist > 0 {
		rr = math.Round((effRewardDist/effStopDist)*100) / 100
	}
	out := base
	out.Side = side
	out.EntryPrice = roundPrice(entry)
	out.StopPrice = roundPrice(stop)
	out.TargetPrice = roundPrice(target)
	out.RiskReward = rr
	return out
}

func causalEMA(vals []float64, period int) float64 {
	if len(vals) == 0 {
		return 0
	}
	if len(vals) < period {
		period = len(vals)
	}
	var sum float64
	for i := 0; i < period; i++ {
		sum += vals[i]
	}
	ema := sum / float64(period)
	k := 2.0 / float64(period+1)
	for i := period; i < len(vals); i++ {
		ema = vals[i]*k + ema*(1.0-k)
	}
	return finiteFloat(ema)
}

func causalRSI(closes []float64, period int) float64 {
	if len(closes) <= period || period <= 0 {
		return 50
	}
	var gain, loss float64
	for i := 1; i <= period; i++ {
		d := closes[i] - closes[i-1]
		if d > 0 {
			gain += d
		} else {
			loss -= d
		}
	}
	avgGain := gain / float64(period)
	avgLoss := loss / float64(period)
	for i := period + 1; i < len(closes); i++ {
		d := closes[i] - closes[i-1]
		var g, l float64
		if d > 0 {
			g = d
		} else {
			l = -d
		}
		avgGain = (avgGain*float64(period-1) + g) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + l) / float64(period)
	}
	if avgLoss == 0 {
		if avgGain == 0 {
			return 50
		}
		return 100
	}
	rs := avgGain / avgLoss
	return finiteFloat(100.0 - (100.0 / (1.0 + rs)))
}

func causalATR(highs, lows, closes []float64, period int) float64 {
	n := len(closes)
	if n < 2 {
		return 0
	}
	if period >= n {
		period = n - 1
	}
	var sum float64
	for i := 1; i <= period; i++ {
		tr := math.Max(highs[i]-lows[i], math.Max(math.Abs(highs[i]-closes[i-1]), math.Abs(lows[i]-closes[i-1])))
		sum += tr
	}
	atr := sum / float64(period)
	for i := period + 1; i < n; i++ {
		tr := math.Max(highs[i]-lows[i], math.Max(math.Abs(highs[i]-closes[i-1]), math.Abs(lows[i]-closes[i-1])))
		atr = (atr*float64(period-1) + tr) / float64(period)
	}
	return finiteFloat(atr)
}

func normalizeCandleSeconds(ts int64) int64 {
	if ts > 1_000_000_000_000 {
		return ts / 1000
	}
	return ts
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return strings.TrimSpace(b)
}
