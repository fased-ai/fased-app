package cex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Config parameterizes the Stage 5 CEX execution service.
type Config struct {
	DataDir     string
	FixtureMode bool
	Now         func() time.Time
}

// Service coordinates trade-only CEX credential sealing, permission checks,
// multi-venue signing, and compile-time locked paper/testnet order routing.
type Service struct {
	store       *store
	client      *Client
	fixtureMode bool
	now         func() time.Time
}

// NewService opens <dataDir>/cex/cex.sqlite and initializes an SSRF-safe CEX router.
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

// Close releases SQLite connections and wipes in-memory master key material.
func (s *Service) Close() error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.close()
}

// Status returns Stage 5 CEX locks, venue metadata, non-secret credentials, and recent orders.
func (s *Service) Status(ctx context.Context) (StatusView, error) {
	if s == nil || s.store == nil {
		return StatusView{}, errors.New("cex service closed")
	}
	creds, err := s.store.listCredentials(ctx)
	if err != nil {
		return StatusView{}, err
	}
	orders, err := s.store.listOrders(ctx, 25)
	if err != nil {
		return StatusView{}, err
	}
	return StatusView{
		ActivationEnabled: CEXActivationEnabled,
		MainnetLocked:     !CEXActivationEnabled,
		SupportedVenues:   SupportedVenues(),
		Credentials:       creds,
		RecentOrders:      orders,
		FixtureMode:       s.fixtureMode,
	}, nil
}

// SaveCredential validates trade-only scopes (denying withdrawal/transfer permissions),
// refuses mainnet when CEXActivationEnabled is false, and seals the credential at rest.
func (s *Service) SaveCredential(ctx context.Context, in CredentialInput) (CredentialStatus, error) {
	if s == nil || s.store == nil {
		return CredentialStatus{}, errors.New("cex service closed")
	}
	if err := ValidateCredentialInput(&in); err != nil {
		return CredentialStatus{}, err
	}
	defer func() {
		in.APIKey = ""
		in.APISecret = ""
		in.Passphrase = ""
	}()
	return s.store.saveCredential(ctx, in)
}

// DeleteCredential removes sealed credentials for a CEX venue.
func (s *Service) DeleteCredential(ctx context.Context, venue string) error {
	if s == nil || s.store == nil {
		return errors.New("cex service closed")
	}
	v := strings.ToLower(strings.TrimSpace(venue))
	if !ValidVenue(v) {
		return errors.New("unsupported cex venue")
	}
	return s.store.deleteCredential(ctx, v)
}

// SubmitOrder validates order bounds, enforces CEXActivationEnabled compile lock
// and trade-only credential scopes, computes a signed envelope digest, and records
// a deterministic paper/sandbox receipt.
func (s *Service) SubmitOrder(ctx context.Context, req OrderRequest) (OrderReceipt, error) {
	if s == nil || s.store == nil {
		return OrderReceipt{}, errors.New("cex service closed")
	}
	if err := ValidateOrderRequest(&req); err != nil {
		return OrderReceipt{}, err
	}

	var apiKey, secret string
	st, payload, err := s.store.loadSealedCredential(ctx, req.Venue)
	if err != nil && !errors.Is(err, ErrCredentialNotFound) {
		return OrderReceipt{}, err
	}
	if err == nil {
		if !st.WithdrawalDisabled {
			return OrderReceipt{}, ErrWithdrawalScopeDenied
		}
		if _, pErr := VerifyTradeOnlyPermissions(req.Venue, st.Permissions); pErr != nil {
			return OrderReceipt{}, pErr
		}
		apiKey = payload.APIKey
		secret = payload.APISecret
		defer func() {
			payload.APIKey = ""
			payload.APISecret = ""
			payload.Passphrase = ""
		}()
	}

	now := s.now().UTC()
	tsMs := fmt.Sprintf("%d", now.UnixMilli())
	digest, err := SignOrderDigest(req.Venue, apiKey, secret, tsMs, req)
	if err != nil {
		return OrderReceipt{}, err
	}

	idBytes := make([]byte, 8)
	_, _ = rand.Read(idBytes)
	orderID := "cex-" + hex.EncodeToString(idBytes)

	status := "paper_simulated"
	msg := fmt.Sprintf("%s %s %.6g %s recorded in paper mode (mainnet locked)", req.Venue, req.Side, req.Quantity, req.Symbol)
	if !req.SimulateOnly && req.Environment != "paper" && secret != "" {
		status = "testnet_signed_dryrun"
		msg = fmt.Sprintf("%s %s %.6g %s signed for %s sandbox (live mainnet locked)", req.Venue, req.Side, req.Quantity, req.Symbol, req.Environment)
	}

	rec := OrderReceipt{
		ID:          orderID,
		Venue:       req.Venue,
		Symbol:      req.Symbol,
		Side:        req.Side,
		OrderType:   req.OrderType,
		Quantity:    req.Quantity,
		Price:       req.Price,
		Environment: req.Environment,
		Status:      status,
		Simulated:   true,
		Digest:      digest,
		Message:     msg,
		CreatedAt:   now.Unix(),
	}
	if err := s.store.appendOrder(ctx, rec); err != nil {
		return OrderReceipt{}, err
	}
	return rec, nil
}
