package signer

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/fased-ai/fased-app/internal/onchain"
)

const (
	// ExecutionProfileOff keeps live Stage 4 financial execution disabled (default).
	ExecutionProfileOff = "off"
	// ExecutionProfileTestnet enables non-WEN Solana Devnet & Hyperliquid Testnet signing under owner policy.
	ExecutionProfileTestnet = "testnet"
)

// SolanaDevnetSignIntent describes a typed Solana Devnet compute+memo intent
// constructed inside fased-signer under a typed AgentVaultPolicy.
// Raw caller-supplied MessageBase64 is unconditionally rejected.
type SolanaDevnetSignIntent struct {
	Network                  string `json:"network"`
	GenesisHash              string `json:"genesisHash"`
	OperationID              string `json:"operationId"`
	Asset                    string `json:"asset"`
	EstimatedMicroUSD        uint64 `json:"estimatedMicroUsd"`
	Blockhash                string `json:"blockhash"`
	ComputeUnitLimit         uint32 `json:"computeUnitLimit,omitempty"`
	PriorityFeeMicroLamports uint64 `json:"priorityFeeMicroLamports,omitempty"`
	Memo                     string `json:"memo"`
	MessageBase64            string `json:"messageBase64,omitempty"` // deprecated: always rejected fail-closed
	ExpiresAt                int64  `json:"expiresAt"`
}

// SolanaDevnetSignedTx is returned after verifying Devnet message instructions
// and journaling spend against the selected AgentVaultPolicy.
type SolanaDevnetSignedTx struct {
	Signature       string `json:"signature"`
	SignedTxBase64  string `json:"signedTxBase64"`
	MessageSHA256   string `json:"messageSha256"`
	PublicKeyBase58 string `json:"publicKeyBase58"`
	GenesisHash     string `json:"genesisHash"`
}

