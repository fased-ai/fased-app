package core

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/kalshi"
	"github.com/fased-ai/fased-app/internal/polymarket"
	"github.com/fased-ai/fased-app/internal/signer"
)

// PolymarketRehearseRequest parameterizes an Amoy EIP-712 Polymarket CLOB order rehearsal.
type PolymarketRehearseRequest struct {
	AgentVaultID string                    `json:"agentVaultId,omitempty"`
	TokenID      string                    `json:"tokenId,omitempty"`
	Asset        string                    `json:"asset,omitempty"`
	Side         string                    `json:"side,omitempty"`
	Price        string                    `json:"price,omitempty"`
	Shares       string                    `json:"shares,omitempty"`
	OrderType    string                    `json:"orderType,omitempty"`
	NegRisk      bool                      `json:"negRisk,omitempty"`
	ReduceOnly   bool                      `json:"reduceOnly,omitempty"`
	FetchBook    bool                      `json:"fetchBook,omitempty"`
	Submit       bool                      `json:"submit,omitempty"`
	Credentials  *polymarket.L2Credentials `json:"credentials,omitempty"`
}

// PolymarketRehearseResult reports non-secret EIP-712 signature & submission outcome.
type PolymarketRehearseResult struct {
	Network           string                            `json:"network"`
	ChainID           uint64                            `json:"chainId"`
	VerifyingContract string                            `json:"verifyingContract"`
	AgentVaultID      string                            `json:"agentVaultId"`
	OperationID       string                            `json:"operationId"`
	SignerAddress     string                            `json:"signerAddress"`
	DomainSeparator   string                            `json:"domainSeparator"`
	StructHash        string                            `json:"structHash"`
	TypedDataDigest   string                            `json:"typedDataDigest"`
	EcrecoverValid    bool                              `json:"ecrecoverValid"`
	NotionalMicroUSD  uint64                            `json:"notionalMicroUsd"`
	BookBestBid       string                            `json:"bookBestBid,omitempty"`
	BookBestAsk       string                            `json:"bookBestAsk,omitempty"`
	Submission        *polymarket.OrderSubmissionResult `json:"submission,omitempty"`
	SubmitBlocked     bool                              `json:"submitBlocked,omitempty"`
}

// KalshiRehearseRequest parameterizes a Kalshi Demo V2 RSA-PSS order rehearsal.
type KalshiRehearseRequest struct {
	AgentVaultID string `json:"agentVaultId,omitempty"`
	Ticker       string `json:"ticker,omitempty"`
	Asset        string `json:"asset,omitempty"`
	Side         string `json:"side,omitempty"`
	Count        string `json:"count,omitempty"`
	Price        string `json:"price,omitempty"`
	TimeInForce  string `json:"timeInForce,omitempty"`
	ReduceOnly   bool   `json:"reduceOnly,omitempty"`
	FetchBook    bool   `json:"fetchBook,omitempty"`
	Submit       bool   `json:"submit,omitempty"`
}

// KalshiRehearseResult reports non-secret RSA-PSS SHA-256 signature & Demo API outcome.
type KalshiRehearseResult struct {
	Environment       string                        `json:"environment"`
	Origin            string                        `json:"origin"`
	AgentVaultID      string                        `json:"agentVaultId"`
	OperationID       string                        `json:"operationId"`
	KeyID             string                        `json:"keyId"`
	PublicKeySHA256   string                        `json:"publicKeySha256"`
	Ticker            string                        `json:"ticker"`
	EstimatedMicroUSD uint64                        `json:"estimatedMicroUsd"`
	PSSVerified       bool                          `json:"pssVerified"`
	BestYesBid        string                        `json:"bestYesBid,omitempty"`
	Submission        *kalshi.OrderSubmissionResult `json:"submission,omitempty"`
}

func (s *Store) resolveStage4Vault(vaultID string) (*stage4Runtime, string, error) {
	rt := s.stage4Runtime()
	if rt == nil || rt.profile != signer.ExecutionProfileTestnet || rt.signerCall == nil {
		return nil, "", signer.ErrUnavailable
	}
	id := strings.TrimSpace(vaultID)
	if id == "" {
		id = rt.agentVaultID
	}
	if id == "" {
		return nil, "", errors.New("agentVaultId required for Stage 4 venue execution")
	}
	return rt, id, nil
}

