package polymarket

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

const (
	// MainnetCLOBOrigin is the pinned official Polymarket CLOB API origin (public read-only while locked).
	MainnetCLOBOrigin = "https://clob.polymarket.com"
)

// ErrExecutionDisabled is returned whenever live Polymarket Mainnet order submission or
// cancellation is attempted without an explicitly controlled Amoy testnet transport.
var ErrExecutionDisabled = errors.New("Polymarket live mainnet execution is disabled")

// ControlledTransport is a Go-test-only HTTP RoundTripper for isolated Polymarket simulation.
type ControlledTransport interface {
	http.RoundTripper
	ControlledFixture() bool
}

// FlexText decodes either a JSON string or JSON scalar number cleanly into a string.
type FlexText string

// UnmarshalJSON implements json.Unmarshaler for Polymarket fields that vary between string and number.
func (f *FlexText) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*f = ""
		return nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		*f = FlexText(s)
		return nil
	}
	*f = FlexText(strings.TrimSpace(string(trimmed)))
	return nil
}

// MarketToken describes one conditional outcome token inside a Polymarket CLOB market.
type MarketToken struct {
	TokenID string   `json:"token_id"`
	Outcome string   `json:"outcome"`
	Price   FlexText `json:"price,omitempty"`
}

// MarketSummary represents normalized public market metadata from GET /markets or /sampling-markets.
type MarketSummary struct {
	ConditionID      string        `json:"condition_id"`
	Question         string        `json:"question"`
	MarketSlug       string        `json:"market_slug"`
	Active           bool          `json:"active"`
	Closed           bool          `json:"closed"`
	NegRisk          bool          `json:"neg_risk"`
	MinimumTickSize  FlexText      `json:"minimum_tick_size,omitempty"`
	MinimumOrderSize FlexText      `json:"minimum_order_size,omitempty"`
	Tokens           []MarketToken `json:"tokens,omitempty"`
}

// OrderBookLevel represents one price/size level on the Polymarket CLOB book.
type OrderBookLevel struct {
	Price string `json:"price"`
	Size  string `json:"size"`
}

// OrderBook holds bids and asks from GET /book?token_id=...
type OrderBook struct {
	Market    string           `json:"market"`
	AssetID   string           `json:"asset_id"`
	Timestamp string           `json:"timestamp,omitempty"`
	Hash      string           `json:"hash,omitempty"`
	TickSize  string           `json:"tick_size,omitempty"`
	NegRisk   bool             `json:"neg_risk,omitempty"`
	Bids      []OrderBookLevel `json:"bids,omitempty"`
	Asks      []OrderBookLevel `json:"asks,omitempty"`
}

// OrderDraft captures operator inputs before fixed-point CTF conversion and EIP-712 signing.
type OrderDraft struct {
	Network     string `json:"network,omitempty"`
	TokenID     string `json:"tokenId"`
	Asset       string `json:"asset,omitempty"`
	Side        string `json:"side"` // BUY or SELL
	Price       string `json:"price"`
	Shares      string `json:"shares"`
	OrderType   string `json:"orderType,omitempty"` // GTC, GTD, FOK, FAK
	Expiration  uint64 `json:"expiration,omitempty"`
	Nonce       uint64 `json:"nonce,omitempty"`
	FeeRateBps  uint64 `json:"feeRateBps,omitempty"`
	NegRisk     bool   `json:"negRisk,omitempty"`
	ReduceOnly  bool   `json:"reduceOnly,omitempty"`
	OperationID string `json:"operationId,omitempty"`
	Salt        string `json:"salt,omitempty"`
}

// L2Credentials holds Polymarket L2 API HMAC credentials derived for the API owner.
type L2Credentials struct {
	Address    string `json:"address"`
	APIKey     string `json:"apiKey"`
	Secret     string `json:"secret"`
	Passphrase string `json:"passphrase"`
}

// OrderSubmissionResult summarizes a POST /order or DELETE /order response.
type OrderSubmissionResult struct {
	Submitted   bool     `json:"submitted"`
	StatusCode  int      `json:"statusCode,omitempty"`
	OrderID     string   `json:"orderId,omitempty"`
	Status      string   `json:"status,omitempty"`
	Message     string   `json:"message,omitempty"`
	OrderHashes []string `json:"orderHashes,omitempty"`
}
