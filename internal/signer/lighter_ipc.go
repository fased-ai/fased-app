package signer

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"slices"
	"strings"
)

const (
	// LighterActivationEnabled remains false until explicit owner Live Mainnet authorization.
	LighterActivationEnabled = false

	// LighterTestnetChainID is the Lighter L2 Testnet ZK rollup chain id.
	LighterTestnetChainID uint32 = 300

	// LighterMainnetChainID is the Lighter L2 Mainnet chain id (locked while !LighterActivationEnabled).
	LighterMainnetChainID uint32 = 304

	lighterMaxAccountIndex int64 = (1 << 48) - 2
	lighterMaxMarketIndex  int64 = (1 << 48) - 1
	lighterNilMarketIndex  int64 = 255
	lighterMaxTimestamp    int64 = (1 << 48) - 1
	lighterMarginTick            = 10_000
)

// LighterStatus reports non-secret Lighter Testnet ECgFp5 signer readiness.
type LighterStatus struct {
	Enabled       bool   `json:"enabled"`
	Network       string `json:"network"`
	ChainID       uint32 `json:"chainId"`
	PublicKeyHex  string `json:"publicKeyHex,omitempty"`
	MainnetLocked bool   `json:"mainnetLocked"`
}

// LighterOrderIntent describes a bounded Lighter L2CreateOrder transaction for Poseidon2+Schnorr signing.
type LighterOrderIntent struct {
	Network           string `json:"network"`
	ChainID           uint32 `json:"chainId"`
	OperationID       string `json:"operationId"`
	Asset             string `json:"asset"`
	EstimatedMicroUSD uint64 `json:"estimatedMicroUsd"`
	Leverage          uint32 `json:"leverage,omitempty"`
	AccountIndex      int64  `json:"accountIndex"`
	ApiKeyIndex       uint8  `json:"apiKeyIndex"`
	MarketIndex       int64  `json:"marketIndex"`
	ClientOrderIndex  int64  `json:"clientOrderIndex"`
	BaseAmount        int64  `json:"baseAmount"`
	Price             uint32 `json:"price"`
	SizeDecimals      uint8  `json:"sizeDecimals,omitempty"`
	PriceDecimals     uint8  `json:"priceDecimals,omitempty"`
	IsAsk             uint8  `json:"isAsk"`
	OrderType         uint8  `json:"orderType"`
	TimeInForce       uint8  `json:"timeInForce"`
	ReduceOnly        uint8  `json:"reduceOnly"`
	TriggerPrice      uint32 `json:"triggerPrice"`
	OrderExpiry       int64  `json:"orderExpiry"`
	Nonce             int64  `json:"nonce"`
	ExpiredAt         int64  `json:"expiredAt"`
	IntentExpiresAt   int64  `json:"intentExpiresAt"`
}

// LighterCancelIntent describes a bounded Lighter L2CancelOrder transaction.
type LighterCancelIntent struct {
	Network         string `json:"network"`
	ChainID         uint32 `json:"chainId"`
	OperationID     string `json:"operationId"`
	Asset           string `json:"asset,omitempty"`
	AccountIndex    int64  `json:"accountIndex"`
	ApiKeyIndex     uint8  `json:"apiKeyIndex"`
	MarketIndex     int64  `json:"marketIndex"`
	OrderIndex      int64  `json:"orderIndex"`
	Nonce           int64  `json:"nonce"`
	ExpiredAt       int64  `json:"expiredAt"`
	IntentExpiresAt int64  `json:"intentExpiresAt"`
}

// LighterUpdateLeverageIntent describes a bounded Lighter L2UpdateLeverage transaction.
type LighterUpdateLeverageIntent struct {
	Network         string `json:"network"`
	ChainID         uint32 `json:"chainId"`
	OperationID     string `json:"operationId"`
	Asset           string `json:"asset"`
	AccountIndex    int64  `json:"accountIndex"`
	ApiKeyIndex     uint8  `json:"apiKeyIndex"`
	MarketIndex     int64  `json:"marketIndex"`
	Leverage        uint32 `json:"leverage"`
	MarginMode      uint8  `json:"marginMode"` // 0 = cross, 1 = isolated
	Nonce           int64  `json:"nonce"`
	ExpiredAt       int64  `json:"expiredAt"`
	IntentExpiresAt int64  `json:"intentExpiresAt"`
}

