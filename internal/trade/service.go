package trade

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/marketdata"
)

// Config wires injected market data, external kill check, live execution router, and alert callback for Service.
type Config struct {
	DataDir            string
	FixtureMode        bool
	Now                func() time.Time
	MarketReader       func(ctx context.Context, venue, symbol, interval string, limit int) ([]marketdata.Candle, error)
	ExternalKillActive func(ctx context.Context) bool
	ExecutionProfile   func() string
	LiveExecutor       func(ctx context.Context, req OrderRequest, refPrice float64) (LiveExecReceipt, error)
	ScriptEvaluator    func(ctx context.Context, bot TradeBot, candles []marketdata.Candle) (signal string, side string, note string, err error)
	AgentEvaluator     func(ctx context.Context, bot TradeBot, candles []marketdata.Candle) (signal string, side string, note string, err error)
	OnAlert            func(ctx context.Context, severity, title, body string)
}

// Service coordinates unified order routing, deterministic risk enforcement,
// global kill switch, and Go-managed advanced order strategies cleanly.
type Service struct {
	cfg   Config
	store *store
	now   func() time.Time
	opMu  sync.Mutex
}

// NewService opens isolated <dataDir>/trade/trade.sqlite storage.
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
		now:   nowFn,
	}, nil
}

// Close closes the underlying trade SQLite store cleanly.
func (s *Service) Close() error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.close()
}

func (s *Service) isKillHalted(ctx context.Context) (bool, KillState, error) {
	ks, err := s.store.getKill(ctx)
	if err != nil {
		return false, KillState{}, err
	}
	if ks.Active {
		return true, ks, nil
	}
	if s.cfg.ExternalKillActive != nil && s.cfg.ExternalKillActive(ctx) {
		return true, KillState{Active: true, Reason: "external kill switch active", UpdatedBy: "external", UpdatedAt: s.now().UTC().Unix()}, nil
	}
	return false, ks, nil
}

func (s *Service) refreshPolicyDay(ctx context.Context) (RiskPolicy, error) {
	pol, err := s.store.getPolicy(ctx)
	if err != nil {
		return RiskPolicy{}, err
	}
	today := s.now().UTC().Format("2006-01-02")
	if pol.DailyLossDay != today {
		pol.DailyLossDay = today
		pol.DailyLossUSD = 0
		pol.DailyNotionalUSD = 0
		_ = s.store.savePolicy(ctx, pol)
	}
	return pol, nil
}

func (s *Service) readCandles(ctx context.Context, venue, symbol, interval string, limit int) ([]marketdata.Candle, error) {
	if s.cfg.MarketReader == nil {
		return nil, errors.New("market reader unavailable")
	}
	return s.cfg.MarketReader(ctx, venue, symbol, interval, limit)
}

func roundUSD(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*100) / 100
}

func calcUnrealizedPnL(side string, qty, entry, mark float64) float64 {
	if qty <= 0 || entry <= 0 || mark <= 0 {
		return 0
	}
	if side == SideSell {
		return roundUSD((entry - mark) * qty)
	}
	return roundUSD((mark - entry) * qty)
}

func calcRealizedPnL(side string, qty, entry, exitPrice float64) float64 {
	if side == SideSell {
		return roundUSD((entry - exitPrice) * qty)
	}
	return roundUSD((exitPrice - entry) * qty)
}

