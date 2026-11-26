package equities

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const hardMaxCappedOrderMicroUSD uint64 = 250_000_000

// Config parameterizes the Stage 5 equities & OCC options router.
type Config struct {
	DataDir         string
	FixtureMode     bool
	Now             func() time.Time
	HTTPTransport   http.RoundTripper
	AlpacaKeyID     string
	AlpacaSecretKey string
}

// Service manages locked Paper equities/options quotes, OCC chains, and paper order routing.
type Service struct {
	store            *store
	client           *Client
	fixtureMode      bool
	alpacaKeyID      string
	alpacaSecretKey  string
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

func resolveEnvCred(explicit, primaryEnv, fallbackEnv string) string {
	if v := strings.TrimSpace(explicit); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv(primaryEnv)); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv(fallbackEnv))
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
		store:           st,
		client:          NewClientWithTransport(cfg.HTTPTransport),
		fixtureMode:     cfg.FixtureMode,
		alpacaKeyID:     resolveEnvCred(cfg.AlpacaKeyID, "FASED_ALPACA_API_KEY", "APCA_API_KEY_ID"),
		alpacaSecretKey: resolveEnvCred(cfg.AlpacaSecretKey, "FASED_ALPACA_API_SECRET", "APCA_API_SECRET_KEY"),
		now:             nowFn,
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

// GetQuote returns a live Alpaca IEX snapshot quote when configured and non-fixture,
// or falls back to deterministic paper quote fixtures cleanly.
func (s *Service) GetQuote(ctx context.Context, symbol string) (Quote, error) {
	clean := strings.ToUpper(strings.TrimSpace(symbol))
	if !tickerPattern.MatchString(clean) {
		return Quote{}, errors.New("invalid equity ticker symbol")
	}
	if !s.fixtureMode && s.alpacaKeyID != "" && s.alpacaSecretKey != "" {
		qCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		return s.client.FetchAlpacaSnapshotQuote(qCtx, clean, s.alpacaKeyID, s.alpacaSecretKey, s.now().UTC().Unix())
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
