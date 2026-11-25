package signer

import (
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"time"
)

// AgentVaultPolicy defines signer-enforced custody bounds for a delegated agent vault.
// WEN/SAT mining venues are strictly excluded until explicit owner authorization.
type AgentVaultPolicy struct {
	Version            int      `json:"version"`
	VaultID            string   `json:"vaultId"`
	AgentID            string   `json:"agentId"`
	AllowedVenues      []string `json:"allowedVenues"`
	AllowedAssets      []string `json:"allowedAssets"`
	MaxOrderMicroUSD   uint64   `json:"maxOrderMicroUsd,string"`
	DailySpendMicroUSD uint64   `json:"dailySpendMicroUsd,string"`
	DailyLossMicroUSD  uint64   `json:"dailyLossMicroUsd,string"`
	MaxLeverage        uint32   `json:"maxLeverage"`
	ExpiresAt          int64    `json:"expiresAt"`
	Revoked            bool     `json:"revoked,omitempty"`
}

// AgentVaultSpendRecord records a durable signer-enforced spend or realized loss delta
// keyed by UTC day (`YYYY-MM-DD`) and unique OperationID.
type AgentVaultSpendRecord struct {
	VaultID       string `json:"vaultId"`
	OperationID   string `json:"operationId"`
	Venue         string `json:"venue"`
	Asset         string `json:"asset"`
	DayUTC        string `json:"dayUtc"`
	SpendMicroUSD uint64 `json:"spendMicroUsd,string"`
	LossMicroUSD  uint64 `json:"lossMicroUsd,string"`
}

// AgentVaultUsage projects an agent vault policy alongside its accumulated spend and
// realized loss for the current UTC day.
type AgentVaultUsage struct {
	Policy             AgentVaultPolicy `json:"policy"`
	DayUTC             string           `json:"dayUtc"`
	DailySpentMicroUSD uint64           `json:"dailySpentMicroUsd,string"`
	DailyLossMicroUSD  uint64           `json:"dailyLossMicroUsd,string"`
}

// HyperliquidStatus projects non-secret signer-owned Hyperliquid policy, monotonic
// nonce floor, derived EIP-55 address (when unlocked), and agent vault usage.
type HyperliquidStatus struct {
	Enabled   bool               `json:"enabled"`
	Address   string             `json:"address,omitempty"`
	Policy    *HyperliquidPolicy `json:"policy,omitempty"`
	LastNonce uint64             `json:"lastNonce"`
	Vaults    []AgentVaultUsage  `json:"vaults,omitempty"`
}

func isHex32ID(s string) bool {
	if len(s) != 32 {
		return false
	}
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 16 && hex.EncodeToString(b) == s
}

// AllowedAgentVaultVenue returns true only for supported non-WEN financial execution venues.
func AllowedAgentVaultVenue(venue string) bool {
	switch venue {
	case "hyperliquid", "solana", "polymarket", "kalshi", "lighter",
		"binance", "bybit", "okx", "kraken", "coinbase", "deribit",
		"alpaca", "ibkr":
		return true
	default:
		return false
	}
}

// ValidateAgentVaultPolicy validates version, identity, non-WEN venue/asset allowlists,
// spend/loss ceilings, leverage limit, and expiry.
func ValidateAgentVaultPolicy(p AgentVaultPolicy, now int64) error {
	if p.Version != 1 {
		return errors.New("unsupported agent vault policy version")
	}
	if !isHex32ID(p.VaultID) {
		return errors.New("agent vault id must be 32 lowercase hex characters")
	}
	trimmedAgent := strings.TrimSpace(p.AgentID)
	if trimmedAgent == "" || len(trimmedAgent) > 64 || trimmedAgent != p.AgentID {
		return errors.New("agent vault agentId must be 1..64 trimmed characters")
	}
	if len(p.AllowedVenues) == 0 || len(p.AllowedVenues) > 16 {
		return errors.New("agent vault requires 1..16 allowed venues")
	}
	seenVenues := make(map[string]bool, len(p.AllowedVenues))
	for _, venue := range p.AllowedVenues {
		if !AllowedAgentVaultVenue(venue) || seenVenues[venue] {
			return errors.New("invalid or unsupported agent vault venue")
		}
		seenVenues[venue] = true
	}
	if len(p.AllowedAssets) == 0 || len(p.AllowedAssets) > 64 {
		return errors.New("agent vault requires 1..64 allowed assets")
	}
	seenAssets := make(map[string]bool, len(p.AllowedAssets))
	for _, asset := range p.AllowedAssets {
		if asset == "" || len(asset) > 24 || strings.ToUpper(strings.TrimSpace(asset)) != asset || seenAssets[asset] {
			return errors.New("invalid or duplicate agent vault asset")
		}
		seenAssets[asset] = true
	}
	if p.MaxOrderMicroUSD == 0 || p.DailySpendMicroUSD < p.MaxOrderMicroUSD || p.DailyLossMicroUSD == 0 {
		return errors.New("invalid agent vault spend or daily-loss limits")
	}
	if p.MaxLeverage == 0 || p.MaxLeverage > 50 {
		return errors.New("agent vault leverage must be between 1 and 50")
	}
	if now > 0 && p.ExpiresAt <= now {
		return errors.New("agent vault policy expired")
	}
	if p.ExpiresAt <= 0 {
		return errors.New("agent vault expiry required")
	}
	return nil
}

// ValidateAgentVaultSpendRecord validates a durable spend/loss delta entry before journal append.
func ValidateAgentVaultSpendRecord(rec AgentVaultSpendRecord) error {
	if !isHex32ID(rec.VaultID) {
		return errors.New("invalid agent vault spend vaultId")
	}
	op := strings.TrimSpace(rec.OperationID)
	if op == "" || len(op) > 96 || op != rec.OperationID {
		return errors.New("invalid agent vault operationId")
	}
	if !AllowedAgentVaultVenue(rec.Venue) {
		return errors.New("invalid or unsupported agent vault spend venue")
	}
	if rec.Asset == "" || len(rec.Asset) > 24 || strings.ToUpper(strings.TrimSpace(rec.Asset)) != rec.Asset {
		return errors.New("invalid agent vault spend asset")
	}
	if len(rec.DayUTC) != 10 {
		return errors.New("agent vault spend dayUtc must be YYYY-MM-DD")
	}
	if _, err := time.Parse("2006-01-02", rec.DayUTC); err != nil {
		return errors.New("invalid agent vault spend dayUtc format")
	}
	if rec.SpendMicroUSD == 0 && rec.LossMicroUSD == 0 {
		return errors.New("agent vault spend record requires non-zero spend or loss")
	}
	return nil
}

func cloneAgentVaultPolicySlice(src []string) []string {
	return slices.Clone(src)
}