// SubmitOrder validates risk limits and kill switch, initializes the strategy plan,
// and stores it in trade.sqlite. Execution remains strictly paper/simulated unless explicitly gated.
func (s *Service) SubmitOrder(ctx context.Context, rawReq OrderRequest) (OrderPlan, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()

	halted, ks, err := s.isKillHalted(ctx)
	if err != nil {
		return OrderPlan{}, err
	}
	if halted {
		return OrderPlan{}, fmt.Errorf("%w: %s", ErrKillSwitchActive, ks.Reason)
	}

	req, err := NormalizeOrderRequest(rawReq)
	if err != nil {
		return OrderPlan{}, err
	}

	pol, err := s.refreshPolicyDay(ctx)
	if err != nil {
		return OrderPlan{}, err
	}
	if pol.DailyLossUSD >= pol.MaxDailyLossUSD {
		return OrderPlan{}, fmt.Errorf("%w: daily loss limit ($%.2f / $%.2f) reached", ErrRiskRejected, pol.DailyLossUSD, pol.MaxDailyLossUSD)
	}
	activePlans, err := s.store.listPlans(ctx, StatusActive, 200)
	if err != nil {
		return OrderPlan{}, err
	}
	if len(activePlans) >= pol.MaxOpenPlans {
		return OrderPlan{}, fmt.Errorf("%w: max open plans (%d) reached", ErrRiskRejected, pol.MaxOpenPlans)
	}

	var candles []marketdata.Candle
	var markPrice float64
	if s.cfg.MarketReader != nil {
		c, cErr := s.readCandles(ctx, req.Venue, req.Symbol, req.Interval, 64)
		if cErr != nil && (req.EntryPrice == 0 || req.Strategy == StrategyTrailingATR || req.Strategy == StrategyMarket || req.ExecutionMode == ExecutionModeLive) {
			return OrderPlan{}, cErr
		}
		if cErr == nil && len(c) > 0 {
			candles = c
			markPrice = c[len(c)-1].Close
		}
	} else if req.Strategy == StrategyTrailingATR {
		return OrderPlan{}, errors.New("market reader required for trailing_atr strategy")
	}

	refPrice := req.EntryPrice
	if refPrice <= 0 {
		refPrice = markPrice
	}
	if !isFinitePositive(refPrice) {
		return OrderPlan{}, errors.New("unable to determine reference price; specify entryPrice")
	}
	if markPrice <= 0 {
		markPrice = refPrice
	}
	// Never allow a caller-supplied low entryPrice to understate live order notional relative to markPrice.
	riskPrice := refPrice
	if req.ExecutionMode == ExecutionModeLive && markPrice > riskPrice {
		riskPrice = markPrice
	}
	if req.Strategy == StrategyMarket && isFinitePositive(markPrice) {
		refPrice = markPrice
	}

	notional := req.Quantity * riskPrice
	if notional > pol.MaxOrderNotionalUSD {
		return OrderPlan{}, fmt.Errorf("%w: order notional $%.2f exceeds max $%.2f", ErrRiskRejected, notional, pol.MaxOrderNotionalUSD)
	}
	if pol.MaxDailyNotionalUSD > 0 && pol.DailyNotionalUSD+notional > pol.MaxDailyNotionalUSD {
		return OrderPlan{}, fmt.Errorf("%w: cumulative daily notional $%.2f + $%.2f exceeds max $%.2f", ErrRiskRejected, pol.DailyNotionalUSD, notional, pol.MaxDailyNotionalUSD)
	}

	if req.Strategy == StrategyBracket {
		if err := validateBracketPrices(req.Side, refPrice, req.StopPrice, req.TargetPrice); err != nil {
			return OrderPlan{}, err
		}
	}

	id, err := newPlanID()
	if err != nil {
		return OrderPlan{}, err
	}
	nowSec := s.now().UTC().Unix()
	plan := OrderPlan{
		ID:              id,
		Venue:           req.Venue,
		Symbol:          req.Symbol,
		Interval:        req.Interval,
		Mode:            req.Mode,
		Strategy:        req.Strategy,
		Side:            req.Side,
		Status:          StatusActive,
		Simulated:       req.ExecutionMode != ExecutionModeLive,
		AccountID:       req.AccountID,
		ExecutionMode:   req.ExecutionMode,
		Source:          req.Source,
		BotID:           req.BotID,
		AgentID:         req.AgentID,
		Quantity:        req.Quantity,
		EntryPrice:      req.EntryPrice,
		StopPrice:       req.StopPrice,
		TargetPrice:     req.TargetPrice,
		ATRMultiplier:   req.ATRMultiplier,
		GridLower:       req.GridLower,
		GridUpper:       req.GridUpper,
		GridLevels:      req.GridLevels,
		Slices:          req.Slices,
		IntervalSeconds: req.IntervalSeconds,
		MarkPrice:       markPrice,
		AgentNote:       req.AgentNote,
		CreatedAt:       nowSec,
		UpdatedAt:       nowSec,
	}

	if req.ExecutionMode == ExecutionModeLive {
		if s.cfg.LiveExecutor == nil {
			return OrderPlan{}, errors.New("live execution router is not configured; switch account to Paper or enable an execution profile")
		}
		receipt, liveErr := s.cfg.LiveExecutor(ctx, req, refPrice)
		if liveErr != nil {
			return OrderPlan{}, liveErr
		}
		plan.Simulated = false
		plan.VenueOrderID = receipt.VenueOrderID
		plan.TxSignature = receipt.TxSignature
		if receipt.FilledPrice > 0 {
			refPrice = receipt.FilledPrice
		}
		plan.EntryPrice = refPrice
		if receipt.Immediate || req.Strategy == StrategyMarket {
			plan.Status = StatusFilled
			plan.FilledQty = req.Quantity
			plan.AvgFillPrice = refPrice
			plan.UnrealizedPnLUSD = calcUnrealizedPnL(req.Side, req.Quantity, refPrice, markPrice)
		} else {
			plan.Status = StatusActive
		}
		if err := s.store.savePlan(ctx, plan); err != nil {
			return OrderPlan{}, err
		}
		pol.DailyNotionalUSD = roundUSD(pol.DailyNotionalUSD + notional)
		_ = s.store.savePolicy(ctx, pol)
		return plan, nil
	}

	switch req.Strategy {
	case StrategyMarket:
		plan.Status = StatusFilled
		plan.EntryPrice = refPrice
		plan.FilledQty = req.Quantity
		plan.AvgFillPrice = refPrice
		plan.UnrealizedPnLUSD = calcUnrealizedPnL(req.Side, req.Quantity, refPrice, markPrice)
	case StrategyLimit:
		plan.Status = StatusActive
	case StrategyBracket:
		if req.EntryPrice == 0 {
			plan.EntryPrice = refPrice
			plan.FilledQty = req.Quantity
			plan.AvgFillPrice = refPrice
			plan.UnrealizedPnLUSD = calcUnrealizedPnL(req.Side, req.Quantity, refPrice, markPrice)
		}
	case StrategyTrailingATR:
		atr, ok := ComputeATR14(candles)
		if !ok {
			return OrderPlan{}, errors.New("insufficient candle history to compute ATR(14)")
		}
		plan.EntryPrice = refPrice
		plan.FilledQty = req.Quantity
		plan.AvgFillPrice = refPrice
		offset := atr * req.ATRMultiplier
		if req.Side == SideBuy {
			plan.TrailingStopPrice = math.Max(refPrice*0.1, refPrice-offset)
		} else {
			plan.TrailingStopPrice = refPrice + offset
		}
		plan.UnrealizedPnLUSD = calcUnrealizedPnL(req.Side, req.Quantity, refPrice, markPrice)
	case StrategyGrid:
		if req.EntryPrice == 0 {
			plan.EntryPrice = refPrice
		}
	case StrategyDCA, StrategyTWAP:
		sliceQty := req.Quantity / float64(req.Slices)
		plan.EntryPrice = refPrice
		plan.SlicesFilled = 1
		plan.FilledQty = sliceQty
		plan.AvgFillPrice = refPrice
		plan.LastEvaluatedAt = nowSec
		plan.UnrealizedPnLUSD = calcUnrealizedPnL(req.Side, sliceQty, refPrice, markPrice)
	}

	if err := s.store.savePlan(ctx, plan); err != nil {
		return OrderPlan{}, err
	}
	pol.DailyNotionalUSD = roundUSD(pol.DailyNotionalUSD + notional)
	_ = s.store.savePolicy(ctx, pol)
	return plan, nil
}

