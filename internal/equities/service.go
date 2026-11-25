package equities

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Config parameterizes the Stage 5 equities & OCC options router.
type Config struct {
	DataDir     string
	FixtureMode bool
	Now         func() time.Time
}

// Service manages locked Paper equities/options quotes, OCC chains, and paper order routing.
type Service struct {
	store       *store
	client      *Client
	fixtureMode bool
	now         func() time.Time
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
	return StatusView{
		ActivationEnabled: EquitiesActivationEnabled,
		LiveLocked:        !EquitiesActivationEnabled,
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
	isOption, err := ValidateOrderRequest(&req)
	if err != nil {
		return OrderReceipt{}, err
	}
	idBytes := make([]byte, 8)
	_, _ = rand.Read(idBytes)
	nowUnix := s.now().UTC().Unix()
	status := "paper_simulated"
	msg := fmt.Sprintf("%s %s %.6g %s recorded in paper mode (live brokerage locked)", req.Venue, req.Side, req.Quantity, req.Symbol)
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