// LighterSignedTx returns the signed ECgFp5 Schnorr tx packet ready for POST /api/v1/sendTx.
type LighterSignedTx struct {
	Network           string `json:"network"`
	ChainID           uint32 `json:"chainId"`
	TxType            uint8  `json:"txType"`
	PublicKeyHex      string `json:"publicKeyHex"`
	TxHashHex         string `json:"txHashHex"`
	SignatureHex      string `json:"signatureHex"`
	SignatureBase64   string `json:"signatureBase64"`
	TxInfoJSON        string `json:"txInfoJson"`
	Nonce             int64  `json:"nonce"`
	OperationID       string `json:"operationId"`
	EstimatedMicroUSD uint64 `json:"estimatedMicroUsd,omitempty"`
}

func validateLighterNetworkAndChain(network string, chainID uint32) (uint32, error) {
	netName := strings.ToLower(strings.TrimSpace(network))
	if netName == "mainnet" || chainID == LighterMainnetChainID {
		if !LighterActivationEnabled {
			return 0, ErrUnavailable
		}
	}
	if netName != "testnet" {
		return 0, errors.New("lighter signing requires testnet network")
	}
	if chainID == 0 {
		chainID = LighterTestnetChainID
	}
	if chainID != LighterTestnetChainID {
		return 0, errors.New("lighter signing requires testnet chainId 300")
	}
	return chainID, nil
}

func (v *Vault) requireLighterUnlockedLocked(network string, chainID uint32, agentVaultID, opID string, expiresAt int64) (uint32, int64, string, error) {
	if !v.testnetExecEnabled || !v.allowStage4HyperliquidVaultLocked() || v.ledger.main == nil {
		return 0, 0, "", ErrUnavailable
	}
	if !v.unlocked() {
		return 0, 0, "", ErrLocked
	}
	cid, err := validateLighterNetworkAndChain(network, chainID)
	if err != nil {
		return 0, 0, "", err
	}
	if strings.TrimSpace(agentVaultID) == "" {
		return 0, 0, "", errors.New("lighter execution requires a typed agent vault")
	}
	if opID == "" || len(opID) > 96 || strings.TrimSpace(opID) != opID {
		return 0, 0, "", errors.New("invalid lighter operationId")
	}
	nowTime := v.now()
	nowUnix := nowTime.Unix()
	if expiresAt <= nowUnix {
		return 0, 0, "", errors.New("lighter sign intent expired")
	}
	return cid, nowUnix, nowTime.UTC().Format("2006-01-02"), nil
}

// LighterStatus returns non-secret Lighter Testnet ECgFp5 public key metadata.
func (v *Vault) LighterStatus() (*LighterStatus, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.testnetExecEnabled || !v.allowStage4HyperliquidVaultLocked() {
		return nil, ErrUnavailable
	}
	st := &LighterStatus{
		Enabled:       true,
		Network:       "testnet",
		ChainID:       LighterTestnetChainID,
		MainnetLocked: !LighterActivationEnabled,
	}
	if v.unlocked() {
		sk, err := DeriveLighterScalarFromSeed(v.seed)
		if err != nil {
			return nil, err
		}
		pk := LighterPubKeyFromScalar(sk)
		sk.SetInt64(0)
		st.PublicKeyHex = "0x" + hex.EncodeToString(pk[:])
	}
	return st, nil
}

