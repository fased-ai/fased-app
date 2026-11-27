package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/custody"
	"github.com/fased-ai/fased-app/internal/httpapi"
	"github.com/fased-ai/fased-app/internal/onchain"
)

const (
	jupiterLiteQuoteURL = "https://lite-api.jup.ag/swap/v1/quote"
	jupiterLiteSwapURL  = "https://lite-api.jup.ag/swap/v1/swap-instructions"
	jupiterAPIQuoteURL  = "https://api.jup.ag/swap/v1/quote"
	jupiterAPISwapURL   = "https://api.jup.ag/swap/v1/swap-instructions"
	maxJupiterRespBytes = 512 * 1024
)

type solanaKnownToken struct {
	Mint     string
	Symbol   string
	Decimals uint8
}

var solanaKnownTokens = []solanaKnownToken{
	{Mint: onchain.SOL, Symbol: "SOL", Decimals: 9},
	{Mint: "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", Symbol: "USDC", Decimals: 6},
	{Mint: "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB", Symbol: "USDT", Decimals: 6},
	{Mint: "JUPyiwrYJFskUPiHa7hkeR8VUtAeFoSYbKedZNsDvCN", Symbol: "JUP", Decimals: 6},
	{Mint: "3NZ9JMVBmGAqocybic2c7LQCJScmgsAZ6vQqTDzcqmJh", Symbol: "WBTC", Decimals: 8},
	{Mint: "7vfCXTUXx5WJV5JADk17DUJ4ksgau7utNKj4b963voxs", Symbol: "WETH", Decimals: 8},
	{Mint: "DezXAZ8z7PnrnRJjz3wXBoRgixCa6xjnB7YaB1pPB263", Symbol: "BONK", Decimals: 5},
	{Mint: "J1toso1uCk3RLmjorhTtrVwY9HJ7X8V9yYac6Y7kGCPn", Symbol: "JitoSOL", Decimals: 9},
	{Mint: "mSoLzYCxHdYgdzU16g5QSh3i5K3z3KZK7ytfqcJm7So", Symbol: "mSOL", Decimals: 9},
}

func lookupKnownSolanaToken(mint string) (solanaKnownToken, bool) {
	m := strings.TrimSpace(mint)
	for _, tok := range solanaKnownTokens {
		if tok.Mint == m {
			return tok, true
		}
	}
	return solanaKnownToken{}, false
}

// JupiterSwapQuoteRequest requests a live Jupiter route quote and optionally prepares
// a signer-verified custody WalletReview (operation: jupiter.swap).
type JupiterSwapQuoteRequest struct {
	WalletID       string `json:"walletId,omitempty"`
	InputMint      string `json:"inputMint"`
	OutputMint     string `json:"outputMint"`
	AmountHuman    string `json:"amountHuman,omitempty"`
	AmountRaw      uint64 `json:"amountRaw,string,omitempty"`
	InputDecimals  uint8  `json:"inputDecimals,omitempty"`
	OutputDecimals uint8  `json:"outputDecimals,omitempty"`
	SlippageBps    uint32 `json:"slippageBps,omitempty"`
	PrepareReview  bool   `json:"prepareReview,omitempty"`
}

// JupiterSwapQuoteResponse returns a bounded route preview, canonical onchain.Quote,
// and (when prepareReview=true) the signer-simulated WalletReview.
type JupiterSwapQuoteResponse struct {
	WalletID          string                `json:"walletId"`
	WalletHandle      string                `json:"walletHandle"`
	Cluster           string                `json:"cluster"`
	InputMint         string                `json:"inputMint"`
	OutputMint        string                `json:"outputMint"`
	InputSymbol       string                `json:"inputSymbol"`
	OutputSymbol      string                `json:"outputSymbol"`
	InputDecimals     uint8                 `json:"inputDecimals"`
	OutputDecimals    uint8                 `json:"outputDecimals"`
	AmountRaw         string                `json:"amountRaw"`
	AmountHuman       string                `json:"amountHuman"`
	OutAmountRaw      string                `json:"outAmountRaw"`
	OutAmountHuman    string                `json:"outAmountHuman"`
	MinOutAmountRaw   string                `json:"minOutAmountRaw"`
	MinOutAmountHuman string                `json:"minOutAmountHuman"`
	PriceImpactPct    string                `json:"priceImpactPct"`
	SlippageBps       uint32                `json:"slippageBps"`
	RouteHops         []string              `json:"routeHops"`
	JupiterQuote      *onchain.Quote        `json:"jupiterQuote,omitempty"`
	Review            *custody.WalletReview `json:"review,omitempty"`
}