// ConfigureTestnetExecution enables or clears non-WEN Devnet/Testnet signing on a Vault.
// Mainnet and Version-2 managed reader vaults are unconditionally rejected.
func (v *Vault) ConfigureTestnetExecution(enabled bool) error {
	if v == nil {
		return ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed && enabled {
		return ErrUnavailable
	}
	if enabled {
		if v.network == "mainnet" || v.network == "mainnet-beta" {
			return ErrUnavailable
		}
		if v.network != "" && v.network != "devnet" {
			return ErrUnavailable
		}
		if v.ledger != nil && !v.ledger.allowStage4PolicyReplay() {
			return ErrUnavailable
		}
	}
	v.testnetExecEnabled = enabled
	v.setHyperliquidControlledLocked(enabled)
	return nil
}

// SignSolanaDevnetIPC validates a Solana Devnet message against allowed program IDs
// and typed AgentVaultPolicy limits before signing with the Vault's Ed25519 key.
func (v *Vault) SignSolanaDevnetIPC(intent SolanaDevnetSignIntent, agentVaultID string) (*SolanaDevnetSignedTx, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.testnetExecEnabled || !v.allowStage4HyperliquidVaultLocked() || v.ledger.main == nil {
		return nil, ErrUnavailable
	}
	if !v.unlocked() {
		return nil, ErrLocked
	}
	if intent.Network == "mainnet" || intent.Network == "mainnet-beta" {
		return nil, ErrUnavailable
	}
	if intent.Network != "devnet" {
		return nil, errors.New("solana devnet signing requires devnet network")
	}
	if intent.GenesisHash != onchain.SolanaDevnetGenesisHash {
		return nil, errors.New("solana devnet genesis hash mismatch")
	}
	if strings.TrimSpace(agentVaultID) == "" {
		return nil, errors.New("solana devnet execution requires a typed agent vault")
	}
	if intent.EstimatedMicroUSD == 0 {
		return nil, errors.New("solana devnet execution requires non-zero estimated micro-USD spend")
	}
	if intent.Asset == "" || len(intent.Asset) > 24 || strings.ToUpper(strings.TrimSpace(intent.Asset)) != intent.Asset {
		return nil, errors.New("invalid solana devnet asset symbol")
	}
	nowTime := v.now()
	if intent.ExpiresAt <= nowTime.Unix() {
		return nil, errors.New("solana devnet sign intent expired")
	}
	if strings.TrimSpace(intent.MessageBase64) != "" {
		return nil, errors.New("raw messageBase64 signing is disabled in signer; use typed memo fields or Custody V2 typed intents")
	}
	bhRaw, err := onchain.Key(strings.TrimSpace(intent.Blockhash))
	if err != nil {
		return nil, errors.New("valid base58 recent blockhash required")
	}
	var bh [32]byte
	copy(bh[:], bhRaw)
	cuLimit := intent.ComputeUnitLimit
	if cuLimit == 0 {
		cuLimit = 50_000
	}
	if cuLimit > 200_000 {
		return nil, errors.New("solana devnet compute unit limit exceeds 200000")
	}
	if intent.PriorityFeeMicroLamports > 100_000 {
		return nil, errors.New("solana devnet priority fee exceeds 100000 micro-lamports/CU")
	}
	memo := strings.TrimSpace(intent.Memo)
	if memo == "" || len(memo) > 256 {
		return nil, errors.New("solana devnet typed memo required (1..256 bytes)")
	}
	priv := ed25519.NewKeyFromSeed(v.seed)
	defer wipe(priv)
	var feePayer [32]byte
	copy(feePayer[:], priv[ed25519.SeedSize:])
	msgBytes, err := onchain.BuildDevnetComputeMemoMessage(feePayer, bh, cuLimit, intent.PriorityFeeMicroLamports, memo)
	if err != nil {
		return nil, err
	}
	if err := onchain.ValidateDevnetMessage(msgBytes, feePayer); err != nil {
		return nil, err
	}
	dayUTC := nowTime.UTC().Format("2006-01-02")
	if err := v.validateAgentVaultForVenueLocked(agentVaultID, "solana", intent.Asset, 0, intent.EstimatedMicroUSD, false, nowTime.Unix(), dayUTC); err != nil {
		return nil, err
	}
	spendRec := AgentVaultSpendRecord{
		VaultID:       agentVaultID,
		OperationID:   intent.OperationID,
		Venue:         "solana",
		Asset:         intent.Asset,
		DayUTC:        dayUTC,
		SpendMicroUSD: intent.EstimatedMicroUSD,
	}
	if err := ValidateAgentVaultSpendRecord(spendRec); err != nil {
		return nil, err
	}
	if v.ledger.agentVaultOps[spendRec.OperationID] {
		return nil, errors.New("duplicate agent vault operation identity")
	}
	rawSig := ed25519.Sign(priv, msgBytes)
	var sigArr [64]byte
	copy(sigArr[:], rawSig)
	wipe(rawSig)
	signedTx, sigB58 := onchain.AssembleSignedLegacyTransaction(sigArr, msgBytes)
	if err := v.ledger.append(v, journalRecord{
		Kind:       "agent-vault-spend",
		AgentSpend: &spendRec,
	}); err != nil {
		return nil, err
	}
	msgSum := sha256.Sum256(msgBytes)
	return &SolanaDevnetSignedTx{
		Signature:       sigB58,
		SignedTxBase64:  base64.StdEncoding.EncodeToString(signedTx),
		MessageSHA256:   hex.EncodeToString(msgSum[:]),
		PublicKeyBase58: base58PublicKey(feePayer[:]),
		GenesisHash:     onchain.SolanaDevnetGenesisHash,
	}, nil
}

func (v *Vault) testnetExecutionAllowed() bool {
	if v == nil {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return !v.closed && v.testnetExecEnabled && (v.network == "" || v.network == "devnet")
}

func isStage4TestnetAction(action string) bool {
	switch action {
	case "hyperliquid-status", "hyperliquid-policy", "hyperliquid-sign-leverage", "hyperliquid-sign-order", "hyperliquid-sign-cancel",
		"agent-vault-set", "agent-vault-revoke", "agent-vault-list", "agent-vault-record-loss",
		"solana-devnet-sign", "solana-mainnet-capped-sign",
		"polymarket-status", "polymarket-sign-order",
		"kalshi-status", "kalshi-generate-key", "kalshi-provision-key", "kalshi-sign-request",
		"lighter-status", "lighter-sign-order", "lighter-sign-cancel", "lighter-sign-leverage":
		return true
	}
	return false
}
