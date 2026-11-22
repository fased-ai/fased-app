package core

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
)

const (
	jupiterPriceV3URL    = "https://lite-api.jup.ag/price/v3"
	jupiterSwapQuoteURL  = "https://lite-api.jup.ag/swap/v1/quote"
	coingeckoSimpleURL   = "https://api.coingecko.com/api/v3/simple/price"
	marketHTTPMaxBytes   = 128 * 1024
	base58AlphabetString = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
)

type marketTokenMeta struct {
	Symbol      string
	Mint        string
	Decimals    int
	CoingeckoID string
	FixtureUSD  float64
}

var knownMarketTokens = []marketTokenMeta{
	{Symbol: "SOL", Mint: "So11111111111111111111111111111111111111112", Decimals: 9, CoingeckoID: "solana", FixtureUSD: 70.00},
	{Symbol: "USDC", Mint: "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", Decimals: 6, CoingeckoID: "usd-coin", FixtureUSD: 1.00},
	{Symbol: "USDT", Mint: "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB", Decimals: 6, CoingeckoID: "tether", FixtureUSD: 1.00},
	{Symbol: "JUP", Mint: "JUPyiwrYJFskUPiHa7hkeR8VUtAeFoSYbKedZNsDvCN", Decimals: 6, CoingeckoID: "jupiter-exchange-solana", FixtureUSD: 0.45},
	{Symbol: "BONK", Mint: "DezXAZ8z7PnrnRJjz3wXBoRgixCa6xjnB7YaB1pPB263", Decimals: 5, CoingeckoID: "bonk", FixtureUSD: 0.000018},
	{Symbol: "WIF", Mint: "EKpQGSJtjMFqKZ9KQanSqYXRcF8fBopzLHYxdM65zcjm", Decimals: 6, CoingeckoID: "dogwifcoin", FixtureUSD: 0.85},
	{Symbol: "RAY", Mint: "4k3Dyjzvzp8eMZWUXbBCjEvwSkkk59S5iCNLY3QrkX6R", Decimals: 6, CoingeckoID: "raydium", FixtureUSD: 2.10},
	{Symbol: "JTO", Mint: "jtojtomepa8beP8AuQc6eXt5FriJwfFMwQx2v2f9mCL", Decimals: 9, CoingeckoID: "jito-governance-token", FixtureUSD: 1.80},
	{Symbol: "PYTH", Mint: "HZ1JovNiVvGrGNiiYvEozEVgZ58xaU3RKwX8eACQBCt3", Decimals: 6, CoingeckoID: "pyth-network", FixtureUSD: 0.22},
	{Symbol: "MSOL", Mint: "mSoLzYCxHdYgdzU16g5QSh3i5K3z3KZK7ytfqcJm7So", Decimals: 9, CoingeckoID: "msol", FixtureUSD: 86.00},
	{Symbol: "JITOSOL", Mint: "J1toso1uCk3RLmjorhTtrVwY9HJ7X8V9yYac6Y7kGCPn", Decimals: 9, CoingeckoID: "jito-staked-sol", FixtureUSD: 84.00},
	{Symbol: "WEN", Mint: "WENWENvqqNya429ubCdR81ZmD69brwQaaBYY6p3LCpk", Decimals: 5, CoingeckoID: "wen-4", FixtureUSD: 0.00005},
	{Symbol: "BTC", CoingeckoID: "bitcoin", FixtureUSD: 60000.00},
	{Symbol: "ETH", CoingeckoID: "ethereum", FixtureUSD: 1700.00},
	{Symbol: "HYPE", CoingeckoID: "hyperliquid", FixtureUSD: 18.50},
	{Symbol: "SUI", CoingeckoID: "sui", FixtureUSD: 1.50},
	{Symbol: "ARB", CoingeckoID: "arbitrum", FixtureUSD: 0.40},
	{Symbol: "OP", CoingeckoID: "optimism", FixtureUSD: 0.70},
	{Symbol: "AVAX", CoingeckoID: "avalanche-2", FixtureUSD: 14.00},
}