type jupRawQuoteResp struct {
	InputMint            string `json:"inputMint"`
	InAmount             string `json:"inAmount"`
	OutputMint           string `json:"outputMint"`
	OutAmount            string `json:"outAmount"`
	OtherAmountThreshold string `json:"otherAmountThreshold"`
	SlippageBps          uint32 `json:"slippageBps"`
	PriceImpactPct       string `json:"priceImpactPct"`
	RoutePlan            []struct {
		SwapInfo struct {
			Label string `json:"label"`
		} `json:"swapInfo"`
	} `json:"routePlan"`
}

type jupSwapInstructionsResp struct {
	SetupInstructions           []onchain.Instruction `json:"setupInstructions"`
	SwapInstruction             onchain.Instruction   `json:"swapInstruction"`
	CleanupInstruction          *onchain.Instruction  `json:"cleanupInstruction"`
	AddressLookupTableAddresses []string              `json:"addressLookupTableAddresses"`
}

func (s *Store) jupiterAPIKey() string {
	if s == nil {
		return ""
	}
	s.pluginsMu.Lock()
	doc, err := s.loadDataPluginsLocked()
	s.pluginsMu.Unlock()
	if err != nil || doc == nil {
		return ""
	}
	for _, p := range doc.Plugins {
		if p.ID == "solana" && p.SealedAPIKey != nil {
			if k, kErr := s.unsealPluginValue(p.ID, "apiKey", p.SealedAPIKey); kErr == nil {
				return strings.TrimSpace(k)
			}
		}
	}
	return ""
}

func (s *Store) jupiterHTTPClient() *http.Client {
	var rt http.RoundTripper
	if s != nil && s.marketDataTransport != nil {
		rt = s.marketDataTransport
	}
	if rt == nil {
		rt = custody.NewSafeTransport(false)
	}
	return &http.Client{
		Transport: rt,
		Timeout:   12 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("jupiter redirects denied")
		},
	}
}

func resolveTokenMeta(mint string, w custody.WalletPublic, overrideDec uint8) (string, uint8) {
	sym := ""
	dec := overrideDec
	if known, ok := lookupKnownSolanaToken(mint); ok {
		sym = known.Symbol
		if dec == 0 {
			dec = known.Decimals
		}
	}
	for _, tc := range w.Policy.TokenCaps {
		if strings.TrimSpace(tc.Mint) == mint {
			if sym == "" && strings.TrimSpace(tc.Symbol) != "" {
				sym = strings.TrimSpace(tc.Symbol)
			}
			if dec == 0 && tc.Decimals > 0 {
				dec = tc.Decimals
			}
		}
	}
	if sym == "" {
		if len(mint) >= 6 {
			sym = mint[:6]
		} else {
			sym = "SPL"
		}
	}
	if dec == 0 {
		dec = 6
	}
	return sym, dec
}

