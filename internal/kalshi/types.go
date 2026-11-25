package kalshi

import (
	"errors"
	"net/http"
)

const (
	// DemoAPIOrigin is the official Kalshi V2 Demo API origin used for Stage 4 rehearsal.
	DemoAPIOrigin = "https://demo-api.kalshi.co"

	// ProdAPIOrigin is the official Kalshi V2 Production API origin (locked while !signer.KalshiActivationEnabled).
	ProdAPIOrigin = "https://api.elections.kalshi.com"
)

// ErrExecutionDisabled is returned whenever Kalshi production mutations are attempted
// while signer.KalshiActivationEnabled is false.
var ErrExecutionDisabled = errors.New("Kalshi production execution is disabled")

// ControlledTransport is a Go-test-only HTTP RoundTripper for isolated Kalshi simulation.
type ControlledTransport interface {
	http.RoundTripper
	ControlledFixture() bool
}

// MarketSummary normalizes open market metadata returned by GET /trade-api/v2/markets.
type MarketSummary struct {
	Ticker           string `json:"ticker"`
	EventTicker      string `json:"event_ticker"`
	Title            string `json:"title"`
	Subtitle         string `json:"subtitle,omitempty"`
	Status           string `json:"status"`
	YesBid           int    `json:"yes_bid,omitempty"`
	YesAsk           int    `json:"yes_ask,omitempty"`
	LastPrice        int    `json:"last_price,omitempty"`
	YesBidDollars    string `json:"yes_bid_dollars,omitempty"`
	YesAskDollars    string `json:"yes_ask_dollars,omitempty"`
	LastPriceDollars string `json:"last_price_dollars,omitempty"`
	Volume24h        int64  `json:"volume_24h,omitempty"`
	OpenInterest     int64  `json:"open_interest,omitempty"`
}

// OrderBook holds bounded bid/ask depth levels for a Kalshi event market.
type OrderBook struct {
	Ticker     string `json:"ticker"`
	YesCents   int    `json:"yesLevels"`
	NoCents    int    `json:"noLevels"`
	BestYesBid string `json:"bestYesBid,omitempty"`
	BestNoBid  string `json:"bestNoBid,omitempty"`
}

// OrderDraft contains operator inputs before fixed-point risk quantification and RSA-PSS signing.
type OrderDraft struct {
	Ticker        string `json:"ticker"`
	Side          string `json:"side"` // bid or ask
	Count         string `json:"count"`
	Price         string `json:"price"`
	TimeInForce   string `json:"timeInForce,omitempty"` // good_till_canceled, immediate_or_cancel, fill_or_kill
	ClientOrderID string `json:"clientOrderId,omitempty"`
	OperationID   string `json:"operationId,omitempty"`
	ReduceOnly    bool   `json:"reduceOnly,omitempty"`
	Asset         string `json:"asset,omitempty"`
}

// ValidatedOrder holds normalized fixed-point strings and exact worst-case collateral microUSD risk.
type ValidatedOrder struct {
	Draft             OrderDraft `json:"draft"`
	CountFormatted    string     `json:"countFormatted"`
	PriceFormatted    string     `json:"priceFormatted"`
	EstimatedMicroUSD uint64     `json:"estimatedMicroUsd"`
	PayloadJSON       []byte     `json:"-"`
}

// BalanceResponse summarizes authenticated portfolio balance from GET /trade-api/v2/portfolio/balance.
type BalanceResponse struct {
	Balance        int64  `json:"balance"`
	PortfolioValue int64  `json:"portfolio_value,omitempty"`
	UpdatedTs      int64  `json:"updated_ts,omitempty"`
	RawStatus      int    `json:"statusCode"`
	Message        string `json:"message,omitempty"`
}

// OrderSubmissionResult summarizes a POST or DELETE order call on Kalshi V2.
type OrderSubmissionResult struct {
	Submitted     bool   `json:"submitted"`
	StatusCode    int    `json:"statusCode,omitempty"`
	OrderID       string `json:"orderId,omitempty"`
	ClientOrderID string `json:"clientOrderId,omitempty"`
	Status        string `json:"status,omitempty"`
	UpstreamCode  string `json:"upstreamCode,omitempty"`
	Message       string `json:"message,omitempty"`
}
