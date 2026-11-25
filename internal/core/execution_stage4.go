package core

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/hyperliquid"
	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/signer"
)

// TestnetExecutionConfig configures opt-in Stage 4 non-WEN Devnet/Testnet execution.
// Mainnet and WEN SAT mining remain permanently blocked under this profile.
type TestnetExecutionConfig struct {
	Profile            string
	SignerCall         func(context.Context, signer.Request) (signer.Response, error)
	AgentVaultID       string
	SolanaDevnetRPC    string
	SolanaRoundTripper http.RoundTripper
	HyperliquidClient  *hyperliquid.Client
}

// ExecutionStatusView describes live Stage 4 testnet execution readiness without leaking credentials.
type ExecutionStatusView struct {
	Profile             string `json:"profile"`
	Available           bool   `json:"available"`
	WalletSigner        bool   `json:"walletSigner"`
	FinancialExecution  bool   `json:"financialExecution"`
	MainnetLocked       bool   `json:"mainnetLocked"`
	WENHeld             bool   `json:"wenHeld"`
	SolanaDevnetReady   bool   `json:"solanaDevnetReady"`
	SolanaDevnetHost    string `json:"solanaDevnetHost,omitempty"`
	HyperliquidTestnet  bool   `json:"hyperliquidTestnet"`
	AgentVaultID        string `json:"agentVaultId,omitempty"`
	SignerTestnetActive bool   `json:"signerTestnetActive"`
	SignerPublicKey     string `json:"signerPublicKey,omitempty"`
	SignerNetwork       string `json:"signerNetwork,omitempty"`
}

// SolanaDevnetRehearseRequest parameterizes an instruction-constrained SPL Memo +
// ComputeBudget transaction rehearsed over a verified Solana Devnet RPC endpoint.
type SolanaDevnetRehearseRequest struct {
	AgentVaultID      string `json:"agentVaultId,omitempty"`
	Asset             string `json:"asset,omitempty"`
	Memo              string `json:"memo,omitempty"`
	EstimatedMicroUSD uint64 `json:"estimatedMicroUsd,omitempty"`
	SimulateOnly      bool   `json:"simulateOnly,omitempty"`
}

// SolanaDevnetRehearseResult summarizes a Devnet simulation/broadcast receipt using
// host-only RPC attribution and public signature/digest metadata.
type SolanaDevnetRehearseResult struct {
	Network                  string `json:"network"`
	GenesisHash              string `json:"genesisHash"`
	RPCHost                  string `json:"rpcHost"`
	AgentVaultID             string `json:"agentVaultId"`
	OperationID              string `json:"operationId"`
	Signature                string `json:"signature"`
	MessageSHA256            string `json:"messageSha256"`
	SignerPublicKey          string `json:"signerPublicKey"`
	PriorityFeeMicroLamports uint64 `json:"priorityFeeMicroLamports"`
	SimulateOnly             bool   `json:"simulateOnly"`
	SimulatedOK              bool   `json:"simulatedOk"`
	SimulatedSlot            uint64 `json:"simulatedSlot"`
	SimulationError          string `json:"simulationError,omitempty"`
	Broadcasted              bool   `json:"broadcasted"`
	Confirmed                bool   `json:"confirmed"`
	Attempts                 int    `json:"attempts,omitempty"`
}

type stage4Runtime struct {
	profile        string
	signerCall     func(context.Context, signer.Request) (signer.Response, error)
	agentVaultID   string
	devnetExecutor *onchain.DevnetExecutor
	devnetHost     string
	hyperliquidOn  bool
}

var (
	stage4RuntimeMu sync.RWMutex
	stage4Runtimes  = map[*Store]*stage4Runtime{}
)

func init() {
	stage4CapabilitiesHook = func(s *Store) (bool, bool, bool) {
		rt := s.stage4Runtime()
		if rt != nil && rt.profile == signer.ExecutionProfileTestnet {
			return true, true, false
		}
		return false, false, false
	}
	stage4LiveExecutionHook = func(s *Store) bool {
		rt := s.stage4Runtime()
		return rt != nil && rt.profile == signer.ExecutionProfileTestnet && !hyperliquidActivationEnabled && !signer.HyperliquidActivationEnabled
	}
	stage4ClearHook = func(s *Store) {
		s.clearStage4Runtime()
	}
}

func (s *Store) stage4Runtime() *stage4Runtime {
	if s == nil {
		return nil
	}
	stage4RuntimeMu.RLock()
	defer stage4RuntimeMu.RUnlock()
	return stage4Runtimes[s]
}

