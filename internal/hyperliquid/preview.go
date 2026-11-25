package hyperliquid

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/fased-ai/fased-app/internal/signer"
)

const takerFeeMilliBps uint64 = 35 // 3.5 bps base taker estimate (35 / 100,000)

func findMarket(snapshot MarketSnapshot, symbol string) (MarketMeta, bool) {
	for _, m := range snapshot.Markets {
		if m.Symbol == symbol {
			return m, true
		}
	}
	return MarketMeta{}, false
}

func fracDigitsCount(normalizedDecimal string) int {
	dot := strings.IndexByte(normalizedDecimal, '.')
	if dot < 0 {
		return 0
	}
	return len(normalizedDecimal) - dot - 1
}

// validatePerpPricePrecision enforces Hyperliquid perpetual price precision:
// integer prices are always accepted, while non-integer prices are limited to
// <= 5 significant figures and <= max(0, 6-szDecimals) decimal places.
func validatePerpPricePrecision(normPrice string, szDecimals int) error {
	dot := strings.IndexByte(normPrice, '.')
	if dot < 0 {
		return nil
	}
	maxDecimals := 6 - szDecimals
	if maxDecimals < 0 {
		maxDecimals = 0
	}
	if fracDigitsCount(normPrice) > maxDecimals {
		return fmt.Errorf("limit price exceeds maximum %d decimal places for perp szDecimals=%d", maxDecimals, szDecimals)
	}
	sig := strings.ReplaceAll(normPrice, ".", "")
	sig = strings.TrimLeft(sig, "0")
	if len(sig) > 5 {
		return errors.New("limit price exceeds 5 significant figures")
	}
	return nil
}

// PreviewOrder validates an operator OrderDraft against live/cached market metadata
// and signer policy, returning a deterministic EIP-712 review preview without touching
// private key material.
func PreviewOrder(snapshot MarketSnapshot, policy signer.HyperliquidPolicy, req OrderDraft, lastNonce, nextNonce uint64, now int64) (OrderPreview, signer.HyperliquidOrderIntent, error) {
	return PreviewOrderForProfile(snapshot, policy, req, lastNonce, nextNonce, now, false, 0)
}

// PreviewOrderForProfile validates an operator OrderDraft with optional mainnet-capped ceilings.
func PreviewOrderForProfile(snapshot MarketSnapshot, policy signer.HyperliquidPolicy, req OrderDraft, lastNonce, nextNonce uint64, now int64, mainnetCapped bool, maxOrderMicroUSD uint64) (OrderPreview, signer.HyperliquidOrderIntent, error) {
	market, ok := findMarket(snapshot, req.Asset)
	if !ok {
		return OrderPreview{}, signer.HyperliquidOrderIntent{}, errors.New("unknown Hyperliquid perpetual market")
	}
	if market.MaxLeverage > 0 && req.Leverage > market.MaxLeverage {
		return OrderPreview{}, signer.HyperliquidOrderIntent{}, errors.New("leverage exceeds market maximum")
	}
	isCross := false
	if req.IsCross != nil {
		isCross = *req.IsCross
	}
	if isCross && market.OnlyIsolated {
		return OrderPreview{}, signer.HyperliquidOrderIntent{}, errors.New("cross margin rejected on onlyIsolated market")
	}
	normPrice, err := signer.NormalizeHyperliquidDecimal(req.LimitPrice)
	if err != nil {
		return OrderPreview{}, signer.HyperliquidOrderIntent{}, err
	}
	if err := validatePerpPricePrecision(normPrice, market.SzDecimals); err != nil {
		return OrderPreview{}, signer.HyperliquidOrderIntent{}, err
	}
	normSize, err := signer.NormalizeHyperliquidDecimal(req.Size)
	if err != nil {
		return OrderPreview{}, signer.HyperliquidOrderIntent{}, err
	}
	if market.SzDecimals >= 0 && fracDigitsCount(normSize) > market.SzDecimals {
		return OrderPreview{}, signer.HyperliquidOrderIntent{}, errors.New("order size exceeds market szDecimals precision")
	}
	normMark, err := signer.NormalizeHyperliquidDecimal(market.MarkPrice)
	if err != nil {
		return OrderPreview{}, signer.HyperliquidOrderIntent{}, errors.New("market mark price unavailable")
	}
	tif := req.TimeInForce
	if tif == "" {
		tif = "Gtc"
	}
	expiresAt := now + 120
	if policy.ExpiresAt > 0 && expiresAt > policy.ExpiresAt {
		expiresAt = policy.ExpiresAt
	}
	intent := signer.HyperliquidOrderIntent{
		Network:            policy.Network,
		Asset:              market.Symbol,
		AssetIndex:         market.AssetIndex,
		IsBuy:              req.IsBuy,
		IsCross:            isCross,
		LimitPrice:         normPrice,
		Size:               normSize,
		ReferenceMarkPrice: normMark,
		Leverage:           req.Leverage,
		ReduceOnly:         req.ReduceOnly,
		TimeInForce:        tif,
		Grouping:           "na",
		ClientOrderID:      req.ClientOrderID,
		Nonce:              nextNonce,
		ExpiresAt:          expiresAt,
	}
	notional, err := signer.ValidateHyperliquidOrderIntentForProfile(intent, policy, lastNonce, now, mainnetCapped, maxOrderMicroUSD)
	if err != nil {
		return OrderPreview{}, signer.HyperliquidOrderIntent{}, err
	}
	packed, err := signer.EncodeHyperliquidOrderAction(intent)
	if err != nil {
		return OrderPreview{}, signer.HyperliquidOrderIntent{}, err
	}
	connID, err := signer.HyperliquidActionConnectionID(packed, intent.Nonce, policy.VaultAddress)
	if err != nil {
		return OrderPreview{}, signer.HyperliquidOrderIntent{}, err
	}
	digest, err := signer.HyperliquidAgentDigest(policy.Network, connID)
	if err != nil {
		return OrderPreview{}, signer.HyperliquidOrderIntent{}, err
	}
	bps, _, err := signer.PriceDeviationBps(normPrice, normMark)
	if err != nil {
		return OrderPreview{}, signer.HyperliquidOrderIntent{}, err
	}
	lev := uint64(intent.Leverage)
	margin := (notional + lev - 1) / lev
	fee := (notional/100_000)*takerFeeMilliBps + ((notional%100_000)*takerFeeMilliBps)/100_000

	return OrderPreview{
		Intent:               intent,
		NormalizedPrice:      normPrice,
		NormalizedSize:       normSize,
		ReferenceMarkPrice:   normMark,
		NotionalMicroUSD:     notional,
		InitialMarginMicro:   margin,
		EstimatedFeeMicroUSD: fee,
		PriceDeviationBps:    bps,
		ConnectionID:         "0x" + hex.EncodeToString(connID[:]),
		TypedDataDigest:      "0x" + hex.EncodeToString(digest[:]),
	}, intent, nil
}