// lighterOrderNotionalMicroUSD derives the authoritative order notional in micro-USD
// directly from the signed BaseAmount and Price using bounded market decimals so a
// compromised app caller cannot under-report EstimatedMicroUSD.
func lighterOrderNotionalMicroUSD(intent LighterOrderIntent) (uint64, error) {
	sizeDec := intent.SizeDecimals
	priceDec := intent.PriceDecimals
	if sizeDec == 0 && priceDec == 0 {
		// Pinned defaults for known Lighter testnet markets when caller omits decimals.
		switch intent.MarketIndex {
		case 0: // ETH
			sizeDec, priceDec = 4, 2
		case 1: // BTC
			sizeDec, priceDec = 5, 1
		case 2: // SOL
			sizeDec, priceDec = 2, 3
		default:
			sizeDec, priceDec = 4, 2
		}
	}
	if sizeDec > 6 || priceDec > 6 || int(sizeDec)+int(priceDec) > 8 {
		return 0, errors.New("lighter market decimals out of safe bounds")
	}
	// ceil(BaseAmount * Price * 1_000_000 / 10^(sizeDec + priceDec))
	denom := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(sizeDec+priceDec)), nil)
	num := new(big.Int).Mul(big.NewInt(intent.BaseAmount), new(big.Int).SetUint64(uint64(intent.Price)))
	num.Mul(num, big.NewInt(1_000_000))
	num.Add(num, new(big.Int).Sub(denom, big.NewInt(1)))
	micro := new(big.Int).Quo(num, denom)
	if !micro.IsUint64() || micro.Sign() <= 0 {
		return 0, errors.New("lighter derived notional micro-USD out of bounds")
	}
	return micro.Uint64(), nil
}