func (s *Store) clearStage4Runtime() {
	if s == nil {
		return
	}
	stage4RuntimeMu.Lock()
	delete(stage4Runtimes, s)
	stage4RuntimeMu.Unlock()
}

// ConfigureTestnetExecution enables opt-in Stage 4 non-WEN Devnet/Testnet execution
// on Store while preserving compile-time Mainnet and WEN SAT mining holds.
func (s *Store) ConfigureTestnetExecution(ctx context.Context, cfg TestnetExecutionConfig) error {
	if s == nil {
		return signer.ErrUnavailable
	}
	if cfg.Profile == "" || cfg.Profile == signer.ExecutionProfileOff {
		s.clearStage4Runtime()
		_ = s.clearHyperliquidRuntime()
		return nil
	}
	if cfg.Profile != signer.ExecutionProfileTestnet {
		return errors.New("stage 4 execution profile must be off or testnet")
	}
	if cfg.SignerCall == nil {
		return errors.New("stage 4 testnet execution requires signer IPC channel")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	stResp, err := cfg.SignerCall(checkCtx, signer.Request{Action: "status"})
	if err != nil {
		return fmt.Errorf("signer status check failed: %w", err)
	}
	if !stResp.Status.TestnetExecution {
		return errors.New("signer daemon does not have testnet execution enabled")
	}
	if stResp.Status.Account != nil && (stResp.Status.Account.Network == "mainnet" || stResp.Status.Account.Network == "mainnet-beta") {
		return signer.ErrUnavailable
	}

	var devnetExec *onchain.DevnetExecutor
	var devnetHost string
	if rpcURL := strings.TrimSpace(cfg.SolanaDevnetRPC); rpcURL != "" {
		devnetExec, err = onchain.NewDevnetExecutor(rpcURL, cfg.SolanaRoundTripper)
		if err != nil {
			return err
		}
		devnetHost = devnetExec.SanitizedHost()
	}

	hlOn := false
	hlResp, hlErr := cfg.SignerCall(checkCtx, signer.Request{Action: "hyperliquid-status"})
	if hlErr == nil && hlResp.HyperliquidStatus != nil && hlResp.HyperliquidStatus.Policy != nil {
		if hlResp.HyperliquidStatus.Policy.Network == "mainnet" {
			return signer.ErrUnavailable
		}
		if hlResp.HyperliquidStatus.Policy.Network == "testnet" {
			hlClient := cfg.HyperliquidClient
			if hlClient == nil {
				hlClient, err = hyperliquid.NewTestnetExecutionClient()
				if err != nil {
					return err
				}
			}
			if err := s.ConfigureHyperliquidIPC(hlClient, cfg.SignerCall, strings.TrimSpace(cfg.AgentVaultID)); err != nil {
				return err
			}
			hlOn = true
		}
	}

	stage4RuntimeMu.Lock()
	stage4Runtimes[s] = &stage4Runtime{
		profile:        signer.ExecutionProfileTestnet,
		signerCall:     cfg.SignerCall,
		agentVaultID:   strings.TrimSpace(cfg.AgentVaultID),
		devnetExecutor: devnetExec,
		devnetHost:     devnetHost,
		hyperliquidOn:  hlOn,
	}
	stage4RuntimeMu.Unlock()
	return nil
}

// ConfigureTestnetExecution enables opt-in Stage 4 non-WEN Devnet/Testnet execution on Server.
func (server *Server) ConfigureTestnetExecution(ctx context.Context, cfg TestnetExecutionConfig) error {
	if server == nil || server.Store == nil {
		return signer.ErrUnavailable
	}
	return server.Store.ConfigureTestnetExecution(ctx, cfg)
}

func (s *Store) executionStatus(ctx context.Context) ExecutionStatusView {
	view := ExecutionStatusView{
		Profile:       signer.ExecutionProfileOff,
		MainnetLocked: !hyperliquidActivationEnabled && !signer.HyperliquidActivationEnabled,
		WENHeld:       !miningActivationEnabled,
	}
	rt := s.stage4Runtime()
	if rt == nil || rt.profile != signer.ExecutionProfileTestnet {
		return view
	}
	view.Profile = rt.profile
	view.Available = true
	view.WalletSigner = true
	view.FinancialExecution = true
	view.SolanaDevnetReady = rt.devnetExecutor != nil
	view.SolanaDevnetHost = rt.devnetHost
	view.HyperliquidTestnet = rt.hyperliquidOn || s.hyperliquidRuntime() != nil
	view.AgentVaultID = rt.agentVaultID
	if rt.signerCall != nil {
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		stResp, err := rt.signerCall(probeCtx, signer.Request{Action: "status"})
		cancel()
		if err == nil {
			view.SignerTestnetActive = stResp.Status.TestnetExecution
			if stResp.Status.Account != nil {
				view.SignerPublicKey = stResp.Status.Account.PublicKey
				view.SignerNetwork = stResp.Status.Account.Network
			}
		}
	}
	return view
}

func (s *Store) listAgentVaults(ctx context.Context) ([]signer.AgentVaultUsage, string, error) {
	rt := s.stage4Runtime()
	if rt == nil || rt.signerCall == nil {
		return []signer.AgentVaultUsage{}, "", nil
	}
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	resp, err := rt.signerCall(callCtx, signer.Request{Action: "agent-vault-list"})
	if err != nil {
		return nil, rt.agentVaultID, err
	}
	vaults := slices.Clone(resp.AgentVaults)
	if vaults == nil {
		vaults = []signer.AgentVaultUsage{}
	}
	return vaults, rt.agentVaultID, nil
}

func (s *Store) rehearseSolanaDevnet(ctx context.Context, req SolanaDevnetRehearseRequest) (*SolanaDevnetRehearseResult, error) {
	rt := s.stage4Runtime()
	if rt == nil || rt.devnetExecutor == nil || rt.signerCall == nil {
		return nil, errors.New("solana devnet execution is not configured")
	}
	vaultID := strings.TrimSpace(req.AgentVaultID)
	if vaultID == "" {
		vaultID = rt.agentVaultID
	}
	if vaultID == "" {
		return nil, errors.New("agentVaultId required for solana devnet execution")
	}
	asset := strings.ToUpper(strings.TrimSpace(req.Asset))
	if asset == "" {
		asset = "SOL"
	}
	memo := strings.TrimSpace(req.Memo)
	if memo == "" {
		memo = "fased-devnet-rehearsal"
	}
	if len(memo) > 64 {
		return nil, errors.New("solana devnet rehearsal memo exceeds 64 bytes")
	}
	est := req.EstimatedMicroUSD
	if est == 0 {
		est = 1_000
	}

	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	if err := rt.devnetExecutor.VerifyGenesis(callCtx); err != nil {
		return nil, err
	}
	_, bh, lastValidHeight, _, err := rt.devnetExecutor.LatestBlockhash(callCtx)
	if err != nil {
		return nil, err
	}
	fee, _, err := rt.devnetExecutor.EstimatePriorityFee(callCtx)
	if err != nil {
		return nil, err
	}
	stResp, err := rt.signerCall(callCtx, signer.Request{Action: "status"})
	if err != nil {
		return nil, err
	}
	if !stResp.Status.TestnetExecution || stResp.Status.Account == nil {
		return nil, signer.ErrUnavailable
	}
	if stResp.Status.Account.Network == "mainnet" || stResp.Status.Account.Network == "mainnet-beta" {
		return nil, signer.ErrUnavailable
	}
	pubRaw, err := hex.DecodeString(stResp.Status.Account.PublicKey)
	if err != nil || len(pubRaw) != 32 {
		return nil, errors.New("invalid signer Ed25519 public key")
	}
	var feePayer [32]byte
	copy(feePayer[:], pubRaw)

	msgBytes, err := onchain.BuildDevnetComputeMemoMessage(feePayer, bh, 50_000, fee, memo)
	if err != nil {
		return nil, err
	}
	var opRand [8]byte
	if _, err := rand.Read(opRand[:]); err != nil {
		return nil, err
	}
	opID := "sol-devnet-" + hex.EncodeToString(opRand[:])

	signResp, err := rt.signerCall(callCtx, signer.Request{
		Action:       "solana-devnet-sign",
		AgentVaultID: vaultID,
		SolanaDevnetSign: &signer.SolanaDevnetSignIntent{
			Network:           "devnet",
			GenesisHash:       onchain.SolanaDevnetGenesisHash,
			OperationID:       opID,
			Asset:             asset,
			EstimatedMicroUSD: est,
			MessageBase64:     base64.StdEncoding.EncodeToString(msgBytes),
			ExpiresAt:         time.Now().Unix() + 120,
		},
	})
	if err != nil {
		return nil, err
	}
	if signResp.SolanaDevnetSigned == nil {
		return nil, errors.New("missing signed devnet transaction")
	}
	signedTxBytes, err := base64.StdEncoding.DecodeString(signResp.SolanaDevnetSigned.SignedTxBase64)
	if err != nil {
		return nil, errors.New("invalid signed devnet packet")
	}

	simSlot, simErr, err := rt.devnetExecutor.SimulateTransaction(callCtx, signedTxBytes)
	if err != nil {
		return nil, err
	}
	if !req.SimulateOnly && simErr != "" {
		return nil, fmt.Errorf("solana devnet simulation failed: %s", simErr)
	}

	result := &SolanaDevnetRehearseResult{
		Network:                  "devnet",
		GenesisHash:              onchain.SolanaDevnetGenesisHash,
		RPCHost:                  rt.devnetHost,
		AgentVaultID:             vaultID,
		OperationID:              opID,
		Signature:                signResp.SolanaDevnetSigned.Signature,
		MessageSHA256:            signResp.SolanaDevnetSigned.MessageSHA256,
		SignerPublicKey:          signResp.SolanaDevnetSigned.PublicKeyBase58,
		PriorityFeeMicroLamports: fee,
		SimulateOnly:             req.SimulateOnly,
		SimulatedOK:              simErr == "",
		SimulatedSlot:            simSlot,
		SimulationError:          simErr,
	}
	if !req.SimulateOnly {
		rbRes, rbErr := rt.devnetExecutor.SendWithRebroadcast(callCtx, signedTxBytes, signResp.SolanaDevnetSigned.Signature, lastValidHeight, onchain.RebroadcastConfig{})
		result.Broadcasted = rbRes.Attempted
		result.Confirmed = rbRes.Confirmed
		result.Attempts = rbRes.Attempts
		if rbErr != nil {
			return result, rbErr
		}
	}
	return result, nil
}

func (h *HTTPHandler) signerStatusAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	rt := h.store.stage4Runtime()
	if rt == nil || rt.profile != signer.ExecutionProfileTestnet {
		writeJSON(w, 200, map[string]any{
			"available":        false,
			"executionEnabled": false,
			"reason":           "real wallets unavailable; public fixture references are managed through accounts",
		})
		return
	}
	writeJSON(w, 200, map[string]any{
		"available":          true,
		"executionEnabled":   true,
		"profile":            rt.profile,
		"mainnetLocked":      !hyperliquidActivationEnabled && !signer.HyperliquidActivationEnabled,
		"wenHeld":            !miningActivationEnabled,
		"solanaDevnetReady":  rt.devnetExecutor != nil,
		"hyperliquidTestnet": rt.hyperliquidOn || h.store.hyperliquidRuntime() != nil,
	})
}

