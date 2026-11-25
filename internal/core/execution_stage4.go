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
	"github.com/fased-ai/fased-app/internal/kalshi"
	"github.com/fased-ai/fased-app/internal/lighter"
	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/polymarket"
	"github.com/fased-ai/fased-app/internal/signer"
)

// TestnetExecutionConfig configures opt-in Stage 4 non-WEN Devnet/Testnet execution.
// Mainnet and WEN SAT mining remain permanently blocked under this profile.
type TestnetExecutionConfig struct {
	Profile            string
	MainnetPolicy      signer.MainnetCappedPolicy
	SignerCall         func(context.Context, signer.Request) (signer.Response, error)
	AgentVaultID       string
	SolanaDevnetRPC    string
	SolanaMainnetRPC   string
	SolanaRoundTripper http.RoundTripper
	HyperliquidClient  *hyperliquid.Client
	PolymarketClient   *polymarket.Client
	KalshiClient       *kalshi.Client
	LighterClient      *lighter.Client
}

// ExecutionStatusView describes live Stage 4 testnet execution readiness without leaking credentials.
type ExecutionStatusView struct {
	Profile                 string `json:"profile"`
	Available               bool   `json:"available"`
	WalletSigner            bool   `json:"walletSigner"`
	FinancialExecution      bool   `json:"financialExecution"`
	MainnetLocked           bool   `json:"mainnetLocked"`
	MainnetCapped           bool   `json:"mainnetCapped,omitempty"`
	MainnetMaxOrderMicroUSD uint64 `json:"mainnetMaxOrderMicroUsd,omitempty"`
	MainnetMaxDailyMicroUSD uint64 `json:"mainnetMaxDailyMicroUsd,omitempty"`
	WENHeld                 bool   `json:"wenHeld"`
	SolanaDevnetReady       bool   `json:"solanaDevnetReady"`
	SolanaDevnetHost        string `json:"solanaDevnetHost,omitempty"`
	SolanaMainnetReady      bool   `json:"solanaMainnetReady,omitempty"`
	SolanaMainnetHost       string `json:"solanaMainnetHost,omitempty"`
	HyperliquidTestnet      bool   `json:"hyperliquidTestnet"`
	HyperliquidMainnet      bool   `json:"hyperliquidMainnet,omitempty"`
	PolymarketTestnetReady  bool   `json:"polymarketTestnetReady"`
	PolymarketSignerAddress string `json:"polymarketSignerAddress,omitempty"`
	KalshiDemoReady         bool   `json:"kalshiDemoReady"`
	KalshiKeyConfigured     bool   `json:"kalshiKeyConfigured"`
	KalshiKeyID             string `json:"kalshiKeyId,omitempty"`
	LighterTestnetReady     bool   `json:"lighterTestnetReady"`
	LighterPublicKey        string `json:"lighterPublicKey,omitempty"`
	AgentVaultID            string `json:"agentVaultId,omitempty"`
	SignerTestnetActive     bool   `json:"signerTestnetActive"`
	SignerPublicKey         string `json:"signerPublicKey,omitempty"`
	SignerNetwork           string `json:"signerNetwork,omitempty"`
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
	mu                sync.Mutex
	profile           string
	mainnetPolicy     signer.MainnetCappedPolicy
	signerCall        func(context.Context, signer.Request) (signer.Response, error)
	agentVaultID      string
	devnetExecutor    *onchain.DevnetExecutor
	devnetHost        string
	mainnetExecutor   *onchain.DevnetExecutor
	mainnetHost       string
	hyperliquidClient *hyperliquid.Client
	polymarketClient  *polymarket.Client
	kalshiClient      *kalshi.Client
	lighterClient     *lighter.Client
}

var (
	stage4RuntimeMu sync.RWMutex
	stage4Runtimes  = map[*Store]*stage4Runtime{}
)

