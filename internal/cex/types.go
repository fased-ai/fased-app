package cex

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"regexp"
	"slices"
	"strings"
)

// CEXActivationEnabled is a compile-time safety lock keeping mainnet CEX
// live order execution disabled until explicitly authorized by the owner.
const CEXActivationEnabled = false

const (
	VenueBinance  = "binance"
	VenueBybit    = "bybit"
	VenueOKX      = "okx"
	VenueKraken   = "kraken"
	VenueCoinbase = "coinbase"
	VenueDeribit  = "deribit"
)

var (
	// ErrExecutionDisabled is returned whenever a mainnet or disabled live CEX path is attempted.
	ErrExecutionDisabled = errors.New("cex live mainnet execution is disabled at compile time (CEXActivationEnabled=false)")
	// ErrWithdrawalScopeDenied is returned when credential scopes include withdrawal or fund-transfer permissions.
	ErrWithdrawalScopeDenied = errors.New("cex credentials must have withdrawal and transfer permissions disabled")
	// ErrCredentialNotFound indicates no sealed credential exists for the requested venue.
	ErrCredentialNotFound = errors.New("cex credential not configured")
)

var symbolPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,31}$`)

// VenueSpec describes a supported Stage 5 CEX venue and its auth/sandbox metadata.
type VenueSpec struct {
	Venue             string `json:"venue"`
	DisplayName       string `json:"displayName"`
	AuthScheme        string `json:"authScheme"`
	TestnetOrigin     string `json:"testnetOrigin"`
	TradeOnlyEnforced bool   `json:"tradeOnlyEnforced"`
}

var supportedVenues = []VenueSpec{
	{
		Venue:             VenueBinance,
		DisplayName:       "Binance",
		AuthScheme:        "hmac-sha256",
		TestnetOrigin:     "https://testnet.binance.vision",
		TradeOnlyEnforced: true,
	},
	{
		Venue:             VenueBybit,
		DisplayName:       "Bybit",
		AuthScheme:        "hmac-sha256-v5",
		TestnetOrigin:     "https://api-testnet.bybit.com",
		TradeOnlyEnforced: true,
	},
	{
		Venue:             VenueOKX,
		DisplayName:       "OKX",
		AuthScheme:        "hmac-sha256-base64",
		TestnetOrigin:     "https://www.okx.com",
		TradeOnlyEnforced: true,
	},
	{
		Venue:             VenueKraken,
		DisplayName:       "Kraken",
		AuthScheme:        "hmac-sha512",
		TestnetOrigin:     "https://demo-futures.kraken.com",
		TradeOnlyEnforced: true,
	},
	{
		Venue:             VenueCoinbase,
		DisplayName:       "Coinbase Advanced",
		AuthScheme:        "hmac-sha256 / ed25519",
		TestnetOrigin:     "https://api-sandbox.exchange.coinbase.com",
		TradeOnlyEnforced: true,
	},
	{
		Venue:             VenueDeribit,
		DisplayName:       "Deribit",
		AuthScheme:        "ed25519",
		TestnetOrigin:     "https://test.deribit.com",
		TradeOnlyEnforced: true,
	},
}

// SupportedVenues returns metadata for all supported CEX venues.
func SupportedVenues() []VenueSpec {
	return slices.Clone(supportedVenues)
}

// ValidVenue returns true for supported Stage 5 CEX venues.
func ValidVenue(venue string) bool {
	v := strings.ToLower(strings.TrimSpace(venue))
	for _, spec := range supportedVenues {
		if spec.Venue == v {
			return true
		}
	}
	return false
}

// LookupVenueSpec returns the canonical VenueSpec for a normalized venue slug.
func LookupVenueSpec(venue string) (VenueSpec, bool) {
	v := strings.ToLower(strings.TrimSpace(venue))
	for _, spec := range supportedVenues {
		if spec.Venue == v {
			return spec, true
		}
	}
	return VenueSpec{}, false
}

// CredentialInput carries API credentials for sealed local storage.
// Raw secrets are sealed at rest and never returned in status views.
type CredentialInput struct {
	Venue       string   `json:"venue"`
	Label       string   `json:"label"`
	Environment string   `json:"environment"`
	APIKey      string   `json:"apiKey"`
	APISecret   string   `json:"apiSecret"`
	Passphrase  string   `json:"passphrase,omitempty"`
	Permissions []string `json:"permissions"`
}

// CredentialStatus exposes non-secret metadata for a configured venue credential.
type CredentialStatus struct {
	Venue              string   `json:"venue"`
	Label              string   `json:"label"`
	Environment        string   `json:"environment"`
	KeyFingerprint     string   `json:"keyFingerprint"`
	Permissions        []string `json:"permissions"`
	WithdrawalDisabled bool     `json:"withdrawalDisabled"`
	UpdatedAt          int64    `json:"updatedAt"`
}

// OrderRequest represents a CEX spot/perp/futures order draft or testnet submission.
type OrderRequest struct {
	Venue         string  `json:"venue"`
	Symbol        string  `json:"symbol"`
	Side          string  `json:"side"`
	OrderType     string  `json:"orderType"`
	TimeInForce   string  `json:"timeInForce,omitempty"`
	Quantity      float64 `json:"quantity"`
	Price         float64 `json:"price,omitempty"`
	Environment   string  `json:"environment,omitempty"`
	SimulateOnly  bool    `json:"simulateOnly"`
	ClientOrderID string  `json:"clientOrderId,omitempty"`
}

// OrderReceipt records the outcome and cryptographic signature digest of a CEX order request.
type OrderReceipt struct {
	ID          string  `json:"id"`
	Venue       string  `json:"venue"`
	Symbol      string  `json:"symbol"`
	Side        string  `json:"side"`
	OrderType   string  `json:"orderType"`
	Quantity    float64 `json:"quantity"`
	Price       float64 `json:"price,omitempty"`
	Environment string  `json:"environment"`
	Status      string  `json:"status"`
	Simulated   bool    `json:"simulated"`
	Digest      string  `json:"digest"`
	Message     string  `json:"message"`
	CreatedAt   int64   `json:"createdAt"`
}

// StatusView summarizes Stage 5 CEX router state and non-secret credential metadata.
type StatusView struct {
	ActivationEnabled bool               `json:"activationEnabled"`
	MainnetLocked     bool               `json:"mainnetLocked"`
	CappedMode        bool               `json:"cappedMode,omitempty"`
	MaxOrderMicroUSD  uint64             `json:"maxOrderMicroUsd,omitempty"`
	KeyStorage        string             `json:"keyStorage,omitempty"`
	SupportedVenues   []VenueSpec        `json:"supportedVenues"`
	Credentials       []CredentialStatus `json:"credentials"`
	RecentOrders      []OrderReceipt     `json:"recentOrders"`
	FixtureMode       bool               `json:"fixtureMode"`
}

var forbiddenPermissionTokens = []string{
	"withdraw",
	"transfer",
	"universal",
	"payout",
	"send",
	"redeem",
	"borrow",
	"subaccount",
	"wallet",
	"sweep",
}

var requiredTradeScopes = map[string]bool{
	"trade":             true,
	"spot_trade":        true,
	"futures_trade":     true,
	"derivatives_trade": true,
	"orders":            true,
	"read_trade":        true,
}

// VerifyTradeOnlyPermissions enforces that a credential grants trading/read scopes
// while strictly excluding any withdrawal or internal/external fund-transfer scopes.
func VerifyTradeOnlyPermissions(venue string, perms []string) ([]string, error) {
	if !ValidVenue(venue) {
		return nil, errors.New("unsupported cex venue")
	}
	if len(perms) == 0 || len(perms) > 16 {
		return nil, errors.New("cex credential requires 1..16 declared permissions")
	}
	normalized := make([]string, 0, len(perms))
	seen := make(map[string]bool, len(perms))
	hasTradeScope := false
	for _, raw := range perms {
		p := strings.ToLower(strings.TrimSpace(raw))
		if p == "" || len(p) > 48 {
			return nil, errors.New("invalid cex permission scope")
		}
		for _, bad := range forbiddenPermissionTokens {
			if strings.Contains(p, bad) {
				return nil, ErrWithdrawalScopeDenied
			}
		}
		if requiredTradeScopes[p] {
			hasTradeScope = true
		}
		if !seen[p] {
			seen[p] = true
			normalized = append(normalized, p)
		}
	}
	if !hasTradeScope {
		return nil, errors.New("cex credential must include a trade permission scope")
	}
	return normalized, nil
}

// ValidateCredentialInput validates inputs before sealing into cex.sqlite.
func ValidateCredentialInput(in *CredentialInput) error {
	return validateCredentialInput(in, false)
}

func validateCredentialInput(in *CredentialInput, allowMainnetCapped bool) error {
	if in == nil {
		return errors.New("credential input required")
	}
	in.Venue = strings.ToLower(strings.TrimSpace(in.Venue))
	if !ValidVenue(in.Venue) {
		return errors.New("unsupported cex venue")
	}
	in.Label = strings.TrimSpace(in.Label)
	if len(in.Label) > 64 {
		return errors.New("credential label exceeds 64 characters")
	}
	if in.Label == "" {
		in.Label = in.Venue + "-trade-only"
	}
	in.Environment = strings.ToLower(strings.TrimSpace(in.Environment))
	if in.Environment == "" {
		in.Environment = "testnet"
	}
	if in.Environment == "mainnet" && !CEXActivationEnabled && !allowMainnetCapped {
		return ErrExecutionDisabled
	}
	if in.Environment != "testnet" && in.Environment != "sandbox" && in.Environment != "paper" && !(allowMainnetCapped && in.Environment == "mainnet") {
		return errors.New("cex environment must be testnet, sandbox, or paper")
	}
	in.APIKey = strings.TrimSpace(in.APIKey)
	in.APISecret = strings.TrimSpace(in.APISecret)
	in.Passphrase = strings.TrimSpace(in.Passphrase)
	if len(in.APIKey) < 6 || len(in.APIKey) > 256 || len(in.APISecret) < 8 || len(in.APISecret) > 512 {
		return errors.New("invalid cex apiKey or apiSecret length")
	}
	if in.Venue == VenueOKX && in.Passphrase == "" {
		return errors.New("okx credentials require a passphrase")
	}
	cleanPerms, err := VerifyTradeOnlyPermissions(in.Venue, in.Permissions)
	if err != nil {
		return err
	}
	in.Permissions = cleanPerms
	return nil
}

// ValidateOrderRequest validates and normalizes a CEX order request.
func ValidateOrderRequest(req *OrderRequest) error {
	return validateOrderRequest(req, 0)
}

func validateOrderRequest(req *OrderRequest, maxOrderMicroUSD uint64) error {
	allowMainnetCapped := maxOrderMicroUSD > 0
	if req == nil {
		return errors.New("order request required")
	}
	req.Venue = strings.ToLower(strings.TrimSpace(req.Venue))
	if !ValidVenue(req.Venue) {
		return errors.New("unsupported cex venue")
	}
	req.Symbol = strings.ToUpper(strings.TrimSpace(req.Symbol))
	if !symbolPattern.MatchString(req.Symbol) {
		return errors.New("invalid cex symbol")
	}
	req.Side = strings.ToUpper(strings.TrimSpace(req.Side))
	if req.Side != "BUY" && req.Side != "SELL" {
		return errors.New("order side must be BUY or SELL")
	}
	req.OrderType = strings.ToUpper(strings.TrimSpace(req.OrderType))
	if req.OrderType == "" {
		req.OrderType = "LIMIT"
	}
	if req.OrderType != "LIMIT" && req.OrderType != "MARKET" {
		return errors.New("order type must be LIMIT or MARKET")
	}
	if math.IsNaN(req.Quantity) || math.IsInf(req.Quantity, 0) || req.Quantity <= 0 {
		return errors.New("order quantity must be positive and finite")
	}
	if req.OrderType == "LIMIT" {
		if math.IsNaN(req.Price) || math.IsInf(req.Price, 0) || req.Price <= 0 {
			return errors.New("limit order price must be positive and finite")
		}
	} else if math.IsNaN(req.Price) || math.IsInf(req.Price, 0) || req.Price < 0 {
		return errors.New("order price must be finite")
	}
	req.TimeInForce = strings.ToUpper(strings.TrimSpace(req.TimeInForce))
	if req.TimeInForce == "" {
		req.TimeInForce = "GTC"
	}
	req.Environment = strings.ToLower(strings.TrimSpace(req.Environment))
	if req.Environment == "" {
		req.Environment = "paper"
	}
	if req.Environment == "mainnet" && !CEXActivationEnabled && !allowMainnetCapped {
		return ErrExecutionDisabled
	}
	if req.Environment != "paper" && req.Environment != "testnet" && req.Environment != "sandbox" && !(allowMainnetCapped && req.Environment == "mainnet") {
		return errors.New("cex environment must be paper, testnet, or sandbox")
	}
	if allowMainnetCapped && req.Environment == "mainnet" {
		if req.OrderType != "LIMIT" || req.Price <= 0 {
			return errors.New("mainnet-capped cex order requires LIMIT with positive price")
		}
		if req.Quantity*req.Price*1_000_000 > float64(maxOrderMicroUSD) {
			return errors.New("cex order notional exceeds mainnet-capped limit")
		}
	}
	req.ClientOrderID = strings.TrimSpace(req.ClientOrderID)
	if len(req.ClientOrderID) > 64 {
		return errors.New("clientOrderId exceeds 64 characters")
	}
	return nil
}

func keyFingerprint(apiKey string) string {
	sum := sha256.Sum256([]byte(apiKey))
	return "sha256:" + hex.EncodeToString(sum[:6])
}