func (s *Store) polymarketStatus(ctx context.Context) map[string]any {
	out := map[string]any{
		"available":     false,
		"network":       "amoy",
		"chainId":       signer.PolymarketAmoyChainID,
		"mainnetLocked": !signer.PolymarketActivationEnabled,
		"wenHeld":       !miningActivationEnabled,
	}
	rt := s.stage4Runtime()
	if rt == nil || rt.profile != signer.ExecutionProfileTestnet || rt.polymarketClient == nil || rt.signerCall == nil {
		return out
	}
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	resp, err := rt.signerCall(callCtx, signer.Request{Action: "polymarket-status"})
	if err != nil || resp.PolymarketStatus == nil {
		return out
	}
	out["available"] = resp.PolymarketStatus.Enabled
	out["signerAddress"] = resp.PolymarketStatus.SignerAddress
	out["exchangeContract"] = resp.PolymarketStatus.ExchangeContract
	out["negRiskExchangeContract"] = resp.PolymarketStatus.NegRiskExchangeContract
	out["origin"] = rt.polymarketClient.Origin()
	return out
}

func (s *Store) kalshiStatus(ctx context.Context) map[string]any {
	out := map[string]any{
		"available":     false,
		"environment":   "demo",
		"mainnetLocked": !signer.KalshiActivationEnabled,
		"wenHeld":       !miningActivationEnabled,
	}
	rt := s.stage4Runtime()
	if rt == nil || rt.profile != signer.ExecutionProfileTestnet || rt.kalshiClient == nil || rt.signerCall == nil {
		return out
	}
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	resp, err := rt.signerCall(callCtx, signer.Request{Action: "kalshi-status"})
	if err != nil || resp.KalshiStatus == nil {
		return out
	}
	out["available"] = resp.KalshiStatus.Enabled
	out["configured"] = resp.KalshiStatus.Configured
	out["keyId"] = resp.KalshiStatus.KeyID
	out["publicKeySha256"] = resp.KalshiStatus.PublicKeySHA256
	out["origin"] = rt.kalshiClient.Origin()
	return out
}

func verifyPolymarketECRECOVER(signerAddr string, signed *signer.PolymarketSignedOrder) error {
	if signed == nil {
		return errors.New("missing polymarket signature")
	}
	sig, err := signer.ParseECDSASignature(signed.SignatureHex)
	if err != nil {
		return err
	}
	digestBytes, err := hex.DecodeString(strings.TrimPrefix(signed.TypedDataDigest, "0x"))
	if err != nil || len(digestBytes) != 32 {
		return errors.New("invalid polymarket typedDataDigest")
	}
	var d32 [32]byte
	copy(d32[:], digestBytes)
	return signer.VerifySecp256k1Signature(signerAddr, d32, sig)
}