func init() {
	stage4CapabilitiesHook = func(s *Store) (bool, bool, bool) {
		rt := s.stage4Runtime()
		if rt != nil && (rt.profile == signer.ExecutionProfileTestnet || rt.profile == signer.ExecutionProfileMainnetCapped) {
			return true, true, false
		}
		return false, false, false
	}
	stage4LiveExecutionHook = func(s *Store) bool {
		rt := s.stage4Runtime()
		return rt != nil && (rt.profile == signer.ExecutionProfileTestnet || rt.profile == signer.ExecutionProfileMainnetCapped) && !hyperliquidActivationEnabled && !signer.HyperliquidActivationEnabled
	}
	stage4MainnetCappedHook = func(s *Store) (bool, uint64) {
		rt := s.stage4Runtime()
		if rt != nil && rt.profile == signer.ExecutionProfileMainnetCapped {
			return true, rt.mainnetPolicy.MaxOrderMicroUSD
		}
		return false, 0
	}
	stage4ClearHook = func(s *Store) {
		s.clearStage4Runtime()
	}
	stage4EnsureHyperliquidIPCHook = func(ctx context.Context, s *Store) bool {
		return s.ensureHyperliquidIPC(ctx)
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
	if cfg.Profile == signer.ExecutionProfileMainnetCapped {
		return s.configureMainnetCappedExecution(ctx, cfg)
	}
	if cfg.Profile != signer.ExecutionProfileTestnet {
		return errors.New("stage 4 execution profile must be off, testnet, or mainnet-capped")
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

	hlClient := cfg.HyperliquidClient
	if hlClient == nil {
		hlClient, err = hyperliquid.NewTestnetExecutionClient()
		if err != nil {
			return err
		}
	}
	hlResp, hlErr := cfg.SignerCall(checkCtx, signer.Request{Action: "hyperliquid-status"})
	if hlErr == nil && hlResp.HyperliquidStatus != nil && hlResp.HyperliquidStatus.Policy != nil {
		if hlResp.HyperliquidStatus.Policy.Network == "mainnet" {
			return signer.ErrUnavailable
		}
		if hlResp.HyperliquidStatus.Enabled && hlResp.HyperliquidStatus.Policy.Network == "testnet" && hlResp.HyperliquidStatus.Address != "" {
			if err := s.ConfigureHyperliquidIPC(hlClient, cfg.SignerCall, strings.TrimSpace(cfg.AgentVaultID)); err != nil {
				return err
			}
		}
	}

	pmClient := cfg.PolymarketClient
	if pmClient == nil {
		pmClient = polymarket.NewTestnetExecutionClient()
	}
	kClient := cfg.KalshiClient
	if kClient == nil {
		kClient = kalshi.NewDemoClient()
	}
	lClient := cfg.LighterClient
	if lClient == nil {
		lClient = lighter.NewTestnetClient()
	}

	stage4RuntimeMu.Lock()
	stage4Runtimes[s] = &stage4Runtime{
		profile:           signer.ExecutionProfileTestnet,
		signerCall:        cfg.SignerCall,
		agentVaultID:      strings.TrimSpace(cfg.AgentVaultID),
		devnetExecutor:    devnetExec,
		devnetHost:        devnetHost,
		hyperliquidClient: hlClient,
		polymarketClient:  pmClient,
		kalshiClient:      kClient,
		lighterClient:     lClient,
	}
	stage4RuntimeMu.Unlock()
	return nil
}

// ensureHyperliquidIPC lazily binds Core's Hyperliquid runtime over signer IPC
// once the signer vault has been unlocked and an eligible policy is active.
func (s *Store) ensureHyperliquidIPC(ctx context.Context) bool {
	if s.hyperliquidRuntime() != nil {
		return true
	}
	rt := s.stage4Runtime()
	if rt == nil || (rt.profile != signer.ExecutionProfileTestnet && rt.profile != signer.ExecutionProfileMainnetCapped) || rt.signerCall == nil || rt.hyperliquidClient == nil {
		return false
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if s.hyperliquidRuntime() != nil {
		return true
	}
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, hyperliquidSignerTimeout)
	defer cancel()
	hlResp, err := rt.signerCall(probeCtx, signer.Request{Action: "hyperliquid-status"})
	if err != nil || hlResp.HyperliquidStatus == nil || !hlResp.HyperliquidStatus.Enabled || hlResp.HyperliquidStatus.Policy == nil {
		return false
	}
	wantNet := "testnet"
	if rt.profile == signer.ExecutionProfileMainnetCapped {
		wantNet = "mainnet"
		if !rt.hyperliquidClient.MainnetCapped() {
			return false
		}
	}
	if hlResp.HyperliquidStatus.Policy.Network != wantNet || hlResp.HyperliquidStatus.Address == "" {
		return false
	}
	if err := s.ConfigureHyperliquidIPC(rt.hyperliquidClient, rt.signerCall, rt.agentVaultID); err != nil {
		return false
	}
	return s.hyperliquidRuntime() != nil
}

func (s *Store) configureMainnetCappedExecution(ctx context.Context, cfg TestnetExecutionConfig) error {
	if err := cfg.MainnetPolicy.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.AgentVaultID) == "" {
		return errors.New("stage 4 mainnet-capped execution requires --agent-vault-id")
	}
	if cfg.SignerCall == nil {
		return errors.New("stage 4 mainnet-capped execution requires signer IPC channel")
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
	if !stResp.Status.MainnetCapped || stResp.Status.TestnetExecution {
		return errors.New("signer daemon does not have mainnet-capped execution enabled")
	}
	if stResp.Status.Account != nil && stResp.Status.Account.Network == "devnet" {
		return signer.ErrUnavailable
	}

	var mainnetExec *onchain.DevnetExecutor
	var mainnetHost string
	if rpcURL := strings.TrimSpace(cfg.SolanaMainnetRPC); rpcURL != "" {
		mainnetExec, err = onchain.NewMainnetExecutor(rpcURL, cfg.SolanaRoundTripper)
		if err != nil {
			return err
		}
		mainnetHost = mainnetExec.SanitizedHost()
	}

	hlResp, hlErr := cfg.SignerCall(checkCtx, signer.Request{Action: "hyperliquid-status"})
	var hlClient *hyperliquid.Client
	if cfg.HyperliquidClient != nil {
		if !cfg.HyperliquidClient.Controlled() || !cfg.HyperliquidClient.MainnetCapped() || cfg.HyperliquidClient.Network() != "mainnet" {
			return signer.ErrUnavailable
		}
		hlClient = cfg.HyperliquidClient
	} else if hlErr == nil && hlResp.HyperliquidStatus != nil && hlResp.HyperliquidStatus.Enabled {
		hlClient, err = hyperliquid.NewMainnetCappedExecutionClient()
		if err != nil {
			return err
		}
	}

	if err := s.configureStage5MainnetCapped(cfg.MainnetPolicy.MaxOrderMicroUSD); err != nil {
		return err
	}
	if err := s.configureStage6MainnetCapped(cfg.MainnetPolicy.MaxOrderMicroUSD); err != nil {
		return err
	}
	stage4RuntimeMu.Lock()
	stage4Runtimes[s] = &stage4Runtime{
		profile:           signer.ExecutionProfileMainnetCapped,
		mainnetPolicy:     cfg.MainnetPolicy,
		signerCall:        cfg.SignerCall,
		agentVaultID:      strings.TrimSpace(cfg.AgentVaultID),
		mainnetExecutor:   mainnetExec,
		mainnetHost:       mainnetHost,
		hyperliquidClient: hlClient,
	}
	stage4RuntimeMu.Unlock()

	if hlClient != nil && hlErr == nil && hlResp.HyperliquidStatus != nil && hlResp.HyperliquidStatus.Enabled && hlResp.HyperliquidStatus.Policy != nil {
		if hlResp.HyperliquidStatus.Policy.Network != "mainnet" {
			s.clearStage4Runtime()
			return signer.ErrUnavailable
		}
		if hlResp.HyperliquidStatus.Address != "" {
			if err := s.ConfigureHyperliquidIPC(hlClient, cfg.SignerCall, strings.TrimSpace(cfg.AgentVaultID)); err != nil {
				s.clearStage4Runtime()
				return err
			}
		}
	}
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
		MainnetLocked: !hyperliquidActivationEnabled && !signer.HyperliquidActivationEnabled && !signer.PolymarketActivationEnabled && !signer.KalshiActivationEnabled && !signer.LighterActivationEnabled,
		WENHeld:       !miningActivationEnabled,
	}
	rt := s.stage4Runtime()
	if rt == nil || (rt.profile != signer.ExecutionProfileTestnet && rt.profile != signer.ExecutionProfileMainnetCapped) {
		return view
	}
	view.Profile = rt.profile
	view.Available = true
	hlReady := s.ensureHyperliquidIPC(ctx)
	hasLiveStage4Venue := rt.devnetExecutor != nil || rt.mainnetExecutor != nil || hlReady || rt.polymarketClient != nil || rt.kalshiClient != nil || rt.lighterClient != nil
	view.WalletSigner = hasLiveStage4Venue
	view.FinancialExecution = hasLiveStage4Venue
	if rt.profile == signer.ExecutionProfileMainnetCapped {
		view.MainnetLocked = !hasLiveStage4Venue
		view.MainnetCapped = true
		view.MainnetMaxOrderMicroUSD = rt.mainnetPolicy.MaxOrderMicroUSD
		view.MainnetMaxDailyMicroUSD = rt.mainnetPolicy.MaxDailyMicroUSD
	}
	view.SolanaDevnetReady = rt.devnetExecutor != nil
	view.SolanaDevnetHost = rt.devnetHost
	view.SolanaMainnetReady = rt.mainnetExecutor != nil
	view.SolanaMainnetHost = rt.mainnetHost
	view.HyperliquidTestnet = hlReady && rt.profile == signer.ExecutionProfileTestnet
	view.HyperliquidMainnet = hlReady && rt.profile == signer.ExecutionProfileMainnetCapped
	view.AgentVaultID = rt.agentVaultID
	if rt.signerCall != nil {
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		stResp, err := rt.signerCall(probeCtx, signer.Request{Action: "status"})
		if err == nil {
			view.SignerTestnetActive = stResp.Status.TestnetExecution || stResp.Status.MainnetCapped
			if stResp.Status.Account != nil {
				view.SignerPublicKey = stResp.Status.Account.PublicKey
				view.SignerNetwork = stResp.Status.Account.Network
			}
		}
		if pmResp, pmErr := rt.signerCall(probeCtx, signer.Request{Action: "polymarket-status"}); pmErr == nil && pmResp.PolymarketStatus != nil {
			view.PolymarketTestnetReady = pmResp.PolymarketStatus.Enabled && rt.polymarketClient != nil
			view.PolymarketSignerAddress = pmResp.PolymarketStatus.SignerAddress
		}
		if kResp, kErr := rt.signerCall(probeCtx, signer.Request{Action: "kalshi-status"}); kErr == nil && kResp.KalshiStatus != nil {
			view.KalshiDemoReady = kResp.KalshiStatus.Enabled && rt.kalshiClient != nil
			view.KalshiKeyConfigured = kResp.KalshiStatus.Configured
			view.KalshiKeyID = kResp.KalshiStatus.KeyID
		}
		if lResp, lErr := rt.signerCall(probeCtx, signer.Request{Action: "lighter-status"}); lErr == nil && lResp.LighterStatus != nil {
			view.LighterTestnetReady = lResp.LighterStatus.Enabled && rt.lighterClient != nil
			view.LighterPublicKey = lResp.LighterStatus.PublicKeyHex
		}
		cancel()
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
	if rt == nil || (rt.profile != signer.ExecutionProfileTestnet && rt.profile != signer.ExecutionProfileMainnetCapped) {
		writeJSON(w, 200, map[string]any{
			"available":        false,
			"executionEnabled": false,
			"reason":           "real wallets unavailable; public fixture references are managed through accounts",
		})
		return
	}
	hlReady := h.store.ensureHyperliquidIPC(r.Context())
	hasLiveStage4Venue := rt.devnetExecutor != nil || rt.mainnetExecutor != nil || hlReady || rt.polymarketClient != nil || rt.kalshiClient != nil || rt.lighterClient != nil
	writeJSON(w, 200, map[string]any{
		"available":              true,
		"executionEnabled":       hasLiveStage4Venue,
		"profile":                rt.profile,
		"mainnetLocked":          !hasLiveStage4Venue || (rt.profile != signer.ExecutionProfileMainnetCapped && !hyperliquidActivationEnabled && !signer.HyperliquidActivationEnabled && !signer.PolymarketActivationEnabled && !signer.KalshiActivationEnabled && !signer.LighterActivationEnabled),
		"mainnetCapped":          rt.profile == signer.ExecutionProfileMainnetCapped,
		"wenHeld":                !miningActivationEnabled,
		"solanaDevnetReady":      rt.devnetExecutor != nil,
		"solanaMainnetReady":     rt.mainnetExecutor != nil,
		"hyperliquidTestnet":     hlReady && rt.profile == signer.ExecutionProfileTestnet,
		"hyperliquidMainnet":     hlReady && rt.profile == signer.ExecutionProfileMainnetCapped,
		"polymarketTestnetReady": rt.polymarketClient != nil,
		"kalshiDemoReady":        rt.kalshiClient != nil,
		"lighterTestnetReady":    rt.lighterClient != nil,
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

// SolanaMainnetExecuteRequest parameterizes a bounded non-WEN Solana Mainnet-Beta transaction
// (legacy or v0/ALT message) executed under --execution-profile=mainnet-capped.
type SolanaMainnetExecuteRequest struct {
	AgentVaultID      string `json:"agentVaultId,omitempty"`
	Asset             string `json:"asset,omitempty"`
	Memo              string `json:"memo,omitempty"`
	MessageBase64     string `json:"messageBase64,omitempty"`
	EstimatedMicroUSD uint64 `json:"estimatedMicroUsd"`
	SimulateOnly      bool   `json:"simulateOnly,omitempty"`
}

func (s *Store) executeSolanaMainnet(ctx context.Context, req SolanaMainnetExecuteRequest) (*SolanaDevnetRehearseResult, error) {
	rt := s.stage4Runtime()
	if rt == nil || rt.profile != signer.ExecutionProfileMainnetCapped || rt.mainnetExecutor == nil || rt.signerCall == nil {
		return nil, signer.ErrUnavailable
	}
	vaultID := strings.TrimSpace(req.AgentVaultID)
	if vaultID == "" {
		vaultID = rt.agentVaultID
	}
	if vaultID == "" {
		return nil, errors.New("agentVaultId required for solana mainnet-capped execution")
	}
	asset := strings.ToUpper(strings.TrimSpace(req.Asset))
	if asset == "" {
		asset = "SOL"
	}
	if req.EstimatedMicroUSD == 0 || req.EstimatedMicroUSD > rt.mainnetPolicy.MaxOrderMicroUSD {
		return nil, errors.New("solana mainnet-capped estimated spend outside policy bounds")
	}

	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	if err := rt.mainnetExecutor.VerifyGenesis(callCtx); err != nil {
		return nil, err
	}
	_, bh, lastValidHeight, _, err := rt.mainnetExecutor.LatestBlockhash(callCtx)
	if err != nil {
		return nil, err
	}
	fee, _, err := rt.mainnetExecutor.EstimatePriorityFee(callCtx)
	if err != nil {
		return nil, err
	}
	stResp, err := rt.signerCall(callCtx, signer.Request{Action: "status"})
	if err != nil {
		return nil, err
	}
	if !stResp.Status.MainnetCapped || stResp.Status.Account == nil {
		return nil, signer.ErrUnavailable
	}
	if stResp.Status.Account.Network == "devnet" {
		return nil, signer.ErrUnavailable
	}

	var msgBytes []byte
	if b64 := strings.TrimSpace(req.MessageBase64); b64 != "" {
		msgBytes, err = base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, errors.New("invalid base64 mainnet transaction message")
		}
	} else {
		memo := strings.TrimSpace(req.Memo)
		if memo == "" {
			return nil, errors.New("messageBase64 or memo required for solana mainnet execution")
		}
		pubRaw, pErr := hex.DecodeString(stResp.Status.Account.PublicKey)
		if pErr != nil || len(pubRaw) != 32 {
			return nil, errors.New("invalid signer Ed25519 public key")
		}
		var feePayer [32]byte
		copy(feePayer[:], pubRaw)
		msgBytes, err = onchain.BuildDevnetComputeMemoMessage(feePayer, bh, 50_000, fee, memo)
		if err != nil {
			return nil, err
		}
	}

	var opRand [8]byte
	if _, err := rand.Read(opRand[:]); err != nil {
		return nil, err
	}
	opID := "sol-mainnet-" + hex.EncodeToString(opRand[:])

	signResp, err := rt.signerCall(callCtx, signer.Request{
		Action:       "solana-mainnet-capped-sign",
		AgentVaultID: vaultID,
		SolanaMainnetCappedSign: &signer.SolanaMainnetCappedSignIntent{
			Network:           "mainnet-beta",
			GenesisHash:       onchain.SolanaMainnetGenesisHash,
			OperationID:       opID,
			Asset:             asset,
			EstimatedMicroUSD: req.EstimatedMicroUSD,
			MessageBase64:     base64.StdEncoding.EncodeToString(msgBytes),
			ExpiresAt:         time.Now().Unix() + 120,
		},
	})
	if err != nil {
		return nil, err
	}
	if signResp.SolanaDevnetSigned == nil {
		return nil, errors.New("missing signed mainnet transaction")
	}
	signedTxBytes, err := base64.StdEncoding.DecodeString(signResp.SolanaDevnetSigned.SignedTxBase64)
	if err != nil {
		return nil, errors.New("invalid signed mainnet packet")
	}

	simSlot, simErr, err := rt.mainnetExecutor.SimulateTransaction(callCtx, signedTxBytes)
	if err != nil {
		return nil, err
	}
	if !req.SimulateOnly && simErr != "" {
		return nil, fmt.Errorf("solana mainnet simulation failed: %s", simErr)
	}

	result := &SolanaDevnetRehearseResult{
		Network:                  "mainnet-beta",
		GenesisHash:              onchain.SolanaMainnetGenesisHash,
		RPCHost:                  rt.mainnetHost,
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
		rbRes, rbErr := rt.mainnetExecutor.SendWithRebroadcast(callCtx, signedTxBytes, signResp.SolanaDevnetSigned.Signature, lastValidHeight, onchain.RebroadcastConfig{})
		result.Broadcasted = rbRes.Attempted
		result.Confirmed = rbRes.Confirmed
		result.Attempts = rbRes.Attempts
		if rbErr != nil {
			return result, rbErr
		}
	}
	return result, nil
}

func (h *HTTPHandler) solanaMainnetExecuteAPI(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !h.mutation(w, r) || !h.csrf(w, r, token) {
		return
	}
	var req SolanaMainnetExecuteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := h.store.executeSolanaMainnet(r.Context(), req)
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