// SignLighterOrderIPC validates a Lighter Testnet L2CreateOrder intent against AgentVaultPolicy,
// signs the Poseidon2 digest using domain-separated ECgFp5 Schnorr, and journals spend.
func (v *Vault) SignLighterOrderIPC(intent LighterOrderIntent, agentVaultID string) (*LighterSignedTx, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	cid, nowUnix, dayUTC, err := v.requireLighterUnlockedLocked(intent.Network, intent.ChainID, agentVaultID, intent.OperationID, intent.IntentExpiresAt)
	if err != nil {
		return nil, err
	}
	if intent.Asset == "" || len(intent.Asset) > 24 || strings.ToUpper(strings.TrimSpace(intent.Asset)) != intent.Asset {
		return nil, errors.New("invalid lighter order asset symbol")
	}
	if intent.AccountIndex < 0 || intent.AccountIndex > lighterMaxAccountIndex {
		return nil, errors.New("lighter accountIndex out of range")
	}
	if intent.ApiKeyIndex > 254 {
		return nil, errors.New("lighter apiKeyIndex out of range")
	}
	if intent.MarketIndex < 0 || intent.MarketIndex == lighterNilMarketIndex || intent.MarketIndex > lighterMaxMarketIndex {
		return nil, errors.New("lighter marketIndex out of range")
	}
	if intent.ClientOrderIndex < 0 || intent.ClientOrderIndex > (1<<48)-1 {
		return nil, errors.New("lighter clientOrderIndex out of range")
	}
	if intent.BaseAmount <= 0 || intent.BaseAmount > (1<<48)-1 || intent.Price == 0 {
		return nil, errors.New("lighter baseAmount and price must be positive")
	}
	derivedMicroUSD, err := lighterOrderNotionalMicroUSD(intent)
	if err != nil {
		return nil, err
	}
	effectiveMicroUSD := derivedMicroUSD
	if intent.EstimatedMicroUSD > effectiveMicroUSD {
		effectiveMicroUSD = intent.EstimatedMicroUSD
	}
	if intent.IsAsk > 1 || intent.ReduceOnly > 1 || intent.OrderType > 6 || intent.TimeInForce > 2 {
		return nil, errors.New("invalid lighter order flags or type")
	}
	if intent.Nonce < 0 || intent.ExpiredAt <= nowUnix*1000 || intent.ExpiredAt > lighterMaxTimestamp {
		return nil, errors.New("lighter nonce or expiredAt invalid")
	}
	if (intent.OrderType == 1 || (intent.OrderType == 0 && intent.TimeInForce == 0)) && intent.OrderExpiry != 0 {
		return nil, errors.New("lighter IOC/market orders must have zero orderExpiry")
	}
	if intent.OrderType == 0 && intent.TimeInForce != 0 && intent.OrderExpiry <= nowUnix*1000 {
		return nil, errors.New("lighter GTT/PostOnly orders require future orderExpiry")
	}

	if err := v.validateAgentVaultForVenueLocked(agentVaultID, "lighter", intent.Asset, intent.Leverage, effectiveMicroUSD, intent.ReduceOnly == 1, nowUnix, dayUTC); err != nil {
		return nil, err
	}
	spendRec := AgentVaultSpendRecord{
		VaultID:       agentVaultID,
		OperationID:   intent.OperationID,
		Venue:         "lighter",
		Asset:         intent.Asset,
		DayUTC:        dayUTC,
		SpendMicroUSD: effectiveMicroUSD,
	}
	if err := ValidateAgentVaultSpendRecord(spendRec); err != nil {
		return nil, err
	}
	if v.ledger.agentVaultOps[spendRec.OperationID] {
		return nil, errors.New("duplicate agent vault operation identity")
	}

	txDigest, txLimbs, err := LighterCreateOrderHash(
		cid,
		intent.Nonce, intent.ExpiredAt,
		intent.AccountIndex, intent.ApiKeyIndex, intent.MarketIndex, intent.ClientOrderIndex,
		intent.BaseAmount, intent.Price, intent.IsAsk, intent.OrderType,
		intent.TimeInForce, intent.ReduceOnly, intent.TriggerPrice, intent.OrderExpiry,
	)
	if err != nil {
		return nil, err
	}
	sk, err := DeriveLighterScalarFromSeed(v.seed)
	if err != nil {
		return nil, err
	}
	pk := LighterPubKeyFromScalar(sk)
	sig, err := LighterSignHashedMessage(txLimbs, sk)
	sk.SetInt64(0)
	if err != nil {
		return nil, err
	}
	if err := LighterVerifySignature(pk[:], txDigest[:], sig[:]); err != nil {
		return nil, err
	}

	wire := struct {
		AccountIndex     int64  `json:"AccountIndex"`
		ApiKeyIndex      uint8  `json:"ApiKeyIndex"`
		MarketIndex      int64  `json:"MarketIndex"`
		ClientOrderIndex int64  `json:"ClientOrderIndex"`
		BaseAmount       int64  `json:"BaseAmount"`
		Price            uint32 `json:"Price"`
		IsAsk            uint8  `json:"IsAsk"`
		Type             uint8  `json:"Type"`
		TimeInForce      uint8  `json:"TimeInForce"`
		ReduceOnly       uint8  `json:"ReduceOnly"`
		TriggerPrice     uint32 `json:"TriggerPrice"`
		OrderExpiry      int64  `json:"OrderExpiry"`
		ExpiredAt        int64  `json:"ExpiredAt"`
		Nonce            int64  `json:"Nonce"`
		Sig              []byte `json:"Sig"`
	}{
		AccountIndex:     intent.AccountIndex,
		ApiKeyIndex:      intent.ApiKeyIndex,
		MarketIndex:      intent.MarketIndex,
		ClientOrderIndex: intent.ClientOrderIndex,
		BaseAmount:       intent.BaseAmount,
		Price:            intent.Price,
		IsAsk:            intent.IsAsk,
		Type:             intent.OrderType,
		TimeInForce:      intent.TimeInForce,
		ReduceOnly:       intent.ReduceOnly,
		TriggerPrice:     intent.TriggerPrice,
		OrderExpiry:      intent.OrderExpiry,
		ExpiredAt:        intent.ExpiredAt,
		Nonce:            intent.Nonce,
		Sig:              sig[:],
	}
	txInfoBytes, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	if err := v.ledger.append(v, journalRecord{
		Kind:       "agent-vault-spend",
		AgentSpend: &spendRec,
	}); err != nil {
		return nil, err
	}

	return &LighterSignedTx{
		Network:           "testnet",
		ChainID:           cid,
		TxType:            LighterTxTypeCreateOrder,
		PublicKeyHex:      "0x" + hex.EncodeToString(pk[:]),
		TxHashHex:         "0x" + hex.EncodeToString(txDigest[:]),
		SignatureHex:      "0x" + hex.EncodeToString(sig[:]),
		SignatureBase64:   base64.StdEncoding.EncodeToString(sig[:]),
		TxInfoJSON:        string(txInfoBytes),
		Nonce:             intent.Nonce,
		OperationID:       intent.OperationID,
		EstimatedMicroUSD: effectiveMicroUSD,
	}, nil
}

