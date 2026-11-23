package hyperliquid

import (
	"errors"

	"github.com/fased-ai/fased-app/internal/signer"
)

const (
	// MainnetAPIOrigin is the pinned official Hyperliquid mainnet API origin.
	MainnetAPIOrigin = "https://api.hyperliquid.xyz"

	// TestnetAPIOrigin is the pinned official Hyperliquid testnet API origin.
	TestnetAPIOrigin = "https://api.hyperliquid-testnet.xyz"
)

// ErrExecutionDisabled is returned whenever live exchange submission is attempted
// outside an explicitly constructed controlled fixture transport.
var ErrExecutionDisabled = errors.New("hyperliquid live execution is disabled")

// MarketMeta describes a single Hyperliquid perpetual market joined with its asset context.
type MarketMeta struct {
	AssetIndex   uint32 `json:"assetIndex"`
	Symbol       string `json:"symbol"`
	SzDecimals   int    `json:"szDecimals"`
	MaxLeverage  uint32 `json:"maxLeverage"`
	OnlyIsolated bool   `json:"onlyIsolated,omitempty"`
	MarkPrice    string `json:"markPrice"`
	OraclePrice  string `json:"oraclePrice"`
	MidPrice     string `json:"midPrice,omitempty"`
	PrevDayPrice string `json:"prevDayPrice,omitempty"`
	DayNotional  string `json:"dayNotionalVolume,omitempty"`
	FundingRate  string `json:"fundingRate"`
	OpenInterest string `json:"openInterest"`
}

// MarketSnapshot holds bounded perpetual market metadata retrieved via POST /info.
type MarketSnapshot struct {
	Network   string       `json:"network"`
	FetchedAt int64        `json:"fetchedAt,omitempty"`
	Markets   []MarketMeta `json:"markets"`
}

// Position describes an open perpetual position returned by clearinghouseState.
type Position struct {
	Symbol           string `json:"symbol"`
	Size             string `json:"size"`
	EntryPrice       string `json:"entryPrice"`
	PositionValue    string `json:"positionValue"`
	UnrealizedPnL    string `json:"unrealizedPnl"`
	LiquidationPrice string `json:"liquidationPrice,omitempty"`
	Leverage         uint32 `json:"leverage"`
	MarginMode       string `json:"marginMode"`
}

// OpenOrder describes an open resting order returned by openOrders.
type OpenOrder struct {
	Symbol        string `json:"symbol"`
	Side          string `json:"side"`
	LimitPrice    string `json:"limitPrice"`
	Size          string `json:"size"`
	OrderID       uint64 `json:"orderId"`
	Timestamp     int64  `json:"timestamp"`
	ClientOrderID string `json:"clientOrderId,omitempty"`
}

// AccountState summarizes Hyperliquid clearinghouse margin state and open orders.
type AccountState struct {
	Address      string      `json:"address"`
	AccountValue string      `json:"accountValue"`
	TotalMargin  string      `json:"totalMarginUsed"`
	Withdrawable string      `json:"withdrawable"`
	Positions    []Position  `json:"positions"`
	OpenOrders   []OpenOrder `json:"openOrders"`
}

// OrderDraft contains operator input from UI/CLI prior to policy validation and EIP-712 digesting.
type OrderDraft struct {
	Asset         string `json:"asset"`
	IsBuy         bool   `json:"isBuy"`
	LimitPrice    string `json:"limitPrice"`
	Size          string `json:"size"`
	Leverage      uint32 `json:"leverage"`
	ReduceOnly    bool   `json:"reduceOnly"`
	TimeInForce   string `json:"timeInForce"`
	ClientOrderID string `json:"clientOrderId,omitempty"`
}

// OrderPreview is the deterministic, unsigned review projection computed before owner confirmation.
type OrderPreview struct {
	Intent               signer.HyperliquidOrderIntent `json:"intent"`
	NormalizedPrice      string                        `json:"normalizedPrice"`
	NormalizedSize       string                        `json:"normalizedSize"`
	ReferenceMarkPrice   string                        `json:"referenceMarkPrice"`
	NotionalMicroUSD     uint64                        `json:"notionalMicroUsd"`
	InitialMarginMicro   uint64                        `json:"initialMarginMicroUsd"`
	EstimatedFeeMicroUSD uint64                        `json:"estimatedFeeMicroUsd"`
	PriceDeviationBps    uint64                        `json:"priceDeviationBps"`
	ConnectionID         string                        `json:"connectionId"`
	TypedDataDigest      string                        `json:"typedDataDigest"`
}

// ExchangeSubmissionResult captures normalized outcome states from POST /exchange or orderStatus.
type ExchangeSubmissionResult struct {
	State         string `json:"state"`
	OrderID       uint64 `json:"orderId,omitempty"`
	ClientOrderID string `json:"clientOrderId,omitempty"`
	FilledSize    string `json:"filledSize,omitempty"`
	AvgPrice      string `json:"avgPrice,omitempty"`
	Message       string `json:"message,omitempty"`
}