func (s *Store) quoteJupiterSwap(ctx context.Context, walletID string, req JupiterSwapQuoteRequest) (JupiterSwapQuoteResponse, error) {
	svc, err := s.custodyBackend()
	if err != nil || svc == nil {
		return JupiterSwapQuoteResponse{}, errors.New("custody signer unavailable")
	}
	targetWallet := strings.TrimSpace(walletID)
	if targetWallet == "" {
		targetWallet = strings.TrimSpace(req.WalletID)
	}
	if targetWallet == "" {
		return JupiterSwapQuoteResponse{}, errors.New("walletId required for Jupiter swap quote")
	}
	w, err := svc.GetWallet(ctx, targetWallet)
	if err != nil {
		return JupiterSwapQuoteResponse{}, err
	}

	inMint, _, err := custody.ValidateSolanaPublicAddress(req.InputMint)
	if err != nil {
		return JupiterSwapQuoteResponse{}, errors.New("invalid inputMint Solana address")
	}
	outMint, _, err := custody.ValidateSolanaPublicAddress(req.OutputMint)
	if err != nil {
		return JupiterSwapQuoteResponse{}, errors.New("invalid outputMint Solana address")
	}
	if inMint == outMint {
		return JupiterSwapQuoteResponse{}, errors.New("inputMint and outputMint must differ")
	}

	inSym, inDec := resolveTokenMeta(inMint, w, req.InputDecimals)
	outSym, outDec := resolveTokenMeta(outMint, w, req.OutputDecimals)

	amountRaw := req.AmountRaw
	if amountRaw == 0 {
		parsed, pErr := custody.ParseHumanAmountToRaw(req.AmountHuman, inDec)
		if pErr != nil {
			return JupiterSwapQuoteResponse{}, fmt.Errorf("invalid swap amount: %w", pErr)
		}
		amountRaw = parsed
	}
	if amountRaw == 0 {
		return JupiterSwapQuoteResponse{}, errors.New("swap amount must be greater than zero")
	}

	slippage := req.SlippageBps
	if slippage == 0 {
		slippage = w.Policy.MaxSlippageBps
	}
	if slippage == 0 {
		slippage = 50
	}
	if slippage > 500 {
		return JupiterSwapQuoteResponse{}, fmt.Errorf("%w: slippageBps %d exceeds safety ceiling 500", custody.ErrPolicyDenied, slippage)
	}
	if w.Policy.MaxSlippageBps > 0 && slippage > w.Policy.MaxSlippageBps {
		return JupiterSwapQuoteResponse{}, fmt.Errorf("%w: slippageBps %d exceeds wallet policy cap %d", custody.ErrPolicyDenied, slippage, w.Policy.MaxSlippageBps)
	}

	inAmtStr := strconv.FormatUint(amountRaw, 10)
	amountHuman := custody.FormatRawAmount(amountRaw, inDec)

	var (
		outAmtRaw   uint64
		minOutRaw   uint64
		priceImpact string
		hops        []string
		quoteObj    *onchain.Quote
	)

	if s.providerFixture && s.marketDataTransport == nil {
		// Deterministic offline fixture quote for automated unit & E2E suites.
		scaleIn := math.Pow10(int(inDec))
		scaleOut := math.Pow10(int(outDec))
		rate := 1.0
		switch {
		case inMint == onchain.SOL && (outSym == "USDC" || outSym == "USDT"):
			rate = 180.0
		case (inSym == "USDC" || inSym == "USDT") && outMint == onchain.SOL:
			rate = 1.0 / 180.0
		case inMint == onchain.SOL && outSym == "JUP":
			rate = 150.0
		case inSym == "JUP" && outMint == onchain.SOL:
			rate = 1.0 / 150.0
		}
		outFloat := (float64(amountRaw) / scaleIn) * rate * scaleOut
		if outFloat < 1 {
			outFloat = 1
		}
		outAmtRaw = uint64(math.Round(outFloat))
		minOutRaw = (outAmtRaw * uint64(10_000-slippage)) / 10_000
		if minOutRaw == 0 {
			minOutRaw = 1
		}
		priceImpact = "0.02"
		hops = []string{"Raydium CLMM", "Orca Whirlpool"}
		quoteObj = &onchain.Quote{
			Network:          w.RPC.Cluster,
			Owner:            w.PublicKey,
			InputMint:        inMint,
			OutputMint:       outMint,
			Amount:           inAmtStr,
			Output:           strconv.FormatUint(outAmtRaw, 10),
			MinimumOutput:    strconv.FormatUint(minOutRaw, 10),
			SlippageBPS:      uint16(slippage),
			MaxFeeLamports:   50_000,
			WrapAndUnwrapSol: inMint == onchain.SOL || outMint == onchain.SOL,
		}
	} else {
		apiKey := s.jupiterAPIKey()
		quoteBase := jupiterLiteQuoteURL
		swapBase := jupiterLiteSwapURL
		if apiKey != "" {
			quoteBase = jupiterAPIQuoteURL
			swapBase = jupiterAPISwapURL
		}
		params := url.Values{
			"inputMint":   {inMint},
			"outputMint":  {outMint},
			"amount":      {inAmtStr},
			"slippageBps": {strconv.Itoa(int(slippage))},
		}
		client := s.jupiterHTTPClient()
		qReq, err := http.NewRequestWithContext(ctx, http.MethodGet, quoteBase+"?"+params.Encode(), nil)
		if err != nil {
			return JupiterSwapQuoteResponse{}, err
		}
		qReq.Header.Set("Accept", "application/json")
		if apiKey != "" {
			qReq.Header.Set("x-api-key", apiKey)
		}
		qResp, err := client.Do(qReq)
		if err != nil {
			return JupiterSwapQuoteResponse{}, fmt.Errorf("jupiter quote unreachable: %w", err)
		}
		rawQuote, rErr := io.ReadAll(io.LimitReader(qResp.Body, maxJupiterRespBytes+1))
		_ = qResp.Body.Close()
		if rErr != nil || qResp.StatusCode != http.StatusOK || len(rawQuote) > maxJupiterRespBytes {
			return JupiterSwapQuoteResponse{}, fmt.Errorf("jupiter quote failed (HTTP %d)", qResp.StatusCode)
		}
		var parsedQuote jupRawQuoteResp
		if err := json.Unmarshal(rawQuote, &parsedQuote); err != nil {
			return JupiterSwapQuoteResponse{}, errors.New("malformed jupiter quote response")
		}
		if parsedQuote.InputMint != inMint || parsedQuote.OutputMint != outMint || parsedQuote.InAmount != inAmtStr {
			return JupiterSwapQuoteResponse{}, errors.New("jupiter quote response mint/amount mismatch")
		}
		var pErr error
		outAmtRaw, pErr = strconv.ParseUint(strings.TrimSpace(parsedQuote.OutAmount), 10, 64)
		if pErr != nil || outAmtRaw == 0 {
			return JupiterSwapQuoteResponse{}, errors.New("jupiter quote returned invalid output amount")
		}
		minOutRaw, pErr = strconv.ParseUint(strings.TrimSpace(parsedQuote.OtherAmountThreshold), 10, 64)
		if pErr != nil || minOutRaw == 0 {
			minOutRaw = (outAmtRaw * uint64(10_000-slippage)) / 10_000
		}
		priceImpact = strings.TrimSpace(parsedQuote.PriceImpactPct)
		if priceImpact == "" {
			priceImpact = "0"
		}
		seenHops := map[string]bool{}
		for _, rp := range parsedQuote.RoutePlan {
			lbl := strings.TrimSpace(rp.SwapInfo.Label)
			if lbl != "" && !seenHops[lbl] {
				seenHops[lbl] = true
				hops = append(hops, lbl)
			}
		}

		quoteObj = &onchain.Quote{
			Network:          w.RPC.Cluster,
			Owner:            w.PublicKey,
			InputMint:        inMint,
			OutputMint:       outMint,
			Amount:           inAmtStr,
			Output:           strconv.FormatUint(outAmtRaw, 10),
			MinimumOutput:    strconv.FormatUint(minOutRaw, 10),
			SlippageBPS:      uint16(slippage),
			MaxFeeLamports:   100_000,
			WrapAndUnwrapSol: inMint == onchain.SOL || outMint == onchain.SOL,
		}

		if req.PrepareReview {
			// Request Jupiter swap instructions for this wallet's public key, then hand off
			// to fased-signer for strict account pinning, ALT RPC verification & simulation.
			swapPayload, _ := json.Marshal(map[string]any{
				"quoteResponse":             json.RawMessage(rawQuote),
				"userPublicKey":             w.PublicKey,
				"wrapAndUnwrapSol":          quoteObj.WrapAndUnwrapSol,
				"useSharedAccounts":         false,
				"asLegacyTransaction":       false,
				"dynamicComputeUnitLimit":   true,
				"prioritizationFeeLamports": 50_000,
			})
			sReq, err := http.NewRequestWithContext(ctx, http.MethodPost, swapBase, bytes.NewReader(swapPayload))
			if err != nil {
				return JupiterSwapQuoteResponse{}, err
			}
			sReq.Header.Set("Content-Type", "application/json")
			sReq.Header.Set("Accept", "application/json")
			if apiKey != "" {
				sReq.Header.Set("x-api-key", apiKey)
			}
			sResp, err := client.Do(sReq)
			if err != nil {
				return JupiterSwapQuoteResponse{}, fmt.Errorf("jupiter swap-instructions unreachable: %w", err)
			}
			rawSwap, srErr := io.ReadAll(io.LimitReader(sResp.Body, maxJupiterRespBytes+1))
			_ = sResp.Body.Close()
			if srErr != nil || sResp.StatusCode != http.StatusOK || len(rawSwap) > maxJupiterRespBytes {
				return JupiterSwapQuoteResponse{}, fmt.Errorf("jupiter swap-instructions failed (HTTP %d)", sResp.StatusCode)
			}
			var swapIx jupSwapInstructionsResp
			if err := json.Unmarshal(rawSwap, &swapIx); err != nil || swapIx.SwapInstruction.ProgramID == "" {
				return JupiterSwapQuoteResponse{}, errors.New("malformed jupiter swap instructions")
			}
			ixs := make([]onchain.Instruction, 0, len(swapIx.SetupInstructions)+2)
			ixs = append(ixs, swapIx.SetupInstructions...)
			ixs = append(ixs, swapIx.SwapInstruction)
			if swapIx.CleanupInstruction != nil && swapIx.CleanupInstruction.ProgramID != "" {
				ixs = append(ixs, *swapIx.CleanupInstruction)
			}
			quoteObj.Instructions = ixs
			if len(swapIx.AddressLookupTableAddresses) > 0 {
				alts := make([]onchain.AddressLookupTable, 0, len(swapIx.AddressLookupTableAddresses))
				for _, addr := range swapIx.AddressLookupTableAddresses {
					alts = append(alts, onchain.AddressLookupTable{AccountKey: strings.TrimSpace(addr)})
				}
				quoteObj.AddressLookupTables = alts
			}
		}
	}

	if hops == nil {
		hops = []string{}
	}
	res := JupiterSwapQuoteResponse{
		WalletID:          w.ID,
		WalletHandle:      w.Handle,
		Cluster:           w.RPC.Cluster,
		InputMint:         inMint,
		OutputMint:        outMint,
		InputSymbol:       inSym,
		OutputSymbol:      outSym,
		InputDecimals:     inDec,
		OutputDecimals:    outDec,
		AmountRaw:         inAmtStr,
		AmountHuman:       amountHuman,
		OutAmountRaw:      strconv.FormatUint(outAmtRaw, 10),
		OutAmountHuman:    custody.FormatRawAmount(outAmtRaw, outDec),
		MinOutAmountRaw:   strconv.FormatUint(minOutRaw, 10),
		MinOutAmountHuman: custody.FormatRawAmount(minOutRaw, outDec),
		PriceImpactPct:    priceImpact,
		SlippageBps:       slippage,
		RouteHops:         hops,
		JupiterQuote:      quoteObj,
	}

	if req.PrepareReview {
		rev, rErr := svc.PrepareReview(ctx, custody.PrepareReviewRequest{
			WalletID:     w.ID,
			Operation:    "jupiter.swap",
			Destination:  outMint,
			Mint:         inMint,
			OutputMint:   outMint,
			SlippageBps:  slippage,
			AmountRaw:    amountRaw,
			AmountHuman:  amountHuman,
			JupiterQuote: quoteObj,
		})
		if rErr != nil {
			return res, rErr
		}
		res.Review = &rev
	}
	return res, nil
}

func (h *HTTPHandler) handleJupiterQuoteAPI(w http.ResponseWriter, r *http.Request, token, pathWalletID string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !h.v2Mutation(w, r, token, false) {
		return
	}
	if !h.allowRate(r, "jupiter-quote", 40) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	var req JupiterSwapQuoteRequest
	if !httpapi.DecodeStrictJSON(w, r, 16*1024, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	res, err := h.store.quoteJupiterSwap(ctx, pathWalletID, req)
	if err != nil {
		writeCustodyError(w, err)
		return
	}
	writeJSON(w, 200, res)
}