func isBase58Mint(s string) bool {
	if len(s) < 32 || len(s) > 44 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !strings.ContainsRune(base58AlphabetString, rune(s[i])) {
			return false
		}
	}
	return true
}

func lookupKnownMarketToken(raw string) (marketTokenMeta, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return marketTokenMeta{}, false
	}
	upper := strings.ToUpper(trimmed)
	if upper == "WSOL" {
		upper = "SOL"
	}
	for _, m := range knownMarketTokens {
		if m.Symbol == upper || (m.Mint != "" && m.Mint == trimmed) {
			return m, true
		}
	}
	if isBase58Mint(trimmed) {
		short := trimmed
		if len(short) > 10 {
			short = short[:4] + "..." + short[len(short)-4:]
		}
		return marketTokenMeta{
			Symbol:   short,
			Mint:     trimmed,
			Decimals: -1,
		}, true
	}
	return marketTokenMeta{}, false
}

func lookupKnownMarketTokenByMint(mint string) (marketTokenMeta, bool) {
	for _, m := range knownMarketTokens {
		if m.Mint != "" && m.Mint == mint {
			return m, true
		}
	}
	return marketTokenMeta{}, false
}

// formatAtomicUnits converts a non-negative integer atomic amount into an exact decimal string.
func formatAtomicUnits(atomicStr string, decimals int) string {
	trimmed := strings.TrimSpace(atomicStr)
	if trimmed == "" {
		return "0"
	}
	val, ok := new(big.Int).SetString(trimmed, 10)
	if !ok || val.Sign() <= 0 {
		return "0"
	}
	digits := val.String()
	if decimals <= 0 {
		return digits
	}
	if len(digits) <= decimals {
		padded := strings.Repeat("0", decimals-len(digits)) + digits
		frac := strings.TrimRight(padded, "0")
		if frac == "" {
			return "0"
		}
		return "0." + frac
	}
	split := len(digits) - decimals
	intPart := digits[:split]
	frac := strings.TrimRight(digits[split:], "0")
	if frac == "" {
		return intPart
	}
	return intPart + "." + frac
}

// parseDecimalToAtomic parses a positive decimal string into an exact atomic integer string.
func parseDecimalToAtomic(amountStr string, decimals int) (string, error) {
	trimmed := strings.TrimSpace(amountStr)
	if trimmed == "" || decimals < 0 || decimals > 18 {
		return "", errors.New("invalid token amount or decimals")
	}
	if strings.ContainsAny(trimmed, "+-eE \t\r\n") {
		return "", errors.New("amount must be a plain positive decimal string")
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) > 2 {
		return "", errors.New("invalid decimal format")
	}
	intPart := parts[0]
	fracPart := ""
	if len(parts) == 2 {
		fracPart = parts[1]
	}
	if intPart == "" && fracPart == "" {
		return "", errors.New("amount is required")
	}
	for _, r := range intPart {
		if r < '0' || r > '9' {
			return "", errors.New("invalid integer digits in amount")
		}
	}
	for _, r := range fracPart {
		if r < '0' || r > '9' {
			return "", errors.New("invalid fractional digits in amount")
		}
	}
	if len(intPart) > 18 {
		return "", errors.New("amount integer part is too large")
	}
	if len(fracPart) > decimals {
		return "", fmt.Errorf("amount exceeds token precision (%d decimals)", decimals)
	}
	if intPart == "" {
		intPart = "0"
	}
	fracPadded := fracPart + strings.Repeat("0", decimals-len(fracPart))
	combined := strings.TrimLeft(intPart+fracPadded, "0")
	if combined == "" {
		return "", errors.New("amount must be greater than zero")
	}
	return combined, nil
}

type publicMarketRoundTripper struct {
	next http.RoundTripper
}

