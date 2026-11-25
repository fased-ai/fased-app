package equities

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// EquitiesActivationEnabled is a compile-time safety lock keeping live
// Alpaca/IBKR brokerage execution disabled until explicitly authorized.
const EquitiesActivationEnabled = false

const (
	VenueAlpaca = "alpaca"
	VenueIBKR   = "ibkr"

	AlpacaPaperOrigin = "https://paper-api.alpaca.markets"
	AlpacaLiveOrigin  = "https://api.alpaca.markets"
	IBKRLocalOrigin   = "https://127.0.0.1:5000"
)

var (
	// ErrExecutionDisabled is returned whenever live mainnet/brokerage execution is attempted while locked.
	ErrExecutionDisabled = errors.New("equities live execution is disabled at compile time (EquitiesActivationEnabled=false)")

	tickerPattern    = regexp.MustCompile(`^[A-Z]{1,6}(?:\.[A-Z])?$`)
	occCompactRegex  = regexp.MustCompile(`^([A-Z ]{1,6})(\d{6})([CP])(\d{8})$`)
	ibkrPaperPattern = regexp.MustCompile(`^DU[0-9]{4,12}$`)
)

// OCCOption represents a parsed 21-character Options Clearing Corporation contract identifier.
type OCCOption struct {
	Underlying string  `json:"underlying"`
	Expiration string  `json:"expiration"` // YYYY-MM-DD
	Right      string  `json:"right"`      // C or P
	Strike     float64 `json:"strike"`
	Canonical  string  `json:"canonical"` // 21-char space-padded OCC symbol
}

// ParseOCCSymbol validates either a canonical 21-char OCC symbol (e.g. "SPY   261218C00600000")
// or compact OCC form (e.g. "SPY261218C00600000") and normalizes it to 21 chars.
func ParseOCCSymbol(raw string) (OCCOption, error) {
	trimmed := strings.ToUpper(strings.Trim(raw, "\r\n\t"))
	var root, yymmdd, right, strikeDigits string
	if len(trimmed) == 21 {
		root = strings.TrimRight(trimmed[:6], " ")
		yymmdd = trimmed[6:12]
		right = trimmed[12:13]
		strikeDigits = trimmed[13:21]
		if !tickerPattern.MatchString(root) || (right != "C" && right != "P") {
			return OCCOption{}, errors.New("invalid 21-char OCC symbol")
		}
	} else {
		m := occCompactRegex.FindStringSubmatch(strings.ReplaceAll(trimmed, " ", ""))
		if len(m) != 5 {
			return OCCOption{}, errors.New("invalid OCC option symbol format")
		}
		root, yymmdd, right, strikeDigits = strings.TrimSpace(m[1]), m[2], m[3], m[4]
		if !tickerPattern.MatchString(root) {
			return OCCOption{}, errors.New("invalid OCC underlying symbol")
		}
	}
	expTime, err := time.Parse("060102", yymmdd)
	if err != nil {
		return OCCOption{}, errors.New("invalid OCC expiration date")
	}
	mills, err := strconv.ParseInt(strikeDigits, 10, 64)
	if err != nil || mills <= 0 {
		return OCCOption{}, errors.New("invalid OCC strike price")
	}
	strike := float64(mills) / 1000.0
	canon := fmt.Sprintf("%-6s%s%s%08d", root, yymmdd, right, mills)
	return OCCOption{
		Underlying: root,
		Expiration: expTime.Format("2006-01-02"),
		Right:      right,
		Strike:     strike,
		Canonical:  canon,
	}, nil
}

// FormatOCCSymbol formats a normalized 21-character OCC contract identifier.
func FormatOCCSymbol(underlying, expYYYYMMDD, right string, strike float64) (string, error) {
	root := strings.ToUpper(strings.TrimSpace(strings.ReplaceAll(underlying, " ", "")))
	if !tickerPattern.MatchString(root) {
		return "", errors.New("invalid underlying symbol")
	}
	expTime, err := time.Parse("2006-01-02", strings.TrimSpace(expYYYYMMDD))
	if err != nil {
		return "", errors.New("expiration must be YYYY-MM-DD")
	}
	r := strings.ToUpper(strings.TrimSpace(right))
	if r != "C" && r != "P" {
		return "", errors.New("option right must be C or P")
	}
	if math.IsNaN(strike) || math.IsInf(strike, 0) || strike <= 0 || strike > 99999.999 {
		return "", errors.New("invalid option strike")
	}
	mills := int64(math.Round(strike * 1000.0))
	return fmt.Sprintf("%-6s%s%s%08d", root, expTime.Format("060102"), r, mills), nil
}