// SignLighterCancelIPC signs a Lighter Testnet L2CancelOrder transaction under active AgentVaultPolicy.
func (v *Vault) SignLighterCancelIPC(intent LighterCancelIntent, agentVaultID string) (*LighterSignedTx, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	cid, nowUnix, _, err := v.requireLighterUnlockedLocked(intent.Network, intent.ChainID, agentVaultID, intent.OperationID, intent.IntentExpiresAt)
	if err != nil {
		return nil, err
	}
	p, ok := v.ledger.agentVaults[agentVaultID]
	if !ok {
		return nil, errors.New("unknown agent vault")
	}
	if p.Revoked || nowUnix >= p.ExpiresAt || !slices.Contains(p.AllowedVenues, "lighter") {
		return nil, errors.New("agent vault forbids or revoked lighter venue")
	}
	if intent.AccountIndex < 0 || intent.AccountIndex > lighterMaxAccountIndex || intent.ApiKeyIndex > 254 {
		return nil, errors.New("lighter account or apiKey index out of range")
	}
	if intent.MarketIndex < 0 || intent.MarketIndex == lighterNilMarketIndex || intent.MarketIndex > lighterMaxMarketIndex {
		return nil, errors.New("lighter marketIndex out of range")
	}
	if intent.OrderIndex <= 0 || intent.Nonce < 0 || intent.ExpiredAt <= nowUnix*1000 || intent.ExpiredAt > lighterMaxTimestamp {
		return nil, errors.New("lighter cancel orderIndex, nonce, or expiredAt invalid")
	}

	txDigest, txLimbs, err := LighterCancelOrderHash(cid, intent.Nonce, intent.ExpiredAt, intent.AccountIndex, intent.ApiKeyIndex, intent.MarketIndex, intent.OrderIndex)
	if err != nil {
		return nil, err
	}
	sk, err := DeriveLighterScalarFromSeed(v.seed)
	if err != nil {
		return nil, err
	}
	pk := LighterPubKeyFromScalar(sk)
	sig, err := LighterSignHashedMessage(txLimbs, sk)
	sk.SetInt64(0)
	if err != nil {
		return nil, err
	}
	if err := LighterVerifySignature(pk[:], txDigest[:], sig[:]); err != nil {
		return nil, err
	}

	wire := struct {
		AccountIndex int64  `json:"AccountIndex"`
		ApiKeyIndex  uint8  `json:"ApiKeyIndex"`
		MarketIndex  int64  `json:"MarketIndex"`
		Index        int64  `json:"Index"`
		ExpiredAt    int64  `json:"ExpiredAt"`
		Nonce        int64  `json:"Nonce"`
		Sig          []byte `json:"Sig"`
	}{
		AccountIndex: intent.AccountIndex,
		ApiKeyIndex:  intent.ApiKeyIndex,
		MarketIndex:  intent.MarketIndex,
		Index:        intent.OrderIndex,
		ExpiredAt:    intent.ExpiredAt,
		Nonce:        intent.Nonce,
		Sig:          sig[:],
	}
	txInfoBytes, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	return &LighterSignedTx{
		Network:         "testnet",
		ChainID:         cid,
		TxType:          LighterTxTypeCancelOrder,
		PublicKeyHex:    "0x" + hex.EncodeToString(pk[:]),
		TxHashHex:       "0x" + hex.EncodeToString(txDigest[:]),
		SignatureHex:    "0x" + hex.EncodeToString(sig[:]),
		SignatureBase64: base64.StdEncoding.EncodeToString(sig[:]),
		TxInfoJSON:      string(txInfoBytes),
		Nonce:           intent.Nonce,
		OperationID:     intent.OperationID,
	}, nil
}

