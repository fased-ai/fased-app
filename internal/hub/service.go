package hub

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Config parameterizes the Stage 6 Community Hub service.
type Config struct {
	DataDir     string
	FixtureMode bool
	Now         func() time.Time
}

// Service manages signed Community Hub package verification, storage, and installation.
type Service struct {
	store       *store
	fixtureMode bool
	now         func() time.Time
}

// NewService opens <dataDir>/hub/hub.sqlite and seeds verified fixture packages when empty in FixtureMode.
func NewService(cfg Config) (*Service, error) {
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	st, err := openStore(cfg.DataDir, nowFn)
	if err != nil {
		return nil, err
	}
	svc := &Service{
		store:       st,
		fixtureMode: cfg.FixtureMode,
		now:         nowFn,
	}
	if err := svc.seedDefaultPackages(context.Background()); err != nil {
		_ = st.close()
		return nil, err
	}
	return svc, nil
}

// Close releases the Hub SQLite connection.
func (s *Service) Close() error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.close()
}

func deterministicFixtureKey(label string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("fased-hub-fixture-author:" + label))
	return ed25519.NewKeyFromSeed(seed[:])
}

func (s *Service) seedDefaultPackages(ctx context.Context) error {
	existing, err := s.store.listPackages(ctx)
	if err != nil || len(existing) > 0 {
		return err
	}
	priv := deterministicFixtureKey("community-publisher")
	nowUnix := s.now().UTC().Unix()
	samples := []Package{
		{
			ID:      "hub-momentum-rsi",
			Name:    "Momentum RSI Filter",
			Kind:    KindFasedScript,
			Version: "1.0.0",
			Payload: `const fast = ema(close, 12);
const r = rsi(close, 14);
const bull = crossunder(r, 32) || (close > fast && r > 52);
signal("long", bull);`,
			SATBond: SATBondMetadata{
				AmountSAT: 250,
				Status:    StatusDeferredWEN,
			},
			CreatedAt: nowUnix - 120,
		},
		{
			ID:        "hub-macro-watchlist",
			Name:      "Stage 5 Multi-Asset Watchlist",
			Kind:      KindWatchlistPack,
			Version:   "1.0.0",
			Payload:   `{"venues":["hyperliquid","binance","okx","alpaca"],"symbols":["BTC","ETH","SOL","SPY","NVDA"]}`,
			SATBond:   SATBondMetadata{AmountSAT: 100, Status: StatusDeferredWEN},
			CreatedAt: nowUnix - 60,
		},
	}
	for i := range samples {
		if err := SignPackage(priv, &samples[i]); err != nil {
			return err
		}
		if err := VerifyPackage(&samples[i]); err != nil {
			return err
		}
		if err := s.store.savePackage(ctx, samples[i]); err != nil {
			return err
		}
	}
	return nil
}

// List returns Community Hub packages along with the WEN SAT hold metadata.
func (s *Service) List(ctx context.Context) (ListView, error) {
	if s == nil || s.store == nil {
		return ListView{}, errors.New("hub service closed")
	}
	pkgs, err := s.store.listPackages(ctx)
	if err != nil {
		return ListView{}, err
	}
	return ListView{
		WENHeld:     true,
		SATStatus:   StatusDeferredWEN,
		Packages:    pkgs,
		FixtureMode: s.fixtureMode,
	}, nil
}

// Publish verifies the Ed25519 signature, size limit, and (for fasedscript) AST compilation
// before persisting a package into hub.sqlite.
func (s *Service) Publish(ctx context.Context, req PublishRequest) (Package, error) {
	if s == nil || s.store == nil {
		return Package{}, errors.New("hub service closed")
	}
	pkg := Package{
		ID:              req.ID,
		Name:            req.Name,
		Kind:            req.Kind,
		Version:         req.Version,
		AuthorPubKeyHex: req.AuthorPubKeyHex,
		Payload:         req.Payload,
		SignatureHex:    req.SignatureHex,
		SATBond: SATBondMetadata{
			AmountSAT: req.AmountSAT,
			Status:    StatusDeferredWEN,
		},
		CreatedAt: s.now().UTC().Unix(),
	}
	if err := VerifyPackage(&pkg); err != nil {
		return Package{}, err
	}
	// TOFU author-key pinning: refuse re-publishing an existing package ID under a different Ed25519 key.
	if existing, gErr := s.store.getPackage(ctx, pkg.ID); gErr == nil {
		if !strings.EqualFold(existing.AuthorPubKeyHex, pkg.AuthorPubKeyHex) {
			return Package{}, fmt.Errorf("%w (hub TOFU violation)", ErrAuthorMismatch)
		}
	}
	if err := s.store.savePackage(ctx, pkg); err != nil {
		return Package{}, err
	}
	return pkg, nil
}

// GetVerified fetches and re-verifies a stored package's signature and AST without marking it installed.
func (s *Service) GetVerified(ctx context.Context, packageID string) (Package, error) {
	if s == nil || s.store == nil {
		return Package{}, errors.New("hub service closed")
	}
	cleanID := strings.ToLower(strings.TrimSpace(packageID))
	pkg, err := s.store.getPackage(ctx, cleanID)
	if err != nil {
		return Package{}, err
	}
	if err := VerifyPackage(&pkg); err != nil {
		return Package{}, err
	}
	return pkg, nil
}

// Install re-verifies signature and AST integrity of a stored package before marking it installed.
func (s *Service) Install(ctx context.Context, packageID string) (Package, error) {
	if s == nil || s.store == nil {
		return Package{}, errors.New("hub service closed")
	}
	cleanID := strings.ToLower(strings.TrimSpace(packageID))
	pkg, err := s.store.getPackage(ctx, cleanID)
	if err != nil {
		return Package{}, err
	}
	if err := VerifyPackage(&pkg); err != nil {
		return Package{}, err
	}
	if err := s.store.markInstalled(ctx, cleanID); err != nil {
		return Package{}, err
	}
	pkg.Installed = true
	return pkg, nil
}
