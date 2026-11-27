package core

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/lighter"
	"github.com/fased-ai/fased-app/internal/signer"
)

// LighterRehearseRequest parameterizes a Lighter Testnet ECgFp5+Poseidon2 order rehearsal.
type LighterRehearseRequest struct {
	AgentVaultID string `json:"agentVaultId,omitempty"`
	Symbol       string `json:"symbol,omitempty"`
	Side         string `json:"side,omitempty"`
	Price        string `json:"price,omitempty"`
	Size         string `json:"size,omitempty"`
	OrderType    string `json:"orderType,omitempty"`
	TimeInForce  string `json:"timeInForce,omitempty"`
	Leverage     uint32 `json:"leverage,omitempty"`
	ReduceOnly   bool   `json:"reduceOnly,omitempty"`
	AccountIndex int64  `json:"accountIndex,omitempty"`
	APIKeyIndex  uint8  `json:"apiKeyIndex,omitempty"`
	Nonce        int64  `json:"nonce,omitempty"`
	FetchMarket  bool   `json:"fetchMarket,omitempty"`
	Submit       bool   `json:"submit,omitempty"`
}

// LighterRehearseResult reports non-secret Lighter Testnet signature & /sendTx outcome.
type LighterRehearseResult struct {
	Network           string                    `json:"network"`
	ChainID           uint32                    `json:"chainId"`
	Origin            string                    `json:"origin"`
	AgentVaultID      string                    `json:"agentVaultId"`
	OperationID       string                    `json:"operationId"`
	MarketID          int64                     `json:"marketId"`
	Symbol            string                    `json:"symbol"`
	PublicKeyHex      string                    `json:"publicKeyHex"`
	TxHashHex         string                    `json:"txHashHex"`
	SchnorrValid      bool                      `json:"schnorrValid"`
	EstimatedMicroUSD uint64                    `json:"estimatedMicroUsd"`
	Submission        *lighter.SubmissionResult `json:"submission,omitempty"`
}

func (s *Store) lighterStatus(ctx context.Context) map[string]any {
	out := map[string]any{
		"available":     false,
		"network":       "testnet",
		"chainId":       signer.LighterTestnetChainID,
		"mainnetLocked": !signer.LighterActivationEnabled,
		"wenHeld":       !miningActivationEnabled,
	}
	rt := s.stage4Runtime()
	if rt == nil || rt.profile != signer.ExecutionProfileTestnet || rt.lighterClient == nil || rt.signerCall == nil {
		return out
	}
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	resp, err := rt.signerCall(callCtx, signer.Request{Action: "lighter-status"})
	if err != nil || resp.LighterStatus == nil {
		return out
	}
	out["available"] = resp.LighterStatus.Enabled
	out["publicKeyHex"] = resp.LighterStatus.PublicKeyHex
	out["origin"] = rt.lighterClient.Origin()
	return out
}

func (s *Store) rehearseLighter(ctx context.Context, req LighterRehearseRequest) (*LighterRehearseResult, error) {
	if s.isGlobalKillActive(ctx) {
		return nil, errGlobalKillSwitchActive
	}
	rt, vaultID, err := s.resolveStage4Vault(req.AgentVaultID)
	if err != nil {
		return nil, err
	}
	if rt.lighterClient == nil {
		return nil, errors.New("lighter execution client is not configured")
	}
	symbol := strings.ToUpper(strings.TrimSpace(req.Symbol))
	if symbol == "" {
		symbol = "ETH"
	}
	side := strings.ToUpper(strings.TrimSpace(req.Side))
	if side == "" {
		side = "BUY"
	}
	price := strings.TrimSpace(req.Price)
	if price == "" {
		price = "1800.00"
	}
	size := strings.TrimSpace(req.Size)
	if size == "" {
		size = "0.002"
	}

	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	meta := lighter.OrderBookMeta{
		Symbol:                 symbol,
		MarketID:               0,
		SupportedSizeDecimals:  4,
		SupportedPriceDecimals: 2,
	}
	if req.FetchMarket {
		books, err := rt.lighterClient.FetchOrderBooks(callCtx)
		if err != nil {
			return nil, err
		}
		found := false
		for _, ob := range books {
			if strings.EqualFold(ob.Symbol, symbol) {
				meta = ob
				found = true
				break
			}
		}
		if !found && len(books) > 0 {
			meta = books[0]
			symbol = strings.ToUpper(meta.Symbol)
		}
	}

	intent, err := lighter.BuildCreateOrderIntent(lighter.OrderDraft{
		Symbol:       symbol,
		Side:         side,
		Price:        price,
		Size:         size,
		OrderType:    req.OrderType,
		TimeInForce:  req.TimeInForce,
		ReduceOnly:   req.ReduceOnly,
		Leverage:     req.Leverage,
		AccountIndex: req.AccountIndex,
		APIKeyIndex:  req.APIKeyIndex,
		Nonce:        req.Nonce,
	}, meta, time.Now().Unix())
	if err != nil {
		return nil, err
	}

	signResp, err := rt.signerCall(callCtx, signer.Request{
		Action:       "lighter-sign-order",
		AgentVaultID: vaultID,
		LighterOrder: &intent,
	})
	if err != nil {
		return nil, err
	}
	if signResp.LighterSigned == nil {
		return nil, errors.New("missing lighter signed tx response")
	}

	pkB, _ := hex.DecodeString(strings.TrimPrefix(signResp.LighterSigned.PublicKeyHex, "0x"))
	txB, _ := hex.DecodeString(strings.TrimPrefix(signResp.LighterSigned.TxHashHex, "0x"))
	sigB, _ := hex.DecodeString(strings.TrimPrefix(signResp.LighterSigned.SignatureHex, "0x"))
	if err := signer.LighterVerifySignature(pkB, txB, sigB); err != nil {
		return nil, err
	}

	res := &LighterRehearseResult{
		Network:           signResp.LighterSigned.Network,
		ChainID:           signResp.LighterSigned.ChainID,
		Origin:            rt.lighterClient.Origin(),
		AgentVaultID:      vaultID,
		OperationID:       signResp.LighterSigned.OperationID,
		MarketID:          meta.MarketID,
		Symbol:            symbol,
		PublicKeyHex:      signResp.LighterSigned.PublicKeyHex,
		TxHashHex:         signResp.LighterSigned.TxHashHex,
		SchnorrValid:      true,
		EstimatedMicroUSD: signResp.LighterSigned.EstimatedMicroUSD,
	}
	if req.Submit {
		sub, subErr := rt.lighterClient.SubmitSignedTx(callCtx, signResp.LighterSigned)
		if subErr != nil {
			return res, subErr
		}
		res.Submission = &sub
	}
	return res, nil
}

func (h *HTTPHandler) lighterStatusAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	writeJSON(w, 200, h.store.lighterStatus(r.Context()))
}

func (h *HTTPHandler) lighterRehearseAPI(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !h.mutation(w, r) || !h.csrf(w, r, token) {
		return
	}
	var req LighterRehearseRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	authCtx, ok := h.requireStepUpIfOwnerAuthConfigured(w, r, token, "execute", "lighter", false)
	if !ok {
		return
	}
	res, err := h.store.rehearseLighter(authCtx, req)
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