// SignLighterUpdateLeverageIPC signs a Lighter Testnet L2UpdateLeverage transaction within AgentVaultPolicy.
func (v *Vault) SignLighterUpdateLeverageIPC(intent LighterUpdateLeverageIntent, agentVaultID string) (*LighterSignedTx, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	cid, nowUnix, dayUTC, err := v.requireLighterUnlockedLocked(intent.Network, intent.ChainID, agentVaultID, intent.OperationID, intent.IntentExpiresAt)
	if err != nil {
		return nil, err
	}
	if intent.Asset == "" || len(intent.Asset) > 24 || strings.ToUpper(strings.TrimSpace(intent.Asset)) != intent.Asset {
		return nil, errors.New("invalid lighter leverage asset symbol")
	}
	if intent.Leverage == 0 || intent.MarginMode > 1 {
		return nil, errors.New("invalid lighter leverage or marginMode")
	}
	if intent.AccountIndex < 0 || intent.AccountIndex > lighterMaxAccountIndex || intent.ApiKeyIndex > 254 {
		return nil, errors.New("lighter account or apiKey index out of range")
	}
	if intent.MarketIndex < 0 || intent.MarketIndex == lighterNilMarketIndex || intent.MarketIndex > lighterMaxMarketIndex {
		return nil, errors.New("lighter marketIndex out of range")
	}
	if intent.Nonce < 0 || intent.ExpiredAt <= nowUnix*1000 || intent.ExpiredAt > lighterMaxTimestamp {
		return nil, errors.New("lighter nonce or expiredAt invalid")
	}
	if err := v.validateAgentVaultForVenueLocked(agentVaultID, "lighter", intent.Asset, intent.Leverage, 0, false, nowUnix, dayUTC); err != nil {
		return nil, err
	}
	imf := lighterMarginTick / int(intent.Leverage)
	if imf <= 0 || imf > lighterMarginTick {
		return nil, errors.New("lighter initialMarginFraction out of bounds")
	}

	txDigest, txLimbs, err := LighterUpdateLeverageHash(cid, intent.Nonce, intent.ExpiredAt, intent.AccountIndex, intent.ApiKeyIndex, intent.MarketIndex, uint16(imf), intent.MarginMode)
	if err != nil {
		return nil, err
	}
	sk, err := DeriveLighterScalarFromSeed(v.seed)
	if err != nil {
		return nil, err
	}
	pk := LighterPubKeyFromScalar(sk)
	sig, err := LighterSignHashedMessage(txLimbs, sk)
	sk.SetInt64(0)
	if err != nil {
		return nil, err
	}
	if err := LighterVerifySignature(pk[:], txDigest[:], sig[:]); err != nil {
		return nil, err
	}

	wire := struct {
		AccountIndex          int64  `json:"AccountIndex"`
		ApiKeyIndex           uint8  `json:"ApiKeyIndex"`
		MarketIndex           int64  `json:"MarketIndex"`
		InitialMarginFraction uint16 `json:"InitialMarginFraction"`
		MarginMode            uint8  `json:"MarginMode"`
		ExpiredAt             int64  `json:"ExpiredAt"`
		Nonce                 int64  `json:"Nonce"`
		Sig                   []byte `json:"Sig"`
	}{
		AccountIndex:          intent.AccountIndex,
		ApiKeyIndex:           intent.ApiKeyIndex,
		MarketIndex:           intent.MarketIndex,
		InitialMarginFraction: uint16(imf),
		MarginMode:            intent.MarginMode,
		ExpiredAt:             intent.ExpiredAt,
		Nonce:                 intent.Nonce,
		Sig:                   sig[:],
	}
	txInfoBytes, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	return &LighterSignedTx{
		Network:         "testnet",
		ChainID:         cid,
		TxType:          LighterTxTypeUpdateLev,
		PublicKeyHex:    "0x" + hex.EncodeToString(pk[:]),
		TxHashHex:       "0x" + hex.EncodeToString(txDigest[:]),
		SignatureHex:    "0x" + hex.EncodeToString(sig[:]),
		SignatureBase64: base64.StdEncoding.EncodeToString(sig[:]),
		TxInfoJSON:      string(txInfoBytes),
		Nonce:           intent.Nonce,
		OperationID:     intent.OperationID,
	}, nil
}
