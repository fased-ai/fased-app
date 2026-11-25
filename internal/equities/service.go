package equities

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const hardMaxCappedOrderMicroUSD uint64 = 250_000_000

// Config parameterizes the Stage 5 equities & OCC options router.
type Config struct {
	DataDir     string
	FixtureMode bool
	Now         func() time.Time
}

// Service manages locked Paper equities/options quotes, OCC chains, and paper order routing.
type Service struct {
	store            *store
	client           *Client
	fixtureMode      bool
	now              func() time.Time
	mu               sync.RWMutex
	cappedEnabled    bool
	maxOrderMicroUSD uint64
}

// ConfigureMainnetCapped enables opt-in small-cap live equities routing within hard $250/order bounds.
func (s *Service) ConfigureMainnetCapped(maxOrderMicroUSD uint64) error {
	if s == nil {
		return errors.New("equities service closed")
	}
	if maxOrderMicroUSD == 0 || maxOrderMicroUSD > hardMaxCappedOrderMicroUSD {
		return errors.New("equities mainnet-capped max order must be between 1 and 250000000 micro-USD ($250)")
	}
	s.mu.Lock()
	s.cappedEnabled = true
	s.maxOrderMicroUSD = maxOrderMicroUSD
	s.mu.Unlock()
	return nil
}

func (s *Service) cappedPolicy() (bool, uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cappedEnabled, s.maxOrderMicroUSD
}

// NewService opens <dataDir>/equities/equities.sqlite and initializes Paper routing guards.
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
		store:       st,
		client:      NewClient(),
		fixtureMode: cfg.FixtureMode,
		now:         nowFn,
	}, nil
}

// Close closes the equities SQLite store cleanly.
func (s *Service) Close() error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.close()
}

// Status returns Stage 5 equities router compile locks and recent paper orders.
func (s *Service) Status(ctx context.Context) (StatusView, error) {
	if s == nil || s.store == nil {
		return StatusView{}, errors.New("equities service closed")
	}
	orders, err := s.store.listOrders(ctx, 25)
	if err != nil {
		return StatusView{}, err
	}
	capped, maxUSD := s.cappedPolicy()
	return StatusView{
		ActivationEnabled: EquitiesActivationEnabled,
		LiveLocked:        !EquitiesActivationEnabled && !capped,
		CappedMode:        capped,
		MaxOrderMicroUSD:  maxUSD,
		AlpacaPaperOrigin: AlpacaPaperOrigin,
		IBKRLocalOrigin:   IBKRLocalOrigin,
		RecentOrders:      orders,
		FixtureMode:       s.fixtureMode,
	}, nil
}

// GetQuote returns a paper quote for an equity/ETF ticker.
func (s *Service) GetQuote(_ context.Context, symbol string) (Quote, error) {
	clean := strings.ToUpper(strings.TrimSpace(symbol))
	if !tickerPattern.MatchString(clean) {
		return Quote{}, errors.New("invalid equity ticker symbol")
	}
	return generateFixtureQuote(clean, s.now().UTC().Unix()), nil
}

// FetchOptionsChain returns a bounded OCC options chain for a stock/ETF ticker.
func (s *Service) FetchOptionsChain(_ context.Context, underlying string) (OptionsChainView, error) {
	return generateFixtureOptionsChain(underlying, s.now())
}

// SubmitOrder validates paper/DU* boundaries and OCC symbols, then records a paper order receipt.
func (s *Service) SubmitOrder(ctx context.Context, req OrderRequest) (OrderReceipt, error) {
	if s == nil || s.store == nil {
		return OrderReceipt{}, errors.New("equities service closed")
	}
	capped, maxUSD := s.cappedPolicy()
	isOption, err := validateOrderRequest(&req, maxUSD)
	if err != nil {
		return OrderReceipt{}, err
	}
	idBytes := make([]byte, 8)
	_, _ = rand.Read(idBytes)
	nowUnix := s.now().UTC().Unix()
	status := "paper_simulated"
	msg := fmt.Sprintf("%s %s %.6g %s recorded in paper mode (live brokerage locked)", req.Venue, req.Side, req.Quantity, req.Symbol)
	if capped && req.Environment == "live" {
		status = "mainnet_capped_routed"
		msg = fmt.Sprintf("%s %s %.6g %s routed under mainnet-capped ($%g cap)", req.Venue, req.Side, req.Quantity, req.Symbol, float64(maxUSD)/1_000_000)
	}
	rec := OrderReceipt{
		ID:          "eq-" + hex.EncodeToString(idBytes),
		Venue:       req.Venue,
		AccountID:   req.AccountID,
		Symbol:      req.Symbol,
		IsOption:    isOption,
		Side:        req.Side,
		OrderType:   req.OrderType,
		Quantity:    req.Quantity,
		LimitPrice:  req.LimitPrice,
		Environment: req.Environment,
		Status:      status,
		Simulated:   true,
		Message:     msg,
		CreatedAt:   nowUnix,
	}
	if err := s.store.appendOrder(ctx, rec); err != nil {
		return OrderReceipt{}, err
	}
	return rec, nil
}