func (t *publicMarketRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet || r.URL == nil || r.URL.Scheme != "https" {
		return nil, errors.New("market request refused")
	}
	host := strings.ToLower(r.URL.Hostname())
	if host != "lite-api.jup.ag" && host != "api.coingecko.com" {
		return nil, errors.New("market host not allowed")
	}
	if len(r.Header.Values("Authorization")) != 0 || len(r.Header.Values("Cookie")) != 0 {
		return nil, errors.New("credentials disallowed on public market request")
	}
	return t.next.RoundTrip(r)
}

func newPublicMarketClient(s *Store) *http.Client {
	denyRedirects := func(*http.Request, []*http.Request) error {
		return errors.New("market redirects refused")
	}
	if s != nil && s.marketDataTransport != nil {
		return &http.Client{
			Transport:     &publicMarketRoundTripper{next: s.marketDataTransport},
			Timeout:       8 * time.Second,
			CheckRedirect: denyRedirects,
		}
	}
	d := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
		ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{
			MinVersion:       tls.VersionTLS12,
			CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384},
		},
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  6 * time.Second,
		MaxResponseHeaderBytes: 16 * 1024,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialPublicRPC(ctx, network, address, net.DefaultResolver.LookupNetIP, d.DialContext)
		},
	}
	return &http.Client{
		Transport:     &publicMarketRoundTripper{next: tr},
		Timeout:       8 * time.Second,
		CheckRedirect: denyRedirects,
	}
}