// Quote represents an equity or ETF top-of-book quote.
type Quote struct {
	Symbol    string  `json:"symbol"`
	Bid       float64 `json:"bid"`
	Ask       float64 `json:"ask"`
	Last      float64 `json:"last"`
	Volume    int64   `json:"volume"`
	Session   string  `json:"session"`
	Source    string  `json:"source"`
	UpdatedAt int64   `json:"updatedAt"`
}

// OptionContract describes one option quote + greeks row in an OCC chain.
type OptionContract struct {
	OCCSymbol         string  `json:"occSymbol"`
	Underlying        string  `json:"underlying"`
	Expiration        string  `json:"expiration"`
	Right             string  `json:"right"`
	Strike            float64 `json:"strike"`
	Bid               float64 `json:"bid"`
	Ask               float64 `json:"ask"`
	ImpliedVolatility float64 `json:"impliedVolatility"`
	Delta             float64 `json:"delta"`
	OpenInterest      int64   `json:"openInterest"`
}

// OptionsChainView returns a bounded OCC options chain for a stock/ETF underlying.
type OptionsChainView struct {
	Underlying string           `json:"underlying"`
	SpotPrice  float64          `json:"spotPrice"`
	Contracts  []OptionContract `json:"contracts"`
	Source     string           `json:"source"`
	Fixture    bool             `json:"fixture"`
	UpdatedAt  int64            `json:"updatedAt"`
}

// OrderRequest submits a paper equities or OCC option order through Alpaca Paper or IBKR DU* Paper.
type OrderRequest struct {
	Venue        string  `json:"venue"`
	AccountID    string  `json:"accountId,omitempty"`
	Symbol       string  `json:"symbol"`
	Side         string  `json:"side"`
	OrderType    string  `json:"orderType"`
	TimeInForce  string  `json:"timeInForce,omitempty"`
	Quantity     float64 `json:"quantity"`
	LimitPrice   float64 `json:"limitPrice,omitempty"`
	Environment  string  `json:"environment,omitempty"`
	SimulateOnly bool    `json:"simulateOnly"`
}

// OrderReceipt records the paper routing outcome for an equities or OCC option order.
type OrderReceipt struct {
	ID          string  `json:"id"`
	Venue       string  `json:"venue"`
	AccountID   string  `json:"accountId,omitempty"`
	Symbol      string  `json:"symbol"`
	IsOption    bool    `json:"isOption"`
	Side        string  `json:"side"`
	OrderType   string  `json:"orderType"`
	Quantity    float64 `json:"quantity"`
	LimitPrice  float64 `json:"limitPrice,omitempty"`
	Environment string  `json:"environment"`
	Status      string  `json:"status"`
	Simulated   bool    `json:"simulated"`
	Message     string  `json:"message"`
	CreatedAt   int64   `json:"createdAt"`
}

// StatusView summarizes Stage 5 equities/options router state and active paper guardrails.
type StatusView struct {
	ActivationEnabled bool           `json:"activationEnabled"`
	LiveLocked        bool           `json:"liveLocked"`
	AlpacaPaperOrigin string         `json:"alpacaPaperOrigin"`
	IBKRLocalOrigin   string         `json:"ibkrLocalOrigin"`
	RecentOrders      []OrderReceipt `json:"recentOrders"`
	FixtureMode       bool           `json:"fixtureMode"`
}