// EvaluateActive evaluates all active multi-strategy plans against fresh market candles
// and enforces global kill-switch flattening.
func (s *Service) EvaluateActive(ctx context.Context) (EvaluateSummary, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()

	halted, ks, err := s.isKillHalted(ctx)
	if err != nil {
		return EvaluateSummary{}, err
	}
	active, err := s.store.listPlans(ctx, StatusActive, 200)
	if err != nil {
		return EvaluateSummary{}, err
	}

	var sum EvaluateSummary
	nowSec := s.now().UTC().Unix()
	if halted {
		sum.Halted = true
		for _, p := range active {
			p.Status = StatusKilled
			p.LastError = ks.Reason
			p.UpdatedAt = nowSec
			if s.store.savePlan(ctx, p) == nil {
				sum.Killed++
			}
		}
		return sum, nil
	}

	pol, err := s.refreshPolicyDay(ctx)
	if err != nil {
		return EvaluateSummary{}, err
	}

	type feedKey struct{ v, sym, iv string }
	candleCache := make(map[feedKey][]marketdata.Candle)
	getCandles := func(v, sym, iv string) ([]marketdata.Candle, error) {
		k := feedKey{v, sym, iv}
		if cached, ok := candleCache[k]; ok {
			return cached, nil
		}
		c, err := s.readCandles(ctx, v, sym, iv, 64)
		if err != nil {
			return nil, err
		}
		candleCache[k] = c
		return c, nil
	}

	policyDirty := false
	for _, p := range active {
		sum.Evaluated++
		candles, cErr := getCandles(p.Venue, p.Symbol, p.Interval)
		if cErr != nil || len(candles) == 0 {
			continue
		}
		latest := candles[len(candles)-1]
		p.MarkPrice = latest.Close
		changed := false

		switch p.Strategy {
		case StrategyLimit:
			if (p.Side == SideBuy && latest.Low <= p.EntryPrice) || (p.Side == SideSell && latest.High >= p.EntryPrice) {
				p.FilledQty = p.Quantity
				p.AvgFillPrice = p.EntryPrice
				p.Status = StatusFilled
				p.UnrealizedPnLUSD = calcUnrealizedPnL(p.Side, p.FilledQty, p.AvgFillPrice, latest.Close)
				sum.Completed++
				changed = true
			}

		case StrategyBracket:
			if p.FilledQty == 0 {
				if (p.Side == SideBuy && latest.Low <= p.EntryPrice) || (p.Side == SideSell && latest.High >= p.EntryPrice) {
					p.FilledQty = p.Quantity
					p.AvgFillPrice = p.EntryPrice
					changed = true
				}
			}
			if p.FilledQty > 0 {
				var exitPrice float64
				hitReason := ""
				if p.Side == SideBuy {
					if latest.Low <= p.StopPrice {
						exitPrice = p.StopPrice
						hitReason = "Stop loss"
					} else if latest.High >= p.TargetPrice {
						exitPrice = p.TargetPrice
						hitReason = "Take profit"
					}
				} else {
					if latest.High >= p.StopPrice {
						exitPrice = p.StopPrice
						hitReason = "Stop loss"
					} else if latest.Low <= p.TargetPrice {
						exitPrice = p.TargetPrice
						hitReason = "Take profit"
					}
				}
				if hitReason != "" {
					realized := calcRealizedPnL(p.Side, p.FilledQty, p.AvgFillPrice, exitPrice)
					p.RealizedPnLUSD = realized
					p.UnrealizedPnLUSD = 0
					p.Status = StatusTriggered
					if realized < 0 {
						pol.DailyLossUSD = roundUSD(pol.DailyLossUSD + (-realized))
						policyDirty = true
					}
					sum.Triggered++
					changed = true
					if s.cfg.OnAlert != nil {
						s.cfg.OnAlert(ctx, "warn",
							fmt.Sprintf("%s %s bracket %s", hitReason, p.Symbol, p.Side),
							fmt.Sprintf("%s closed @ %.4f (PnL $%.2f)", p.Venue, exitPrice, realized),
						)
					}
				} else {
					uPnL := calcUnrealizedPnL(p.Side, p.FilledQty, p.AvgFillPrice, latest.Close)
					if uPnL != p.UnrealizedPnLUSD {
						p.UnrealizedPnLUSD = uPnL
						changed = true
					}
				}
			}

		case StrategyTrailingATR:
			if atr, ok := ComputeATR14(candles); ok {
				offset := atr * p.ATRMultiplier
				if p.Side == SideBuy {
					if latest.Low <= p.TrailingStopPrice {
						realized := calcRealizedPnL(p.Side, p.FilledQty, p.AvgFillPrice, p.TrailingStopPrice)
						p.RealizedPnLUSD = realized
						p.UnrealizedPnLUSD = 0
						p.Status = StatusTriggered
						if realized < 0 {
							pol.DailyLossUSD = roundUSD(pol.DailyLossUSD + (-realized))
							policyDirty = true
						}
						sum.Triggered++
						changed = true
						if s.cfg.OnAlert != nil {
							s.cfg.OnAlert(ctx, "warn",
								fmt.Sprintf("Trailing ATR triggered: %s", p.Symbol),
								fmt.Sprintf("Exited @ %.4f (PnL $%.2f)", p.TrailingStopPrice, realized),
							)
						}
					} else {
						cand := math.Max(p.EntryPrice*0.1, latest.Close-offset)
						if cand > p.TrailingStopPrice {
							p.TrailingStopPrice = cand
							changed = true
						}
						uPnL := calcUnrealizedPnL(p.Side, p.FilledQty, p.AvgFillPrice, latest.Close)
						if uPnL != p.UnrealizedPnLUSD {
							p.UnrealizedPnLUSD = uPnL
							changed = true
						}
					}
				} else {
					if latest.High >= p.TrailingStopPrice {
						realized := calcRealizedPnL(p.Side, p.FilledQty, p.AvgFillPrice, p.TrailingStopPrice)
						p.RealizedPnLUSD = realized
						p.UnrealizedPnLUSD = 0
						p.Status = StatusTriggered
						if realized < 0 {
							pol.DailyLossUSD = roundUSD(pol.DailyLossUSD + (-realized))
							policyDirty = true
						}
						sum.Triggered++
						changed = true
					} else {
						cand := latest.Close + offset
						if p.TrailingStopPrice == 0 || cand < p.TrailingStopPrice {
							p.TrailingStopPrice = cand
							changed = true
						}
						uPnL := calcUnrealizedPnL(p.Side, p.FilledQty, p.AvgFillPrice, latest.Close)
						if uPnL != p.UnrealizedPnLUSD {
							p.UnrealizedPnLUSD = uPnL
							changed = true
						}
					}
				}
			}

		case StrategyGrid:
			if latest.Close >= p.GridLower && latest.Close <= p.GridUpper && p.GridFills < p.GridLevels {
				sliceQty := p.Quantity / float64(p.GridLevels)
				prevNotional := p.FilledQty * p.AvgFillPrice
				p.GridFills++
				p.FilledQty = math.Min(p.Quantity, float64(p.GridFills)*sliceQty)
				if p.GridFills >= p.GridLevels {
					p.FilledQty = p.Quantity
					p.Status = StatusCompleted
					sum.Completed++
				}
				if p.FilledQty > 0 {
					p.AvgFillPrice = (prevNotional + sliceQty*latest.Close) / p.FilledQty
				}
				p.UnrealizedPnLUSD = calcUnrealizedPnL(p.Side, p.FilledQty, p.AvgFillPrice, latest.Close)
				changed = true
			} else if p.FilledQty > 0 {
				p.UnrealizedPnLUSD = calcUnrealizedPnL(p.Side, p.FilledQty, p.AvgFillPrice, latest.Close)
				changed = true
			}

		case StrategyDCA, StrategyTWAP:
			if p.SlicesFilled < p.Slices && (p.LastEvaluatedAt == 0 || nowSec-p.LastEvaluatedAt >= int64(p.IntervalSeconds)) {
				sliceQty := p.Quantity / float64(p.Slices)
				prevNotional := p.FilledQty * p.AvgFillPrice
				p.SlicesFilled++
				if p.SlicesFilled >= p.Slices {
					rem := math.Max(0, p.Quantity-p.FilledQty)
					p.FilledQty = p.Quantity
					p.AvgFillPrice = (prevNotional + rem*latest.Close) / p.Quantity
					p.Status = StatusCompleted
					sum.Completed++
				} else {
					p.FilledQty += sliceQty
					p.AvgFillPrice = (prevNotional + sliceQty*latest.Close) / p.FilledQty
				}
				p.LastEvaluatedAt = nowSec
				p.UnrealizedPnLUSD = calcUnrealizedPnL(p.Side, p.FilledQty, p.AvgFillPrice, latest.Close)
				changed = true
			} else if p.FilledQty > 0 {
				p.UnrealizedPnLUSD = calcUnrealizedPnL(p.Side, p.FilledQty, p.AvgFillPrice, latest.Close)
				changed = true
			}
		}

		if changed {
			p.UpdatedAt = nowSec
			if p.LastEvaluatedAt == 0 {
				p.LastEvaluatedAt = nowSec
			}
			if s.store.savePlan(ctx, p) == nil {
				sum.Updated++
			}
		}
	}

	if policyDirty {
		_ = s.store.savePolicy(ctx, pol)
	}
	return sum, nil
}

