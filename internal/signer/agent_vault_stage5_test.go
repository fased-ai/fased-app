package signer

import "testing"

func TestAllowedAgentVaultVenueStage5AndWENHold(t *testing.T) {
	t.Parallel()
	allowed := []string{
		"hyperliquid", "solana", "polymarket", "kalshi", "lighter",
		"binance", "bybit", "okx", "kraken", "coinbase", "deribit",
		"alpaca", "ibkr",
	}
	for _, v := range allowed {
		if !AllowedAgentVaultVenue(v) {
			t.Fatalf("expected AllowedAgentVaultVenue(%q) == true", v)
		}
	}
	for _, rejected := range []string{"wen", "sat", "satcoin", "wenpm1", ""} {
		if AllowedAgentVaultVenue(rejected) {
			t.Fatalf("expected AllowedAgentVaultVenue(%q) == false", rejected)
		}
	}
}