// ValidateVenueOrigin enforces compile-time live locks: Alpaca is restricted to Paper
// endpoints and IBKR is restricted to loopback localhost Client Portal Gateway with DU* accounts.
func ValidateVenueOrigin(venue, env, rawURL, accountID string) error {
	v := strings.ToLower(strings.TrimSpace(venue))
	e := strings.ToLower(strings.TrimSpace(env))
	if e == "" {
		e = "paper"
	}
	if e == "live" && !EquitiesActivationEnabled {
		return ErrExecutionDisabled
	}
	switch v {
	case VenueAlpaca:
		if e != "paper" {
			return ErrExecutionDisabled
		}
		if rawURL != "" {
			u, err := url.Parse(rawURL)
			if err != nil || u.Scheme != "https" || u.User != nil {
				return errors.New("invalid alpaca url")
			}
			canon := "https://" + strings.ToLower(u.Host)
			if canon != AlpacaPaperOrigin {
				return ErrExecutionDisabled
			}
		}
		return nil
	case VenueIBKR:
		if e != "paper" {
			return ErrExecutionDisabled
		}
		if acct := strings.ToUpper(strings.TrimSpace(accountID)); acct != "" && !ibkrPaperPattern.MatchString(acct) {
			return errors.New("ibkr account must be a DU* paper account while EquitiesActivationEnabled is false")
		}
		if rawURL != "" {
			u, err := url.Parse(rawURL)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
				return errors.New("invalid ibkr gateway url")
			}
			host := strings.ToLower(u.Hostname())
			if host != "127.0.0.1" && host != "localhost" {
				if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
					return errors.New("ibkr gateway url must resolve to localhost loopback")
				}
			}
		}
		return nil
	default:
		return errors.New("equities venue must be alpaca or ibkr")
	}
}

// ValidateOrderRequest normalizes and validates an equity/OCC option order request.
func ValidateOrderRequest(req *OrderRequest) (bool, error) {
	if req == nil {
		return false, errors.New("order request required")
	}
	req.Venue = strings.ToLower(strings.TrimSpace(req.Venue))
	req.Environment = strings.ToLower(strings.TrimSpace(req.Environment))
	if req.Environment == "" {
		req.Environment = "paper"
	}
	req.AccountID = strings.ToUpper(strings.TrimSpace(req.AccountID))
	if err := ValidateVenueOrigin(req.Venue, req.Environment, "", req.AccountID); err != nil {
		return false, err
	}
	symUpper := strings.ToUpper(strings.TrimSpace(req.Symbol))
	isOption := false
	if opt, err := ParseOCCSymbol(symUpper); err == nil {
		req.Symbol = opt.Canonical
		isOption = true
	} else {
		cleanSym := strings.ReplaceAll(symUpper, " ", "")
		if !tickerPattern.MatchString(cleanSym) {
			return false, errors.New("invalid equity ticker or 21-char OCC option symbol")
		}
		req.Symbol = cleanSym
	}
	req.Side = strings.ToUpper(strings.TrimSpace(req.Side))
	if req.Side != "BUY" && req.Side != "SELL" {
		return false, errors.New("order side must be BUY or SELL")
	}
	req.OrderType = strings.ToUpper(strings.TrimSpace(req.OrderType))
	if req.OrderType == "" {
		req.OrderType = "LIMIT"
	}
	if req.OrderType != "LIMIT" && req.OrderType != "MARKET" {
		return false, errors.New("order type must be LIMIT or MARKET")
	}
	if math.IsNaN(req.Quantity) || math.IsInf(req.Quantity, 0) || req.Quantity <= 0 {
		return false, errors.New("order quantity must be positive and finite")
	}
	if req.OrderType == "LIMIT" {
		if math.IsNaN(req.LimitPrice) || math.IsInf(req.LimitPrice, 0) || req.LimitPrice <= 0 {
			return false, errors.New("limit order requires positive limitPrice")
		}
	} else if math.IsNaN(req.LimitPrice) || math.IsInf(req.LimitPrice, 0) || req.LimitPrice < 0 {
		return false, errors.New("limitPrice must be finite")
	}
	req.TimeInForce = strings.ToUpper(strings.TrimSpace(req.TimeInForce))
	if req.TimeInForce == "" {
		req.TimeInForce = "DAY"
	}
	return isOption, nil
}