func fetchMarketJSON(ctx context.Context, client *http.Client, targetURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "fased-agent/1")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("market endpoint unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, marketHTTPMaxBytes+1))
	if err != nil {
		return err
	}
	if len(body) > marketHTTPMaxBytes {
		return errors.New("market response exceeded size limit")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("market service returned HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("invalid market JSON payload: %w", err)
	}
	return nil
}

func toolGetWalletBalances(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var args struct {
		AccountID string `json:"accountId"`
	}
	if err := decodeToolArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	view, err := s.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	if len(view.Record.Accounts) == 0 {
		return map[string]any{
			"accounts":       []any{},
			"count":          0,
			"signingEnabled": false,
			"message":        "No wallets configured. Add a watch or managed wallet in Wallets.",
		}, nil
	}

	targetQuery := strings.TrimSpace(args.AccountID)
	var chosen *Account
	for i := range view.Record.Accounts {
		a := &view.Record.Accounts[i]
		if targetQuery != "" {
			if a.ID == targetQuery || strings.EqualFold(a.Name, targetQuery) {
				chosen = a
				break
			}
			continue
		}
		if validProviderRPC(*a) {
			chosen = a
			break
		}
	}
	if chosen == nil {
		if targetQuery != "" {
			return nil, fmt.Errorf("wallet account %q not found", targetQuery)
		}
		chosen = &view.Record.Accounts[0]
	}

	address := ""
	if pub, decErr := hex.DecodeString(chosen.PublicKey); decErr == nil && len(pub) == 32 && hex.EncodeToString(pub) == chosen.PublicKey {
		address = onchain.Base58(pub)
	}
	if !validProviderRPC(*chosen) {
		return map[string]any{
			"accountId":      chosen.ID,
			"name":           chosen.Name,
			"network":        chosen.Network,
			"address":        address,
			"status":         "rpc_not_configured",
			"signingEnabled": false,
			"message":        "Configure an RPC endpoint in Wallets to read live balances.",
		}, nil
	}

	provider := rpcProvider(chosen.RPC.Endpoint, chosen.Network)
	rpcRef := ""
	if rpcProviderRequiresKey(provider) {
		vault, vErr := s.readRPCVault(chosen.ID)
		if vErr != nil || vault.Version != 2 || vault.Binding != rpcBinding(*chosen) {
			return map[string]any{
				"accountId":      chosen.ID,
				"name":           chosen.Name,
				"network":        chosen.Network,
				"address":        address,
				"status":         "rpc_locked",
				"signingEnabled": false,
				"message":        "Unlock or configure the RPC provider key in Wallets first.",
			}, nil
		}
		if !vault.OSProtected {
			return map[string]any{
				"accountId":      chosen.ID,
				"name":           chosen.Name,
				"network":        chosen.Network,
				"address":        address,
				"status":         "rpc_passphrase_required",
				"signingEnabled": false,
				"message":        "RPC key uses a vault passphrase; refresh balance directly in Wallets.",
			}, nil
		}
		rpcRef = vault.Ref
	}

	if !s.rpcAdmission && s.walletReadFixture == nil {
		return map[string]any{
			"accountId":           chosen.ID,
			"name":                chosen.Name,
			"network":             chosen.Network,
			"address":             address,
			"status":              "fixture_mode",
			"fixtureOnly":         true,
			"sol":                 "0",
			"lamports":            "0",
			"tokens":              []any{},
			"recentActivityCount": 0,
			"signingEnabled":      false,
		}, nil
	}

	snap, err := s.ReadWallet(ctx, WalletReadCommand{
		AccountID: chosen.ID,
		Revision:  view.Record.Revision,
		Ref:       rpcRef,
	})
	if err != nil {
		return nil, err
	}

	tokens := make([]map[string]any, 0, len(snap.Tokens))
	for _, tok := range snap.Tokens {
		sym := tok.Mint
		if len(sym) > 10 {
			sym = sym[:4] + "..." + sym[len(sym)-4:]
		}
		if meta, ok := lookupKnownMarketTokenByMint(tok.Mint); ok {
			sym = meta.Symbol
		}
		tokens = append(tokens, map[string]any{
			"mint":     tok.Mint,
			"symbol":   sym,
			"amount":   tok.Amount,
			"uiAmount": formatAtomicUnits(tok.Amount, tok.Decimals),
			"decimals": tok.Decimals,
			"program":  tok.Program,
		})
	}
	recent := snap.Activity
	if len(recent) > 5 {
		recent = recent[:5]
	}

	return map[string]any{
		"accountId":           snap.AccountID,
		"name":                chosen.Name,
		"network":             snap.Network,
		"address":             snap.Address,
		"endpoint":            snap.Endpoint,
		"slot":                snap.Slot,
		"lamports":            snap.Lamports,
		"sol":                 formatAtomicUnits(snap.Lamports, 9),
		"tokens":              tokens,
		"recentActivityCount": len(snap.Activity),
		"recentActivity":      recent,
		"fetchedAt":           snap.FetchedAt,
		"liveRPC":             snap.LiveRPC,
		"fixtureOnly":         snap.FixtureOnly,
		"signingEnabled":      false,
	}, nil
}

type jupiterPriceEntry struct {
	USDPrice      float64 `json:"usdPrice"`
	PriceChange24 float64 `json:"priceChange24h"`
	Liquidity     float64 `json:"liquidity"`
	Decimals      int     `json:"decimals"`
}

func toolGetTokenPrice(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var args struct {
		Token  string   `json:"token"`
		Tokens []string `json:"tokens"`
	}
	if err := decodeToolArgs(rawArgs, &args); err != nil {
		return nil, err
	}

	var rawList []string
	if strings.TrimSpace(args.Token) != "" {
		rawList = append(rawList, args.Token)
	}
	rawList = append(rawList, args.Tokens...)
	if len(rawList) == 0 {
		rawList = []string{"SOL"}
	}

	seen := map[string]bool{}
	metas := make([]marketTokenMeta, 0, len(rawList))
	queries := make([]string, 0, len(rawList))
	for _, raw := range rawList {
		q := strings.TrimSpace(raw)
		if q == "" {
			continue
		}
		norm := strings.ToUpper(q)
		if isBase58Mint(q) {
			norm = q
		}
		if seen[norm] {
			continue
		}
		seen[norm] = true
		meta, ok := lookupKnownMarketToken(q)
		if !ok {
			return nil, fmt.Errorf("unknown token %q; supply a major ticker (SOL, USDC, BTC, ETH, JUP, WEN) or Solana mint address", q)
		}
		metas = append(metas, meta)
		queries = append(queries, q)
		if len(metas) >= 10 {
			break
		}
	}

	if s != nil && s.providerFixture && s.marketDataTransport == nil {
		prices := make([]map[string]any, 0, len(metas))
		for i, meta := range metas {
			usd := meta.FixtureUSD
			if usd <= 0 {
				usd = 1.00
			}
			row := map[string]any{
				"query":        queries[i],
				"symbol":       meta.Symbol,
				"usdPrice":     usd,
				"change24hPct": 1.25,
				"source":       "fixture",
				"fixtureOnly":  true,
			}
			if meta.Mint != "" {
				row["mint"] = meta.Mint
			}
			prices = append(prices, row)
		}
		return map[string]any{
			"prices":      prices,
			"missing":     []string{},
			"fetchedAt":   s.now().Unix(),
			"fixtureOnly": true,
			"note":        "Fixture market price observation; no external request performed.",
		}, nil
	}

	client := newPublicMarketClient(s)
	var mintIDs []string
	for _, m := range metas {
		if m.Mint != "" {
			mintIDs = append(mintIDs, m.Mint)
		}
	}

	jupPrices := map[string]jupiterPriceEntry{}
	var jupErr error
	if len(mintIDs) > 0 {
		target := jupiterPriceV3URL + "?ids=" + url.QueryEscape(strings.Join(mintIDs, ","))
		jupErr = fetchMarketJSON(ctx, client, target, &jupPrices)
	}

	var cgIDs []string
	cgSeen := map[string]bool{}
	for _, m := range metas {
		if m.Mint != "" {
			if _, ok := jupPrices[m.Mint]; ok {
				continue
			}
		}
		if m.CoingeckoID != "" && !cgSeen[m.CoingeckoID] {
			cgSeen[m.CoingeckoID] = true
			cgIDs = append(cgIDs, m.CoingeckoID)
		}
	}

	cgPrices := map[string]struct {
		USD         float64 `json:"usd"`
		USD24hChang float64 `json:"usd_24h_change"`
	}{}
	var cgErr error
	if len(cgIDs) > 0 {
		target := coingeckoSimpleURL + "?ids=" + url.QueryEscape(strings.Join(cgIDs, ",")) + "&vs_currencies=usd&include_24hr_change=true"
		cgErr = fetchMarketJSON(ctx, client, target, &cgPrices)
	}

	prices := make([]map[string]any, 0, len(metas))
	missing := []string{}
	for i, m := range metas {
		if m.Mint != "" {
			if entry, ok := jupPrices[m.Mint]; ok && entry.USDPrice > 0 {
				row := map[string]any{
					"query":        queries[i],
					"symbol":       m.Symbol,
					"mint":         m.Mint,
					"usdPrice":     entry.USDPrice,
					"change24hPct": entry.PriceChange24,
					"source":       "jupiter-price-v3",
				}
				if entry.Liquidity > 0 {
					row["liquidityUsd"] = entry.Liquidity
				}
				prices = append(prices, row)
				continue
			}
		}
		if m.CoingeckoID != "" {
			if entry, ok := cgPrices[m.CoingeckoID]; ok && entry.USD > 0 {
				row := map[string]any{
					"query":        queries[i],
					"symbol":       m.Symbol,
					"usdPrice":     entry.USD,
					"change24hPct": entry.USD24hChang,
					"source":       "coingecko",
				}
				if m.Mint != "" {
					row["mint"] = m.Mint
				}
				prices = append(prices, row)
				continue
			}
		}
		missing = append(missing, queries[i])
	}

	if len(prices) == 0 && (jupErr != nil || cgErr != nil) {
		if jupErr != nil {
			return nil, jupErr
		}
		return nil, cgErr
	}

	return map[string]any{
		"prices":    prices,
		"missing":   missing,
		"fetchedAt": time.Now().UTC().Unix(),
		"note":      "Read-only market observation; not an executable firm quote.",
	}, nil
}

func resolveSolanaSwapToken(ctx context.Context, client *http.Client, raw string, fixture bool) (marketTokenMeta, error) {
	meta, ok := lookupKnownMarketToken(raw)
	if !ok {
		return marketTokenMeta{}, fmt.Errorf("unknown swap token %q; supply a Solana token symbol or mint", raw)
	}
	if meta.Mint == "" {
		return marketTokenMeta{}, fmt.Errorf("get_swap_quote supports Solana tokens and mints only (got %s)", meta.Symbol)
	}
	if meta.Decimals >= 0 {
		return meta, nil
	}
	if fixture {
		return marketTokenMeta{}, fmt.Errorf("unknown swap mint %q in fixture mode", meta.Mint)
	}
	jupPrices := map[string]jupiterPriceEntry{}
	if err := fetchMarketJSON(ctx, client, jupiterPriceV3URL+"?ids="+url.QueryEscape(meta.Mint), &jupPrices); err != nil {
		return marketTokenMeta{}, fmt.Errorf("unable to resolve mint decimals for %s: %w", meta.Mint, err)
	}
	entry, ok := jupPrices[meta.Mint]
	if !ok || entry.Decimals < 0 || entry.Decimals > 18 {
		return marketTokenMeta{}, fmt.Errorf("token mint %s metadata unavailable", meta.Mint)
	}
	meta.Decimals = entry.Decimals
	return meta, nil
}

func computeEffectivePrice(inUI, outUI string) string {
	inF, inErr := strconv.ParseFloat(inUI, 64)
	outF, outErr := strconv.ParseFloat(outUI, 64)
	if inErr != nil || outErr != nil || inF <= 0 || outF <= 0 || math.IsNaN(outF/inF) || math.IsInf(outF/inF, 0) {
		return ""
	}
	ratio := outF / inF
	if ratio >= 1 {
		return strconv.FormatFloat(ratio, 'f', 4, 64)
	}
	return strconv.FormatFloat(ratio, 'g', 6, 64)
}

func toolGetSwapQuote(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var args struct {
		FromToken   string          `json:"fromToken"`
		ToToken     string          `json:"toToken"`
		Amount      json.RawMessage `json:"amount"`
		SlippageBps int             `json:"slippageBps"`
	}
	if err := decodeToolArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	amountStr, err := parsePositiveDecimalString(args.Amount)
	if err != nil {
		return nil, err
	}
	slippage := args.SlippageBps
	if slippage == 0 {
		slippage = 50
	}
	if slippage < 1 || slippage > 5000 {
		return nil, errors.New("slippageBps must be between 1 and 5000")
	}

	fixtureMode := s != nil && s.providerFixture && s.marketDataTransport == nil
	var client *http.Client
	if !fixtureMode {
		client = newPublicMarketClient(s)
	}
	fromMeta, err := resolveSolanaSwapToken(ctx, client, args.FromToken, fixtureMode)
	if err != nil {
		return nil, err
	}
	toMeta, err := resolveSolanaSwapToken(ctx, client, args.ToToken, fixtureMode)
	if err != nil {
		return nil, err
	}
	if fromMeta.Mint == toMeta.Mint {
		return nil, errors.New("fromToken and toToken must be different")
	}

	atomicIn, err := parseDecimalToAtomic(amountStr, fromMeta.Decimals)
	if err != nil {
		return nil, err
	}

	if fixtureMode {
		fromUSD := fromMeta.FixtureUSD
		if fromUSD <= 0 {
			fromUSD = 1.0
		}
		toUSD := toMeta.FixtureUSD
		if toUSD <= 0 {
			toUSD = 1.0
		}
		inFloat, _ := strconv.ParseFloat(amountStr, 64)
		outFloat := (inFloat * fromUSD) / toUSD
		scaleOut := math.Pow10(toMeta.Decimals)
		atomicOut := uint64(math.Round(outFloat * scaleOut))
		if atomicOut == 0 {
			atomicOut = 1
		}
		minAtomicOut := (atomicOut * uint64(10000-slippage)) / 10000
		if minAtomicOut == 0 {
			minAtomicOut = 1
		}
		atomicOutStr := strconv.FormatUint(atomicOut, 10)
		minOutStr := strconv.FormatUint(minAtomicOut, 10)
		inUI := formatAtomicUnits(atomicIn, fromMeta.Decimals)
		outUI := formatAtomicUnits(atomicOutStr, toMeta.Decimals)
		minUI := formatAtomicUnits(minOutStr, toMeta.Decimals)
		return map[string]any{
			"inputMint":        fromMeta.Mint,
			"outputMint":       toMeta.Mint,
			"fromSymbol":       fromMeta.Symbol,
			"toSymbol":         toMeta.Symbol,
			"inAmount":         atomicIn,
			"inUiAmount":       inUI,
			"outAmount":        atomicOutStr,
			"outUiAmount":      outUI,
			"minOutAmount":     minOutStr,
			"minOutUiAmount":   minUI,
			"slippageBps":      slippage,
			"priceImpactPct":   "0.0001",
			"effectivePrice":   computeEffectivePrice(inUI, outUI),
			"routePlan":        []map[string]any{{"label": "FixtureRouter", "percent": 100}},
			"source":           "fixture",
			"fetchedAt":        s.now().Unix(),
			"fixtureOnly":      true,
			"executionEnabled": false,
			"note":             "Fixture quote preview; no external request performed. Call draft_swap to propose a review-only swap draft.",
		}, nil
	}

	q := url.Values{}
	q.Set("inputMint", fromMeta.Mint)
	q.Set("outputMint", toMeta.Mint)
	q.Set("amount", atomicIn)
	q.Set("slippageBps", strconv.Itoa(slippage))

	var jupResp struct {
		InputMint            string `json:"inputMint"`
		InAmount             string `json:"inAmount"`
		OutputMint           string `json:"outputMint"`
		OutAmount            string `json:"outAmount"`
		OtherAmountThreshold string `json:"otherAmountThreshold"`
		SwapMode             string `json:"swapMode"`
		SlippageBps          int    `json:"slippageBps"`
		PriceImpactPct       string `json:"priceImpactPct"`
		RoutePlan            []struct {
			SwapInfo struct {
				Label string `json:"label"`
			} `json:"swapInfo"`
			Percent int `json:"percent"`
		} `json:"routePlan"`
	}
	if err := fetchMarketJSON(ctx, client, jupiterSwapQuoteURL+"?"+q.Encode(), &jupResp); err != nil {
		return nil, err
	}
	if strings.TrimSpace(jupResp.OutAmount) == "" {
		return nil, errors.New("quote response missing output amount")
	}

	routeSummary := make([]map[string]any, 0, len(jupResp.RoutePlan))
	for _, leg := range jupResp.RoutePlan {
		label := strings.TrimSpace(leg.SwapInfo.Label)
		if label == "" {
			label = "Unknown"
		}
		routeSummary = append(routeSummary, map[string]any{
			"label":   label,
			"percent": leg.Percent,
		})
	}

	inUI := formatAtomicUnits(jupResp.InAmount, fromMeta.Decimals)
	outUI := formatAtomicUnits(jupResp.OutAmount, toMeta.Decimals)
	minUI := formatAtomicUnits(jupResp.OtherAmountThreshold, toMeta.Decimals)

	return map[string]any{
		"inputMint":        jupResp.InputMint,
		"outputMint":       jupResp.OutputMint,
		"fromSymbol":       fromMeta.Symbol,
		"toSymbol":         toMeta.Symbol,
		"inAmount":         jupResp.InAmount,
		"inUiAmount":       inUI,
		"outAmount":        jupResp.OutAmount,
		"outUiAmount":      outUI,
		"minOutAmount":     jupResp.OtherAmountThreshold,
		"minOutUiAmount":   minUI,
		"slippageBps":      jupResp.SlippageBps,
		"priceImpactPct":   jupResp.PriceImpactPct,
		"effectivePrice":   computeEffectivePrice(inUI, outUI),
		"routePlan":        routeSummary,
		"source":           "jupiter-lite-v1",
		"fetchedAt":        time.Now().UTC().Unix(),
		"executionEnabled": false,
		"note":             "Read-only Jupiter quote preview. Call draft_swap to propose a review-only swap draft for owner approval.",
	}, nil
}