func (s *Store) rehearsePolymarket(ctx context.Context, req PolymarketRehearseRequest) (*PolymarketRehearseResult, error) {
	if s.isGlobalKillActive(ctx) {
		return nil, errGlobalKillSwitchActive
	}
	rt, vaultID, err := s.resolveStage4Vault(req.AgentVaultID)
	if err != nil {
		return nil, err
	}
	if rt.polymarketClient == nil {
		return nil, errors.New("polymarket execution client is not configured")
	}
	tokenID := strings.TrimSpace(req.TokenID)
	if tokenID == "" {
		tokenID = "1000001"
	}
	side := strings.ToUpper(strings.TrimSpace(req.Side))
	if side == "" {
		side = "BUY"
	}
	price := strings.TrimSpace(req.Price)
	if price == "" {
		price = "0.45"
	}
	shares := strings.TrimSpace(req.Shares)
	if shares == "" {
		shares = "4"
	}
	asset := strings.ToUpper(strings.TrimSpace(req.Asset))
	if asset == "" {
		asset = "USDC"
	}

	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var bestBid, bestAsk string
	if req.FetchBook {
		book, err := rt.polymarketClient.FetchOrderBook(callCtx, tokenID)
		if err != nil {
			return nil, err
		}
		if len(book.Bids) > 0 {
			bestBid = book.Bids[0].Price
		}
		if len(book.Asks) > 0 {
			bestAsk = book.Asks[0].Price
		}
	}

	intent, err := polymarket.BuildOrderIntent(polymarket.OrderDraft{
		Network:    "amoy",
		TokenID:    tokenID,
		Asset:      asset,
		Side:       side,
		Price:      price,
		Shares:     shares,
		OrderType:  req.OrderType,
		NegRisk:    req.NegRisk,
		ReduceOnly: req.ReduceOnly,
	}, time.Now().Unix())
	if err != nil {
		return nil, err
	}

	signResp, err := rt.signerCall(callCtx, signer.Request{
		Action:          "polymarket-sign-order",
		AgentVaultID:    vaultID,
		PolymarketOrder: &intent,
	})
	if err != nil {
		return nil, err
	}
	if signResp.PolymarketSigned == nil {
		return nil, errors.New("missing polymarket signed order response")
	}
	if err := verifyPolymarketECRECOVER(signResp.PolymarketSigned.SignerAddress, signResp.PolymarketSigned); err != nil {
		return nil, err
	}

	res := &PolymarketRehearseResult{
		Network:           signResp.PolymarketSigned.Network,
		ChainID:           signResp.PolymarketSigned.ChainID,
		VerifyingContract: signResp.PolymarketSigned.VerifyingContract,
		AgentVaultID:      vaultID,
		OperationID:       signResp.PolymarketSigned.OperationID,
		SignerAddress:     signResp.PolymarketSigned.SignerAddress,
		DomainSeparator:   signResp.PolymarketSigned.DomainSeparator,
		StructHash:        signResp.PolymarketSigned.StructHash,
		TypedDataDigest:   signResp.PolymarketSigned.TypedDataDigest,
		EcrecoverValid:    true,
		NotionalMicroUSD:  signResp.PolymarketSigned.NotionalMicroUSD,
		BookBestBid:       bestBid,
		BookBestAsk:       bestAsk,
	}
	if req.Submit {
		var creds polymarket.L2Credentials
		if req.Credentials != nil {
			creds = *req.Credentials
		} else {
			creds = polymarket.L2Credentials{Address: signResp.PolymarketSigned.SignerAddress}
		}
		sub, subErr := rt.polymarketClient.SubmitSignedOrder(callCtx, intent, signResp.PolymarketSigned, req.OrderType, creds)
		if errors.Is(subErr, polymarket.ErrExecutionDisabled) {
			res.SubmitBlocked = true
			res.Submission = &polymarket.OrderSubmissionResult{
				Submitted: false,
				Status:    "blocked_mainnet_locked",
				Message:   subErr.Error(),
			}
			return res, nil
		}
		if subErr != nil {
			return res, subErr
		}
		res.Submission = &sub
	}
	return res, nil
}