// CancelPlan cancels an active trade plan cleanly.
func (s *Service) CancelPlan(ctx context.Context, id string) (OrderPlan, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()

	p, err := s.store.getPlan(ctx, id)
	if err != nil {
		return OrderPlan{}, err
	}
	if p.Status != StatusActive {
		return OrderPlan{}, fmt.Errorf("plan %s cannot be cancelled in status %q", p.ID, p.Status)
	}
	p.Status = StatusCancelled
	p.UpdatedAt = s.now().UTC().Unix()
	if err := s.store.savePlan(ctx, p); err != nil {
		return OrderPlan{}, err
	}
	return p, nil
}

// SetKillSwitch activates or clears the trade engine kill switch and halts all active plans on activation.
func (s *Service) SetKillSwitch(ctx context.Context, active bool, reason, actor string) (KillState, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()

	nowSec := s.now().UTC().Unix()
	cleanReason := strings.TrimSpace(reason)
	if active && cleanReason == "" {
		cleanReason = "Owner emergency kill switch"
	}
	cleanActor := strings.TrimSpace(actor)
	if cleanActor == "" {
		cleanActor = "owner"
	}
	ks := KillState{
		Active:    active,
		Reason:    cleanReason,
		UpdatedBy: cleanActor,
		UpdatedAt: nowSec,
	}
	if err := s.store.setKill(ctx, ks); err != nil {
		return KillState{}, err
	}
	if active {
		activePlans, err := s.store.listPlans(ctx, StatusActive, 200)
		if err == nil {
			for _, p := range activePlans {
				p.Status = StatusKilled
				p.LastError = cleanReason
				p.UpdatedAt = nowSec
				_ = s.store.savePlan(ctx, p)
			}
		}
		if bots, bErr := s.store.listBots(ctx, BotStatusRunning, 200); bErr == nil {
			for _, b := range bots {
				if b.Status == BotStatusRunning {
					b.Status = BotStatusPaused
					b.LastError = cleanReason
					b.UpdatedAt = nowSec
					_ = s.store.saveBot(ctx, b)
				}
			}
		}
	}
	return ks, nil
}

