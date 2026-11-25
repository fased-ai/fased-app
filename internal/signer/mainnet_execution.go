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
	// ExecutionProfileMainnetCapped enables owner-confirmed, small-cap non-WEN Mainnet
	// execution strictly bounded by hard per-order ($250) and daily ($1,000) ceilings.
	ExecutionProfileMainnetCapped = "mainnet-capped"

	// MainnetCappedHardMaxOrderMicroUSD caps any single Mainnet-capped order at $250.
	MainnetCappedHardMaxOrderMicroUSD uint64 = 250_000_000

	// MainnetCappedHardMaxDailyMicroUSD caps aggregate daily Mainnet-capped spend at $1,000.
	MainnetCappedHardMaxDailyMicroUSD uint64 = 1_000_000_000
)

func init() {
	hyperliquidMainnetCappedHook = func(v *Vault) (bool, uint64) {
		if v == nil || !v.mainnetCappedEnabled {
			return false, 0
		}
		return true, v.mainnetCappedPolicy.MaxOrderMicroUSD
	}
}

// MainnetCappedPolicy defines hard owner-confirmed runtime ceilings for non-WEN
// Stage 4/5 execution when --execution-profile=mainnet-capped is explicitly selected.
type MainnetCappedPolicy struct {
	Confirmed        bool   `json:"confirmed"`
	MaxOrderMicroUSD uint64 `json:"maxOrderMicroUsd,string"`
	MaxDailyMicroUSD uint64 `json:"maxDailyMicroUsd,string"`
}

// Validate enforces explicit confirmation and hard $250/order, $1,000/day ceilings.
func (p MainnetCappedPolicy) Validate() error {
	if !p.Confirmed {
		return errors.New("mainnet-capped execution requires explicit owner confirmation")
	}
	if p.MaxOrderMicroUSD == 0 || p.MaxOrderMicroUSD > MainnetCappedHardMaxOrderMicroUSD {
		return errors.New("mainnet-capped max order must be between 1 and 250000000 micro-USD ($250)")
	}
	if p.MaxDailyMicroUSD < p.MaxOrderMicroUSD || p.MaxDailyMicroUSD > MainnetCappedHardMaxDailyMicroUSD {
		return errors.New("mainnet-capped max daily spend must be between max-order and 1000000000 micro-USD ($1000)")
	}
	return nil
}

// SolanaMainnetCappedSignIntent describes a strictly bounded, non-WEN Solana Mainnet-Beta
// legacy message submitted to fased-signer under a typed AgentVaultPolicy.
type SolanaMainnetCappedSignIntent struct {
	Network           string `json:"network"`
	GenesisHash       string `json:"genesisHash"`
	OperationID       string `json:"operationId"`
	Asset             string `json:"asset"`
	EstimatedMicroUSD uint64 `json:"estimatedMicroUsd"`
	MessageBase64     string `json:"messageBase64"`
	ExpiresAt         int64  `json:"expiresAt"`
}

// ConfigureMainnetCappedExecution enables owner-confirmed, small-cap non-WEN Mainnet signing
// on a non-devnet Vault without modifying WEN SAT mining holds.
func (v *Vault) ConfigureMainnetCappedExecution(policy MainnetCappedPolicy) error {
	if v == nil {
		return ErrUnavailable
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.testnetExecEnabled {
		return ErrUnavailable
	}
	if v.network == "devnet" {
		return ErrUnavailable
	}
	if v.network != "" && v.network != "mainnet" && v.network != "mainnet-beta" {
		return ErrUnavailable
	}
	if v.ledger != nil && !v.ledger.allowStage4PolicyReplay() {
		return ErrUnavailable
	}
	v.mainnetCappedEnabled = true
	v.mainnetCappedPolicy = policy
	v.setHyperliquidControlledLocked(true)
	return nil
}

// SignSolanaMainnetCappedIPC validates a non-WEN Solana Mainnet-Beta message against
// owner MainnetCappedPolicy and Typed Agent Vault ceilings before signing and journaling spend.
func (v *Vault) SignSolanaMainnetCappedIPC(intent SolanaMainnetCappedSignIntent, agentVaultID string) (*SolanaDevnetSignedTx, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.mainnetCappedEnabled || !v.allowStage4HyperliquidVaultLocked() || v.ledger.main == nil {
		return nil, ErrUnavailable
	}
	if err := v.mainnetCappedPolicy.Validate(); err != nil {
		return nil, ErrUnavailable
	}
	if !v.unlocked() {
		return nil, ErrLocked
	}
	if intent.Network != "mainnet-beta" {
		return nil, errors.New("solana mainnet-capped signing requires mainnet-beta network")
	}
	if intent.GenesisHash != onchain.SolanaMainnetGenesisHash {
		return nil, errors.New("solana mainnet genesis hash mismatch")
	}
	if strings.TrimSpace(agentVaultID) == "" {
		return nil, errors.New("solana mainnet-capped execution requires a typed agent vault")
	}
	if intent.EstimatedMicroUSD == 0 || intent.EstimatedMicroUSD > v.mainnetCappedPolicy.MaxOrderMicroUSD {
		return nil, errors.New("solana mainnet-capped estimated spend outside policy bounds")
	}
	if intent.Asset == "" || len(intent.Asset) > 24 || strings.ToUpper(strings.TrimSpace(intent.Asset)) != intent.Asset {
		return nil, errors.New("invalid solana mainnet asset symbol")
	}
	upperAsset := strings.ToUpper(strings.TrimSpace(intent.Asset))
	if upperAsset == "WEN" || upperAsset == "SAT" {
		return nil, errors.New("wen and sat assets remain held under NOT READY policy")
	}
	nowTime := v.now()
	if intent.ExpiresAt <= nowTime.Unix() {
		return nil, errors.New("solana mainnet-capped sign intent expired")
	}
	msgBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(intent.MessageBase64))
	if err != nil {
		return nil, errors.New("invalid base64 mainnet transaction message")
	}
	priv := ed25519.NewKeyFromSeed(v.seed)
	defer wipe(priv)
	var feePayer [32]byte
	copy(feePayer[:], priv[ed25519.SeedSize:])
	if err := onchain.ValidateSolanaMainnetMessage(msgBytes, feePayer); err != nil {
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
		GenesisHash:     onchain.SolanaMainnetGenesisHash,
	}, nil
}

func (v *Vault) stage4ExecutionAllowed() bool {
	if v == nil {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return false
	}
	if v.testnetExecEnabled && (v.network == "" || v.network == "devnet") {
		return true
	}
	return v.mainnetCappedEnabled && (v.network == "" || v.network == "mainnet" || v.network == "mainnet-beta")
}