func (s *Store) rehearseKalshi(ctx context.Context, req KalshiRehearseRequest) (*KalshiRehearseResult, error) {
	if s.isGlobalKillActive(ctx) {
		return nil, errGlobalKillSwitchActive
	}
	rt, vaultID, err := s.resolveStage4Vault(req.AgentVaultID)
	if err != nil {
		return nil, err
	}
	if rt.kalshiClient == nil {
		return nil, errors.New("kalshi execution client is not configured")
	}
	ticker := strings.ToUpper(strings.TrimSpace(req.Ticker))
	if ticker == "" {
		ticker = "KXFED-26DEC-T400"
	}
	side := strings.ToLower(strings.TrimSpace(req.Side))
	if side == "" {
		side = "bid"
	}
	count := strings.TrimSpace(req.Count)
	if count == "" {
		count = "2"
	}
	price := strings.TrimSpace(req.Price)
	if price == "" {
		price = "0.4500"
	}
	asset := strings.ToUpper(strings.TrimSpace(req.Asset))
	if asset == "" {
		asset = "USD"
	}

	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	kStResp, err := rt.signerCall(callCtx, signer.Request{Action: "kalshi-status"})
	if err != nil {
		return nil, err
	}
	if kStResp.KalshiStatus == nil || !kStResp.KalshiStatus.Configured {
		return nil, errors.New("kalshi demo RSA key must be generated or provisioned by owner")
	}

	var bestYesBid string
	if req.FetchBook {
		book, err := rt.kalshiClient.FetchOrderBook(callCtx, ticker)
		if err != nil {
			return nil, err
		}
		bestYesBid = book.BestYesBid
	}

	draft := kalshi.OrderDraft{
		Ticker:      ticker,
		Side:        side,
		Count:       count,
		Price:       price,
		TimeInForce: req.TimeInForce,
		ReduceOnly:  req.ReduceOnly,
		Asset:       asset,
	}
	validated, err := kalshi.BuildOrder(draft)
	if err != nil {
		return nil, err
	}

	signViaIPC := func(sCtx context.Context, intent signer.KalshiSignIntent) (*signer.KalshiSignedRequest, error) {
		resp, err := rt.signerCall(sCtx, signer.Request{
			Action:       "kalshi-sign-request",
			AgentVaultID: vaultID,
			KalshiSign:   &intent,
		})
		if err != nil {
			return nil, err
		}
		if resp.KalshiSigned == nil {
			return nil, errors.New("missing kalshi signed request")
		}
		if err := signer.VerifyKalshiPSSSignature(
			kStResp.KalshiStatus.PublicKeyPEM,
			resp.KalshiSigned.TimestampMs,
			resp.KalshiSigned.Method,
			resp.KalshiSigned.Path,
			resp.KalshiSigned.SignatureBase64,
		); err != nil {
			return nil, err
		}
		return resp.KalshiSigned, nil
	}

	res := &KalshiRehearseResult{
		Environment:       rt.kalshiClient.Environment(),
		Origin:            rt.kalshiClient.Origin(),
		AgentVaultID:      vaultID,
		OperationID:       validated.Draft.OperationID,
		KeyID:             kStResp.KalshiStatus.KeyID,
		PublicKeySHA256:   kStResp.KalshiStatus.PublicKeySHA256,
		Ticker:            validated.Draft.Ticker,
		EstimatedMicroUSD: validated.EstimatedMicroUSD,
		BestYesBid:        bestYesBid,
	}
	if req.Submit {
		sub, subErr := rt.kalshiClient.SubmitOrder(callCtx, validated.Draft, signViaIPC)
		if subErr != nil {
			return res, subErr
		}
		res.PSSVerified = true
		res.Submission = &sub
		return res, nil
	}

	if _, err := signViaIPC(callCtx, signer.KalshiSignIntent{
		Environment:       "demo",
		Method:            http.MethodPost,
		Path:              "/trade-api/v2/portfolio/events/orders",
		TimestampMs:       time.Now().UnixMilli(),
		Ticker:            validated.Draft.Ticker,
		Asset:             validated.Draft.Asset,
		OperationID:       validated.Draft.OperationID,
		EstimatedMicroUSD: validated.EstimatedMicroUSD,
		ReduceOnly:        validated.Draft.ReduceOnly,
		ExpiresAt:         time.Now().Unix() + 120,
	}); err != nil {
		return nil, err
	}
	res.PSSVerified = true
	return res, nil
}

func (h *HTTPHandler) polymarketStatusAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	writeJSON(w, 200, h.store.polymarketStatus(r.Context()))
}

func (h *HTTPHandler) kalshiStatusAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	writeJSON(w, 200, h.store.kalshiStatus(r.Context()))
}

func (h *HTTPHandler) polymarketRehearseAPI(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !h.mutation(w, r) || !h.csrf(w, r, token) {
		return
	}
	var req PolymarketRehearseRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	authCtx, ok := h.requireStepUpIfOwnerAuthConfigured(w, r, token, "execute", "polymarket", false)
	if !ok {
		return
	}
	res, err := h.store.rehearsePolymarket(authCtx, req)
	if err != nil {
		code := 400
		if errors.Is(err, signer.ErrUnavailable) || errors.Is(err, errGlobalKillSwitchActive) {
			code = 403
		}
		writeError(w, code, err.Error())
		return
	}
	writeJSON(w, 200, res)
}

func (h *HTTPHandler) kalshiRehearseAPI(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !h.mutation(w, r) || !h.csrf(w, r, token) {
		return
	}
	var req KalshiRehearseRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	authCtx, ok := h.requireStepUpIfOwnerAuthConfigured(w, r, token, "execute", "kalshi", false)
	if !ok {
		return
	}
	res, err := h.store.rehearseKalshi(authCtx, req)
	if err != nil {
		code := 400
		if errors.Is(err, signer.ErrUnavailable) || errors.Is(err, errGlobalKillSwitchActive) {
			code = 403
		}
		writeError(w, code, err.Error())
		return
	}
	writeJSON(w, 200, res)
}
