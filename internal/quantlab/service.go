package quantlab

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/fasedscript"
	"github.com/fased-ai/fased-app/internal/marketdata"
)

// Config parameterizes the deterministic QuantLab Service.
type Config struct {
	DataDir      string
	FixtureMode  bool
	Now          func() time.Time
	MarketReader func(ctx context.Context, venue, symbol, interval string, limit int) ([]marketdata.Candle, error)
}

// Service manages FasedScript storage, deterministic causal backtests,
// 4-Gate statistical validation runs, and paper/shadow promotion eligibility.
type Service struct {
	store *store
	now   func() time.Time
	cfg   Config
}

// NewService opens isolated <dataDir>/quantlab/lab.sqlite storage and seeds built-in templates.
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
		store: st,
		now:   nowFn,
		cfg:   cfg,
	}, nil
}

// Close closes the isolated SQLite store deterministically.
func (s *Service) Close() error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.close()
}

// ListScripts returns all built-in and custom FasedScript records.
func (s *Service) ListScripts(ctx context.Context) ([]ScriptRecord, error) {
	return s.store.listScripts(ctx)
}

// GetScript returns one stored script by ID.
func (s *Service) GetScript(ctx context.Context, id string) (ScriptRecord, bool, error) {
	return s.store.getScript(ctx, id)
}

// SaveScript compiles and persists a custom FasedScript definition cleanly.
func (s *Service) SaveScript(ctx context.Context, in ScriptInput) (ScriptRecord, error) {
	return s.store.saveScript(ctx, in)
}

// DeleteScript deletes a custom non-built-in script by ID.
func (s *Service) DeleteScript(ctx context.Context, id string) (bool, error) {
	return s.store.deleteScript(ctx, id)
}

// ListRuns returns compact summaries of recent persisted 4-Gate validation runs.
func (s *Service) ListRuns(ctx context.Context, limit int) ([]ValidationRunSummary, error) {
	return s.store.listRuns(ctx, limit)
}

// GetRun retrieves a complete persisted 4-Gate validation record by ID.
func (s *Service) GetRun(ctx context.Context, id string) (ValidationRunRecord, bool, error) {
	return s.store.getRun(ctx, id)
}

func (s *Service) resolveProgram(ctx context.Context, scriptID, inlineSource string) (id string, name string, prog *fasedscript.Program, err error) {
	if trimmedSrc := strings.TrimSpace(inlineSource); trimmedSrc != "" {
		compiled, cErr := fasedscript.Compile(inlineSource)
		if cErr != nil {
			return "", "", nil, cErr
		}
		resolvedID := strings.TrimSpace(scriptID)
		if resolvedID == "" {
			resolvedID = "inline"
		}
		return resolvedID, resolvedID, compiled, nil
	}
	safeID, err := ValidateSafeID(scriptID)
	if err != nil {
		return "", "", nil, err
	}
	rec, found, err := s.store.getScript(ctx, safeID)
	if err != nil {
		return "", "", nil, err
	}
	if !found {
		return "", "", nil, os.ErrNotExist
	}
	compiled, cErr := fasedscript.Compile(rec.Source)
	if cErr != nil {
		return "", "", nil, cErr
	}
	return rec.ID, rec.Name, compiled, nil
}

func (s *Service) fetchCandles(ctx context.Context, venue, symbol, interval string, limit int) ([]marketdata.Candle, error) {
	if s.cfg.MarketReader == nil {
		return nil, errors.New("market data reader unavailable")
	}
	candles, err := s.cfg.MarketReader(ctx, venue, symbol, interval, limit)
	if err != nil {
		return nil, err
	}
	if len(candles) < 30 {
		return nil, errors.New("at least 30 candles required for QuantLab evaluation")
	}
	return candles, nil
}

// EvaluateIndicator evaluates a compiled script over market candles and emits JSON-safe plots.
func (s *Service) EvaluateIndicator(ctx context.Context, req EvalIndicatorRequest) (EvalIndicatorResult, error) {
	normCfg, err := NormalizeBacktestConfig(BacktestConfig{
		Venue:    req.Venue,
		Symbol:   req.Symbol,
		Interval: req.Interval,
		Limit:    req.Limit,
		Params:   req.Params,
	})
	if err != nil {
		return EvalIndicatorResult{}, err
	}

	scriptID, _, prog, err := s.resolveProgram(ctx, req.ScriptID, req.Source)
	if err != nil {
		return EvalIndicatorResult{}, err
	}
	candles, err := s.fetchCandles(ctx, normCfg.Venue, normCfg.Symbol, normCfg.Interval, normCfg.Limit)
	if err != nil {
		return EvalIndicatorResult{}, err
	}

	ev, err := prog.Eval(candles, normCfg.Params)
	if err != nil {
		return EvalIndicatorResult{}, err
	}
	times := make([]int64, len(candles))
	for i, c := range candles {
		times[i] = c.Time
	}

	return EvalIndicatorResult{
		ScriptID: scriptID,
		Venue:    normCfg.Venue,
		Symbol:   normCfg.Symbol,
		Interval: normCfg.Interval,
		Times:    times,
		Inputs:   ev.Inputs,
		Params:   ev.Params,
		Plots:    ev.Plots,
		Signals:  ev.Signals,
		BarCount: ev.BarCount,
	}, nil
}

// RunBacktest executes a deterministic next-bar-open backtest without look-ahead.
func (s *Service) RunBacktest(ctx context.Context, req BacktestRequest) (BacktestResult, error) {
	cfg, err := NormalizeBacktestConfig(req.Config)
	if err != nil {
		return BacktestResult{}, err
	}
	scriptID, _, prog, err := s.resolveProgram(ctx, req.ScriptID, req.Source)
	if err != nil {
		return BacktestResult{}, err
	}
	candles, err := s.fetchCandles(ctx, cfg.Venue, cfg.Symbol, cfg.Interval, cfg.Limit)
	if err != nil {
		return BacktestResult{}, err
	}
	ev, err := prog.Eval(candles, cfg.Params)
	if err != nil {
		return BacktestResult{}, err
	}
	metrics, trades, equity := Simulate(candles, ev.Strategy, cfg)
	return BacktestResult{
		ScriptID: scriptID,
		Config:   cfg,
		Metrics:  metrics,
		Trades:   trades,
		Equity:   equity,
		Plots:    ev.Plots,
		Params:   ev.Params,
		BarCount: len(candles),
	}, nil
}

// RunValidation executes Gates 1-4 (plateau, bar permutations, walk-forward + holdout,
// WF permutations) and persists the resulting ValidationRunRecord into lab.sqlite.
func (s *Service) RunValidation(ctx context.Context, req ValidateRequest) (ValidationRunRecord, error) {
	cfg, err := NormalizeBacktestConfig(req.Config)
	if err != nil {
		return ValidationRunRecord{}, err
	}
	scriptID, scriptName, prog, err := s.resolveProgram(ctx, req.ScriptID, req.Source)
	if err != nil {
		return ValidationRunRecord{}, err
	}
	candles, err := s.fetchCandles(ctx, cfg.Venue, cfg.Symbol, cfg.Interval, cfg.Limit)
	if err != nil {
		return ValidationRunRecord{}, err
	}

	start := time.Now()
	report, err := RunFourGateValidation(ctx, candles, prog, cfg)
	if err != nil {
		return ValidationRunRecord{}, err
	}
	durMs := time.Since(start).Milliseconds()

	return s.store.insertRun(ctx, ValidationRunRecord{
		ScriptID:   scriptID,
		ScriptName: scriptName,
		Config:     cfg,
		Report:     report,
		DurationMs: durMs,
	})
}