// SyncExternalKill halts all active trade plans when an external kill switch fires.
func (s *Service) SyncExternalKill(ctx context.Context, reason, actor string) error {
	_, err := s.SetKillSwitch(ctx, true, reason, actor)
	return err
}

// UpdatePolicy updates hard Go risk caps cleanly.
func (s *Service) UpdatePolicy(ctx context.Context, in RiskPolicy) (RiskPolicy, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()

	cur, err := s.refreshPolicyDay(ctx)
	if err != nil {
		return RiskPolicy{}, err
	}
	in.DailyLossUSD = cur.DailyLossUSD
	in.DailyNotionalUSD = cur.DailyNotionalUSD
	in.DailyLossDay = cur.DailyLossDay
	if err := ValidateRiskPolicy(in); err != nil {
		return RiskPolicy{}, err
	}
	if err := s.store.savePolicy(ctx, in); err != nil {
		return RiskPolicy{}, err
	}
	return in, nil
}

// GetSnapshot returns unified trade engine state, policy, kill status, and recent plans.
func (s *Service) GetSnapshot(ctx context.Context) (Snapshot, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()

	halted, ks, err := s.isKillHalted(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	pol, err := s.refreshPolicyDay(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	plans, err := s.store.listPlans(ctx, "", 100)
	if err != nil {
		return Snapshot{}, err
	}
	activeCount := 0
	for _, p := range plans {
		if p.Status == StatusActive {
			activeCount++
		}
	}
	profile := "off"
	if s.cfg.ExecutionProfile != nil {
		if p := strings.TrimSpace(s.cfg.ExecutionProfile()); p != "" {
			profile = p
		}
	}
	return Snapshot{
		ExecutionProfile: profile,
		Simulated:        true,
		KillSwitchActive: halted,
		KillReason:       ks.Reason,
		Policy:           pol,
		ActiveCount:      activeCount,
		Plans:            plans,
	}, nil
}
