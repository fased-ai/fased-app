package lighter

import (
	"errors"
	"net/http"
)

const (
	// TestnetAPIOrigin is the official Lighter DEX Testnet REST origin.
	TestnetAPIOrigin = "https://api.testnet.lighter.xyz"

	// MainnetAPIOrigin is the official Lighter DEX Mainnet REST origin (public reads only while locked).
	MainnetAPIOrigin = "https://mainnet.zklighter.elliot.ai"
)

// ErrExecutionDisabled is returned whenever Lighter Mainnet L2 mutations are attempted
// while signer.LighterActivationEnabled is false.
var ErrExecutionDisabled = errors.New("Lighter mainnet execution is disabled")

// ControlledTransport is a Go-test-only HTTP RoundTripper for isolated Lighter simulation.
type ControlledTransport interface {
	http.RoundTripper
	ControlledFixture() bool
}

// OrderBookMeta describes one market returned by GET /api/v1/orderBooks.
type OrderBookMeta struct {
	Symbol                 string `json:"symbol"`
	MarketID               int64  `json:"market_id"`
	MarketType             string `json:"market_type"`
	Status                 string `json:"status"`
	MinBaseAmount          string `json:"min_base_amount"`
	MinQuoteAmount         string `json:"min_quote_amount"`
	SupportedSizeDecimals  int    `json:"supported_size_decimals"`
	SupportedPriceDecimals int    `json:"supported_price_decimals"`
	SupportedQuoteDecimals int    `json:"supported_quote_decimals"`
}

// OrderBookLevel represents one resting level returned by GET /api/v1/orderBookOrders.
type OrderBookLevel struct {
	OrderIndex          int64  `json:"order_index"`
	OrderID             string `json:"order_id"`
	Price               string `json:"price"`
	RemainingBaseAmount string `json:"remaining_base_amount"`
	InitialBaseAmount   string `json:"initial_base_amount"`
}

// OrderBookOrdersResponse normalizes bid and ask depth from GET /api/v1/orderBookOrders.
type OrderBookOrdersResponse struct {
	Code      int              `json:"code"`
	TotalAsks int              `json:"total_asks"`
	TotalBids int              `json:"total_bids"`
	Asks      []OrderBookLevel `json:"asks"`
	Bids      []OrderBookLevel `json:"bids"`
}

// CandleRow represents one OHLCV bar returned by GET /api/v1/candles.
type CandleRow struct {
	T int64   `json:"t"`
	O float64 `json:"o"`
	H float64 `json:"h"`
	L float64 `json:"l"`
	C float64 `json:"c"`
	V float64 `json:"v"`
	Q float64 `json:"V"`
}

// TradeRow represents one recent fill returned by GET /api/v1/recentTrades.
type TradeRow struct {
	TradeID    int64  `json:"trade_id"`
	TradeIDStr string `json:"trade_id_str"`
	MarketID   int64  `json:"market_id"`
	Symbol     string `json:"market_symbol"`
	Type       string `json:"type"`
	Price      string `json:"price"`
	Size       string `json:"size"`
	USDAmount  string `json:"usd_amount"`
	Timestamp  int64  `json:"timestamp"`
}

// OrderDraft specifies human-readable parameters scaled using OrderBookMeta decimals.
type OrderDraft struct {
	Symbol           string `json:"symbol"`
	Side             string `json:"side"` // BUY or SELL
	Price            string `json:"price"`
	Size             string `json:"size"`
	OrderType        string `json:"orderType,omitempty"`   // LIMIT or MARKET
	TimeInForce      string `json:"timeInForce,omitempty"` // GTC, IOC, POST_ONLY
	ReduceOnly       bool   `json:"reduceOnly,omitempty"`
	Leverage         uint32 `json:"leverage,omitempty"`
	AccountIndex     int64  `json:"accountIndex"`
	APIKeyIndex      uint8  `json:"apiKeyIndex,omitempty"`
	ClientOrderIndex int64  `json:"clientOrderIndex,omitempty"`
	Nonce            int64  `json:"nonce"`
	OperationID      string `json:"operationId,omitempty"`
}

// SubmissionResult summarizes POST /api/v1/sendTx outcomes.
type SubmissionResult struct {
	Submitted  bool   `json:"submitted"`
	StatusCode int    `json:"statusCode"`
	Code       int    `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
	TxHash     string `json:"txHash,omitempty"`
}