func (h *HTTPHandler) executionStatusAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	writeJSON(w, 200, h.store.executionStatus(r.Context()))
}

func (h *HTTPHandler) agentVaultsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	vaults, activeID, err := h.store.listAgentVaults(r.Context())
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	profile := signer.ExecutionProfileOff
	if rt := h.store.stage4Runtime(); rt != nil {
		profile = rt.profile
	}
	writeJSON(w, 200, map[string]any{
		"vaults":        vaults,
		"profile":       profile,
		"activeVaultId": activeID,
		"mainnetLocked": !hyperliquidActivationEnabled && !signer.HyperliquidActivationEnabled,
		"wenHeld":       !miningActivationEnabled,
	})
}

func (h *HTTPHandler) solanaDevnetRehearseAPI(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !h.mutation(w, r) || !h.csrf(w, r, token) {
		return
	}
	var req SolanaDevnetRehearseRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := h.store.rehearseSolanaDevnet(r.Context(), req)
	if err != nil {
		code := 400
		if errors.Is(err, signer.ErrUnavailable) {
			code = 403
		}
		writeError(w, code, err.Error())
		return
	}
	writeJSON(w, 200, res)
}

func toolGetExecutionStatus(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var empty struct{}
	if err := decodeToolArgs(rawArgs, &empty); err != nil {
		return nil, err
	}
	return s.executionStatus(ctx), nil
}

func toolListAgentVaults(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (any, error) {
	var empty struct{}
	if err := decodeToolArgs(rawArgs, &empty); err != nil {
		return nil, err
	}
	vaults, activeID, err := s.listAgentVaults(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"vaults":        vaults,
		"count":         len(vaults),
		"activeVaultId": activeID,
		"mainnetLocked": !hyperliquidActivationEnabled && !signer.HyperliquidActivationEnabled,
		"wenHeld":       !miningActivationEnabled,
	}, nil
}
